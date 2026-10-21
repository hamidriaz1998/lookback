package extract

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"io"
	"math"
	"os"
	"sync"
	"unicode"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/responses"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

const (
	// minPageText is the number of non-space characters below which a page is rendered as an image (SPEC section 9).
	minPageText = 200
	// renderDPI is the resolution of a rendered page, before the long-edge cap.
	renderDPI = 150
	// maxRenderEdge caps the long edge of a rendered page in pixels.
	maxRenderEdge = 1600
)

// PDF extracts each page of a PDF as one unit. A page with at least 200 non-space characters of text emits that
// text. Any other page is rendered at 150 DPI, with its long edge capped at 1600 px, and emits a PNG image unit.
// The PDF engine is PDFium compiled to WebAssembly, run by the wazero runtime, so there is no cgo. The runtime
// starts on first use and is shared by every extraction; Close releases it.
type PDF struct {
	gateMu sync.Mutex
	gate   chan struct{}
	pool   pdfium.Pool
	inst   pdfium.Pdfium
}

// NewPDF returns a PDF extractor. It does not start the runtime until the first Extract.
func NewPDF() *PDF { return &PDF{gate: make(chan struct{}, 1)} }

func (p *PDF) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.gateMu.Lock()
	if p.gate == nil {
		p.gate = make(chan struct{}, 1)
	}
	gate := p.gate
	p.gateMu.Unlock()
	select {
	case gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *PDF) release() { <-p.gate }

// instance starts the runtime on first use. The caller holds the extraction gate.
func (p *PDF) instance(ctx context.Context) (pdfium.Pdfium, error) {
	if p.inst != nil {
		return p.inst, nil
	}
	if p.pool == nil {
		runtimeConfig := wazero.NewRuntimeConfig().WithCoreFeatures(
			api.CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling).WithCloseOnContextDone(true)
		pool, err := webassembly.Init(webassembly.Config{
			MinIdle: 1, MaxIdle: 1, MaxTotal: 1, RuntimeConfig: runtimeConfig,
		})
		if err != nil {
			return nil, fmt.Errorf("pdfium: %w", err)
		}
		p.pool = pool
	}
	inst, err := p.pool.GetInstanceWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("pdfium: %w", err)
	}
	p.inst = inst
	return inst, nil
}

// Close releases the runtime. The extractor starts it again on the next Extract.
func (p *PDF) Close() error {
	if err := p.acquire(context.Background()); err != nil {
		return err
	}
	defer p.release()
	var err error
	if p.inst != nil {
		err = p.inst.Close()
	}
	if p.pool != nil {
		if perr := p.pool.Close(); err == nil {
			err = perr
		}
	}
	p.inst, p.pool = nil, nil
	return err
}

// Extract emits the units of the PDF f, one per page, in page order.
func (p *PDF) Extract(ctx context.Context, f contract.FileRec, emit func(contract.Unit) error) error {
	if err := p.acquire(ctx); err != nil {
		return err
	}
	defer p.release()
	file, err := os.Open(f.Path)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(contextReader{ctx: ctx, r: file})
	file.Close()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	inst, err := p.instance(ctx)
	if err != nil {
		return err
	}
	var doc *responses.OpenDocument
	interrupted, err := interruptPDFCall(ctx, inst.Kill, func() error {
		var err error
		doc, err = inst.OpenDocument(&requests.OpenDocument{File: &data})
		if err != nil {
			return fmt.Errorf("open pdf: %w", err)
		}
		count, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
		if err != nil {
			return fmt.Errorf("page count: %w", err)
		}
		for i := 0; i < count.PageCount; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			page := requests.Page{ByIndex: &requests.PageByIndex{Document: doc.Document, Index: i}}
			text, err := inst.GetPageText(&requests.GetPageText{Page: page})
			if err != nil {
				return fmt.Errorf("page %d text: %w", i+1, err)
			}
			if countNonSpace(text.Text) >= minPageText {
				if err := emitUnit(ctx, emit, textUnit(f, i+1, text.Text)); err != nil {
					return err
				}
				continue
			}
			img, err := renderPage(inst, page)
			if err != nil {
				return fmt.Errorf("page %d render: %w", i+1, err)
			}
			if err := emitUnit(ctx, emit, contract.Unit{FileID: f.ID, Unit: i + 1, Start: -1, End: -1, Image: img, EstTokens: 1500}); err != nil {
				return err
			}
		}
		return nil
	})
	if interrupted {
		p.inst = nil
	} else if doc != nil {
		inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})
	}
	return err
}

func interruptPDFCall(ctx context.Context, kill func() error, call func() error) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	done := make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		select {
		case <-ctx.Done():
			_ = kill()
			result <- true
		case <-done:
			result <- false
		}
	}()
	err := call()
	close(done)
	interrupted := <-result
	if err := ctx.Err(); err != nil {
		return interrupted, err
	}
	return interrupted, err
}

// renderPage renders one page at renderDPI, scaled down so its long edge is at most maxRenderEdge, and returns
// it as PNG.
func renderPage(inst pdfium.Pdfium, page requests.Page) ([]byte, error) {
	size, err := inst.GetPageSize(&requests.GetPageSize{Page: page})
	if err != nil {
		return nil, err
	}
	dpi := float64(renderDPI)
	if long := math.Max(size.Width, size.Height); long*dpi/72 > maxRenderEdge {
		dpi = maxRenderEdge * 72 / long
	}
	w := max(1, int(math.Round(size.Width*dpi/72)))
	h := max(1, int(math.Round(size.Height*dpi/72)))
	out, err := inst.RenderPageInPixels(&requests.RenderPageInPixels{Page: page, Width: w, Height: h})
	if err != nil {
		return nil, err
	}
	defer out.CleanupFunc()
	var buf bytes.Buffer
	if err := png.Encode(&buf, out.Result.RenderedImage); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// countNonSpace returns the number of characters in s that are not white space.
func countNonSpace(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}
