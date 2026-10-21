package extract

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"
)

// Fixtures are self-made. TestWriteFixtures regenerates everything in testdata when
// LOOKBACK_WRITE_FIXTURES=1; the other tests only read the committed files.
//
//	LOOKBACK_WRITE_FIXTURES=1 go test -run TestWriteFixtures ./internal/extract/

func TestWriteFixtures(t *testing.T) {
	if os.Getenv("LOOKBACK_WRITE_FIXTURES") != "1" {
		t.Skip("set LOOKBACK_WRITE_FIXTURES=1 to regenerate testdata")
	}
	dir := "testdata"
	files := map[string][]byte{}
	add := func(name string, data []byte) { files[name] = data }

	add("sample.docx", zipBytes(map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p><w:r><w:t>Quarterly plan</w:t></w:r></w:p>
<w:p><w:r><w:t xml:space="preserve">Budget for </w:t></w:r><w:r><w:t>Oslo travel</w:t></w:r></w:p>
</w:body></w:document>`,
	}))
	add("sample.pptx", zipBytes(map[string]string{
		"ppt/slides/slide1.xml": slideXML("Title slide", "Launch date"),
		"ppt/slides/slide2.xml": slideXML("Roadmap", "Ship the indexer"),
		"ppt/slides/slide3.xml": `<?xml version="1.0"?><p:sld xmlns:p="x" xmlns:a="y"><p:cSld><p:spTree/></p:cSld></p:sld>`,
	}))
	add("sample.xlsx", zipBytes(map[string]string{
		"xl/sharedStrings.xml": `<?xml version="1.0"?><sst><si><t>Name</t></si><si><t>Amount</t></si><si><t>Rent</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<?xml version="1.0"?><worksheet><sheetData>
<row r="1"><c r="A1" t="s"><v>0</v></c><c r="C1" t="s"><v>1</v></c></row>
<row r="2"><c r="A2" t="s"><v>2</v></c><c r="C2"><v>1200</v></c></row>
</sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml": `<?xml version="1.0"?><worksheet><sheetData>
<row r="1"><c r="B1" t="inlineStr"><is><t>Totals</t></is></c></row>
</sheetData></worksheet>`,
	}))
	add("sample.odt", zipBytes(map[string]string{
		"mimetype":    "application/vnd.oasis.opendocument.text",
		"content.xml": odfDoc(`<text:h>Meeting notes</text:h><text:p>Agreed on <text:span>the storage</text:span> plan.</text:p><text:p>Next<text:s/>steps</text:p>`),
	}))
	add("sample.odp", zipBytes(map[string]string{
		"content.xml": odfDoc(`<draw:page><draw:frame><draw:text-box><text:p>Slide one heading</text:p></draw:text-box></draw:frame></draw:page>` +
			`<draw:page><draw:frame><draw:text-box><text:p>Slide two body</text:p></draw:text-box></draw:frame></draw:page>`),
	}))
	add("sample.ods", zipBytes(map[string]string{
		"content.xml": odfDoc(`<table:table table:name="Costs"><table:table-row>` +
			`<table:table-cell><text:p>Item</text:p></table:table-cell><table:table-cell><text:p>Cost</text:p></table:table-cell>` +
			`</table:table-row><table:table-row>` +
			`<table:table-cell><text:p>Desk</text:p></table:table-cell><table:table-cell table:number-columns-repeated="2"><text:p>40</text:p></table:table-cell>` +
			`</table:table-row></table:table>` +
			`<table:table table:name="Empty"><table:table-row><table:table-cell/></table:table-row></table:table>`),
	}))
	add("sample.rtf", []byte(`{\rtf1\ansi\deff0{\fonttbl{\f0 Arial;}}{\colortbl;\red0\green0\blue0;}
{\*\generator Test;}\pard Hello \b bold\b0  world\par
Caf\'e9 and \u8364?euro\tab done\line
{\pict\pngblip 89504e47}Last line.\par}`))

	add("notes.txt", []byte("Plain UTF-8 notes with café and 日本語.\n"))
	add("utf16.txt", utf16LE("UTF-16 text file\n"))
	add("binary.txt", append([]byte("header"), 0, 1, 2, 3))
	add("page.html", []byte(`<html><head><title>T</title><style>p{color:red}</style></head>
<body><script>var secret = 1;</script><h1>Heading</h1><p>Body &amp; text.</p></body></html>`))

	add("wide.png", encodePNG(gradient(2000, 1000)))
	add("photo.jpg", encodeJPEG(gradient(1000, 500)))
	add("anim.gif", encodeGIF(gradient(120, 80)))
	add("tiny.bmp", bmp24(3, 2))

	add("text-and-image.pdf", buildPDF([]pdfPage{{text: longText}, {image: true}, {text: "Page three text"}}))

	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// zipBytes returns a zip archive of files, entries sorted by name.
func zipBytes(files map[string]string) []byte {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, _ := zw.Create(n)
		w.Write([]byte(files[n]))
	}
	zw.Close()
	return buf.Bytes()
}

func slideXML(paras ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><p:sld xmlns:p="x" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:txBody>`)
	for _, p := range paras {
		fmt.Fprintf(&b, `<a:p><a:r><a:t>%s</a:t></a:r></a:p>`, p)
	}
	b.WriteString(`</p:txBody></p:sld>`)
	return b.String()
}

func odfDoc(body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><office:document-content ` +
		`xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" ` +
		`xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" ` +
		`xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0" ` +
		`xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0"><office:body>` + body + `</office:body></office:document-content>`
}

func utf16LE(s string) []byte {
	out := []byte{0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune(s)) {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}

func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	return img
}

func encodePNG(img image.Image) []byte {
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func encodeJPEG(img image.Image) []byte {
	var b bytes.Buffer
	jpeg.Encode(&b, img, &jpeg.Options{Quality: 85})
	return b.Bytes()
}

func encodeGIF(img *image.RGBA) []byte {
	pal := image.NewPaletted(img.Bounds(), palette.Plan9)
	draw.Draw(pal, img.Bounds(), img, img.Bounds().Min, draw.Src)
	var b bytes.Buffer
	if err := gif.Encode(&b, pal, nil); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// bmp24 returns a 24-bit uncompressed BMP of w by h pixels.
func bmp24(w, h int) []byte {
	row := (w*3 + 3) &^ 3
	size := 54 + row*h
	b := make([]byte, size)
	copy(b, "BM")
	binary.LittleEndian.PutUint32(b[2:], uint32(size))
	binary.LittleEndian.PutUint32(b[10:], 54)
	binary.LittleEndian.PutUint32(b[14:], 40)
	binary.LittleEndian.PutUint32(b[18:], uint32(w))
	binary.LittleEndian.PutUint32(b[22:], uint32(h))
	binary.LittleEndian.PutUint16(b[26:], 1)
	binary.LittleEndian.PutUint16(b[28:], 24)
	for i := 54; i < size; i++ {
		b[i] = byte(i * 7)
	}
	return b
}

// pdfPage describes one page for buildPDF: a text page, or a page whose only content is an image (a scan).
type pdfPage struct {
	text  string
	image bool
}

// longText is more than 200 non-space characters, so a page with it is a text page (SPEC section 9).
const longText = "Lookback indexes local files for semantic search. This page holds a long paragraph so that " +
	"text extraction returns it as a text unit rather than rendering the page. The words here are ordinary " +
	"English, and they repeat the idea that every page with enough characters keeps its text in the index."

// buildPDF writes a PDF with one page per spec. Objects are written by hand with a computed xref table.
// Page i uses objects 4+3i (page), 5+3i (content) and 6+3i (image, or null for a text page).
func buildPDF(pages []pdfPage) []byte {
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	raw := make([]byte, 0, 40*40*3+40)
	for y := 0; y < 40; y++ {
		raw = append(raw, 0)
		for x := 0; x < 40; x++ {
			raw = append(raw, byte(x*6), byte(y*6), 90)
		}
	}
	zw.Write(raw)
	zw.Close()
	imageBody := "<< /Type /XObject /Subtype /Image /Width 40 /Height 40 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length " +
		fmt.Sprint(z.Len()) + " >>\nstream\n" + z.String() + "\nendstream"

	kids := make([]string, len(pages))
	for i := range pages {
		kids[i] = fmt.Sprintf("%d 0 R", 4+3*i)
	}
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages)),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	for i, pg := range pages {
		cid, iid := 5+3*i, 6+3*i
		content := "BT /F1 11 Tf 40 740 Td (" + pdfEscape(pg.text) + ") Tj ET"
		img := "null"
		if pg.image {
			content = "q 612 0 0 792 0 0 cm /Im1 Do Q"
			img = imageBody
		}
		objs = append(objs,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 3 0 R >> /XObject << /Im1 %d 0 R >> >> >>", cid, iid),
			contentStream(content),
			img,
		)
	}

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

// pdfEscape escapes the characters that are special inside a PDF literal string.
func pdfEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(s)
}

func contentStream(s string) string {
	return fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(s), s)
}
