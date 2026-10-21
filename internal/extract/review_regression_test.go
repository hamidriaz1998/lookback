package extract

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

func TestSpreadsheetTextAndRepeatLimits(t *testing.T) {
	xml := []byte(`<office:document-content xmlns:office="office" xmlns:table="table" xmlns:text="text"><office:body><table:table>` +
		`<table:table-row><table:table-cell table:number-columns-repeated="4"><text:p>abc</text:p></table:table-cell></table:table-row>` +
		`</table:table></office:body></office:document-content>`)
	emitted := 0
	err := odsUnitsWithLimit(context.Background(), xml, 8, func(int, string) error { emitted++; return nil })
	if err == nil || emitted != 0 {
		t.Fatalf("ODS repeat over budget: emitted %d units, err %v", emitted, err)
	}

	row := []byte(`<worksheet><sheetData><row r="1"><c r="A1"><v>123456</v></c></row></sheetData></worksheet>`)
	if _, err := sheetRowsLimit(context.Background(), row, nil, 5); err == nil {
		t.Fatal("XLSX row over output budget was accepted")
	}
}

func TestXLSXSharedStringErrors(t *testing.T) {
	tests := []struct {
		name      string
		parts     map[string][]byte
		corrupt   []byte
		wantError bool
		wantErr   error // when set, the error must wrap it, so a read failure is not confused with a bad index
	}{
		{name: "missing table reference", parts: xlsxParts(`<c r="A1" t="s"><v>0</v></c>`, nil), wantError: true},
		{name: "invalid index", parts: xlsxParts(`<c r="A1" t="s"><v>1</v></c>`, []byte(`<sst><si><t>one</t></si></sst>`)), wantError: true},
		{name: "optional table absent", parts: xlsxParts(`<c r="A1" t="inlineStr"><is><t>inline</t></is></c>`, nil)},
		{name: "corrupt table", parts: xlsxParts(`<c r="A1" t="s"><v>0</v></c>`, []byte(`<sst><si><t>UniqueShared</t></si></sst>`)), corrupt: []byte("UniqueShared"), wantError: true, wantErr: zip.ErrChecksum},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := officeZip(tt.parts)
			if len(tt.corrupt) != 0 {
				at := bytes.Index(archive, tt.corrupt)
				if at < 0 {
					t.Fatal("shared string bytes not found in stored archive")
				}
				archive[at] ^= 1
			}
			_, units, err := extractOfficeBytes(t, ".xlsx", archive)
			if tt.wantError && err == nil {
				t.Fatalf("expected extraction error, got units %+v", units)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("shared table read error: got %v, want %v", err, tt.wantErr)
			}
			if !tt.wantError && (err != nil || len(units) != 1 || units[0].Text != "inline") {
				t.Fatalf("optional table: units %+v, err %v", units, err)
			}
		})
	}
}

func TestXLSXOversizedSharedStringsFail(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a shared table at the size limit; skipped under -short")
	}
	big := bytes.Repeat([]byte(" "), maxEntryBytes+1)
	_, units, err := extractOfficeBytes(t, ".xlsx", officeZip(xlsxParts(`<c r="A1" t="s"><v>0</v></c>`, big)))
	if !errors.Is(err, errPartTooLarge) || len(units) != 0 {
		t.Fatalf("oversized shared table: units %+v, err %v; want errPartTooLarge and no units", units, err)
	}
}

func TestOOXMLRelationshipTargetsStayInPackage(t *testing.T) {
	for _, target := range []string{"../../outside.xml", "https://example.invalid/slide.xml"} {
		t.Run(target, func(t *testing.T) {
			parts := map[string][]byte{
				"ppt/presentation.xml":            []byte(`<p:presentation xmlns:r="` + relNS + `"><p:sldIdLst><p:sldId r:id="r1"/></p:sldIdLst></p:presentation>`),
				"ppt/_rels/presentation.xml.rels": []byte(`<Relationships><Relationship Id="r1" Target="` + target + `"/></Relationships>`),
			}
			_, _, err := extractOfficeBytes(t, ".pptx", officeZip(parts))
			if err == nil {
				t.Fatalf("unsafe relationship target %q was accepted", target)
			}
		})
	}
}

func TestPDFInFlightCallIsInterruptedOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, killed := make(chan struct{}), make(chan struct{})
	type outcome struct {
		interrupted bool
		err         error
	}
	done := make(chan outcome, 1)
	go func() {
		interrupted, err := interruptPDFCall(ctx, func() error {
			close(killed)
			return nil
		}, func() error {
			close(started)
			<-killed
			return errors.New("worker interrupted")
		})
		done <- outcome{interrupted: interrupted, err: err}
	}()
	<-started
	cancel()
	select {
	case result := <-done:
		if !result.interrupted || !errors.Is(result.err, context.Canceled) {
			t.Fatalf("in-flight cancellation: interrupted=%v err=%v", result.interrupted, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight PDF call was not interrupted")
	}
}

func xlsxParts(cell string, shared []byte) map[string][]byte {
	return xlsxSheetParts(`<worksheet><sheetData><row r="1">`+cell+`</row></sheetData></worksheet>`, shared)
}

// xlsxSheetParts returns a one-sheet workbook whose worksheet part is sheet verbatim.
func xlsxSheetParts(sheet string, shared []byte) map[string][]byte {
	parts := map[string][]byte{
		"xl/workbook.xml":            []byte(`<workbook xmlns:r="` + relNS + `"><sheets><sheet name="Sheet" r:id="r1"/></sheets></workbook>`),
		"xl/_rels/workbook.xml.rels": []byte(`<Relationships><Relationship Id="r1" Target="worksheets/custom.xml"/></Relationships>`),
		"xl/worksheets/custom.xml":   []byte(sheet),
	}
	if len(shared) != 0 {
		parts["xl/sharedStrings.xml"] = shared
	}
	return parts
}

func officeZip(parts map[string][]byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range parts {
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		w, err := zw.CreateHeader(h)
		if err != nil {
			panic(err)
		}
		if _, err := w.Write(data); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func extractOfficeBytes(t *testing.T, ext string, archive []byte) (string, []unitResult, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input"+ext)
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	var units []unitResult
	err := (Office{}).Extract(context.Background(), contract.FileRec{ID: 1, Path: path, Kind: contract.KindOffice}, func(u contract.Unit) error {
		units = append(units, unitResult{Unit: u.Unit, Text: u.Text})
		return nil
	})
	return path, units, err
}

type unitResult struct {
	Unit int
	Text string
}

// TestOfficeMalformedStructureIsRejected covers R2 finding 1: well-formed XML whose row, cell or string-item
// nesting is impossible. Each input must return an extraction error; none may panic.
func TestOfficeMalformedStructureIsRejected(t *testing.T) {
	sst := func(body string) []byte { return []byte(`<sst>` + body + `</sst>`) }
	tests := []struct {
		name   string
		sheet  string
		shared []byte
	}{
		{name: "cell outside any row", sheet: `<c r="A1"><v>x</v></c>`},
		{name: "cell without reference outside any row", sheet: `<c><v>x</v></c>`},
		{name: "row nested in row", sheet: `<row r="1"><row r="2"><c r="A1"><v>x</v></c></row></row>`},
		{name: "cell nested in cell", sheet: `<row r="1"><c r="A1"><c r="B1"><v>x</v></c></c></row>`},
		{name: "shared string item nested in item", sheet: `<row r="1"><c r="A1" t="s"><v>0</v></c></row>`,
			shared: sst(`<si><si><t>x</t></si></si>`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("malformed structure panicked: %v", r)
				}
			}()
			parts := xlsxSheetParts(`<worksheet><sheetData>`+tt.sheet+`</sheetData></worksheet>`, tt.shared)
			_, units, err := extractOfficeBytes(t, ".xlsx", officeZip(parts))
			if err == nil {
				t.Fatalf("malformed structure accepted: units %+v", units)
			}
		})
	}
}

// TestPPTXSlideManifestWithNumericIDs covers R2 finding 2: an ordinary slide list carries a numeric id
// attribute next to the namespaced r:id, in either attribute order. Both must resolve to the relationship.
func TestPPTXSlideManifestWithNumericIDs(t *testing.T) {
	const pns = "http://schemas.openxmlformats.org/presentationml/2006/main"
	parts := map[string][]byte{
		"ppt/presentation.xml": []byte(`<p:presentation xmlns:p="` + pns + `" xmlns:r="` + relNS + `"><p:sldIdLst>` +
			`<p:sldId id="256" r:id="rId2"/><p:sldId r:id="rId1" id="257"/></p:sldIdLst></p:presentation>`),
		"ppt/_rels/presentation.xml.rels": []byte(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Target="slides/cover.xml"/><Relationship Id="rId2" Target="slides/roadmap.xml"/></Relationships>`),
		"ppt/slides/cover.xml":   []byte(slideXML("Cover")),
		"ppt/slides/roadmap.xml": []byte(slideXML("Roadmap")),
	}
	_, units, err := extractOfficeBytes(t, ".pptx", officeZip(parts))
	if err != nil {
		t.Fatalf("ordinary slide manifest rejected: %v", err)
	}
	if len(units) != 2 || strings.TrimSpace(units[0].Text) != "Roadmap" || strings.TrimSpace(units[1].Text) != "Cover" {
		t.Fatalf("slides: got %+v, want Roadmap then Cover", units)
	}
}

// TestRTFBinaryPayloadIsNotParsed covers R2 finding 5: the bytes after \binN are raw data, so braces and
// backslashes inside them must not change group state, in skipped groups or in text groups.
func TestRTFBinaryPayloadIsNotParsed(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name, src, want string
	}{
		{name: "brace in skipped picture group", src: `{\rtf1 before{\pict\bin2 }X}after}`, want: "beforeafter"},
		// The 3-byte payload is \, \, {: the \ must not start an escape and the { must not open a group.
		{name: "backslash in skipped picture group", src: `{\rtf1 a{\pict\bin3 \\{}b}c}`, want: "abc"},
		// The 3-byte payload is }, {, \: the } must not close the root group.
		{name: "binary bytes in text group are dropped", src: `{\rtf1 one\bin3 }{\}two}`, want: "onetwo"},
		{name: "zero-length payload", src: `{\rtf1 a\bin0 b}`, want: "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rtfText(ctx, []byte(tt.src))
			if err != nil || got != tt.want {
				t.Fatalf("rtfText(%q) = %q, %v; want %q", tt.src, got, err, tt.want)
			}
		})
	}
	for _, bad := range []string{`{\rtf1 a\bin5 xy}`, `{\rtf1 a\bin-1 b}`, `{\rtf1 a\bin b}`} {
		if got, err := rtfText(ctx, []byte(bad)); err == nil {
			t.Fatalf("rtfText(%q) = %q with no error; want a truncated or invalid \\bin error", bad, got)
		}
	}
}

// TestRegistryImageEdgeSetting covers R2 finding 6: the image edge is a setting (SPEC section 9, 1600 px by
// default). The registry the integration layer holds must apply a new edge to the next image extraction.
func TestRegistryImageEdgeSetting(t *testing.T) {
	r := NewRegistry()
	defer r.Close()
	longEdge := func() (int, int) {
		t.Helper()
		units := extractFile(t, r, "wide.png")
		if len(units) != 1 {
			t.Fatalf("wide.png: %d units, want 1", len(units))
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(units[0].Image))
		if err != nil {
			t.Fatal(err)
		}
		return cfg.Width, cfg.Height
	}
	if w, h := longEdge(); w != 1600 || h != 800 {
		t.Fatalf("default edge: got %dx%d, want 1600x800", w, h)
	}
	if err := r.SetImageEdge(800); err != nil {
		t.Fatal(err)
	}
	if w, h := longEdge(); w != 800 || h != 400 {
		t.Fatalf("edge 800: got %dx%d, want 800x400", w, h)
	}
	if err := r.SetImageEdge(0); err == nil {
		t.Fatal("edge 0 accepted")
	}
	if w, h := longEdge(); w != 800 || h != 400 {
		t.Fatalf("rejected edge changed the setting: got %dx%d", w, h)
	}
}

// buildContentPDF writes a PDF with one page per content stream. Every page can use font F1 (Helvetica). Each
// stream is Flate-compressed, so a long repetitive stream costs little file size.
func buildContentPDF(contents ...string) []byte {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"", // Pages, filled in below once the kids are known
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	kids := make([]string, len(contents))
	for i, c := range contents {
		pageObj, contentObj := 4+2*i, 5+2*i
		kids[i] = fmt.Sprintf("%d 0 R", pageObj)
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		zw.Write([]byte(c))
		zw.Close()
		objs = append(objs,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 3 0 R >> >> >>", contentObj),
			fmt.Sprintf("<< /Length %d /Filter /FlateDecode >>\nstream\n%s\nendstream", z.Len(), z.String()))
	}
	objs[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(contents))

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, body := range objs {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return out.Bytes()
}

// TestPDFCancelledStartupReturnsPromptly covers R2 finding 3: a cancelled extraction must not stay blocked
// while the PDFium runtime starts, and a later extraction must still work.
func TestPDFCancelledStartupReturnsPromptly(t *testing.T) {
	if testing.Short() {
		t.Skip("PDF runtime start is heavy; skipped under -short")
	}
	p := NewPDF()
	defer p.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	releaseStartup := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseStartup()
	p.startHook = func() { once.Do(func() { close(entered); <-release }) }

	f := contract.FileRec{ID: 1, Path: filepath.Join("testdata", "text-and-image.pdf"), Kind: contract.KindPDF}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { first <- p.Extract(ctx, f, func(contract.Unit) error { return nil }) }()
	select {
	case <-entered:
	case <-time.After(15 * time.Second):
		t.Fatal("runtime startup did not begin")
	}
	cancel()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled extraction returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled extraction waited for runtime startup")
	}

	// The startup finishes in the background; the next extraction uses it.
	releaseStartup()
	units := 0
	if err := p.Extract(context.Background(), f, func(contract.Unit) error { units++; return nil }); err != nil {
		t.Fatalf("extraction after a cancelled startup: %v", err)
	}
	if units != 3 {
		t.Fatalf("extraction after a cancelled startup: %d units, want 3", units)
	}
}

// TestPDFCloseDoesNotWaitForStartup covers R2 finding 3 from the shutdown side: Close must return while a
// cancelled startup is still running.
func TestPDFCloseDoesNotWaitForStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("PDF runtime start is heavy; skipped under -short")
	}
	p := NewPDF()
	entered, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	releaseStartup := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseStartup()
	p.startHook = func() { once.Do(func() { close(entered); <-release }) }

	f := contract.FileRec{ID: 1, Path: filepath.Join("testdata", "text-and-image.pdf"), Kind: contract.KindPDF}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { first <- p.Extract(ctx, f, func(contract.Unit) error { return nil }) }()
	select {
	case <-entered:
	case <-time.After(15 * time.Second):
		t.Fatal("runtime startup did not begin")
	}
	cancel()
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled extraction waited for runtime startup")
	}

	closed := make(chan error, 1)
	go func() { closed <- p.Close() }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited for a cancelled runtime startup")
	}
}

// TestPDFInFlightPDFiumCallIsInterrupted covers R2 finding 4 on the real PDFium runtime. Cancelling while page 2
// is inside one long PDFium render call must end that call promptly with the context error, and the extractor must
// still extract another PDF afterwards. Rendering is pure computation inside WebAssembly: without the runtime's
// close-on-context-done option the call runs to completion (about 3 s for this page), so the 1 s bound catches it.
func TestPDFInFlightPDFiumCallIsInterrupted(t *testing.T) {
	if testing.Short() {
		t.Skip("PDF runtime start and a multi-second PDFium call are heavy; skipped under -short")
	}
	dir := t.TempDir()
	// Few characters, so page 2 is rendered: 1000 four-segment paths make one PDFium render call of several seconds.
	heavy := "BT /F1 11 Tf 40 740 Td (Vector page.) Tj ET 0.1 w " + strings.Repeat("10 10 m 600 780 l 20 700 l 500 20 l 300 600 l S ", 1000)
	path := filepath.Join(dir, "heavy.pdf")
	if err := os.WriteFile(path, buildContentPDF("BT /F1 11 Tf 40 740 Td (First page.) Tj ET", heavy), 0o600); err != nil {
		t.Fatal(err)
	}
	p := NewPDF()
	defer p.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstPage := make(chan struct{})
	var once sync.Once
	done := make(chan error, 1)
	go func() {
		done <- p.Extract(ctx, contract.FileRec{ID: 1, Path: path, Kind: contract.KindPDF}, func(contract.Unit) error {
			once.Do(func() { close(firstPage) })
			return nil
		})
	}()
	select {
	case <-firstPage:
	case <-time.After(60 * time.Second):
		t.Fatal("first page did not extract")
	}
	// Page 1 is done, so page 2's heavy render call is running. Cancel while it is in flight.
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("interrupted extraction returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight PDFium call was not interrupted within 1 s")
	}

	units := 0
	if err := p.Extract(context.Background(), contract.FileRec{ID: 2, Path: filepath.Join("testdata", "text-and-image.pdf"), Kind: contract.KindPDF}, func(contract.Unit) error { units++; return nil }); err != nil {
		t.Fatalf("extraction after interruption: %v", err)
	}
	if units != 3 {
		t.Fatalf("extraction after interruption: %d units, want 3", units)
	}
}
