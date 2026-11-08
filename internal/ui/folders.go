package ui

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
)

// foldersState is the Folders page's dialogs and their inputs.
type foldersState struct {
	editOpen bool
	editID   int64
	editText string
	rmOpen   bool
	rmID     int64
	rmPath   string
}

// foldersPage builds the Folders page (UI guide section 3.6): a list of folders with their status and actions,
// and the Add folder button.
func (a *App) foldersPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Fill().Padding(t.Space(6)).Gap(t.Space(3)).Children(func() {
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Folders").FontSize(t.Rem(1.15)).Bold().Grow(1)
				if ui.PrimaryButton(c, "").Label("Add folder").Children(func() {
					ui.Icon(c, icon("folder-plus"))
					ui.Text(c, "Add folder")
				}).Clicked() {
					a.addFolder(true)
				}
			})
			if !a.data.ready {
				ui.Spinner(c)
				return
			}
			if len(a.data.folders) == 0 {
				a.folderEmpty(c)
			}
			for _, fv := range a.data.folders {
				a.folderRow(c, fv)
			}
		})
	})
	a.rulesDialog(c)
	a.removeDialog(c)
}

// folderEmpty is the state with no folders: one large Add folder button and a sentence on what is indexed.
func (a *App) folderEmpty(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Padding(t.Space(6)).Gap(t.Space(3)).Radius(8).Background(t.Surface).Center().Children(func() {
		ui.Text(c, "Lookback indexes the images and documents in the folders you add.").TextColor(t.TextMuted)
		if ui.PrimaryButton(c, "").Label("Add folder").Children(func() {
			ui.Icon(c, icon("folder-plus"))
			ui.Text(c, "Add folder")
		}).Clicked() {
			a.addFolder(true)
		}
	})
}

// folderRow draws one folder: its path, counts, last change, status, and actions.
func (a *App) folderRow(c *ui.Context, fv folderView) {
	t := c.Theme()
	f := &a.folders
	ui.Row(c).Gap(t.Space(3)).Padding(t.Space(3)).Radius(8).Background(t.Surface).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icon("folder")).TextColor(t.TextMuted)
		ui.Column(c).Grow(1).Gap(t.Space(1)).Children(func() {
			ui.Text(c, fv.folder.Path).Bold().SingleLine()
			ui.Text(c, countsLine(fv)).TextColor(t.TextMuted).FontSize(t.Rem(0.85)).FontFeatures("tnum").SingleLine()
			if fv.files > 0 {
				ui.Text(c, "Last change "+time.Unix(0, fv.latest).Format("2 Jan 2006 15:04")).
					TextColor(t.TextMuted).FontSize(t.Rem(0.85)).SingleLine()
			}
			if parent := nestedIn(fv.folder.Path, a.data.folders); parent != "" {
				ui.Text(c, "Inside "+parent+". Its files are merged with that folder.").TextColor(t.TextMuted).FontSize(t.Rem(0.85))
			}
			if fv.network {
				ui.Text(c, "Network folder: changes appear after a restart.").TextColor(t.TextMuted).FontSize(t.Rem(0.85))
			}
			a.folderNotice(c, fv)
		})
		a.folderStatus(c, fv)
		if ui.Button(c, "").Label("Edit rules").Tooltip("Edit rules").Children(func() {
			ui.Text(c, "Edit rules")
		}).Clicked() {
			f.editOpen, f.editID, f.editText = true, fv.folder.ID, fv.folder.Ignore
		}
		if ui.Button(c, "").Label("Rescan " + fv.folder.Path).Tooltip("Rescan").Children(func() {
			ui.Icon(c, icon("refresh-cw"))
		}).Clicked() {
			id := fv.folder.ID
			a.run(func(ctx context.Context) error { return a.d.Indexer.Rescan(ctx, id) })
		}
		if ui.Button(c, "").Label("Remove " + fv.folder.Path).Tooltip("Remove folder").Children(func() {
			ui.Icon(c, icon("x"))
		}).Clicked() {
			f.rmOpen, f.rmID, f.rmPath = true, fv.folder.ID, fv.folder.Path
		}
	})
}

// folderNotice shows the state of the folder itself: not found, or a permission problem.
func (a *App) folderNotice(c *ui.Context, fv folderView) {
	t := c.Theme()
	switch {
	case fv.missing:
		ui.Text(c, "Not found. It will be indexed again when it's back.").TextColor(t.Warning).FontSize(t.Rem(0.85))
	case fv.denied != "":
		ui.Text(c, "Can't read this folder: "+fv.denied).TextColor(t.Danger).FontSize(t.Rem(0.85))
	}
}

// folderStatus is the status label: Up to date, Indexing with a percentage, Paused, or Errors.
func (a *App) folderStatus(c *ui.Context, fv folderView) {
	switch {
	case fv.missing:
		ui.Badge(c, "Not found")
	case fv.denied != "":
		ui.Badge(c, "Can't read")
	case fv.errors > 0:
		ui.Badge(c, "Errors "+count(fv.errors))
	case a.hasProgress && a.progress.Paused != contract.PauseNone:
		ui.Badge(c, "Paused")
	case a.hasProgress && folderBusy(a.progress, fv.folder.ID):
		ui.Badge(c, "Indexing "+percent(folderFraction(a.progress, fv.folder.ID)))
	default:
		ui.Badge(c, "Up to date")
	}
}

// folderBusy reports whether the job still has files to do in folder id.
func folderBusy(p contract.Progress, id int64) bool {
	for _, f := range p.PerFolder {
		if f.FolderID == id {
			return f.FilesDone < f.FilesTotal
		}
	}
	return false
}

// folderFraction is the share of folder id that the job has done.
func folderFraction(p contract.Progress, id int64) float64 {
	for _, f := range p.PerFolder {
		if f.FolderID == id && f.FilesTotal > 0 {
			return float64(f.FilesDone) / float64(f.FilesTotal)
		}
	}
	return 0
}

// countsLine is "2,104 images · 12 documents", the counts by kind of a folder.
func countsLine(fv folderView) string {
	images := fv.counts[contract.KindImage]
	docs := fv.counts[contract.KindText] + fv.counts[contract.KindPDF] + fv.counts[contract.KindOffice]
	return count(images) + " images · " + count(docs) + " documents"
}

// nestedIn returns the path of the folder that contains path, or "" when none does. On Windows the comparison
// ignores case, so C:\Data contains c:\data\sub.
func nestedIn(p string, folders []folderView) string {
	for _, o := range folders {
		if !pathsSame(o.folder.Path, p, foldPathCase) &&
			pathHasPrefix(p, o.folder.Path+string(filepath.Separator), foldPathCase) {
			return o.folder.Path
		}
	}
	return ""
}

// folderNamed returns the folder whose path is p. On Windows the match ignores case, so c:\data is the folder
// C:\Data.
func folderNamed(p string, folders []folderView) (folderView, bool) {
	for _, fv := range folders {
		if pathsSame(fv.folder.Path, p, foldPathCase) {
			return fv, true
		}
	}
	return folderView{}, false
}

// addFolder asks for a folder with the native directory dialog, then saves it. With rescan, the folder is scanned at
// once; onboarding saves only, and Start indexing scans the folders. The dialog blocks, so it runs off the main
// thread. A failure is shown on the page, and a canceled dialog changes nothing.
func (a *App) addFolder(rescan bool) {
	ctx, pick := a.ctx, a.d.PickFolder
	go func() {
		dir, err := pick(ctx)
		a.dispatch(func() {
			switch {
			case err != nil:
				a.notice = &notice{page: a.page, msg: err.Error()}
			case dir != "":
				if fv, ok := folderNamed(dir, a.data.folders); ok {
					a.notice = &notice{page: a.page, msg: "That folder is already added: " + fv.folder.Path}
					return
				}
				a.run(func(ctx context.Context) error {
					fo := contract.Folder{Path: dir, AddedAt: time.Now()}
					if err := a.d.Store.SaveFolder(ctx, &fo); err != nil {
						return err
					}
					if !rescan {
						return nil
					}
					return a.d.Indexer.Rescan(ctx, fo.ID)
				})
			}
		})
	}()
}

// rulesDialog edits the ignore list of a folder, one pattern per line, with a live count of the files it skips. In
// onboarding the rules are saved only; Start indexing scans the folders.
func (a *App) rulesDialog(c *ui.Context) {
	t := c.Theme()
	f := &a.folders
	fv, ok := a.folderByID(f.editID)
	if !ok {
		f.editOpen = false
		return
	}
	ui.Modal(c, &f.editOpen, func() {
		ui.Column(c).Width(480).Gap(t.Space(3)).Children(func() {
			ui.Text(c, "Edit rules for "+fv.folder.Path).Bold().FontSize(t.Rem(1.15))
			ui.Text(c, "One pattern per line. Patterns match file names, with * as a wildcard.").TextColor(t.TextMuted)
			ui.TextArea(c, &f.editText).Placeholder("*.tmp\nnode_modules").Lines(4, 10)
			ui.Text(c, fmt.Sprintf("matches %s files", count(ignoreMatches(f.editText, a.data.filesOf(fv.folder.ID))))).
				TextColor(t.TextMuted).FontFeatures("tnum")
			ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					f.editOpen = false
				}
				if ui.PrimaryButton(c, "Save rules").Clicked() {
					id, text, rescan := fv.folder.ID, f.editText, a.onb == nil
					f.editOpen = false
					a.run(func(ctx context.Context) error {
						fo := fv.folder
						fo.Ignore = text
						if err := a.d.Store.SaveFolder(ctx, &fo); err != nil {
							return err
						}
						if !rescan {
							return nil
						}
						return a.d.Indexer.Rescan(ctx, id)
					})
				}
			})
		})
	})
}

// removeDialog asks before a folder is removed from the index. Its files stay on disk.
func (a *App) removeDialog(c *ui.Context) {
	f := &a.folders
	if !f.rmOpen {
		return
	}
	msg := fmt.Sprintf("Remove %s from Lookback? Its files stay on disk; only the index entries are deleted.", f.rmPath)
	if i := ui.AlertDialog(c, &f.rmOpen, "Remove folder", msg, "Remove", "Cancel"); i == 0 {
		id := f.rmID
		a.run(func(ctx context.Context) error { return a.d.Store.DeleteFolder(ctx, id) })
	}
}

// folderByID returns the folder with id from the snapshot.
func (a *App) folderByID(id int64) (folderView, bool) {
	for _, fv := range a.data.folders {
		if fv.folder.ID == id {
			return fv, true
		}
	}
	return folderView{}, false
}

// ignoreMatches counts the files that one of the patterns in ignore matches by file name.
func ignoreMatches(ignore string, files []contract.FileRec) int {
	var pats []string
	for _, line := range strings.Split(ignore, "\n") {
		if p := strings.TrimSpace(line); p != "" {
			pats = append(pats, p)
		}
	}
	n := 0
	for _, f := range files {
		name := filepath.Base(f.Path)
		for _, p := range pats {
			if ok, _ := path.Match(p, name); ok {
				n++
				break
			}
		}
	}
	return n
}

// filesOf returns the files of folder id from the snapshot.
func (s snapshot) filesOf(id int64) []contract.FileRec {
	var out []contract.FileRec
	for _, f := range s.files {
		if f.FolderID == id {
			out = append(out, f)
		}
	}
	return out
}
