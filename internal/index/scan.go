package index

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"image"
	_ "image/gif" // registers the decoders that image.DecodeConfig uses for the size rule
	_ "image/jpeg"
	_ "image/png"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/extract"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

// maxFileBytes is the size above which a file is skipped (SPEC section 9, Q15).
const maxFileBytes = 50 << 20

// minImageEdge is the smallest width and height of an indexed image (SPEC section 9, Q15).
const minImageEdge = 64

// excludedDirNames are directories that are never indexed (SPEC section 9, Q15). Dot-prefixed names are excluded too, and on Windows names with the hidden attribute.
var excludedDirNames = map[string]bool{
	".git": true, "node_modules": true, "__pycache__": true, ".venv": true, "venv": true,
	"target": true, "build": true, "dist": true, ".cache": true,
}

// excludedPath reports whether p, under fo, is excluded by the defaults or by the folder's ignore patterns. The
// last path element counts only for a directory; the patterns match the base name or the relative path.
func excludedPath(fo contract.Folder, p string, isDir bool) bool {
	rel, err := filepath.Rel(fo.Path, p)
	if err != nil || rel == "." {
		return false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		if strings.HasPrefix(part, ".") {
			return true
		}
		if isHidden(filepath.Join(fo.Path, filepath.Join(parts[:i+1]...))) { // the Windows hidden attribute
			return true
		}
		if (i < len(parts)-1 || isDir) && excludedDirNames[part] {
			return true
		}
	}
	for _, pat := range strings.Split(fo.Ignore, "\n") {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		if ok, _ := filepath.Match(pat, filepath.Base(p)); ok {
			return true
		}
		if ok, _ := filepath.Match(pat, rel); ok {
			return true
		}
	}
	return false
}

// tooBigOrTiny applies the file size and image size rules. An image whose header cannot be read is kept, so the
// extractor reports its error.
func tooBigOrTiny(kind contract.Kind, path string, info os.FileInfo) bool {
	if info.Size() > maxFileBytes {
		return true
	}
	if kind != contract.KindImage {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	return err == nil && (cfg.Width < minImageEdge || cfg.Height < minImageEdge)
}

// eligible applies the SPEC section 11 exclusion check to the file at p, under fo: the folder and file exclusions,
// a symlink that leaves the folder, a file that is not regular, and the size and image rules. It returns the
// file's stat when the file may be indexed.
func eligible(fo contract.Folder, kind contract.Kind, p string) (os.FileInfo, bool) {
	if excludedPath(fo, p, false) {
		return nil, false
	}
	li, err := os.Lstat(p)
	if err != nil {
		return nil, false
	}
	if li.Mode()&fs.ModeSymlink != 0 && !insideRoot(fo.Path, p) {
		return nil, false
	}
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	if tooBigOrTiny(kind, p, info) {
		return nil, false
	}
	return info, true
}

// forget deletes the file id from the store and from the queues and rows of both jobs, as a scan does for a
// file that disappeared or became excluded.
func (x *Indexer) forget(ctx context.Context, id int64) error {
	if err := x.store.DeleteFile(ctx, id); err != nil {
		return err
	}
	x.mu.Lock()
	delete(x.files, id)
	if x.active != nil {
		delete(x.active.rows, id)
	}
	if x.rebuild != nil {
		delete(x.rebuild.rows, id)
	}
	x.mu.Unlock()
	return nil
}

// scanFolder walks fo and makes the store match the disk: new and changed files are saved and queued where they
// need work, and files that vanished or became excluded are deleted. A walk error that is not on the root leaves
// the files of that directory alone.
func (x *Indexer) scanFolder(ctx context.Context, fo contract.Folder) error {
	x.scanMu.Lock()
	defer x.scanMu.Unlock()
	if err := x.live(); err != nil { // the lifetime may have ended while this scan waited for the lock
		return err
	}

	x.mu.Lock()
	known := map[string]contract.FileRec{}
	for _, f := range x.files {
		if f.FolderID == fo.ID {
			known[f.Path] = f
		}
	}
	x.mu.Unlock()

	seen := map[string]bool{}
	incomplete := false
	err := filepath.WalkDir(fo.Path, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			if p == fo.Path {
				return err
			}
			incomplete = true
			return nil
		}
		if p == fo.Path {
			return nil
		}
		if excludedPath(fo, p, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		kind, ok := extract.KindOf(p)
		if !ok {
			return nil
		}
		info, ok := eligible(fo, kind, p)
		if !ok {
			return nil
		}
		prev, had := known[p]
		if had && prev.Size == info.Size() && prev.MTime == info.ModTime().UnixNano() {
			seen[p] = true
			x.mu.Lock()
			x.enqueueLocked(prev.ID)
			x.mu.Unlock()
			return nil
		}
		rec := contract.FileRec{FolderID: fo.ID, Path: p, Kind: kind, Size: info.Size(), MTime: info.ModTime().UnixNano()}
		if had {
			rec.ID = prev.ID
		}
		if err := x.store.SaveFile(ctx, &rec); err != nil {
			return err
		}
		seen[p] = true
		x.mu.Lock()
		x.files[rec.ID] = rec
		x.enqueueLocked(rec.ID)
		x.mu.Unlock()
		return nil
	})
	if err != nil {
		return err
	}
	if incomplete {
		return nil
	}
	for p, prev := range known {
		if seen[p] {
			continue
		}
		if err := x.forget(ctx, prev.ID); err != nil {
			return err
		}
	}
	return nil
}

// insideRoot reports whether the symbolic link at p resolves to a path under root.
func insideRoot(root, p string) bool {
	target, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
