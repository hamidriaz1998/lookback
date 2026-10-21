package ui

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
)

const (
	searchDebounce = 250 * time.Millisecond // a query runs this long after the last keystroke (UI guide section 7)
	spinnerDelay   = 150 * time.Millisecond // the query's spinner shows only after this long
	searchLimit    = 300                    // hits per query; the grid shows them all
	queryPrefix    = "task: search result | query: "
	previewMinWide = 960 // below this window width, the preview opens as an overlay (UI guide section 2)
	previewMin     = 240
	previewDefault = 360
)

// seed is the file that Find similar searches from. Its vector is in the active generation's space.
type seed struct {
	itemID, fileID int64
	name           string
}

// searchState is the Search page's state.
type searchState struct {
	field     ui.Handle
	query     string
	editedAt  time.Time
	submit    bool // Enter was pressed: run now
	kind      int  // 0 all, 1 images, 2 documents
	ranKey    string
	hits      []contract.Hit
	seed      *seed
	prevQuery string // the query Find similar replaced

	seq       int
	cancel    context.CancelFunc
	searching bool
	startedAt time.Time
	elapsed   time.Duration
	err       string

	selected  int
	grid      ui.GridState
	previewOn bool
	previewW  float32
	splitW    float32
	focus     bool
	zoom      bool
	reloaded  bool // recent files asked for once per index change
}

func newSearchState() searchState {
	return searchState{selected: -1, previewOn: true, previewW: previewDefault}
}

// key identifies what the results show: a query, a filter and a seed. A change of key runs a query.
func (s *searchState) key() string {
	if s.seed != nil {
		return "seed:" + strconv.FormatInt(s.seed.itemID, 10) + "/" + itoa(s.kind)
	}
	if strings.TrimSpace(s.query) == "" {
		return ""
	}
	return itoa(s.kind) + "|" + s.query
}

// kinds maps the filter to the file kinds it shows.
func (s *searchState) kinds() []contract.Kind {
	switch s.kind {
	case 1:
		return []contract.Kind{contract.KindImage}
	case 2:
		return []contract.Kind{contract.KindText, contract.KindPDF, contract.KindOffice}
	}
	return nil
}

// searchPage builds the Search page (UI guide section 3.4).
func (a *App) searchPage(c *ui.Context) {
	t := c.Theme()
	s := &a.search
	a.searchKeys(c)
	a.maybeRunSearch(c)

	ui.Column(c).Fill().Padding(t.Space(6), t.Space(6), 0, t.Space(6)).Gap(t.Space(2)).Children(func() {
		a.queryBar(c)
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Segmented(c, &s.kind, "All", "Images", "Documents").Changed()
			ui.Spacer(c).Grow(1)
			a.resultCount(c)
		})
		if msg := a.searchNotice(); msg != "" {
			ui.Text(c, msg).TextColor(t.TextMuted).SingleLine()
		}
		a.resultsArea(c)
	})
}

// searchKeys handles the keys of UI guide section 6 that the Search page owns.
func (a *App) searchKeys(c *ui.Context) {
	s := &a.search
	typing := c.Focused(s.field)
	if c.Shortcut(ui.Ctrl, ui.KeyK) || (!typing && c.Shortcut(ui.Modifiers(0), ui.KeySlash)) {
		s.focus = true
	}
	if c.Shortcut(ui.Modifiers(0), ui.KeyEscape) {
		switch {
		case s.seed != nil:
			a.clearSeed()
		case s.query != "":
			s.query = ""
		case s.previewOn:
			s.previewOn = false
		case a.d.Hide != nil:
			a.d.Hide()
		}
	}
	if !typing && c.Shortcut(ui.Modifiers(0), ui.KeySpace) {
		s.previewOn = !s.previewOn
	}
	if !typing && c.Shortcut(ui.Modifiers(0), ui.KeyZ) {
		s.zoom = !s.zoom
	}
	for i, k := range []ui.Key{ui.Key1, ui.Key2, ui.Key3} {
		if !typing && c.Shortcut(ui.Modifiers(0), k) {
			s.kind = i
		}
	}
	if c.Shortcut(ui.Ctrl, ui.KeyC) && (!typing || s.query == "") {
		if h, ok := a.selectedHit(); ok {
			c.WriteClipboard(h.Path)
		}
	}
	if c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyF) {
		if h, ok := a.selectedHit(); ok {
			a.findSimilar(h)
		}
	}
	if c.Shortcut(ui.Ctrl, ui.KeyEnter) {
		if h, ok := a.selectedHit(); ok {
			openInFolder(c, h.Path)
		}
	}
}

// queryBar is the search field, or the Similar-to chip while Find similar runs (UI guide section 3.4).
func (a *App) queryBar(c *ui.Context) {
	t := c.Theme()
	s := &a.search
	if s.seed != nil {
		ui.Row(c).Gap(t.Space(1)).Padding(t.Space(1), t.Space(1), t.Space(1), t.Space(2)).Radius(999).
			Background(t.Surface).Border(1, t.Border).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon("sparkles"))
			ui.Text(c, "Similar to: "+s.seed.name).SingleLine().Grow(1)
			if ui.Button(c, "").Label("Clear similar search").Tooltip("Clear (Esc)").Children(func() {
				ui.Icon(c, icon("x"))
			}).Clicked() {
				a.clearSeed()
			}
		})
		return
	}
	field := ui.SearchField(c, &s.query).Label("Search your files").Bind(&s.field).Placeholder("Search your files…")
	if s.focus {
		field.Focus()
		s.focus = false
	}
	if field.Changed() {
		s.editedAt = c.Now()
	}
	if field.Submitted() {
		s.submit = true
	}
	field.Tooltip("Search (/ or Ctrl+K)")
}

// resultCount shows the number of results and the search time, with a spinner once a query runs long.
func (a *App) resultCount(c *ui.Context) {
	t := c.Theme()
	s := &a.search
	ui.Row(c).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
		if s.searching && c.Now().Sub(s.startedAt) >= spinnerDelay {
			ui.Spinner(c)
		}
		items := a.items()
		if s.key() == "" {
			ui.Text(c, "Recent files").TextColor(t.TextMuted).FontFeatures("tnum").SingleLine()
			return
		}
		text := count(len(items)) + " results"
		if len(items) == 1 {
			text = "1 result"
		}
		if !s.searching && s.elapsed > 0 {
			text += " · " + count(int(s.elapsed.Milliseconds())) + " ms"
		}
		ui.Text(c, text).TextColor(t.TextMuted).FontSize(t.Rem(0.85)).FontFeatures("tnum").SingleLine()
	})
}

// searchNotice is the one-line notice above the grid: indexing progress, a starting model, or an error.
func (a *App) searchNotice() string {
	s := &a.search
	switch {
	case s.err != "":
		return "" // shown in resultsArea with a Retry button
	case a.hasProgress && a.progress.FilesTotal > 0 && a.progress.FilesDone < a.progress.FilesTotal:
		p := a.progress
		return "Still indexing: " + count(p.FilesDone) + " of " + count(p.FilesTotal) + " files. Results may change."
	case a.runtime.State == contract.RuntimeStarting || a.runtime.State == contract.RuntimeRestarting:
		return "Starting the model… your query runs when it's ready."
	}
	return ""
}

// resultsArea builds the grid and the preview, or the empty, no-result and error states.
func (a *App) resultsArea(c *ui.Context) {
	t := c.Theme()
	s := &a.search
	if s.err != "" {
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon("triangle-alert")).TextColor(t.Danger)
			ui.Text(c, "Search failed: "+s.err).TextColor(t.Danger).Grow(1)
			if ui.Button(c, "Retry").Clicked() {
				s.ranKey, s.err = "", ""
			}
		})
	}
	if len(a.data.folders) == 0 {
		a.emptyState(c, "No folders yet", "Add a folder in Folders to start indexing. Lookback then finds your files by what's in them.", true)
		return
	}
	items := a.items()
	if len(items) == 0 {
		switch {
		case s.err != "":
			// The error row above says what failed; "nothing matches" would be wrong.
		case s.key() != "" && !s.searching:
			a.emptyState(c, "Nothing matches “"+s.query+"”.", "Try fewer words, describe what's in the picture, or check the type filter.", false)
		case a.hasProgress && a.progress.FilesDone == 0:
			a.emptyState(c, "Indexing your files…", "Results appear as they're ready.", false)
		case s.key() == "":
			a.emptyState(c, "Nothing indexed yet", "Your files appear here when they are indexed.", false)
		}
		return
	}
	if s.selected >= len(items) {
		s.selected = len(items) - 1
	}
	if s.selected < 0 {
		s.selected = 0 // the first result is the preview until the user moves
	}
	ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
		body := func() { a.grid(c, items) }
		wide, _ := c.Size()
		bodyW := wide - a.sidebarWidth()
		if !s.previewOn {
			ui.Column(c).Grow(1).Fill().Children(body)
			return
		}
		if bodyW >= previewMinWide {
			s.previewW = min(max(s.previewW, previewMin), bodyW/2)
			s.splitW = bodyW - s.previewW
			if ui.Split(c, &s.splitW, func() { ui.Column(c).Grow(1).Fill().Children(body) }, func() {
				a.previewPanel(c, items)
			}).Changed() {
				s.previewW = min(max(bodyW-s.splitW, previewMin), bodyW/2)
				a.setting("preview_width", itoa(int(s.previewW)))
			}
			return
		}
		ui.Column(c).Grow(1).Fill().Children(body)
		ui.Overlay(c, func() {
			ui.Column(c).Absolute().Top(0).Right(0).Bottom(0).Width(s.previewW).Background(t.Background).Children(func() {
				a.previewPanel(c, items)
			})
		})
	})
}

// sidebarWidth is the room the sidebar takes, or 0 when Ctrl+B collapsed it.
func (a *App) sidebarWidth() float32 {
	if a.sidebarOpen {
		return sidebarWidth
	}
	return 0
}

// emptyState is a centered message with an optional button to Folders.
func (a *App) emptyState(c *ui.Context, title, body string, toFolders bool) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(t.Space(2)).Children(func() {
		ui.Text(c, title).Bold().FontSize(t.Rem(1.15))
		ui.Text(c, body).TextColor(t.TextMuted).MaxWidth(640).Lines(1, 4)
		if toFolders && ui.PrimaryButton(c, "Add folder").Clicked() {
			a.page = pageFolders
		}
	})
}

// items returns what the grid shows: the results of the query, or the recent files before one.
func (a *App) items() []contract.Hit {
	s := &a.search
	if s.key() == "" {
		return a.data.recent
	}
	return s.hits
}

// selectedHit returns the highlighted result.
func (a *App) selectedHit() (contract.Hit, bool) {
	items := a.items()
	i := a.search.selected
	if i < 0 || i >= len(items) {
		return contract.Hit{}, false
	}
	return items[i], true
}

// grid draws the thumbnails. Each tile is a square picture and a name; the row height fits both.
func (a *App) grid(c *ui.Context, items []contract.Hit) {
	t := c.Theme()
	s := &a.search
	tile := a.thumbSize
	rowH := tile + t.Rem(2.2) + t.Space(2)
	s.grid.Selected = &s.selected
	s.grid.Label = func(i int) string { return filepath.Base(items[i].Path) }
	g := ui.GridView(c, &s.grid, len(items), tile, rowH, func(i int) {
		h := items[i]
		ui.Column(c).Gap(t.Space(1)).Children(func() {
			a.thumbnail(c, h, tile, 2*tile)
			ui.Text(c, filepath.Base(h.Path)).SingleLine().FontSize(t.Rem(0.85)).Label(tileLabel(h))
		})
	}).Grow(1)
	if g.Submitted() {
		if h, ok := a.selectedHit(); ok {
			openFile(c, h.Path)
		}
	}
}

// thumbnail draws one picture on Surface. It shows the decoded picture when there is one, and the file's icon
// until then, or when the file is gone.
func (a *App) thumbnail(c *ui.Context, h contract.Hit, edge, decodeEdge float32) {
	t := c.Theme()
	ui.Box(c).Size(edge, edge).Radius(8).Background(t.Surface).Center().Children(func() {
		bm, _, ok := a.thumbs.get(thumbKey{path: h.Path, edge: int(decodeEdge)})
		if ok && bm != nil {
			ui.Image(c, bm).Fit(ui.Contain).Fill().Radius(8)
			return
		}
		ui.Icon(c, fileIcon(h.Kind)).Size(t.Rem(2), t.Rem(2)).TextColor(t.TextMuted)
	})
}

// tileLabel is the accessible name of a tile: file name, type and page (UI guide section 9).
func tileLabel(h contract.Hit) string {
	name := filepath.Base(h.Path)
	if h.Unit > 0 {
		return name + ", " + kindName(h.Kind) + ", page " + itoa(h.Unit)
	}
	return name + ", " + kindName(h.Kind)
}

// fileIcon returns the icon for a file kind.
func fileIcon(k contract.Kind) *ui.SVG {
	switch k {
	case contract.KindImage:
		return icon("image")
	case contract.KindOffice:
		return icon("file-spreadsheet")
	}
	return icon("file-text")
}

// kindName is the word for a file kind, for labels.
func kindName(k contract.Kind) string {
	switch k {
	case contract.KindImage:
		return "image"
	case contract.KindPDF:
		return "PDF"
	case contract.KindOffice:
		return "office document"
	}
	return "text"
}

// maybeRunSearch starts a query when the key changed and the user stopped typing (or pressed Enter).
// It also asks for the recent files again when the index changed.
func (a *App) maybeRunSearch(c *ui.Context) {
	s := &a.search
	key := s.key()
	if key == s.ranKey {
		return
	}
	if key != "" && !s.submit && c.Now().Sub(s.editedAt) < searchDebounce {
		c.After(searchDebounce - c.Now().Sub(s.editedAt))
		return
	}
	s.submit = false
	s.ranKey = key
	if key == "" {
		// Ending the query ends its run: the run's context is canceled, and its sequence no longer matches.
		if s.cancel != nil {
			s.cancel()
			s.cancel = nil
		}
		s.seq++
		s.hits, s.err, s.searching = nil, "", false
		s.selected = -1
		return
	}
	a.startSearch(c)
}

// startSearch runs the query in the background. A newer run ends the older one, and its result is dropped.
func (a *App) startSearch(c *ui.Context) {
	s := &a.search
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	s.cancel = cancel
	s.seq++
	seq := s.seq
	s.searching, s.startedAt, s.err = true, c.Now(), ""
	gen := a.data.active
	q, kinds, sd := s.query, s.kinds(), s.seed
	go func() {
		t0 := time.Now()
		hits, err := a.runQuery(ctx, gen, q, kinds, sd)
		elapsed := time.Since(t0)
		a.dispatch(func() {
			if seq != s.seq {
				return
			}
			s.searching, s.elapsed = false, elapsed
			switch {
			case errors.Is(err, contract.ErrStaleItem):
				a.clearSeed()
			case err != nil && !errors.Is(err, context.Canceled):
				s.err = err.Error()
			case err == nil:
				s.hits = hits
				s.selected = -1
				if len(hits) > 0 {
					s.selected = 0
				}
			}
		})
	}()
}

// runQuery embeds the query and searches the generation, or finds the files similar to the seed. It runs off
// the main thread. A session waits for the sidecar to be ready, so a query that starts during a restart runs
// once the model is up.
func (a *App) runQuery(ctx context.Context, gen *contract.Generation, q string, kinds []contract.Kind, sd *seed) ([]contract.Hit, error) {
	if gen == nil {
		return nil, nil
	}
	if sd != nil {
		return a.d.Store.Similar(ctx, gen.ID, sd.itemID, kinds, searchLimit)
	}
	sess, err := a.d.Runtime.Session(ctx, gen.Space)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	vecs, _, err := sess.EmbedTexts(ctx, []string{queryPrefix + q})
	if errors.Is(err, contract.ErrRestarting) {
		if err := sess.Ready(ctx); err != nil {
			return nil, err
		}
		vecs, _, err = sess.EmbedTexts(ctx, []string{queryPrefix + q})
	}
	if err != nil {
		return nil, err
	}
	return a.d.Store.Search(ctx, gen.ID, vecs[0], kinds, searchLimit)
}

// findSimilar replaces the query with a chip for h, and searches from its vector (UI guide section 3.4).
func (a *App) findSimilar(h contract.Hit) {
	s := &a.search
	if s.seed == nil {
		s.prevQuery = s.query
	}
	s.seed = &seed{itemID: h.ItemID, fileID: h.FileID, name: filepath.Base(h.Path)}
	s.query = ""
	s.ranKey = ""
}

// clearSeed ends Find similar and returns to the previous query.
func (a *App) clearSeed() {
	s := &a.search
	s.seed = nil
	s.query = s.prevQuery
	s.ranKey = ""
}

// fileURL returns the file: URL of a path, which the desktop opens with its default application.
func fileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

// openFile opens path with its default application.
func openFile(c *ui.Context, path string) { c.OpenURL(fileURL(path)) }

// openInFolder opens the folder that holds path in the file manager.
func openInFolder(c *ui.Context, path string) { c.OpenURL(fileURL(filepath.Dir(path))) }
