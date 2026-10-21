package extract

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/gif"
	_ "image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// extractFile runs the registry on testdata/name and returns the units it emits.
func extractFile(t *testing.T, r *Registry, name string) []contract.Unit {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	kind, ok := KindOf(path)
	if !ok {
		t.Fatalf("%s: no kind", name)
	}
	var units []contract.Unit
	f := contract.FileRec{ID: 7, Path: path, Kind: kind}
	if err := r.Extract(context.Background(), f, func(u contract.Unit) error {
		units = append(units, u)
		return nil
	}); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return units
}

func TestKindOf(t *testing.T) {
	cases := map[string]contract.Kind{
		"a.PNG": contract.KindImage, "b.jpeg": contract.KindImage, "c.gif": contract.KindImage, "d.webp": contract.KindImage,
		"d.md": contract.KindText, "e.go": contract.KindText, "f.htm": contract.KindText,
		"g.pdf": contract.KindPDF, "h.docx": contract.KindOffice, "i.rtf": contract.KindOffice,
		"j.exe": "", "k": "",
	}
	for name, want := range cases {
		got, ok := KindOf(name)
		if want == "" {
			if ok {
				t.Errorf("%s: kind %q, want none", name, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("%s: kind %q (%v), want %q", name, got, ok, want)
		}
	}
}

func TestImageUnits(t *testing.T) {
	r := NewRegistry()
	defer r.Close()

	// 2000x1000 PNG is over the long-edge cap, so it is decoded, downscaled and sent as PNG.
	units := extractFile(t, r, "wide.png")
	if len(units) != 1 || units[0].Start != -1 || units[0].End != -1 {
		t.Fatalf("wide.png: got %d units", len(units))
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(units[0].Image))
	if err != nil || format != "png" {
		t.Fatalf("wide.png output: format %q, err %v", format, err)
	}
	if cfg.Width != 1600 || cfg.Height != 800 {
		t.Fatalf("wide.png resized to %dx%d, want 1600x800", cfg.Width, cfg.Height)
	}

	// A JPEG within the cap passes through unchanged.
	units = extractFile(t, r, "photo.jpg")
	orig, _ := os.ReadFile(filepath.Join("testdata", "photo.jpg"))
	if len(units) != 1 || !bytes.Equal(units[0].Image, orig) {
		t.Fatalf("photo.jpg: want the file bytes unchanged")
	}

	// GIF: the first frame, as PNG. BMP: re-encoded as PNG.
	for _, name := range []string{"anim.gif", "tiny.bmp", "sample.webp"} {
		units = extractFile(t, r, name)
		if len(units) != 1 {
			t.Fatalf("%s: got %d units", name, len(units))
		}
		if _, format, err := image.DecodeConfig(bytes.NewReader(units[0].Image)); err != nil || format != "png" {
			t.Fatalf("%s output: format %q, err %v", name, format, err)
		}
		if name == "anim.gif" || name == "sample.webp" {
			first, _, err := image.Decode(bytes.NewReader(units[0].Image))
			if err != nil {
				t.Fatal(err)
			}
			got := first.At(0, 0)
			r, g, b, _ := got.RGBA()
			if name == "anim.gif" && (r < 0xF000 || g > 0x1000 || b > 0x1000) {
				t.Fatalf("GIF output pixel is not first-frame red: %v", got)
			}
			if name == "sample.webp" && (g < 0xF000 || r > 0x1000 || b > 0x1000) {
				t.Fatalf("WebP output pixel is not green: %v", got)
			}
		}
		if name == "anim.gif" {
			file, err := os.Open(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			animation, err := gif.DecodeAll(file)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			if len(animation.Image) != 2 {
				t.Fatalf("animated GIF fixture has %d frames; want two", len(animation.Image))
			}
		}
	}
}

func TestTextUnits(t *testing.T) {
	r := NewRegistry()
	defer r.Close()

	units := extractFile(t, r, "notes.txt")
	if len(units) != 1 || !strings.Contains(units[0].Text, "日本語") || units[0].End != len(units[0].Text) {
		t.Fatalf("notes.txt: got %+v", units)
	}
	units = extractFile(t, r, "utf16.txt")
	if len(units) != 1 || units[0].Text != "UTF-16 text file\n" {
		t.Fatalf("utf16.txt: got %+v", units)
	}
	if units := extractFile(t, r, "binary.txt"); len(units) != 0 {
		t.Fatalf("binary.txt: want no units, got %d", len(units))
	}
	units = extractFile(t, r, "page.html")
	if len(units) != 1 {
		t.Fatalf("page.html: got %d units", len(units))
	}
	text := units[0].Text
	for _, want := range []string{"Heading", "Body & text."} {
		if !strings.Contains(text, want) {
			t.Errorf("page.html text %q lacks %q", text, want)
		}
	}
	for _, bad := range []string{"secret", "color:red", "<h1>"} {
		if strings.Contains(text, bad) {
			t.Errorf("page.html text %q keeps %q", text, bad)
		}
	}
}

func TestOfficeUnits(t *testing.T) {
	r := NewRegistry()
	defer r.Close()

	type want struct {
		name  string
		units []string // expected text fragments, one per unit, in order
	}
	cases := []want{
		{"sample.docx", []string{"Quarterly plan\nBudget for Oslo travel"}},
		{"sample.pptx", []string{"Roadmap\nShip the indexer", "Title slide\nLaunch date"}},
		{"sample.xlsx", []string{"Totals", "Name |  | Amount\nRent |  | 1200"}},
		{"sample.odt", []string{"Meeting notes\nAgreed on the storage plan.\nNext steps"}},
		{"sample.odp", []string{"Slide one heading", "Slide two body"}},
		{"sample.ods", []string{"Item | Cost\nDesk | 40 | 40"}},
		{"sample.rtf", []string{"Hello bold world\nCafé and €euro\tdone\nLast line."}},
	}
	for _, c := range cases {
		units := extractFile(t, r, c.name)
		if len(units) != len(c.units) {
			t.Errorf("%s: %d units, want %d", c.name, len(units), len(c.units))
			continue
		}
		for i, u := range units {
			if !strings.Contains(u.Text, c.units[i]) {
				t.Errorf("%s unit %d: text %q, want it to contain %q", c.name, u.Unit, u.Text, c.units[i])
			}
			if u.Unit != i+1 && (c.name == "sample.pptx" || c.name == "sample.odp" || c.name == "sample.xlsx") {
				t.Errorf("%s unit %d: numbered %d, want %d", c.name, i, u.Unit, i+1)
			}
		}
	}

	// Office units carry the source text, not the zip or XML markup, and skipped RTF groups stay out.
	rtf := extractFile(t, r, "sample.rtf")[0].Text
	for _, bad := range []string{"Arial", "Test;", "89504e47", "fonttbl"} {
		if strings.Contains(rtf, bad) {
			t.Errorf("rtf text keeps %q: %q", bad, rtf)
		}
	}
}

func TestSheetRowsCellReferences(t *testing.T) {
	got, err := sheetRows(context.Background(), []byte(`<worksheet><sheetData><row><c><v>first</v></c><c><v>second</v></c></row></sheetData></worksheet>`), nil)
	if err != nil || got != "first | second" {
		t.Fatalf("omitted cell references: got %q, %v; want inferred consecutive columns", got, err)
	}
	if _, err := sheetRows(context.Background(), []byte(`<worksheet><sheetData><row><c r="XFE1"><v>outside</v></c></row></sheetData></worksheet>`), nil); err == nil {
		t.Fatal("column XFE, outside XLSX's XFD limit, was accepted")
	}
	if got := columnIndex("ZZZZZZ"); got != -1 {
		t.Fatalf("oversized column parsed as %d, want invalid", got)
	}
	if _, err := sheetRows(context.Background(), []byte(`<worksheet><sheetData><row r="1"><c r="ZZZZZZ1"><v>outside</v></c></row></sheetData></worksheet>`), nil); err == nil {
		t.Fatal("oversized cell reference was accepted")
	}
}

func TestRTFSupplementaryUnicodeAndGroupState(t *testing.T) {
	got, err := rtfText(context.Background(), []byte(`{\rtf1\uc1 \u-10179?\u-8704?}`))
	if want := "😀"; err != nil || got != want {
		t.Errorf("surrogate pair: got %q, %v; want %q", got, err, want)
	}
	got, err = rtfText(context.Background(), []byte(`{\rtf1\uc1 {\uc0 X}\u233?}`))
	if want := "Xé"; err != nil || got != want {
		t.Errorf("group-scoped uc: got %q, %v; want %q", got, err, want)
	}
}

func TestTextImageAndOfficeCancelBeforeFurtherEmission(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		err := (Text{}).Extract(ctx, contract.FileRec{ID: 1, Path: filepath.Join("testdata", "notes.txt")}, func(contract.Unit) error {
			cancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("text extraction after callback cancel: %v", err)
		}
	})
	t.Run("image", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		err := (Image{}).Extract(ctx, contract.FileRec{ID: 1, Path: filepath.Join("testdata", "wide.png")}, func(contract.Unit) error {
			cancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("image extraction after callback cancel: %v", err)
		}
	})
	t.Run("office", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		count := 0
		err := (Office{}).Extract(ctx, contract.FileRec{ID: 1, Path: filepath.Join("testdata", "sample.pptx")}, func(contract.Unit) error {
			count++
			cancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) || count != 1 {
			t.Fatalf("office extraction after first unit cancellation: emitted %d units, err %v", count, err)
		}
	})
}

func TestPDFUnits(t *testing.T) {
	if testing.Short() {
		t.Skip("PDF runtime start is heavy; skipped under -short")
	}
	r := NewRegistry()
	defer r.Close()

	units := extractFile(t, r, "text-and-image.pdf")
	if len(units) != 3 {
		t.Fatalf("got %d units, want 3 pages", len(units))
	}
	if units[0].Unit != 1 || !strings.Contains(units[0].Text, "Lookback indexes local files") {
		t.Errorf("page 1: unit %d text %q", units[0].Unit, units[0].Text)
	}
	// Page 2 has no text, only an image: a scanned page comes back as an image unit.
	for _, i := range []int{1, 2} {
		u := units[i]
		if u.Unit != i+1 || u.Text != "" || u.Start != -1 {
			t.Errorf("page %d: want an image unit, got unit %d text %q", i+1, u.Unit, u.Text)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(u.Image))
		if err != nil || format != "png" {
			t.Fatalf("page %d image: format %q, err %v", i+1, format, err)
		}
		if cfg.Height > maxRenderEdge || cfg.Width > maxRenderEdge {
			t.Errorf("page %d rendered at %dx%d, long edge over %d", i+1, cfg.Width, cfg.Height, maxRenderEdge)
		}
		if i == 1 {
			rendered, _, err := image.Decode(bytes.NewReader(u.Image))
			if err != nil {
				t.Fatal(err)
			}
			r, g, b, _ := rendered.At(rendered.Bounds().Dx()/2, rendered.Bounds().Dy()/2).RGBA()
			if r == 0xFFFF && g == 0xFFFF && b == 0xFFFF {
				t.Fatal("scanned-page fixture rendered as a blank white page")
			}
		}
	}
}

func TestPDFWaiterCancellationLeavesActiveExtractionAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("PDF runtime start is heavy; skipped under -short")
	}
	p := NewPDF()
	defer p.Close()
	f := contract.FileRec{ID: 1, Path: filepath.Join("testdata", "text-and-image.pdf"), Kind: contract.KindPDF}
	firstUnit, release, firstDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	go func() {
		n := 0
		firstDone <- p.Extract(context.Background(), f, func(contract.Unit) error {
			n++
			if n == 1 {
				close(firstUnit)
				<-release
			}
			return nil
		})
	}()
	select {
	case <-firstUnit:
	case <-time.After(15 * time.Second):
		t.Fatal("first PDF extraction did not reach its callback")
	}

	base, cancel := context.WithCancel(context.Background())
	ctx := &observedContext{Context: base, checked: make(chan struct{})}
	secondDone := make(chan error, 1)
	go func() { secondDone <- p.Extract(ctx, f, func(contract.Unit) error { return nil }) }()
	select {
	case <-ctx.checked:
	case <-time.After(time.Second):
		t.Fatal("second PDF extraction did not start")
	}
	// Let the second call reach the serialized PDF gate before cancelling its context.
	runtime.Gosched()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-secondDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("waiting extraction returned %v, want context.Canceled", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Error("cancelled PDF waiter remained blocked behind active extraction")
	}
	close(release)
	released = true
	if err := <-firstDone; err != nil {
		t.Errorf("active extraction was interrupted by cancelled waiter: %v", err)
	}
}

type observedContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (c *observedContext) Err() error {
	c.once.Do(func() { close(c.checked) })
	return c.Context.Err()
}
