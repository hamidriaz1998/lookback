package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/egoist/mygo/ui"
)

// peekMax is how much of a text file the preview reads.
const peekMax = 32 << 10

// peek is the head of the text file the preview shows, kept until another file or version is selected.
type peek struct {
	key  thumbKey
	text string
	cut  bool // the file is longer than peekMax
}

// codeExts maps a code extension to its line-comment style: true for "//", false for "#".
var codeExts = map[string]bool{
	".go": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true, ".c": true, ".h": true, ".cpp": true,
	".java": true, ".rs": true, ".css": true,
	".py": false, ".sh": false, ".rb": false, ".yaml": false, ".yml": false, ".toml": false,
}

// ponytail: one regex per comment style, not a lexer per language. Keywords are a shared list, so a word that is a
// keyword in one language is colored in all. Swap in chroma if exact highlighting matters.
const tokenPattern = `(?P<comment>%s)|(?P<string>"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'|` + "`[^`]*`" + `)|` +
	`(?P<keyword>\b(?:func|def|class|import|from|return|if|else|elif|for|while|switch|case|break|continue|const|let|var|` +
	`type|struct|interface|package|async|await|try|catch|except|finally|throw|raise|new|in|is|not|and|or|null|nil|` +
	`None|true|false|True|False|self|this|export|default|public|private|static|void|defer|range|with|as|pass|lambda|yield)\b)|` +
	`(?P<number>\b\d[\d_.]*\b)`

var (
	slashTokens = regexp.MustCompile(strings.Replace(tokenPattern, "%s", `//[^\n]*|/\*[\s\S]*?\*/`, 1))
	hashTokens  = regexp.MustCompile(strings.Replace(tokenPattern, "%s", `#[^\n]*`, 1))
)

// peekOf returns the head of the text file of h, read once per file version.
func (a *App) peekOf(path string, key thumbKey) (peek, bool) {
	s := &a.search
	if s.peek.key == key && s.peek.text != "" {
		return s.peek, true
	}
	f, err := os.Open(path)
	if err != nil {
		return peek{}, false
	}
	defer f.Close()
	buf := make([]byte, peekMax+1)
	n, _ := f.Read(buf)
	p := peek{key: key, cut: n > peekMax}
	p.text = strings.ToValidUTF8(string(buf[:min(n, peekMax)]), "")
	s.peek = p
	return p, true
}

// codeSpans colors text by the extension of path. Other text is one plain span.
func codeSpans(text, path string, t *ui.Theme) []ui.Span {
	slash, ok := codeExts[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return []ui.Span{{Text: text, Color: t.Text}}
	}
	re := hashTokens
	if slash {
		re = slashTokens
	}
	colors := []ui.Color{0: t.Text, 1: t.TextMuted, 2: t.Success, 3: t.Accent, 4: t.Warning} // by group number
	var spans []ui.Span
	pos := 0
	for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
		if m[0] > pos {
			spans = append(spans, ui.Span{Text: text[pos:m[0]], Color: t.Text})
		}
		g := 1
		for ; g <= 4 && m[2*g] < 0; g++ {
		}
		spans = append(spans, ui.Span{Text: text[m[0]:m[1]], Color: colors[g]})
		pos = m[1]
	}
	if pos < len(text) {
		spans = append(spans, ui.Span{Text: text[pos:], Color: t.Text})
	}
	return spans
}
