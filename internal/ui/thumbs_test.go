package ui

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// hugePNG writes a PNG whose header claims w×h pixels, followed by data that cannot decode. Only the header
// is valid, so a decoder that allocates from the header does so before it finds the data is short.
func hugePNG(t *testing.T, w, h uint32) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	// The IHDR chunk is at offset 8: length 13, type "IHDR", then width, height, and the rest of the header.
	binary.BigEndian.PutUint32(b[16:20], w)
	binary.BigEndian.PutUint32(b[20:24], h)
	binary.BigEndian.PutUint32(b[29:33], crc32.ChecksumIEEE(b[12:29]))
	path := filepath.Join(t.TempDir(), "huge.png")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestFileMediaRefusesHugeHeader checks that a 10000x10000 picture is refused from its header, without
// allocating its 400 MB of pixels (UI guide section 10).
func TestFileMediaRefusesHugeHeader(t *testing.T) {
	path := hugePNG(t, 10000, 10000)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := FileMedia{}.Image(context.Background(), path, 480)
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("Image returned no error for a 10000x10000 header")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 64<<20 {
		t.Fatalf("Image allocated %d MB for a refused picture", allocated>>20)
	}
}

// TestFileMediaCachesTiles checks that a grid tile is written as a JPEG in the cache folder, and that the
// second request reads it from there.
func TestFileMediaCachesTiles(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "pic.png")
	img := image.NewRGBA(image.Rect(0, 0, 900, 600))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	m := FileMedia{CacheDir: dir}
	first, err := m.Image(context.Background(), src, 480)
	if err != nil {
		t.Fatal(err)
	}
	if first.Bounds().Dx() != 480 {
		t.Fatalf("tile is %d px wide, want 480", first.Bounds().Dx())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".jpg" {
		t.Fatalf("cache folder holds %v, want one .jpg", entries)
	}
	if _, err := m.Image(context.Background(), src, 480); err != nil {
		t.Fatalf("second request: %v", err)
	}
	if entries2, _ := os.ReadDir(dir); len(entries2) != 1 {
		t.Fatalf("second request wrote %d files, want the cached one", len(entries2))
	}
}

// TestPreviewCacheStaysInBudget checks that repeated previews of large pictures stay within the preview
// budget, and that tiles keep their own budget.
func TestPreviewCacheStaysInBudget(t *testing.T) {
	th := newThumbs(solidMedia{})
	th.dispatch = func(f func()) { f() }
	big := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
	for i := 0; i < 200; i++ {
		key := thumbKey{path: string(rune('a'+i%26)) + string(rune('a'+i/26)), edge: 1600}
		th.finish(key, big, nil)
	}
	if got := th.bytes[classPreview]; got > previewBudget {
		t.Fatalf("previews hold %d MB, budget is %d MB", got>>20, previewBudget>>20)
	}
	if len(th.cache) == 0 {
		t.Fatal("cache is empty")
	}
	tile := image.NewRGBA(image.Rect(0, 0, 240, 180))
	th.finish(thumbKey{path: "tile", edge: 480}, tile, nil)
	if _, _, ok := th.get(thumbKey{path: "tile", edge: 480}); !ok {
		t.Fatal("a tile was evicted by preview pressure")
	}
}
