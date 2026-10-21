package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
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

const (
	maxSpreadsheetColumns = 16384
	maxSpreadsheetRows    = 1048576
	maxSpreadsheetCells   = 1 << 20
	maxSpreadsheetText    = 64 << 20
)

var (
	errMissingPart  = errors.New("missing Office part")
	errPartTooLarge = errors.New("office part is over the size limit")
)

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
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return emitUnit(ctx, emit, textUnit(f, n, text))
	}

	if ext == ".rtf" {
		raw, err := readFileLimited(ctx, f.Path)
		if err != nil {
			return err
		}
		text, err := rtfText(ctx, raw)
		if err != nil {
			return err
		}
		return unitOut(0, text)
	}

	zr, err := zip.OpenReader(f.Path)
	if err != nil {
		return fmt.Errorf("%s: %w", ext, err)
	}
	defer zr.Close()
	part := func(name string) ([]byte, error) { return zipPart(ctx, zr, name) }

	switch ext {
	case ".docx":
		data, err := part("word/document.xml")
		if err != nil {
			return err
		}
		text, err := wordText(ctx, data)
		if err != nil {
			return err
		}
		return unitOut(0, text)
	case ".pptx":
		slides, err := orderedRelationshipParts(ctx, zr, "ppt/presentation.xml", "sldId")
		if err != nil {
			return err
		}
		for i, name := range slides {
			data, err := part(name)
			if err != nil {
				return err
			}
			text, err := wordText(ctx, data)
			if err != nil {
				return err
			}
			if err := unitOut(i+1, text); err != nil {
				return err
			}
		}
		return nil
	case ".xlsx":
		return xlsxUnits(ctx, zr, unitOut)
	case ".odt", ".odp", ".ods":
		data, err := part("content.xml")
		if err != nil {
			return err
		}
		switch ext {
		case ".odt":
			text, err := odfParagraphs(ctx, data)
			if err != nil {
				return err
			}
			return unitOut(0, text)
		case ".odp":
			return odpUnits(ctx, data, unitOut)
		default:
			return odsUnits(ctx, data, unitOut)
		}
	}
	return ErrUnsupported
}

// readFileLimited reads a whole file, refusing one larger than maxEntryBytes.
func readFileLimited(ctx context.Context, name string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, r: f}, maxEntryBytes+1))
	if err == nil && len(data) > maxEntryBytes {
		return nil, fmt.Errorf("file is over %d bytes", maxEntryBytes)
	}
	return data, err
}

// zipPart returns the bytes of one part of an Office file.
func zipPart(ctx context.Context, zr *zip.ReadCloser, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, r: rc}, maxEntryBytes+1))
		closeErr := rc.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(data) > maxEntryBytes {
			return nil, fmt.Errorf("%s: %w (%d bytes)", name, errPartTooLarge, maxEntryBytes)
		}
		return data, nil
	}
	return nil, fmt.Errorf("%w: %s", errMissingPart, name)
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

// relationshipNamespaces are the namespaces of r:id on a presentation or workbook item: the ECMA-376 transitional
// and the ISO strict OOXML relationships namespaces.
var relationshipNamespaces = map[string]bool{
	"http://schemas.openxmlformats.org/officeDocument/2006/relationships": true,
	"http://purl.oclc.org/ooxml/officeDocument/relationships":             true,
}

type packageRelationship struct {
	ID         string `xml:"Id,attr"`
	Target     string `xml:"Target,attr"`
	TargetMode string `xml:"TargetMode,attr"`
}

// orderedRelationshipParts follows the ordered part IDs in an OOXML manifest and resolves them through its rels.
func orderedRelationshipParts(ctx context.Context, zr *zip.ReadCloser, manifest, itemName string) ([]string, error) {
	data, err := zipPart(ctx, zr, manifest)
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	var ids []string
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != itemName {
			continue
		}
		// The item's own numeric id (unqualified) sits beside its r:id; only the relationship ID counts.
		id := ""
		for _, attr := range start.Attr {
			if attr.Name.Local == "id" && relationshipNamespaces[attr.Name.Space] {
				id = attr.Value
				break
			}
		}
		if id == "" {
			return nil, fmt.Errorf("%s entry has no relationship id", itemName)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	relsName := path.Join(path.Dir(manifest), "_rels", path.Base(manifest)+".rels")
	data, err = zipPart(ctx, zr, relsName)
	if err != nil {
		return nil, err
	}
	var rels struct {
		Items []packageRelationship `xml:"Relationship"`
	}
	if err := xml.Unmarshal(data, &rels); err != nil {
		return nil, err
	}
	byID := make(map[string]packageRelationship, len(rels.Items))
	for _, rel := range rels.Items {
		if rel.ID == "" || rel.Target == "" {
			return nil, fmt.Errorf("invalid relationship in %s", relsName)
		}
		if _, exists := byID[rel.ID]; exists {
			return nil, fmt.Errorf("duplicate relationship %q in %s", rel.ID, relsName)
		}
		byID[rel.ID] = rel
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		rel, ok := byID[id]
		if !ok || rel.TargetMode == "External" {
			return nil, fmt.Errorf("unresolved internal relationship %q in %s", id, relsName)
		}
		u, err := url.Parse(rel.Target)
		if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(u.Path, `\`) {
			return nil, fmt.Errorf("invalid internal relationship target %q", rel.Target)
		}
		target, err := url.PathUnescape(u.EscapedPath())
		if err != nil {
			return nil, fmt.Errorf("invalid relationship target %q: %w", rel.Target, err)
		}
		if strings.HasPrefix(target, "/") {
			target = strings.TrimPrefix(target, "/")
		} else {
			target = path.Join(path.Dir(manifest), target)
		}
		target = path.Clean(target)
		if target == "." || target == ".." || strings.HasPrefix(target, "../") || path.IsAbs(target) {
			return nil, fmt.Errorf("relationship target escapes package: %q", rel.Target)
		}
		parts = append(parts, target)
	}
	return parts, nil
}

// wordText returns the text of a WordprocessingML or DrawingML part: the text of each t element, with a
// newline at the end of each p element and a tab for tab elements.
func wordText(ctx context.Context, data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var b strings.Builder
	inText := false
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
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

// xlsxUnits emits one unit per worksheet in workbook order.
func xlsxUnits(ctx context.Context, zr *zip.ReadCloser, unitOut func(int, string) error) error {
	var shared []string
	if data, err := zipPart(ctx, zr, "xl/sharedStrings.xml"); err == nil {
		if shared, err = sharedStrings(ctx, data); err != nil {
			return err
		}
	} else if !errors.Is(err, errMissingPart) {
		return err
	}
	sheets, err := orderedRelationshipParts(ctx, zr, "xl/workbook.xml", "sheet")
	if err != nil {
		return err
	}
	remaining := maxSpreadsheetText
	for i, name := range sheets {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := zipPart(ctx, zr, name)
		if err != nil {
			return err
		}
		text, err := sheetRowsLimit(ctx, data, shared, remaining)
		if err != nil {
			return err
		}
		if err := unitOut(i+1, text); err != nil {
			return err
		}
		remaining -= len(text)
	}
	return nil
}

// sharedStrings returns the text of each si element of a shared strings table.
func sharedStrings(ctx context.Context, data []byte) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out []string
	var cur *strings.Builder
	inText := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
				if cur != nil {
					return nil, errors.New("shared string item nested in another item")
				}
				cur = &strings.Builder{}
			case "t":
				inText = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				if cur == nil {
					return nil, errors.New("shared string item end without start")
				}
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
func sheetRows(ctx context.Context, data []byte, shared []string) (string, error) {
	return sheetRowsLimit(ctx, data, shared, maxSpreadsheetText)
}

func sheetRowsLimit(ctx context.Context, data []byte, shared []string, budget int) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var lines []string
	var row map[int]string
	var maxCol, rowNumber, nextCol, rowBytes int
	var cellRef, cellType string
	var cellVal *strings.Builder // text of the v or inline t element of the current cell
	inCell, inVal := false, false
	totalBytes := 0
	totalCells := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
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
				if row != nil {
					return "", errors.New("spreadsheet row nested in another row")
				}
				row, maxCol, nextCol, rowBytes = map[int]string{}, -1, 0, 0
				rowNumber++
				for _, a := range t.Attr {
					if a.Name.Local == "r" {
						n, err := strconv.Atoi(a.Value)
						if err != nil || n < 1 || n > maxSpreadsheetRows {
							return "", fmt.Errorf("invalid spreadsheet row reference %q", a.Value)
						}
						rowNumber = n
					}
				}
			case "c":
				if row == nil {
					return "", errors.New("spreadsheet cell outside a row")
				}
				if inCell {
					return "", errors.New("spreadsheet cell nested in another cell")
				}
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
				totalCells++
				if totalCells > maxSpreadsheetCells {
					return "", fmt.Errorf("spreadsheet cell count exceeds %d", maxSpreadsheetCells)
				}
				col := nextCol
				if cellRef != "" {
					var cellRow int
					col, cellRow, err = parseCellRef(cellRef)
					if err != nil {
						return "", err
					}
					if cellRow != rowNumber {
						return "", fmt.Errorf("cell %s does not belong to row %d", cellRef, rowNumber)
					}
				}
				if col < 0 || col >= maxSpreadsheetColumns {
					return "", fmt.Errorf("cell column %d is outside the spreadsheet limit", col+1)
				}
				val := cellVal.String()
				if cellType == "s" {
					n, err := strconv.Atoi(strings.TrimSpace(val))
					if err != nil || n < 0 || n >= len(shared) {
						return "", fmt.Errorf("invalid shared string index %q", val)
					}
					val = shared[n]
				}
				old := row[col]
				rowBytes += len(val) - len(old)
				maxCol = max(maxCol, col)
				if rowBytes+maxCol*3 > budget-totalBytes {
					return "", fmt.Errorf("spreadsheet text exceeds %d bytes", budget)
				}
				row[col] = val
				nextCol = max(nextCol, col+1)
			case "row":
				rowCells, rowMax, rowSize := row, maxCol, rowBytes
				row = nil
				if rowMax >= 0 {
					lineBytes := rowSize + rowMax*3
					if len(lines) > 0 {
						lineBytes++
					}
					if lineBytes > budget-totalBytes {
						return "", fmt.Errorf("spreadsheet text exceeds %d bytes", budget)
					}
					cells := make([]string, rowMax+1)
					for c, v := range rowCells {
						cells[c] = v
					}
					line := strings.Join(cells, " | ")
					if strings.Trim(line, " |") == "" {
						continue
					}
					lines = append(lines, line)
					totalBytes += len(line)
					if len(lines) > 1 {
						totalBytes++
					}
				}
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

// parseCellRef validates an A1 reference and returns its zero-based column and one-based row.
func parseCellRef(ref string) (int, int, error) {
	col, row, letters, digits := 0, 0, 0, 0
	for _, c := range ref {
		switch {
		case c >= 'A' && c <= 'Z' && digits == 0:
			letters++
			if letters > 3 {
				return 0, 0, fmt.Errorf("invalid spreadsheet cell reference %q", ref)
			}
			col = col*26 + int(c-'A'+1)
			if col > maxSpreadsheetColumns {
				return 0, 0, fmt.Errorf("spreadsheet column in %q exceeds XFD", ref)
			}
		case c >= '0' && c <= '9' && letters > 0:
			digits++
			row = row*10 + int(c-'0')
			if row > maxSpreadsheetRows {
				return 0, 0, fmt.Errorf("spreadsheet row in %q exceeds %d", ref, maxSpreadsheetRows)
			}
		default:
			return 0, 0, fmt.Errorf("invalid spreadsheet cell reference %q", ref)
		}
	}
	if letters == 0 || digits == 0 || row == 0 {
		return 0, 0, fmt.Errorf("invalid spreadsheet cell reference %q", ref)
	}
	return col - 1, row, nil
}

// columnIndex returns the zero-based column of a valid A1 reference, or -1 otherwise.
func columnIndex(ref string) int {
	col, _, err := parseCellRef(ref)
	if err != nil {
		return -1
	}
	return col
}

// odfParagraphs returns the paragraphs of an ODF content.xml, one per line.
func odfParagraphs(ctx context.Context, data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var b strings.Builder
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
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
func odpUnits(ctx context.Context, data []byte, unitOut func(int, string) error) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var page *strings.Builder
	n := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
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
func odsUnits(ctx context.Context, data []byte, unitOut func(int, string) error) error {
	return odsUnitsWithLimit(ctx, data, maxSpreadsheetText, unitOut)
}

func odsUnitsWithLimit(ctx context.Context, data []byte, limit int, unitOut func(int, string) error) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var lines []string
	var cells []string
	var cell *strings.Builder
	repeat := 1
	inTable := false
	n := 0
	usedBytes, tableBytes, totalCells := 0, 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
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
				inTable, lines, tableBytes = true, nil, 0
			case "table-row":
				cells = nil
			case "table-cell", "covered-table-cell":
				cell, repeat = &strings.Builder{}, 1
				for _, a := range t.Attr {
					if a.Name.Local == "number-columns-repeated" {
						var err error
						repeat, err = strconv.Atoi(a.Value)
						if err != nil {
							return fmt.Errorf("invalid ODS column repetition %q", a.Value)
						}
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
					if repeat > maxSpreadsheetColumns-len(cells) || repeat > maxSpreadsheetCells-totalCells {
						return fmt.Errorf("ODS spreadsheet cell count exceeds limit")
					}
					if len(text) > 0 && repeat > (limit-usedBytes-tableBytes)/len(text) {
						return fmt.Errorf("ODS spreadsheet text exceeds %d bytes", limit)
					}
					separators := repeat - 1
					if len(cells) > 0 {
						separators += repeat
					}
					addBytes := len(text)*repeat + separators*3
					if addBytes > limit-usedBytes-tableBytes {
						return fmt.Errorf("ODS spreadsheet text exceeds %d bytes", limit)
					}
					for i := 0; i < repeat; i++ {
						cells = append(cells, text)
					}
					totalCells += repeat
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
					lineBytes := 0
					for _, text := range cells {
						lineBytes += len(text)
					}
					lineBytes += max(0, len(cells)-1) * 3
					if lineBytes+max(0, len(lines)) > limit-usedBytes-tableBytes {
						return fmt.Errorf("ODS spreadsheet text exceeds %d bytes", limit)
					}
					lines = append(lines, strings.Join(cells, " | "))
					tableBytes += lineBytes
					if len(lines) > 1 {
						tableBytes++
					}
				}
			case "table":
				if inTable {
					n++
					text := strings.Join(lines, "\n")
					if len(text) > limit-usedBytes {
						return fmt.Errorf("ODS spreadsheet text exceeds %d bytes", limit)
					}
					if err := unitOut(n, text); err != nil {
						return err
					}
					usedBytes += len(text)
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
