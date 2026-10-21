package extract

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

// rtfSkipped lists the destination control words whose whole group is dropped (SPEC section 9).
var rtfSkipped = map[string]bool{"fonttbl": true, "colortbl": true, "pict": true}

// rtfText returns the plain text of an RTF document. It strips control words, decodes \uN and \'hh escapes,
// and skips {\*...} groups and the fonttbl, colortbl and pict groups. \par and \line become newlines and
// \tab becomes a tab. \'hh is read as Latin-1, not as the code page the document names (a known ceiling).
func rtfText(ctx context.Context, src []byte) (string, error) {
	var out strings.Builder
	uc := 1   // characters to skip after \uN (the \uc control word)
	skip := 0 // characters still to skip after \uN
	type group struct {
		skipping bool
		uc       int
		skip     int
	}
	var stack []group
	skipping := func() bool { return len(stack) > 0 && stack[len(stack)-1].skipping }
	var highSurrogate uint16
	flushHigh := func() {
		if highSurrogate != 0 {
			out.WriteRune('\uFFFD')
			highSurrogate = 0
		}
	}
	writeUnit := func(unit uint16) {
		switch {
		case unit >= 0xD800 && unit <= 0xDBFF:
			flushHigh()
			highSurrogate = unit
		case unit >= 0xDC00 && unit <= 0xDFFF:
			if highSurrogate == 0 {
				out.WriteRune('\uFFFD')
			} else {
				out.WriteRune(utf16.DecodeRune(rune(highSurrogate), rune(unit)))
				highSurrogate = 0
			}
		default:
			flushHigh()
			out.WriteRune(rune(unit))
		}
	}

	emit := func(s string) {
		if skip > 0 {
			skip--
			return
		}
		if !skipping() {
			flushHigh()
			out.WriteString(s)
		}
	}

	for i := 0; i < len(src); i++ {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		c := src[i]
		switch c {
		case '{':
			// A group is ignorable when it starts with \* or names a skipped destination.
			g := group{skipping: skipping(), uc: uc, skip: skip}
			if !g.skipping {
				word, star := rtfPeekWord(src, i+1)
				if star || rtfSkipped[word] {
					g.skipping = true
				}
			}
			stack = append(stack, g)
		case '}':
			if len(stack) > 0 {
				g := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				uc, skip = g.uc, g.skip
			}
		case '\\':
			if i+1 >= len(src) {
				break
			}
			n := src[i+1]
			switch {
			case n == '\\' || n == '{' || n == '}':
				emit(string(n))
				i++
			case n == '\'':
				if i+3 < len(src) {
					if v, err := strconv.ParseUint(string(src[i+2:i+4]), 16, 8); err == nil {
						emit(string(rune(v)))
					}
				}
				i += 3
			case n == '~':
				emit(" ")
				i++
			case n == '-' || n == '_' || n == '*':
				i++
			case n == '\r' || n == '\n':
				emit("\n")
				i++
			case isLetter(n):
				j := i + 1
				for j < len(src) && isLetter(src[j]) {
					j++
				}
				word := string(src[i+1 : j])
				// An optional signed numeric parameter, then one delimiting space.
				k := j
				if k < len(src) && src[k] == '-' {
					k++
				}
				for k < len(src) && isDigit(src[k]) {
					k++
				}
				param, hasParam := "", k > j
				if hasParam {
					param = string(src[j:k])
				}
				if k < len(src) && src[k] == ' ' {
					k++
				}
				i = k - 1
				switch word {
				case "par", "line", "row":
					if !skipping() {
						flushHigh()
						out.WriteByte('\n')
					}
				case "tab":
					if !skipping() {
						flushHigh()
						out.WriteByte('\t')
					}
				case "bin":
					// The next N bytes are raw data, not RTF: consume them whole, in any group.
					n, err := strconv.Atoi(param)
					if !hasParam || err != nil || n < 0 {
						return "", fmt.Errorf("invalid RTF binary length %q", param)
					}
					if n > len(src)-k {
						return "", fmt.Errorf("RTF binary data of %d bytes is truncated", n)
					}
					i = k + n - 1
				case "uc":
					if hasParam {
						uc, _ = strconv.Atoi(param)
						uc = max(0, uc)
					}
				case "u":
					if hasParam && !skipping() {
						v, err := strconv.Atoi(param)
						if err != nil || v < -32768 || v > 32767 {
							return "", fmt.Errorf("invalid RTF Unicode value %q", param)
						}
						if v < 0 {
							v += 65536
						}
						writeUnit(uint16(v))
						skip = uc
					}
				}
			default:
				// Any other control symbol is dropped.
				i++
			}
		case '\r', '\n':
			// Raw line breaks in RTF are whitespace.
		default:
			emit(string(c))
		}
	}
	flushHigh()
	return out.String(), ctx.Err()
}

// rtfPeekWord returns the control word that starts at src[i] (after a '{'), and whether the group starts
// with the \* destination marker.
func rtfPeekWord(src []byte, i int) (word string, star bool) {
	for i < len(src) && (src[i] == ' ' || src[i] == '\r' || src[i] == '\n') {
		i++
	}
	if i+1 < len(src) && src[i] == '\\' && src[i+1] == '*' {
		return "", true
	}
	if i >= len(src) || src[i] != '\\' {
		return "", false
	}
	j := i + 1
	for j < len(src) && isLetter(src[j]) {
		j++
	}
	return string(src[i+1 : j]), false
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
