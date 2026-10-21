package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
