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

// unpack extracts the archive at path, whose asset name is name, into dest. Every filesystem operation goes
// through an os.Root on dest, so symlinks already extracted cannot redirect a later entry outside it. Entry
// names and link targets are also checked lexically, so the refusal does not depend on the order of entries.
func unpack(path, name, dest string) error {
	_, err := unpackFiles(path, name, dest)
	return err
}

func unpackFiles(path, name, dest string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var files []string
	switch {
	case strings.HasSuffix(name, ".tar.gz"):
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		if err := untar(tar.NewReader(gz), root, func(name string) { files = append(files, filepath.ToSlash(name)) }); err != nil {
			return nil, err
		}
	case strings.HasSuffix(name, ".zip"):
		st, err := f.Stat()
		if err != nil {
			return nil, err
		}
		zr, err := zip.NewReader(f, st.Size())
		if err != nil {
			return nil, err
		}
		if err := unzip(zr, root, func(name string) { files = append(files, filepath.ToSlash(name)) }); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("download: %s is not a .tar.gz or .zip archive", name)
	}
	return files, nil
}

// entryName cleans an archive name and refuses one that is absolute or climbs out of the archive root.
func entryName(name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return "", fmt.Errorf("%w: %s", errUnsafe, name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", errUnsafe, name)
	}
	return clean, nil
}

func untar(tr *tar.Reader, root *os.Root, record func(string)) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name, err := entryName(hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(root, name, tr, fs.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Llama.cpp ships libraries as links. A target is only accepted when it is relative and stays
			// inside the archive lexically; Root confines the operations themselves.
			if filepath.IsAbs(hdr.Linkname) || !within(".", filepath.Join(filepath.Dir(name), hdr.Linkname)) {
				return fmt.Errorf("%w: link %s -> %s", errUnsafe, hdr.Name, hdr.Linkname)
			}
			if err := makeLink(root, name, func() error { return root.Symlink(hdr.Linkname, name) }); err != nil {
				return err
			}
		case tar.TypeLink:
			src, err := entryName(hdr.Linkname)
			if err != nil {
				return err
			}
			if err := makeLink(root, name, func() error { return root.Link(src, name) }); err != nil {
				return err
			}
		default:
			return fmt.Errorf("download: unsupported archive entry %s (type %c)", hdr.Name, hdr.Typeflag)
		}
		record(name)
	}
}

func unzip(zr *zip.Reader, root *os.Root, record func(string)) error {
	for _, zf := range zr.File {
		name, err := entryName(zf.Name)
		if err != nil {
			return err
		}
		if zf.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("download: archive entry %s is a link, which is refused", zf.Name)
		}
		if zf.FileInfo().IsDir() {
			if err := root.MkdirAll(name, 0o755); err != nil {
				return err
			}
			record(name)
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		record(name)
		err = writeFile(root, name, rc, zf.Mode())
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// writeFile writes r to name under root, with the owner's read and write bits set. It creates the parents
// and replaces an existing entry instead of writing through it.
func writeFile(root *os.Root, name string, r io.Reader, mode fs.FileMode) error {
	if err := mkParents(root, name); err != nil {
		return err
	}
	if err := root.Remove(name); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode.Perm()|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// makeLink replaces whatever is at name with the link that create makes.
func makeLink(root *os.Root, name string, create func() error) error {
	if err := mkParents(root, name); err != nil {
		return err
	}
	if err := root.Remove(name); err != nil && !os.IsNotExist(err) {
		return err
	}
	return create()
}

func mkParents(root *os.Root, name string) error {
	dir := filepath.Dir(name)
	if dir == "." {
		return nil
	}
	return root.MkdirAll(dir, 0o755)
}

// within reports whether p, a cleaned path, lies inside dir.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
