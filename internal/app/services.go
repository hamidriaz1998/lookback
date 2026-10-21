package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/hamidriaz1998/lookback/internal/bench"
	"github.com/hamidriaz1998/lookback/internal/catalog"
	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/download"
	"github.com/hamidriaz1998/lookback/internal/extract"
	"github.com/hamidriaz1998/lookback/internal/hw"
	"github.com/hamidriaz1998/lookback/internal/index"
	lrt "github.com/hamidriaz1998/lookback/internal/runtime"
	"github.com/hamidriaz1998/lookback/internal/store"
	"github.com/hamidriaz1998/lookback/internal/theme"
	lookui "github.com/hamidriaz1998/lookback/internal/ui"
)

// osName is the operating system the app runs on. Windows comes with its phase (SPEC "Windows phase").
const osName = contract.OS("linux")

// Services is the set of real implementations the window and the indexer use. OpenServices builds it.
type Services struct {
	Deps    lookui.Deps
	Indexer contract.Indexer
	Config  Config // the settings the app starts with (SPEC section 14)
	life    context.Context
	cancel  context.CancelFunc // ends life; Close calls it first
	waits   []func()           // join the service workers, after cancel and before the closers
	closers []func()
	// startIndexer starts the indexer once the first run's choice is saved and installed. It is idempotent.
	startIndexer func()
}

// newRuntime builds the sidecar runtime. Tests replace it with a fake.
var newRuntime = func(cfg lrt.Config) contract.Runtime { return lrt.New(cfg) }

// Close cancels the services' lifetime, waits for the indexer and the startup to end (which stops the sidecars
// and interrupts any extraction or embedding), then releases the extractors and the store. Call it after the
// app has quit.
func (s *Services) Close() {
	s.cancel()
	for _, w := range s.waits {
		w()
	}
	for i := len(s.closers) - 1; i >= 0; i-- {
		s.closers[i]()
	}
}

// DefaultDataDir is the SPEC section 2 data root: $XDG_DATA_HOME/lookback, or ~/.local/share/lookback.
func DefaultDataDir() (string, error) {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "lookback"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "lookback"), nil
}

// OpenServices builds the real services under dataDir. life ends them all.
func OpenServices(life context.Context, dataDir string) (*Services, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "logs"), 0o755); err != nil {
		return nil, err
	}
	parent := life
	life, cancel := context.WithCancel(parent)
	s := &Services{life: life, cancel: cancel}
	st, err := store.Open(dataDir)
	if err != nil {
		cancel()
		return nil, err
	}
	s.closers = append(s.closers, func() { st.Close() })

	cat, err := catalog.New()
	if err != nil {
		s.Close()
		return nil, err
	}
	dl, err := download.New(life, download.Config{Dir: dataDir, Network: download.MygoNetwork()})
	if err != nil {
		s.Close()
		return nil, err
	}

	// files resolves the server binary and the model files of a combination from the catalog's assets.
	files := func(c contract.Combo) (lrt.Files, error) {
		assets, err := cat.Assets(osName, c)
		if err != nil {
			return lrt.Files{}, err
		}
		var f lrt.Files
		for _, a := range assets {
			switch a.Part {
			case contract.PartText:
				f.Model = modelPath(dataDir, a)
			case contract.PartEncoder:
				f.MMProj = modelPath(dataDir, a)
			}
		}
		if f.Server, err = serverBinary(dataDir, assets, c.Backend); err != nil {
			return lrt.Files{}, err
		}
		return f, nil
	}
	det := hw.NewDetector(func(b contract.Backend) (string, error) {
		return serverBinary(dataDir, runtimeAssets(cat, b), b)
	})

	// The runtime and the first generation take their backend and space from the first run's choice, so the
	// indexer starts only once that choice is saved and installed (startIndexer).
	rt := newRuntime(lrt.Config{
		Life:    life,
		Backend: contract.BackendCPU,
		Files: func(b contract.Backend, sp contract.VectorSpace) (lrt.Files, error) {
			return files(contract.Combo{Backend: b, TextQuant: sp.TextQuant, EncQuant: sp.EncQuant})
		},
		LogPath: filepath.Join(dataDir, "logs", "llama-server.log"),
	})
	ext := extract.NewRegistry()
	s.closers = append(s.closers, func() { ext.Close() })

	ix := index.New(st, ext, rt, contract.VectorSpace{})
	s.Indexer = ix
	s.waits = append(s.waits, ix.Wait)
	installed := func(c contract.Combo) bool {
		cs, err := st.ListCombos(life)
		if err != nil {
			return false
		}
		for _, in := range cs {
			if in.Combo.ID() == c.ID() {
				return true
			}
		}
		return false
	}
	fl := &folderStore{Store: st, ix: ix}
	// startIndexer runs when the choice is saved (folderStore.SetSetting) or a combination is installed
	// (recordInstalls). It does nothing until the choice is installed, and a failed start is retried on the next
	// event. Holding fl.mu across Start makes a folder edit either listed by Start or sent to AddFolder.
	fl.startFn = func() {
		fl.mu.Lock()
		defer fl.mu.Unlock()
		if fl.started {
			return
		}
		c, ok := onboardCombo(life, st)
		if !ok || !installed(c) {
			return
		}
		sp, err := spaceOf(cat, c)
		if err != nil {
			log.Printf("indexer: %v", err)
			return
		}
		if err := rt.Use(life, c.Backend); err != nil {
			log.Printf("indexer: %v", err)
			return
		}
		ix.SetInitial(sp)
		if err := ix.Start(life); err != nil {
			log.Printf("indexer: %v", err)
			return
		}
		fl.started = true
	}
	s.startIndexer = fl.startFn
	go s.startIndexer()
	lab := bench.New(life, ix, det, files, installed, filepath.Join(dataDir, "logs", "lab.log"), 0)

	themes := theme.Builtins()
	if om := theme.Omarchy(life); om != nil {
		themes = append([]contract.ThemeSource{om}, themes...)
	}

	go recordInstalls(life, st, cat, dl, func() { go s.startIndexer() })

	s.Config = readConfig(life, st)
	s.Deps = lookui.Deps{
		Store:      fl,
		Indexer:    ix,
		Runtime:    rt,
		Lab:        lab,
		Downloader: dl,
		Detector:   det,
		Catalog:    cat,
		Themes:     themes,
		Media:      lookui.FileMedia{},
		OS:         osName,
		DataDir:    dataDir,
	}
	return s, nil
}

// folderStore is the store the window uses. Once the indexer runs, folder adds and removes go through it, which
// saves the folder and starts or stops its watcher and scan (WP8). Before that (onboarding, before any
// combination is installed) they go to the store directly; the indexer loads them when it starts.
type folderStore struct {
	contract.Store
	ix      *index.Indexer
	mu      sync.Mutex // held across the indexer's start; guards started
	started bool
	startFn func() // starts the indexer when the first run's choice is saved
}

func (s *folderStore) SaveFolder(ctx context.Context, f *contract.Folder) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.ID == 0 && s.started {
		return s.ix.AddFolder(ctx, f)
	}
	return s.Store.SaveFolder(ctx, f)
}

func (s *folderStore) DeleteFolder(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return s.ix.RemoveFolder(ctx, id)
	}
	return s.Store.DeleteFolder(ctx, id)
}

// SetSetting saves the setting and, for the first run's choice, starts the indexer if its files are installed.
func (s *folderStore) SetSetting(ctx context.Context, key, value string) error {
	if err := s.Store.SetSetting(ctx, key, value); err != nil {
		return err
	}
	if key == "onboard_combo" {
		go s.startFn()
	}
	return nil
}

// readConfig reads the settings table into Config (SPEC section 14). A missing key keeps its default.
func readConfig(ctx context.Context, st contract.Store) Config {
	cfg := DefaultConfig()
	if v, ok, err := st.GetSetting(ctx, "hotkey"); err == nil && ok && v != "" {
		cfg.Hotkey = v
	}
	if v, ok, err := st.GetSetting(ctx, "battery_pause"); err == nil && ok {
		cfg.PauseOnBattery = v != "off"
	}
	return cfg
}

// onboardCombo returns the combination the first run chose (the "onboard_combo" setting).
func onboardCombo(ctx context.Context, st contract.Store) (contract.Combo, bool) {
	id, ok, err := st.GetSetting(ctx, "onboard_combo")
	if err != nil || !ok {
		return contract.Combo{}, false
	}
	for _, b := range []contract.Backend{contract.BackendCPU, contract.BackendVulkan, contract.BackendCUDA12, contract.BackendCUDA13} {
		for _, tq := range []contract.Quant{contract.QuantQ8_0, contract.QuantBF16} {
			for _, eq := range []contract.Quant{contract.QuantQ8_0, contract.QuantBF16} {
				c := contract.Combo{Backend: b, TextQuant: tq, EncQuant: eq}
				if c.ID() == id {
					return c, true
				}
			}
		}
	}
	return contract.Combo{}, false
}

// spaceOf returns the vector space of combination c: its precisions and the model revision of its text asset.
func spaceOf(cat contract.Catalog, c contract.Combo) (contract.VectorSpace, error) {
	assets, err := cat.Assets(osName, c)
	if err != nil {
		return contract.VectorSpace{}, err
	}
	for _, a := range assets {
		if a.Part == contract.PartText {
			return contract.VectorSpace{TextQuant: c.TextQuant, EncQuant: c.EncQuant, ModelRev: revisionOf(a)}, nil
		}
	}
	return contract.VectorSpace{}, errors.New("combination has no text model")
}

// runtimeAssets returns the runtime archives of backend b.
func runtimeAssets(cat contract.Catalog, b contract.Backend) []contract.Asset {
	var out []contract.Asset
	as, err := cat.Assets(osName, contract.Combo{Backend: b, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16})
	if err != nil {
		return nil
	}
	for _, a := range as {
		if a.Part == contract.PartRuntime {
			out = append(out, a)
		}
	}
	return out
}

// serverBinary finds the llama-server of backend b in the runtime folders of assets (SPEC section 4 names).
func serverBinary(dataDir string, assets []contract.Asset, b contract.Backend) (string, error) {
	for _, a := range assets {
		if a.Part != contract.PartRuntime || a.Backend != b {
			continue
		}
		root := filepath.Join(dataDir, "runtimes", revisionOf(a)+"-"+string(b))
		var found string
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && d.Name() == "llama-server" {
				found = p
				return fs.SkipAll
			}
			return nil
		})
		if found != "" {
			return found, nil
		}
	}
	return "", fmt.Errorf("llama-server for %s is not downloaded", b)
}

// modelPath is where the downloader keeps a model file: models/<revision>/<name> (download.modelPath).
func modelPath(dataDir string, a contract.Asset) string {
	return filepath.Join(dataDir, "models", revisionOf(a), a.Name)
}

// revisionOf is the release or model revision in an asset's URL: the folder name above the file.
func revisionOf(a contract.Asset) string {
	u, err := url.Parse(a.URL)
	if err != nil {
		return "unknown"
	}
	if r := path.Base(path.Dir(u.Path)); r != "." && r != "/" && r != ".." {
		return r
	}
	return "unknown"
}

// recordInstalls saves a combination as installed once every one of its files is verified (SPEC section 7).
// It reads the downloader's stream, whose first snapshot covers the files that were already verified.
func recordInstalls(ctx context.Context, st contract.Store, cat contract.Catalog, dl contract.Downloader, onInstall func()) {
	have := map[string]bool{}
	cs, _ := st.ListCombos(ctx)
	for _, c := range cs {
		have[c.Combo.ID()] = true
	}
	done := map[string]bool{}
	for p := range dl.Subscribe(ctx) {
		if p.State == contract.FileDone {
			done[p.Key] = true
		}
		for _, b := range cat.Backends(osName) {
			for _, tq := range []contract.Quant{contract.QuantQ8_0, contract.QuantBF16} {
				for _, eq := range []contract.Quant{contract.QuantQ8_0, contract.QuantBF16} {
					c := contract.Combo{Backend: b, TextQuant: tq, EncQuant: eq}
					if have[c.ID()] {
						continue
					}
					assets, err := cat.Assets(osName, c)
					if err != nil || !allDone(assets, done) {
						continue
					}
					if err := st.SaveCombo(ctx, contract.Installed{Combo: c, InstalledAt: time.Now()}); err == nil {
						have[c.ID()] = true
						onInstall()
					}
				}
			}
		}
	}
}

func allDone(assets []contract.Asset, done map[string]bool) bool {
	for _, a := range assets {
		if !done[a.Key()] {
			return false
		}
	}
	return true
}
