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
	if _, _, err := l.measure(newRun([]string{path}, combo(contract.QuantQ8_0)), srv); err != nil {
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
	if _, _, err := l.measure(newRun([]string{path}, combo(contract.QuantQ8_0)), srv); err != nil {
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

// TestPickedFileQualityNeedsAnInstalledBF16Combination: without an installed BF16 combination a picked-file run is n/a
// with a reason, and no BF16 server starts.
func TestPickedFileQualityNeedsAnInstalledBF16Combination(t *testing.T) {
	starts := &fakeStarts{}
	l := newFakeLab(t, false, starts)
	picked := writePick(t, "a.txt", "alpha beta gamma")
	step := l.execute(newRun([]string{picked}, combo(contract.QuantQ8_0)))
	if step.State != contract.LabDone {
		t.Fatalf("run ended %v: %s", step.State, step.Err)
	}
	if res := step.Result.Bench; res.Quality != nil || res.QualityReason != "needs an installed BF16 combination" {
		t.Fatalf("quality %v, reason %q; want n/a with the installed-BF16 reason", res.Quality, res.QualityReason)
	}
	if got := starts.models; len(got) != 1 {
		t.Fatalf("servers started %v, want only the run's own", got)
	}
}

// TestPickedFileQualityComparesWithInstalledBF16: with BF16 installed, a run's picked files are embedded again by a
// BF16 server on the run's backend, and the quality is their cosine against that reference. The fake's BF16 vectors
// point along another dimension, so the quality is 0, not 1.
func TestPickedFileQualityComparesWithInstalledBF16(t *testing.T) {
	starts := &fakeStarts{}
	l := newFakeLab(t, true, starts)
	picked := writePick(t, "a.txt", "alpha beta gamma")
	step := l.execute(newRun([]string{picked}, combo(contract.QuantQ8_0)))
	if step.State != contract.LabDone {
		t.Fatalf("run ended %v: %s", step.State, step.Err)
	}
	res := step.Result.Bench
	if res.Quality == nil || *res.Quality > 1e-9 {
		t.Fatalf("quality %v, want 0 against the BF16 reference", res.Quality)
	}
	if got := starts.models; len(got) != 2 || got[0] != "q8_0" || got[1] != "bf16" {
		t.Fatalf("servers started %v, want the run then a BF16 reference", got)
	}
}

// TestBF16RunIsItsOwnPickReference: a BF16 run of picked files is their reference, so its quality is 1 and it starts
// no second server.
func TestBF16RunIsItsOwnPickReference(t *testing.T) {
	starts := &fakeStarts{}
	l := newFakeLab(t, true, starts)
	picked := writePick(t, "a.txt", "alpha beta gamma")
	step := l.execute(newRun([]string{picked}, combo(contract.QuantBF16)))
	if step.State != contract.LabDone {
		t.Fatalf("run ended %v: %s", step.State, step.Err)
	}
	if q := step.Result.Bench.Quality; q == nil || *q < 0.999999 {
		t.Fatalf("quality %v, want 1", q)
	}
	if got := starts.models; len(got) != 1 {
		t.Fatalf("servers started %v, want the run only", got)
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
	if _, _, err := l.measure(newRun([]string{path}, combo(contract.QuantQ8_0)), srv); err != nil {
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
