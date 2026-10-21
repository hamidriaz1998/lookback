package bench

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hamidriaz1998/lookback/assets"
	"github.com/hamidriaz1998/lookback/internal/contract"
)

// TestPickedTextIsChunkedAndEveryPromptFitsTheCap: a picked text longer than one window is split by the chunker, so
// every prompt stays within the 512-token cap instead of one oversized prompt.
func TestPickedTextIsChunkedAndEveryPromptFitsTheCap(t *testing.T) {
	l := newTestLab(t)
	path := writePick(t, "notes.txt", strings.Repeat("lorem ipsum dolor ", 100))
	srv := &fakeServer{}
	if _, err := l.measure(newRun([]string{path}, combo(contract.QuantQ8_0)), srv); err != nil {
		t.Fatal(err)
	}
	got := srv.userPrompts("notes.txt")
	if len(got) < 2 {
		t.Fatalf("picked text sent as %d prompt(s), want several windows", len(got))
	}
	for _, p := range got {
		if len(p) > 512 {
			t.Errorf("prompt of %d tokens is over the 512-token cap", len(p))
		}
	}
}

// TestPickedDocumentIsExtractedBeforeEmbedding: an RTF picked file embeds its text, not its markup.
func TestPickedDocumentIsExtractedBeforeEmbedding(t *testing.T) {
	l := newTestLab(t)
	path := writePick(t, "report.rtf", `{\rtf1\ansi{\fonttbl{\f0 Arial;}}Quarterly results were strong.\par}`)
	srv := &fakeServer{}
	if _, err := l.measure(newRun([]string{path}, combo(contract.QuantQ8_0)), srv); err != nil {
		t.Fatal(err)
	}
	got := srv.userPrompts("report.rtf")
	if len(got) != 1 {
		t.Fatalf("picked document sent as %d prompts, want 1", len(got))
	}
	if strings.Contains(got[0], `\rtf1`) || strings.Contains(got[0], "fonttbl") {
		t.Errorf("prompt carries RTF markup: %q", got[0])
	}
	if !strings.Contains(got[0], "Quarterly results were strong.") {
		t.Errorf("prompt lacks the document text: %q", got[0])
	}
}

// TestPickedFileQualityNeedsAMatchingBF16Run: a picked file has quality only against a BF16 run of the same files.
// Without one the result shows n/a with a reason, and the bundled sample alone does not count.
func TestPickedFileQualityNeedsAMatchingBF16Run(t *testing.T) {
	l := newTestLab(t)
	picked := writePick(t, "a.txt", "alpha beta gamma")
	other := writePick(t, "b.txt", "delta epsilon")
	q8 := combo(contract.QuantQ8_0)

	res, err := l.measure(newRun([]string{picked}, q8), &fakeServer{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Quality != nil || res.QualityReason == "" {
		t.Fatalf("no BF16 run yet: quality %v, reason %q; want n/a with a reason", res.Quality, res.QualityReason)
	}

	if _, err := l.measure(newRun([]string{picked}, combo(contract.QuantBF16)), &fakeServer{}); err != nil {
		t.Fatal(err)
	}
	res, err = l.measure(newRun([]string{picked}, q8), &fakeServer{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Quality == nil || *res.Quality < 0.999 {
		t.Fatalf("matching BF16 run: quality %v, want about 1", res.Quality)
	}

	res, err = l.measure(newRun([]string{other}, q8), &fakeServer{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Quality != nil || res.QualityReason == "" {
		t.Fatalf("different files: quality %v, want n/a with a reason", res.Quality)
	}
}

// TestPickedImageIsEmbeddedWithTheSample: a picked image is embedded, so the sample's image count grows by one.
func TestPickedImageIsEmbeddedWithTheSample(t *testing.T) {
	l := newTestLab(t)
	path := filepath.Join(t.TempDir(), "shot.png")
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	img.Set(3, 3, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := &fakeServer{}
	if _, err := l.measure(newRun([]string{path}, combo(contract.QuantQ8_0)), srv); err != nil {
		t.Fatal(err)
	}
	imgs, err := assets.Images()
	if err != nil {
		t.Fatal(err)
	}
	if srv.images != len(imgs)+1 {
		t.Fatalf("server embedded %d images, want %d sample images plus the picked one", srv.images, len(imgs))
	}
}
