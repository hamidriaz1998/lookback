package ui

import (
	"bufio"
	"context"
	"fmt"
	"image"
	_ "image/gif" // decoders for the image kinds the index reads
	_ "image/jpeg"
	_ "image/png"
	"os"

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

// FileMedia decodes picture files from disk. It handles PNG, JPEG and GIF; other kinds need a renderer that the
// extraction package provides, so they return ErrNoPicture here.
type FileMedia struct{}

// Image implements Media.
func (FileMedia) Image(ctx context.Context, path string, edge int) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(bufio.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, ErrNoPicture)
	}
	return scaleTo(src, edge), nil
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
