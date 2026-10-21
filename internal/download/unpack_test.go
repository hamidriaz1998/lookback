package download

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// tarWithLinks builds a .tar.gz with the given regular files and symbolic links (name -> target).
func tarWithLinks(t *testing.T, files map[string]string, links map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	for name, target := range links {
		tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: target, Mode: 0o777})
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// llama.cpp ships libraries as links inside its own folder; those extract.
func TestUnpackKeepsInsideLinks(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "a.tar.gz")
	os.WriteFile(arc, tarWithLinks(t, map[string]string{"lib/libx.so.0": "lib"}, map[string]string{"lib/libx.so": "libx.so.0"}), 0o644)
	dest := filepath.Join(dir, "out")
	if err := unpack(arc, "a.tar.gz", dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "lib", "libx.so"))
	if err != nil || string(got) != "lib" {
		t.Fatalf("link does not read the target: %q, %v", got, err)
	}
}

// Links that point outside the folder are refused, whether absolute or relative.
func TestUnpackRefusesEscapingLinks(t *testing.T) {
	for name, target := range map[string]string{"abs": "/etc/passwd", "rel": "../../secret"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			arc := filepath.Join(dir, "a.tar.gz")
			os.WriteFile(arc, tarWithLinks(t, nil, map[string]string{"bin/evil": target}), 0o644)
			err := unpack(arc, "a.tar.gz", filepath.Join(dir, "out"))
			if !errors.Is(err, errUnsafe) {
				t.Fatalf("err = %v, want errUnsafe", err)
			}
		})
	}
}

// Ordered entries: a -> ., then b -> a/.. (lexically inside, really the parent once a is followed), then a
// regular file written through b. Root must refuse the write; the parent of dest must stay clean.
func TestUnpackRefusesChainedSymlinkEscape(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "a", Typeflag: tar.TypeSymlink, Linkname: ".", Mode: 0o777})
	tw.WriteHeader(&tar.Header{Name: "b", Typeflag: tar.TypeSymlink, Linkname: "a/..", Mode: 0o777})
	tw.WriteHeader(&tar.Header{Name: "b/escape.txt", Typeflag: tar.TypeReg, Size: 3, Mode: 0o644})
	tw.Write([]byte("bad"))
	tw.Close()
	gz.Close()

	dir := t.TempDir()
	arc := filepath.Join(dir, "chain.tar.gz")
	os.WriteFile(arc, buf.Bytes(), 0o644)
	dest := filepath.Join(dir, "out")
	if err := unpack(arc, "chain.tar.gz", dest); err == nil {
		t.Fatal("a chained symlink escape was extracted")
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Fatal("escape.txt was written outside the runtime folder")
	}
}
