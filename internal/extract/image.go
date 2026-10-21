package extract

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	_ "image/gif"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// maxLongEdge is the longest side of an image sent to the sidecar (SPEC section 9, a setting).
const maxLongEdge = 1600

// maxPixels refuses images whose decoded size would be too large to hold in memory.
const maxPixels = 100_000_000

// jpegQuality is the quality of resized JPEG output.
const jpegQuality = 90

// Image extracts one image unit per image file. PNG and JPEG that need no resize go to the sidecar as they
// are. Other formats, and any image with a long edge over maxLongEdge, are decoded, downscaled and re-encoded
// (JPEG stays JPEG; everything else becomes PNG, because the sidecar's decoder does not read WebP). GIF
// sends its first frame.
type Image struct{}

// Extract emits the one image unit of f.
func (Image) Extract(ctx context.Context, f contract.FileRec, emit func(contract.Unit) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.Open(f.Path)
	if err != nil {
		return err
	}
	defer file.Close()

	// Header-only read: the dimensions and the format, with no pixel decoding.
	reader := contextReader{ctx: ctx, r: file}
	cfg, format, err := image.DecodeConfig(reader)
	if err != nil {
		return fmt.Errorf("image header: %w", err)
	}
	if cfg.Width*cfg.Height > maxPixels {
		return fmt.Errorf("image is %dx%d, over the %d-pixel limit", cfg.Width, cfg.Height, maxPixels)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader = contextReader{ctx: ctx, r: file}
	long := max(cfg.Width, cfg.Height)

	var out []byte
	if (format == "png" || format == "jpeg") && long <= maxLongEdge {
		out, err = io.ReadAll(reader)
		if err != nil {
			return err
		}
	} else {
		img, _, err := image.Decode(reader)
		if err != nil {
			return fmt.Errorf("image decode: %w", err)
		}
		if long > maxLongEdge {
			img = downscale(img, maxLongEdge)
		}
		out, err = encode(img, format == "jpeg" && long > maxLongEdge)
		if err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return emitUnit(ctx, emit, contract.Unit{FileID: f.ID, Start: -1, End: -1, Image: out, EstTokens: 1500})
}

// downscale returns img scaled so its long edge is at most edge. It keeps the aspect ratio.
func downscale(img image.Image, edge int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w >= h {
		h = max(1, h*edge/w)
		w = edge
	} else {
		w = max(1, w*edge/h)
		h = edge
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}

// encode returns img as JPEG when asJPEG is set and as PNG otherwise.
func encode(img image.Image, asJPEG bool) ([]byte, error) {
	var buf bytes.Buffer
	var err error
	if asJPEG {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality})
	} else {
		err = png.Encode(&buf, img)
	}
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
