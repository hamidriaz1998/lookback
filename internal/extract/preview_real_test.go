package extract

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// TestRenderOfficePage converts a real Office file with LibreOffice. It skips when soffice or the sample is missing.
func TestRenderOfficePage(t *testing.T) {
	if _, err := exec.LookPath("soffice"); err != nil {
		t.Skip("soffice is not installed")
	}
	sample := os.Getenv("LOOKBACK_OFFICE_SAMPLE")
	if sample == "" {
		t.Skip("set LOOKBACK_OFFICE_SAMPLE to a docx, pptx or xlsx file")
	}
	r := NewRegistry()
	defer r.Close()
	r.PreviewDir = t.TempDir()
	img, err := r.RenderPage(context.Background(), sample, 1, 480)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() < 100 {
		t.Fatalf("page is %v", img.Bounds())
	}
}
