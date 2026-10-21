package ui

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// recentLimit is how many files the Search page shows before the first query (UI guide section 3.4).
const recentLimit = 24

// recentPool is how many of the newest files the snapshot keeps, so the type filter can show recentLimit files of
// one kind. ponytail: the store's RecentItems has no kind argument, so the filter runs on this pool; a kind
// argument on RecentItems would make the filter exact past 240 files.
const recentPool = 240

// snapshot is the data the screens read. buildSnapshot makes it off the main thread; the App swaps it in.
type snapshot struct {
	ready    bool
	gens     []contract.Generation
	active   *contract.Generation
	folders  []folderView
	files    map[int64]contract.FileRec
	index    map[int64]contract.FileIndex // rows of the active generation, by file ID
	combos   []contract.Installed
	errors   []fileError
	recent   []contract.Hit
	settings map[string]string
	indexSig indexSig // the active generation and its done rows; a change means the results may change
	err      error
}

// indexSig identifies the state of the active index: its generation and how many files it has done.
type indexSig struct {
	gen, done int64
}

// diskPart is the size of one part of the data folder.
type diskPart struct {
	name  string
	bytes int64
}

// folderView is a watched folder with what the screens show about it.
type folderView struct {
	folder  contract.Folder
	counts  map[contract.Kind]int
	files   int
	errors  int
	latest  int64  // newest modification time of its files, Unix nanoseconds
	missing bool   // the folder is not on disk (an unmounted drive, say)
	denied  string // why the folder can't be read, for display
}

// fileError is a file whose last indexing failed in the active generation.
type fileError struct {
	file contract.FileRec
	msg  string
	ix   contract.FileIndex
}

// buildSnapshot reads the stores and the disk. It runs on a goroutine.
func buildSnapshot(ctx context.Context, d Deps) snapshot {
	s := snapshot{ready: true, settings: map[string]string{}, files: map[int64]contract.FileRec{}, index: map[int64]contract.FileIndex{}}
	fail := func(err error) snapshot {
		log.Printf("ui: load: %v", err)
		s.err = err
		return s
	}
	for _, key := range []string{"onboarded", "onboard_step", "onboard_combo", "battery_pause", "max_image_edge", "hotkey", "theme", "thumb_size", "preview_width"} {
		v, ok, err := d.Store.GetSetting(ctx, key)
		if err != nil {
			return fail(err)
		}
		if ok {
			s.settings[key] = v
		}
	}
	gens, err := d.Store.ListGenerations(ctx)
	if err != nil {
		return fail(err)
	}
	s.gens = gens
	for i := range gens {
		if gens[i].State == contract.GenActive {
			s.active = &gens[i]
		}
	}
	combos, err := d.Store.ListCombos(ctx)
	if err != nil {
		return fail(err)
	}
	s.combos = combos
	folders, err := d.Store.ListFolders(ctx)
	if err != nil {
		return fail(err)
	}
	if s.active != nil {
		rows, err := d.Store.ListFileIndex(ctx, s.active.ID)
		if err != nil {
			return fail(err)
		}
		for _, r := range rows {
			s.index[r.FileID] = r
		}
		recent, err := d.Store.RecentItems(ctx, s.active.ID, recentPool)
		if err != nil {
			return fail(err)
		}
		s.recent = recent
	}
	for _, f := range folders {
		fv := folderView{folder: f}
		files, err := d.Store.ListFiles(ctx, f.ID)
		if err != nil {
			return fail(err)
		}
		fv.counts = map[contract.Kind]int{}
		for _, rec := range files {
			s.files[rec.ID] = rec
			fv.counts[rec.Kind]++
			fv.files++
			fv.latest = max(fv.latest, rec.MTime)
			if ix, ok := s.index[rec.ID]; ok && ix.Status == contract.StatusError {
				fv.errors++
				s.errors = append(s.errors, fileError{file: rec, msg: ix.Error, ix: ix})
			}
		}
		if _, err := os.Stat(f.Path); errors.Is(err, os.ErrNotExist) {
			fv.missing = true
		} else if err != nil {
			fv.denied = err.Error()
		}
		s.folders = append(s.folders, fv)
	}
	if s.active != nil {
		s.indexSig.gen = s.active.ID
		for _, ix := range s.index {
			if ix.Status == contract.StatusDone {
				s.indexSig.done++
			}
		}
	}
	return s
}

// diskUsage sums the sizes of the top-level entries of dir, one part per entry, largest first. It stops when ctx
// ends. It walks the whole folder, so the app runs it only when the Settings page shows the storage.
func diskUsage(ctx context.Context, dir string) []diskPart {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []diskPart
	for _, e := range entries {
		var n int64
		_ = filepath.WalkDir(filepath.Join(dir, e.Name()), func(_ string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err == nil && !d.IsDir() {
				if info, ierr := d.Info(); ierr == nil {
					n += info.Size()
				}
			}
			return nil
		})
		out = append(out, diskPart{name: e.Name(), bytes: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].bytes > out[j].bytes })
	return out
}

// setting returns the value of key, or def when it is not set.
func (s snapshot) setting(key, def string) string {
	if v, ok := s.settings[key]; ok {
		return v
	}
	return def
}

// intSetting parses an integer setting, or returns def.
func (s snapshot) intSetting(key string, def int) int {
	if n, err := strconv.Atoi(s.setting(key, "")); err == nil {
		return n
	}
	return def
}
