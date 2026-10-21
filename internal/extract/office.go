package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// maxEntryBytes caps the size of one decompressed part of an Office file, so a zip bomb cannot exhaust memory.
const maxEntryBytes = 64 << 20

// maxRepeat caps how many copies of a repeated spreadsheet cell are kept, so a cell repeated to the end of the
// sheet does not produce a huge row.
const maxRepeat = 64

// Office extracts the text of DOCX, PPTX, XLSX, ODT, ODP, ODS and RTF files. A DOCX, ODT or RTF file is one unit;
// a PPTX slide, an ODP page, an XLSX sheet or an ODS sheet is one unit numbered from 1. Units with no text emit
// nothing.
type Office struct{}

// Extract emits the units of f.
func (Office) Extract(ctx context.Context, f contract.FileRec, emit func(contract.Unit) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(f.Path))
	unitOut := func(n int, text string) error {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return emit(textUnit(f, n, text))
	}

	if ext == ".rtf" {
		raw, err := readFileLimited(f.Path)
		if err != nil {
			return err
		}
		return unitOut(0, rtfText(raw))
	}

	zr, err := zip.OpenReader(f.Path)
	if err != nil {
		return fmt.Errorf("%s: %w", ext, err)
	}
	defer zr.Close()
	part := func(name string) ([]byte, error) { return zipPart(zr, name) }

	switch ext {
	case ".docx":
		data, err := part("word/document.xml")
		if err != nil {
			return err
		}
		text, err := wordText(data)
		if err != nil {
			return err
		}
		return unitOut(0, text)
	case ".pptx":
		slides := zipNames(zr, "ppt/slides/slide", ".xml")
		for i, name := range slides {
			data, err := part(name)
			if err != nil {
				return err
			}
			text, err := wordText(data)
			if err != nil {
				return err
			}
			if err := unitOut(i+1, text); err != nil {
				return err
			}
		}
		return nil
	case ".xlsx":
		return xlsxUnits(zr, unitOut)
	case ".odt", ".odp", ".ods":
		data, err := part("content.xml")
		if err != nil {
			return err
		}
		switch ext {
		case ".odt":
			text, err := odfParagraphs(data)
			if err != nil {
				return err
			}
			return unitOut(0, text)
		case ".odp":
			return odpUnits(data, unitOut)
		default:
			return odsUnits(data, unitOut)
		}
	}
	return ErrUnsupported
}

// readFileLimited reads a whole file, refusing one larger than maxEntryBytes.
func readFileLimited(name string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxEntryBytes+1))
}

// zipPart returns the bytes of one part of an Office file.
func zipPart(zr *zip.ReadCloser, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, maxEntryBytes+1))
		if err != nil {
			return nil, err
		}
		if len(data) > maxEntryBytes {
			return nil, fmt.Errorf("%s: part is over %d bytes", name, maxEntryBytes)
		}
		return data, nil
	}
	return nil, fmt.Errorf("missing part %s", name)
}

// zipNames returns the parts whose names are prefix + number + suffix, sorted by number.
func zipNames(zr *zip.ReadCloser, prefix, suffix string) []string {
	type numbered struct {
		name string
		n    int
	}
	var found []numbered
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, prefix) || !strings.HasSuffix(f.Name, suffix) {
			continue
		}
		num, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(f.Name, prefix), suffix))
		if err != nil {
			continue
		}
		found = append(found, numbered{f.Name, num})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].n < found[j].n })
	names := make([]string, len(found))
	for i, n := range found {
		names[i] = n.name
	}
	return names
}

// wordText returns the text of a WordprocessingML or DrawingML part: the text of each t element, with a
// newline at the end of each p element and a tab for tab elements.
func wordText(data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var b strings.Builder
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return b.String(), nil
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "tab":
				b.WriteByte('\t')
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				b.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				b.Write(t)
			}
		}
	}
}

// xlsxUnits emits one unit per worksheet: rows as "A | B | C" lines, read through the shared strings table.
func xlsxUnits(zr *zip.ReadCloser, unitOut func(int, string) error) error {
	var shared []string
	if data, err := zipPart(zr, "xl/sharedStrings.xml"); err == nil {
		if shared, err = sharedStrings(data); err != nil {
			return err
		}
	}
	for i, name := range zipNames(zr, "xl/worksheets/sheet", ".xml") {
		data, err := zipPart(zr, name)
		if err != nil {
			return err
		}
		text, err := sheetRows(data, shared)
		if err != nil {
			return err
		}
		if err := unitOut(i+1, text); err != nil {
			return err
		}
	}
	return nil
}

// sharedStrings returns the text of each si element of a shared strings table.
func sharedStrings(data []byte) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out []string
	var cur *strings.Builder
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				cur = &strings.Builder{}
			case "t":
				inText = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				out = append(out, cur.String())
				cur = nil
			case "t":
				inText = false
			}
		case xml.CharData:
			if inText && cur != nil {
				cur.Write(t)
			}
		}
	}
}

// sheetRows renders a worksheet: one line per row, cells separated by " | ", in their column positions.
func sheetRows(data []byte, shared []string) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var lines []string
	var row map[int]string
	var maxCol int
	var cellRef, cellType string
	var cellVal *strings.Builder // text of the v or inline t element of the current cell
	inCell, inVal := false, false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				row, maxCol = map[int]string{}, 0
			case "c":
				inCell = true
				cellRef, cellType = "", ""
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "r":
						cellRef = a.Value
					case "t":
						cellType = a.Value
					}
				}
				cellVal = &strings.Builder{}
			case "v", "t":
				inVal = inCell
			}
		case xml.CharData:
			if inVal {
				cellVal.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v", "t":
				inVal = false
			case "c":
				inCell = false
				col := columnIndex(cellRef)
				val := cellVal.String()
				if cellType == "s" {
					if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil && n >= 0 && n < len(shared) {
						val = shared[n]
					}
				}
				if val != "" {
					row[col] = val
					maxCol = max(maxCol, col)
				}
			case "row":
				cells := make([]string, maxCol+1)
				for c, v := range row {
					cells[c] = v
				}
				if line := strings.Join(cells, " | "); strings.Trim(line, " |") != "" {
					lines = append(lines, line)
				}
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

// columnIndex returns the zero-based column of a cell reference such as "B3".
func columnIndex(ref string) int {
	col := 0
	for _, c := range ref {
		if c < 'A' || c > 'Z' {
			break
		}
		col = col*26 + int(c-'A'+1)
	}
	return col - 1
}

// odfParagraphs returns the paragraphs of an ODF content.xml, one per line.
func odfParagraphs(data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return b.String(), nil
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "s":
				b.WriteByte(' ')
			case "tab":
				b.WriteByte('\t')
			case "line-break":
				b.WriteByte('\n')
			}
		case xml.EndElement:
			if t.Name.Local == "p" || t.Name.Local == "h" {
				b.WriteByte('\n')
			}
		case xml.CharData:
			b.Write(t)
		}
	}
}

// odpUnits emits one unit per draw:page of an ODP content.xml.
func odpUnits(data []byte, unitOut func(int, string) error) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var page *strings.Builder
	n := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "page" {
				page = &strings.Builder{}
			}
			if page != nil {
				switch t.Name.Local {
				case "s":
					page.WriteByte(' ')
				case "tab":
					page.WriteByte('\t')
				case "line-break":
					page.WriteByte('\n')
				}
			}
		case xml.EndElement:
			if t.Name.Local == "page" && page != nil {
				n++
				if err := unitOut(n, page.String()); err != nil {
					return err
				}
				page = nil
			} else if page != nil && (t.Name.Local == "p" || t.Name.Local == "h") {
				page.WriteByte('\n')
			}
		case xml.CharData:
			if page != nil {
				page.Write(t)
			}
		}
	}
}

// odsUnits emits one unit per table:table of an ODS content.xml, rows as "A | B | C" lines.
func odsUnits(data []byte, unitOut func(int, string) error) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var lines []string
	var cells []string
	var cell *strings.Builder
	repeat := 1
	inTable := false
	n := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "table":
				inTable, lines = true, nil
			case "table-row":
				cells = nil
			case "table-cell", "covered-table-cell":
				cell, repeat = &strings.Builder{}, 1
				for _, a := range t.Attr {
					if a.Name.Local == "number-columns-repeated" {
						repeat, _ = strconv.Atoi(a.Value)
						repeat = min(max(repeat, 1), maxRepeat)
					}
				}
			case "s":
				if cell != nil {
					cell.WriteByte(' ')
				}
			case "tab":
				if cell != nil {
					cell.WriteByte('\t')
				}
			case "line-break":
				if cell != nil {
					cell.WriteByte('\n')
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "table-cell", "covered-table-cell":
				if cell != nil {
					text := strings.TrimSpace(cell.String())
					for i := 0; i < repeat; i++ {
						cells = append(cells, text)
					}
				}
				cell = nil
			case "p":
				if cell != nil {
					cell.WriteByte(' ')
				}
			case "table-row":
				for len(cells) > 0 && cells[len(cells)-1] == "" {
					cells = cells[:len(cells)-1]
				}
				if len(cells) > 0 {
					lines = append(lines, strings.Join(cells, " | "))
				}
			case "table":
				if inTable {
					n++
					if err := unitOut(n, strings.Join(lines, "\n")); err != nil {
						return err
					}
					inTable = false
				}
			}
		case xml.CharData:
			if cell != nil {
				cell.Write(t)
			}
		}
	}
}
