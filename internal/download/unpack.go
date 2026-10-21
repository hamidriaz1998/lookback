package download

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// errUnsafe is returned for an archive entry that would write outside the destination folder.
var errUnsafe = errors.New("archive entry leaves the runtime folder")

// unpack extracts the archive at path, whose asset name is name, into dest. It refuses any entry whose
// path leaves dest, and any link whose target leaves dest (SPEC section 7).
func unpack(path, name, dest string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	switch {
	case strings.HasSuffix(name, ".tar.gz"):
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		return untar(tar.NewReader(gz), dest)
	case strings.HasSuffix(name, ".zip"):
		st, err := f.Stat()
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(f, st.Size())
		if err != nil {
			return err
		}
		return unzip(zr, dest)
	}
	return fmt.Errorf("download: %s is not a .tar.gz or .zip archive", name)
}

// within reports whether p, a cleaned path, lies inside dir.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// target maps an archive entry name to a path under dest, or errUnsafe.
func target(dest, name string) (string, error) {
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", errUnsafe
	}
	p := filepath.Join(dest, filepath.FromSlash(name))
	if !within(dest, p) {
		return "", errUnsafe
	}
	return p, nil
}

func untar(tr *tar.Reader, dest string) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p, err := target(dest, hdr.Name)
		if err != nil {
			return fmt.Errorf("%w: %s", err, hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(p, tr, fs.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Relative links are fine when they stay inside dest; llama.cpp ships libraries as links.
			if filepath.IsAbs(hdr.Linkname) || !within(dest, filepath.Join(filepath.Dir(p), hdr.Linkname)) {
				return fmt.Errorf("%w: link %s -> %s", errUnsafe, hdr.Name, hdr.Linkname)
			}
			if err := makeLink(p, func() error { return os.Symlink(hdr.Linkname, p) }); err != nil {
				return err
			}
		case tar.TypeLink:
			src, err := target(dest, hdr.Linkname)
			if err != nil {
				return fmt.Errorf("%w: hard link %s", err, hdr.Name)
			}
			if err := makeLink(p, func() error { return os.Link(src, p) }); err != nil {
				return err
			}
		default:
			return fmt.Errorf("download: unsupported archive entry %s (type %c)", hdr.Name, hdr.Typeflag)
		}
	}
}

func unzip(zr *zip.Reader, dest string) error {
	for _, zf := range zr.File {
		p, err := target(dest, zf.Name)
		if err != nil {
			return fmt.Errorf("%w: %s", err, zf.Name)
		}
		if zf.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("download: archive entry %s is a link, which is refused", zf.Name)
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		err = writeFile(p, rc, zf.Mode())
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// writeFile writes r to p with the owner's read and write bits set. It replaces an existing entry rather
// than writing through a link.
func writeFile(p string, r io.Reader, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode.Perm()|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// makeLink replaces whatever is at p with the link that create makes.
func makeLink(p string, create func() error) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return create()
}
