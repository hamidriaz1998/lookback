package extract

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// binarySniff is how many leading bytes are checked for NUL (SPEC section 9).
const binarySniff = 8 << 10

// Text extracts the whole content of a text or source file as one unit. It accepts valid UTF-8, or UTF-16
// with a byte-order mark, which it converts. A file with a NUL byte in its first 8 KB is binary, and any
// other text that is not UTF-8 or UTF-16 is skipped: both emit no unit. HTML is reduced to its text, with
// script and style removed.
type Text struct{}

// Extract emits the text unit of f, or nothing when f is binary or not text.
func (Text) Extract(ctx context.Context, f contract.FileRec, emit func(contract.Unit) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := os.ReadFile(f.Path)
	if err != nil {
		return err
	}
	text, ok := decodeText(raw)
	if !ok {
		return nil
	}
	if ext := strings.ToLower(filepath.Ext(f.Path)); ext == ".html" || ext == ".htm" {
		text, err = htmlText(text)
		if err != nil {
			return err
		}
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return emit(textUnit(f, 0, text))
}

// decodeText returns the UTF-8 text of raw. ok is false when raw is binary or not text.
func decodeText(raw []byte) (text string, ok bool) {
	switch {
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE}) || bytes.HasPrefix(raw, []byte{0xFE, 0xFF}):
		return decodeUTF16(raw)
	case bytes.IndexByte(raw[:min(len(raw), binarySniff)], 0) >= 0:
		return "", false
	case utf8.Valid(raw):
		return strings.TrimPrefix(string(raw), "\uFEFF"), true
	}
	return "", false
}

// decodeUTF16 converts UTF-16 bytes with a byte-order mark to UTF-8.
func decodeUTF16(raw []byte) (string, bool) {
	big := raw[0] == 0xFE
	body := raw[2:]
	if len(body)%2 != 0 {
		body = body[:len(body)-1]
	}
	units := make([]uint16, len(body)/2)
	for i := range units {
		if big {
			units[i] = uint16(body[2*i])<<8 | uint16(body[2*i+1])
		} else {
			units[i] = uint16(body[2*i+1])<<8 | uint16(body[2*i])
		}
	}
	return string(utf16.Decode(units)), true
}

// htmlText returns the text of an HTML document, one block per line, without script and style content.
func htmlText(src string) (string, error) {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.ElementNode:
			switch n.Data {
			case "script", "style", "head":
				return
			case "p", "div", "br", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "section", "article":
				b.WriteByte('\n')
			}
		case html.TextNode:
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return b.String(), nil
}
