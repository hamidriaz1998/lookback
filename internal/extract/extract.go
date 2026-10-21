// Package extract turns files into units: one extractor per file kind (images, text, PDF and Office/RTF),
// with a registry by extension. Images come back resized and encoded (SPEC section 9).
package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// extensions maps each indexed extension to its kind (SPEC section 9, file types).
var extensions = map[string]contract.Kind{}

func init() {
	add := func(kind contract.Kind, exts ...string) {
		for _, e := range exts {
			extensions[e] = kind
		}
	}
	add(contract.KindImage, ".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp")
	add(contract.KindText,
		".txt", ".md", ".markdown", ".rst", ".log", ".json", ".yaml", ".yml", ".toml", ".csv", ".tsv",
		".html", ".htm", ".xml", ".ini", ".cfg", ".conf",
		// source code
		".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".rs", ".c", ".h", ".cpp", ".hpp", ".cc", ".java",
		".kt", ".swift", ".rb", ".php", ".sh", ".fish", ".lua", ".sql", ".css", ".scss", ".sass", ".less")
	add(contract.KindPDF, ".pdf")
	add(contract.KindOffice, ".docx", ".pptx", ".xlsx", ".odt", ".odp", ".ods", ".rtf")
}

// KindOf returns the kind of a file from its extension, case-insensitively. ok is false for files the
// index does not take.
func KindOf(path string) (kind contract.Kind, ok bool) {
	kind, ok = extensions[strings.ToLower(filepath.Ext(path))]
	return kind, ok
}

// ErrUnsupported is returned for a file whose kind has no extractor.
var ErrUnsupported = errors.New("no extractor for this file kind")

// Registry holds one extractor per kind and dispatches by the kind of a FileRec.
type Registry struct {
	extractors map[contract.Kind]contract.Extractor
	pdf        *PDF
}

// NewRegistry returns a registry with the extractors of every kind. The PDF extractor starts its
// WebAssembly runtime on first use; Close releases it.
func NewRegistry() *Registry {
	pdf := NewPDF()
	return &Registry{
		extractors: map[contract.Kind]contract.Extractor{
			contract.KindImage:  Image{},
			contract.KindText:   Text{},
			contract.KindPDF:    pdf,
			contract.KindOffice: Office{},
		},
		pdf: pdf,
	}
}

// Extract streams the units of f to emit, using the extractor of f.Kind.
func (r *Registry) Extract(ctx context.Context, f contract.FileRec, emit func(contract.Unit) error) error {
	ex, ok := r.extractors[f.Kind]
	if !ok {
		return fmt.Errorf("%s: %w", f.Path, ErrUnsupported)
	}
	return ex.Extract(ctx, f, emit)
}

// Close releases the PDF runtime, if it started.
func (r *Registry) Close() error {
	return r.pdf.Close()
}

// snippetLen is the length of a unit's snippet in characters (SPEC section 9).
const snippetLen = 300

// snippet returns the first 300 characters of s, whitespace collapsed to single spaces.
func snippet(s string) string {
	out := strings.Join(strings.Fields(s), " ")
	if r := []rune(out); len(r) > snippetLen {
		out = string(r[:snippetLen])
	}
	return out
}

// textUnit builds a whole-text unit for f. The text is the full content; the pipeline chunks it.
func textUnit(f contract.FileRec, unit int, text string) contract.Unit {
	return contract.Unit{FileID: f.ID, Unit: unit, Start: 0, End: len(text), Text: text, Snippet: snippet(text),
		EstTokens: len(text) / 4}
}

func emitUnit(ctx context.Context, emit func(contract.Unit) error, unit contract.Unit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := emit(unit); err != nil {
		return err
	}
	return ctx.Err()
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
