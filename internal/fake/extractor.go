package fake

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"path/filepath"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Extractor is a contract.Extractor that makes units without reading files. Images come back
// as small generated PNGs, text as one unit, and the fixture's broken PDF fails the way SPEC §11 describes.
type Extractor struct{}

// NewExtractor returns an Extractor.
func NewExtractor() *Extractor { return &Extractor{} }

// Extract emits the units of f to emit.
func (e *Extractor) Extract(ctx context.Context, f contract.FileRec, emit func(contract.Unit) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch {
	case f.Path == brokenPDF:
		return errors.New("encrypted PDF: no text and the pages can't be rendered")
	case f.Kind == contract.KindImage:
		img, err := generatedPNG(f.ID)
		if err != nil {
			return err
		}
		return emit(contract.Unit{FileID: f.ID, Start: -1, End: -1, Image: img, EstTokens: 1500})
	default:
		text := "Extracted text of " + filepath.Base(f.Path) + "."
		return emit(contract.Unit{FileID: f.ID, Text: text, Start: 0, End: len(text), Snippet: text, EstTokens: len(text) / 4})
	}
}

// generatedPNG returns a 160 by 100 gradient PNG whose colors depend on seed.
func generatedPNG(seed int64) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 160, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 160; x++ {
			img.Set(x, y, color.RGBA{uint8(x + int(seed)), uint8(y * 2), uint8(seed * 37), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
