package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
)

// previewPanel shows the selected result: the picture or the matched text, then its metadata and actions
// (UI guide section 3.5).
func (a *App) previewPanel(c *ui.Context, items []contract.Hit) {
	t := c.Theme()
	h, ok := a.selectedHit()
	if !ok {
		ui.Column(c).Fill().Center().Children(func() {
			ui.Text(c, "Select a result to preview it.").TextColor(t.TextMuted)
		})
		return
	}
	ui.Column(c).Fill().Padding(t.Space(4)).Gap(t.Space(2)).Children(func() {
		ui.Column(c).Grow(1).Fill().Children(func() {
			a.previewBody(c, h)
		})
		a.previewMeta(c, h)
		a.previewActions(c, h)
	})
}

// previewBody draws the picture, the matched text, or the state of the file.
func (a *App) previewBody(c *ui.Context, h contract.Hit) {
	t := c.Theme()
	s := &a.search
	page, isPicture := pictureOf(h)
	if !isPicture {
		a.snippetCard(c, h)
		return
	}
	bm, err, ok := a.thumbs.get(a.pictureKey(h, page, a.data.intSetting("max_image_edge", 1600)))
	switch {
	case !ok:
		ui.Column(c).Fill().Center().Children(func() { ui.Spinner(c) })
	case errors.Is(err, ErrNoPicture):
		// A document without a renderer, or a page it cannot draw: its matched text shows instead.
		a.snippetCard(c, h)
	case errors.Is(err, os.ErrNotExist):
		ui.Column(c).Fill().Center().Gap(t.Space(2)).Children(func() {
			ui.Icon(c, icon("triangle-alert")).TextColor(t.TextMuted)
			ui.Text(c, "This file was moved or deleted.").TextColor(t.TextMuted)
			if ui.Button(c, "Remove from index").Clicked() {
				a.removeFromIndex(h.FileID)
			}
		})
	case err != nil:
		ui.Column(c).Fill().Center().Children(func() {
			ui.Text(c, "No preview for this file.").TextColor(t.TextMuted)
		})
	case s.zoom:
		w, hgt := bm.Size()
		ui.Scroll(c).Fill().Radius(8).Background(t.Surface).Children(func() {
			ui.Image(c, bm).Size(float32(w), float32(hgt))
		})
	default:
		ui.Column(c).Fill().Center().Radius(8).Background(t.Surface).Children(func() {
			ui.Image(c, bm).Fit(ui.Contain).Fill().Radius(8).Label(filepath.Base(h.Path))
		})
	}
}

// snippetCard shows the matched text of a result, with the query's words highlighted.
func (a *App) snippetCard(c *ui.Context, h contract.Hit) {
	t := c.Theme()
	ui.Column(c).Fill().Radius(8).Background(t.Surface).Padding(t.Space(3)).Gap(t.Space(2)).Children(func() {
		ui.RichText(c, highlight(h.Snippet, a.search.query, t)...).Lines(1, 0)
	})
}

// previewMeta shows the name, the folder (a link to the file manager), the size and the modification time.
func (a *App) previewMeta(c *ui.Context, h contract.Hit) {
	t := c.Theme()
	f, known := a.data.files[h.FileID]
	dir := filepath.Dir(h.Path)
	ui.Column(c).Gap(t.Space(1)).Children(func() {
		ui.Text(c, filepath.Base(h.Path)).Bold().SingleLine()
		ui.Link(c, dir, fileURL(dir)).SingleLine()
		meta := kindName(h.Kind)
		if h.Unit > 0 {
			meta += ", page " + fmt.Sprint(h.Unit)
		}
		if known {
			meta += " · " + size(f.Size)
			meta += " · " + time.Unix(0, f.MTime).Format("2 Jan 2006 15:04")
		}
		ui.Text(c, meta).TextColor(t.TextMuted).FontSize(t.Rem(0.85)).FontFeatures("tnum").Lines(1, 2)
	})
}

// previewActions is the row of icon buttons: Open, Show in folder, Find similar and Copy path.
func (a *App) previewActions(c *ui.Context, h contract.Hit) {
	t := c.Theme()
	actions := []struct {
		label, ic, tip string
		run            func(*ui.Context)
	}{
		{"Open", "external-link", "Open (Enter)", func(c *ui.Context) { openFile(c, h.Path) }},
		{"Show in folder", "folder", "Show in folder (Ctrl+Enter)", func(c *ui.Context) { openInFolder(c, h.Path) }},
		{"Find similar", "sparkles", "Find similar (Ctrl+Shift+F)", func(*ui.Context) { a.findSimilar(h) }},
		{"Copy path", "copy", "Copy path (Ctrl+C)", func(c *ui.Context) { c.WriteClipboard(h.Path) }},
	}
	ui.Row(c).Gap(t.Space(1)).Children(func() {
		for _, act := range actions {
			b := ui.Button(c, "").Label(act.label).Tooltip(act.tip).Children(func() {
				ui.Icon(c, icon(act.ic))
			})
			if b.Clicked() {
				act.run(c)
			}
		}
	})
}

// highlight splits text into spans, with each occurrence of a query word on the Selection color. Words match
// by case folding, rune by rune, on the original text, so every span boundary is a boundary of text. Lowering
// the text first would shift byte offsets for characters whose lowercase form has another length.
func highlight(text, query string, t *ui.Theme) []ui.Span {
	words := strings.Fields(query)
	if len(words) == 0 {
		return []ui.Span{{Text: text, Color: t.Text}}
	}
	var spans []ui.Span
	pos := 0
	for pos < len(text) {
		next, end := -1, -1
		for _, w := range words {
			if i, j, ok := indexFold(text[pos:], w); ok && (next < 0 || pos+i < next) {
				next, end = pos+i, pos+j
			}
		}
		if next < 0 {
			spans = append(spans, ui.Span{Text: text[pos:], Color: t.Text})
			break
		}
		if next > pos {
			spans = append(spans, ui.Span{Text: text[pos:next], Color: t.Text})
		}
		spans = append(spans, ui.Span{Text: text[next:end], Color: t.Text, Background: t.Selection})
		pos = end
	}
	return spans
}

// indexFold finds the first run of runes in s that equals word under case folding. It returns the byte range
// of that run in s. Simple case folding keeps the rune count, so a run has as many runes as word.
func indexFold(s, word string) (start, end int, ok bool) {
	n := utf8.RuneCountInString(word)
	if n == 0 {
		return 0, 0, false
	}
	for i := range s {
		j, count := i, 0
		for j < len(s) && count < n {
			_, size := utf8.DecodeRuneInString(s[j:])
			j += size
			count++
		}
		if count == n && strings.EqualFold(s[i:j], word) {
			return i, j, true
		}
	}
	return 0, 0, false
}

// removeFromIndex deletes a file's rows from the index, for a file that is gone (UI guide section 3.5).
func (a *App) removeFromIndex(fileID int64) {
	ctx := a.ctx
	go func() {
		if err := a.d.Store.DeleteFile(ctx, fileID); err != nil {
			a.dispatch(func() { a.search.err = err.Error() })
			return
		}
		a.dispatch(func() {
			a.search.ranKey = ""
			a.reload()
		})
	}()
}
