package extract

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// TestPDFSpeed times the PDF extractor on a generated 50-page PDF (40 text pages, 10 image-only pages), then on
// two 50-page PDFs of one kind each. It logs the cold run (which starts the runtime) and the warm runs. It skips
// under -short.
func TestPDFSpeed(t *testing.T) {
	if testing.Short() {
		t.Skip("PDF speed measurement skipped under -short")
	}
	dir := t.TempDir()
	mixed := make([]pdfPage, 50)
	for i := range mixed {
		if i%5 == 4 {
			mixed[i] = pdfPage{image: true}
		} else {
			mixed[i] = pdfPage{text: longText}
		}
	}
	write := func(name string, pages []pdfPage) contract.FileRec {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, buildPDF(pages), 0o644); err != nil {
			t.Fatal(err)
		}
		return contract.FileRec{ID: 1, Path: path, Kind: contract.KindPDF}
	}
	mixedFile := write("mixed.pdf", mixed)
	textFile := write("text.pdf", repeat(pdfPage{text: longText}, 50))
	imageFile := write("image.pdf", repeat(pdfPage{image: true}, 50))

	r := NewRegistry()
	defer r.Close()
	run := func(f contract.FileRec) (time.Duration, int) {
		n := 0
		start := time.Now()
		if err := r.Extract(t.Context(), f, func(contract.Unit) error { n++; return nil }); err != nil {
			t.Fatal(err)
		}
		return time.Since(start), n
	}

	cold, n := run(mixedFile)
	if n != 50 {
		t.Fatalf("mixed PDF: got %d units, want 50", n)
	}
	warm, _ := run(mixedFile)
	textTime, _ := run(textFile)
	imageTime, _ := run(imageFile)

	t.Logf("50-page PDF, 40 text + 10 scanned: cold %.2f s (starts the runtime), warm %.2f s",
		cold.Seconds(), warm.Seconds())
	t.Logf("50 text pages: %.2f s total, %.1f ms per page; 50 scanned pages: %.2f s total, %.1f ms per page",
		textTime.Seconds(), float64(textTime.Milliseconds())/50,
		imageTime.Seconds(), float64(imageTime.Milliseconds())/50)
}

func repeat(p pdfPage, n int) []pdfPage {
	out := make([]pdfPage, n)
	for i := range out {
		out[i] = p
	}
	return out
}
