package theme

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

const (
	omarchyDebounce  = 200 * time.Millisecond
	omarchyRetryWait = 300 * time.Millisecond
	omarchyRetries   = 3
)

// omarchy is the theme source that reads Omarchy's colors.toml and follows its changes.
type omarchy struct {
	dir     string // <state>/omarchy/current
	changed chan struct{}

	mu    sync.Mutex
	kv    map[string]string // the last colors.toml that read and parsed; nil if none has
	emits int               // how many times kv changed; for tests
}

// Omarchy returns the Omarchy theme source, or nil when colors.toml does not exist at startup.
// The state folder is $XDG_STATE_HOME, or ~/.local/state. The source watches the theme until ctx
// ends.
func Omarchy(ctx context.Context) contract.ThemeSource {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		state = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(state, "omarchy", "current")
	if _, err := os.Stat(filepath.Join(dir, "theme", "colors.toml")); err != nil {
		return nil
	}
	return newOmarchy(ctx, dir)
}

func newOmarchy(ctx context.Context, dir string) *omarchy {
	s := &omarchy{dir: dir, changed: make(chan struct{}, 1)}
	if kv, err := readOmarchy(dir); err == nil {
		s.kv = kv
	}
	go s.watch(ctx)
	return s
}

func (s *omarchy) ID() string   { return "omarchy" }
func (s *omarchy) Name() string { return "Omarchy" }

func (s *omarchy) Changed() <-chan struct{} { return s.changed }

// Theme maps the last good colors.toml over the current System theme.
func (s *omarchy) Theme(base *ui.Theme) *ui.Theme {
	s.mu.Lock()
	kv := s.kv
	s.mu.Unlock()
	return omarchyTheme(systemTheme(base, DesktopAccent()), kv)
}

// readOmarchy reads and parses the colors.toml that current/theme holds. A symlink or a real
// folder both work.
func readOmarchy(dir string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "theme", "colors.toml"))
	if err != nil {
		return nil, err
	}
	return parseColors(data)
}

// retryRead calls read until it succeeds, up to retries more times, wait apart. It is for a read
// that can fail while a theme swap is under way.
func retryRead(ctx context.Context, read func() (map[string]string, error), wait time.Duration, retries int) (map[string]string, error) {
	for i := 0; ; i++ {
		kv, err := read()
		if err == nil {
			return kv, nil
		}
		if i == retries {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// reload reads colors.toml, retrying while a swap is under way. When every read fails, the last
// good palette stays in place.
func (s *omarchy) reload(ctx context.Context, wait time.Duration) {
	kv, err := retryRead(ctx, func() (map[string]string, error) { return readOmarchy(s.dir) }, wait, omarchyRetries)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("theme: colors.toml not read, keeping the current colors: %v", err)
		}
		return
	}
	s.set(kv)
}

// set stores kv and emits a change when it differs from the stored one.
func (s *omarchy) set(kv map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if reflect.DeepEqual(kv, s.kv) {
		return
	}
	s.kv = kv
	s.emits++
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// watch follows the theme folder. The parent (current/) catches a folder swap by rm -rf and mv,
// or a symlink swap by ln -nsf. The resolved folder catches in-place edits of colors.toml. Each
// batch of events, after a 200 ms quiet period, re-arms the resolved watch and reads the file.
func (s *omarchy) watch(ctx context.Context) {
	parent, err := mygo.WatchFiles(ctx, s.dir, mygo.FileWatchOptions{})
	if err != nil {
		log.Printf("theme: watch %s: %v", s.dir, err)
		return
	}
	defer parent.Close()
	sig := make(chan struct{}, 1)
	go pumpWatch(ctx, parent, sig)

	var resolved *mygo.FileWatcher
	var armedRoot string
	defer func() {
		if resolved != nil {
			resolved.Close()
		}
	}()
	arm := func() {
		if resolved != nil {
			resolved.Close()
			resolved = nil
		}
		root, err := filepath.EvalSymlinks(filepath.Join(s.dir, "theme"))
		if err != nil {
			return // mid-swap: the next batch tries again
		}
		w, err := mygo.WatchFiles(ctx, root, mygo.FileWatchOptions{})
		if err != nil {
			return
		}
		resolved, armedRoot = w, root
		go pumpWatch(ctx, w, sig)
	}
	needArm := func() bool {
		if resolved == nil {
			return true
		}
		select {
		case <-resolved.Done(): // its folder was removed by a swap
			return true
		default:
		}
		root, err := filepath.EvalSymlinks(filepath.Join(s.dir, "theme"))
		return err != nil || root != armedRoot
	}

	arm()
	// WatchFiles does not report existing files; reread after both watches are enrolled to close
	// the gap between the constructor's initial read and watcher setup.
	s.reload(ctx, omarchyRetryWait)
	timer := time.NewTimer(omarchyDebounce)
	timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sig:
			timer.Reset(omarchyDebounce)
		case <-timer.C:
			if needArm() {
				arm()
			}
			s.reload(ctx, omarchyRetryWait)
		}
	}
}

// pumpWatch turns each batch of watch events into a signal. An error (the watch ended) also
// signals, so that the folder is looked at again.
func pumpWatch(ctx context.Context, w *mygo.FileWatcher, sig chan<- struct{}) {
	for {
		_, err := w.Next(ctx)
		select {
		case sig <- struct{}{}:
		default:
		}
		if err != nil {
			return
		}
	}
}
