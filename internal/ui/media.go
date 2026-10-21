package ui

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // decoders for the image kinds the index reads
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/image/draw"
)

// Media decodes the pictures the UI shows: thumbnails and the preview of an image. It is the only place the
// UI reads file content, and it runs on worker goroutines, never on the main thread (UI guide section 10).
type Media interface {
	// Image returns the picture at path, downscaled so its longest edge is at most edge pixels. It returns an
	// error wrapping os.ErrNotExist when the file is gone, and ErrNoPicture for a file that holds no picture.
	Image(ctx context.Context, path string, edge int) (image.Image, error)
}

// ErrNoPicture is returned by Media for a file the UI shows with its icon, such as a text file.
var ErrNoPicture = fmt.Errorf("file holds no picture")

// ErrTooLarge is returned by Media for a picture with more pixels than maxDecodePixels. It is refused from its
// header, so none of its pixels are allocated.
var ErrTooLarge = errors.New("picture is too large to preview")

// maxDecodePixels bounds what the UI decodes. A decoded picture takes 4 bytes a pixel, so this is about 160 MB
// per decode, and at most decodeWorkers decode at once.
const maxDecodePixels = 40 << 20

// tileEdgeMax is the longest edge of a grid tile's decode: twice the largest tile (240 DIPs). Larger edges are
// previews, which the preview budget holds.
const tileEdgeMax = 480

// FileMedia decodes picture files from disk. It handles PNG, JPEG and GIF; other kinds need a renderer that the
// extraction package provides, so they return ErrNoPicture here.
//
// CacheDir, when set, keeps the grid tiles as JPEG files named by a hash of the path, size, modification
// time and edge (UI guide section 10: thumbs/<hash>.jpg). A changed file gets a new name, so the cache never
// serves a stale tile.
type FileMedia struct {
	CacheDir string
}

// Image implements Media.
func (m FileMedia) Image(ctx context.Context, path string, edge int) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	cache := m.tilePath(path, info, edge)
	if cache != "" {
		if img, ok := readTile(cache); ok {
			return img, nil
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(bufio.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, ErrNoPicture)
	}
	if cfg.Width*cfg.Height > maxDecodePixels {
		return nil, fmt.Errorf("%s: %w", path, ErrTooLarge)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	src, _, err := image.Decode(bufio.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, ErrNoPicture)
	}
	out := scaleTo(src, edge)
	if cache != "" {
		writeTile(cache, out)
	}
	return out, nil
}

// tilePath returns the cache file of a tile of path at edge, or "" when tiles are not cached.
func (m FileMedia) tilePath(path string, info os.FileInfo, edge int) string {
	if m.CacheDir == "" || edge <= 0 || edge > tileEdgeMax {
		return ""
	}
	sum := sha1.Sum([]byte(path + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" +
		strconv.FormatInt(info.ModTime().UnixNano(), 10) + "\x00" + strconv.Itoa(edge)))
	return filepath.Join(m.CacheDir, hex.EncodeToString(sum[:])+".jpg")
}

// readTile decodes a cached tile. A missing or unreadable file is a miss.
func readTile(path string) (image.Image, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	img, err := jpeg.Decode(bufio.NewReader(f))
	if err != nil {
		return nil, false
	}
	return img, true
}

// writeTile stores a tile in the cache. It writes a temporary file and renames it, so a reader never sees a
// partial tile. A failed write costs only a later decode, so its error is dropped.
func writeTile(path string, img image.Image) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "tile-*.part")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if err := jpeg.Encode(tmp, img, &jpeg.Options{Quality: 85}); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(tmp.Name(), path)
}

// scaleTo returns src scaled down so its longest edge is at most edge pixels. Smaller pictures come back as they are.
func scaleTo(src image.Image, edge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	long := max(w, h)
	if edge <= 0 || long <= edge {
		return src
	}
	dw := max(1, w*edge/long)
	dh := max(1, h*edge/long)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}
