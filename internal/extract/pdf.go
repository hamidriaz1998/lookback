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
	pool   pdfium.Pool   // guarded by the gate
	inst   pdfium.Pdfium // guarded by the gate
	// starting carries the result of a runtime startup that no extraction has adopted yet. The startup runs in its
	// own goroutine, so a cancelled extraction can return while it runs. Guarded by the gate.
	starting chan startResult
	// startHook, when set, runs at the start of each runtime startup. Tests use it to hold a startup.
	startHook func()
}

type startResult struct {
	pool pdfium.Pool
	inst pdfium.Pdfium
	err  error
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

// startRuntime creates the WebAssembly runtime when pool is nil, then borrows an instance from it. go-pdfium's
// startup calls (module compilation, pool preparation, worker creation) cannot be interrupted, so this runs without
// the gate and without a context. The caller stops waiting instead, and the result is adopted later.
func startRuntime(pool pdfium.Pool, hook func()) startResult {
	if hook != nil {
		hook()
	}
	if pool == nil {
		runtimeConfig := wazero.NewRuntimeConfig().WithCoreFeatures(
			api.CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling).WithCloseOnContextDone(true)
		created, err := webassembly.Init(webassembly.Config{
			MinIdle: 1, MaxIdle: 1, MaxTotal: 1, RuntimeConfig: runtimeConfig,
		})
		if err != nil {
			return startResult{err: fmt.Errorf("pdfium: %w", err)}
		}
		pool = created
	}
	inst, err := pool.GetInstanceWithContext(context.Background())
	if err != nil {
		return startResult{pool: pool, err: fmt.Errorf("pdfium: %w", err)}
	}
	return startResult{pool: pool, inst: inst}
}

// instance returns the runtime instance, starting the runtime on first use or after an interruption. The caller
// holds the extraction gate. If ctx ends first, it returns ctx.Err() and the startup stays pending: the next call
// adopts it, so an abandoned startup never leaves the extractor unusable.
func (p *PDF) instance(ctx context.Context) (pdfium.Pdfium, error) {
	if p.inst != nil {
		return p.inst, nil
	}
	if p.starting == nil {
		ch := make(chan startResult, 1)
		p.starting = ch
		go func(pool pdfium.Pool, hook func()) { ch <- startRuntime(pool, hook) }(p.pool, p.startHook)
	}
	select {
	case r := <-p.starting:
		p.starting = nil
		p.pool = r.pool
		if r.err != nil {
			return nil, r.err
		}
		p.inst = r.inst
		return r.inst, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close releases the runtime. The extractor starts it again on the next Extract. A startup still running is
// detached: Close does not wait for it, and its result is closed when it finishes.
func (p *PDF) Close() error {
	if err := p.acquire(context.Background()); err != nil {
		return err
	}
	defer p.release()
	var err error
	if p.inst != nil {
		err = p.inst.Close()
	}
	if p.starting != nil {
		go discardStartup(p.starting)
		p.starting = nil
	} else if p.pool != nil {
		err = firstErr(err, p.pool.Close())
	}
	p.inst, p.pool = nil, nil
	return err
}

// discardStartup closes what a detached startup produces.
func discardStartup(ch chan startResult) {
	r := <-ch
	if r.inst != nil {
		_ = r.inst.Close()
	}
	if r.pool != nil {
		_ = r.pool.Close()
	}
}

func firstErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
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
