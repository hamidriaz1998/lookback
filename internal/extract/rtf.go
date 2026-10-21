package extract

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// rtfSkipped lists the destination control words whose whole group is dropped (SPEC section 9).
var rtfSkipped = map[string]bool{"fonttbl": true, "colortbl": true, "pict": true}

// rtfText returns the plain text of an RTF document. It strips control words, decodes \uN and \'hh escapes,
// and skips {\*...} groups and the fonttbl, colortbl and pict groups. \par and \line become newlines and
// \tab becomes a tab. \'hh is read as Latin-1, not as the code page the document names (a known ceiling).
func rtfText(src []byte) string {
	var out strings.Builder
	uc := 1   // characters to skip after \uN (the \uc control word)
	skip := 0 // characters still to skip after \uN
	type group struct{ skipping bool }
	var stack []group
	skipping := func() bool { return len(stack) > 0 && stack[len(stack)-1].skipping }

	emit := func(s string) {
		if skip > 0 {
			skip--
			return
		}
		if !skipping() {
			out.WriteString(s)
		}
	}

	for i := 0; i < len(src); i++ {
		c := src[i]
		switch c {
		case '{':
			// A group is ignorable when it starts with \* or names a skipped destination.
			g := group{skipping: skipping()}
			if !g.skipping {
				word, star := rtfPeekWord(src, i+1)
				if star || rtfSkipped[word] {
					g.skipping = true
				}
			}
			stack = append(stack, g)
		case '}':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
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
						out.WriteByte('\n')
					}
				case "tab":
					if !skipping() {
						out.WriteByte('\t')
					}
				case "uc":
					if hasParam {
						uc, _ = strconv.Atoi(param)
					}
				case "u":
					if hasParam && !skipping() {
						v, _ := strconv.Atoi(param)
						if v < 0 {
							v += 65536
						}
						r := rune(v)
						if !utf8.ValidRune(r) {
							r = utf8.RuneError
						}
						out.WriteRune(r)
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
	return out.String()
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
