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

// noBF16 installs no combination.
func noBF16(contract.Combo) bool { return false }

// anyBF16 installs BF16 on every backend.
func anyBF16(c contract.Combo) bool { return isBF16(c) }

// TestPickedFileQualityNeedsAnInstalledBF16Combination: without an installed BF16 combination a picked-file run is n/a
// with a reason, and no BF16 server starts.
func TestPickedFileQualityNeedsAnInstalledBF16Combination(t *testing.T) {
	starts := &fakeStarts{}
	l := newFakeLab(t, noBF16, starts)
	picked := writePick(t, "a.txt", "alpha beta gamma")
	step := l.execute(newRun([]string{picked}, combo(contract.QuantQ8_0)))
	if step.State != contract.LabDone {
		t.Fatalf("run ended %v: %s", step.State, step.Err)
	}
	if res := step.Result.Bench; res.Quality != nil || res.QualityReason != "needs an installed BF16 combination" {
		t.Fatalf("quality %v, reason %q; want n/a with the installed-BF16 reason", res.Quality, res.QualityReason)
	}
	if got := starts.opts; len(got) != 1 {
		t.Fatalf("servers started %d, want only the run's own", len(got))
	}
}

// TestPickedFileQualityComparesWithInstalledBF16: with BF16 installed, a run's picked files are embedded again by a
// BF16 server on the run's backend, and the quality is their cosine against that reference. The fake's BF16 vectors
// point along another dimension, so the quality is 0, not 1.
func TestPickedFileQualityComparesWithInstalledBF16(t *testing.T) {
	starts := &fakeStarts{}
	l := newFakeLab(t, anyBF16, starts)
	picked := writePick(t, "a.txt", "alpha beta gamma")
	step := l.execute(newRun([]string{picked}, combo(contract.QuantQ8_0)))
	if step.State != contract.LabDone {
		t.Fatalf("run ended %v: %s", step.State, step.Err)
	}
	res := step.Result.Bench
	if res.Quality == nil || *res.Quality > 1e-9 {
		t.Fatalf("quality %v, want 0 against the BF16 reference", res.Quality)
	}
	if got := starts.opts; len(got) != 2 || got[0].Files.Model != "q8_0" || got[1].Files.Model != "bf16" {
		t.Fatalf("servers started %v, want the run then a BF16 reference", got)
	}
}

// TestPickedFileQualityFallsBackToBF16OnAnotherBackend: BF16 installed on Vulkan only still gives a CPU run its
// quality. The reference server is the Vulkan BF16 one (GPU on).
func TestPickedFileQualityFallsBackToBF16OnAnotherBackend(t *testing.T) {
	starts := &fakeStarts{}
	vulkanBF16 := func(c contract.Combo) bool { return c.Backend == contract.BackendVulkan && isBF16(c) }
	l := newFakeLab(t, vulkanBF16, starts)
	picked := writePick(t, "a.txt", "alpha beta gamma")
	step := l.execute(newRun([]string{picked}, combo(contract.QuantQ8_0)))
	if step.State != contract.LabDone {
		t.Fatalf("run ended %v: %s", step.State, step.Err)
	}
	if q := step.Result.Bench.Quality; q == nil || *q > 1e-9 {
		t.Fatalf("quality %v, want 0 against the Vulkan BF16 reference", q)
	}
	if got := starts.opts; len(got) != 2 || got[1].Files.Model != "bf16" || !got[1].GPU {
		t.Fatalf("servers started %+v, want the run then a Vulkan BF16 reference", got)
	}
}

// TestBF16RunIsCheckedAgainstTheEmbeddedReference: a BF16 run with picked files is its own reference for them, but
// its sample vectors are still checked against the embedded reference, and the quality is the lower of the two. The
// fake's BF16 vectors do not match the reference, so the quality must be below the bar; the picks alone would say 1.
func TestBF16RunIsCheckedAgainstTheEmbeddedReference(t *testing.T) {
	starts := &fakeStarts{}
	l := newFakeLab(t, anyBF16, starts)
	picked := writePick(t, "a.txt", "alpha beta gamma")
	step := l.execute(newRun([]string{picked}, combo(contract.QuantBF16)))
	if step.State != contract.LabDone {
		t.Fatalf("run ended %v: %s", step.State, step.Err)
	}
	if q := step.Result.Bench.Quality; q == nil || *q >= qualityBar {
		t.Fatalf("quality %v, want below %v: the sample does not match the reference", q, qualityBar)
	}
	if got := starts.opts; len(got) != 1 {
		t.Fatalf("servers started %d, want the run only", len(got))
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
