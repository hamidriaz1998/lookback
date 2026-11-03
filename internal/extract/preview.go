package extract

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrNoRenderer is returned by RenderPage when the file needs a converter that is not installed.
var ErrNoRenderer = errors.New("no converter for this file")

// officeConvertTimeout bounds one LibreOffice conversion.
const officeConvertTimeout = 90 * time.Second

var officeExts = map[string]bool{".docx": true, ".pptx": true, ".xlsx": true}

// RenderPage draws one page of a PDF, or of an Office file converted to PDF, for the preview. It is a
// ui.PageRenderer.
func (r *Registry) RenderPage(ctx context.Context, path string, page, edge int) (image.Image, error) {
	if officeExts[strings.ToLower(filepath.Ext(path))] {
		pdf, err := r.officePDF(ctx, path)
		if err != nil {
			return nil, err
		}
		path = pdf
	}
	return r.pdf.RenderPage(ctx, path, page, edge)
}

// officePDF converts path with LibreOffice and keeps the PDF under PreviewDir, named by the file's path, size
// and modification time, so a changed file converts again and an unchanged one converts once.
func (r *Registry) officePDF(ctx context.Context, path string) (string, error) {
	soffice, err := exec.LookPath("soffice")
	if err != nil || r.PreviewDir == "" {
		return "", ErrNoRenderer
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano())))
	out := filepath.Join(r.PreviewDir, hex.EncodeToString(sum[:8])+".pdf")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	if err := os.MkdirAll(r.PreviewDir, 0o755); err != nil {
		return "", err
	}
	// ponytail: one conversion at a time, each with its own profile so a running LibreOffice does not block it.
	// Run a pool if the grid of a large Office folder is too slow to fill.
	r.convertMu.Lock()
	defer r.convertMu.Unlock()
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	work, err := os.MkdirTemp(r.PreviewDir, "convert-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	ctx, cancel := context.WithTimeout(ctx, officeConvertTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, soffice, "--headless", "--norestore",
		"-env:UserInstallation=file://"+filepath.Join(work, "profile"),
		"--convert-to", "pdf", "--outdir", work, path)
	if msg, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("soffice: %w: %s", err, strings.TrimSpace(string(msg)))
	}
	made := filepath.Join(work, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+".pdf")
	if err := os.Rename(made, out); err != nil {
		return "", err
	}
	return out, nil
}
