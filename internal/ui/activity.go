package ui

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
)

// activityPage builds the Activity page (UI guide section 3.8): the indexing job, the folders, the rebuild
// card and the errors with Retry.
func (a *App) activityPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Fill().Padding(t.Space(6)).Gap(t.Space(4)).Children(func() {
			a.indexingCard(c)
			a.rebuildCard(c)
			a.folderProgress(c)
			a.errorList(c)
		})
	})
}

// indexingCard shows the active job, its pause state and its numbers. Numbers come from the job's
// snapshot; while the job estimates, the ETA reads "estimating…".
func (a *App) indexingCard(c *ui.Context) {
	t := c.Theme()
	p := a.progress
	st := a.indexStatus()
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Indexing").FontSize(t.Rem(1.15)).Bold().Grow(1)
		a.pauseButton(c)
	})
	switch {
	case !a.hasProgress:
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			ui.Text(c, "Checking your folders…").TextColor(t.TextMuted)
		})
	case p.Paused == contract.PauseBattery:
		a.pausedNotice(c, "Paused on battery power.", "Index anyway", contract.PauseBattery)
	case p.Paused == contract.PauseSuspend:
		ui.Text(c, "Paused for suspend. Indexing resumes after wake.").TextColor(t.TextMuted)
	case p.Paused == contract.PauseLab:
		ui.Text(c, "Paused while a test runs in the test lab.").TextColor(t.TextMuted)
	case p.Paused == contract.PauseUser:
		ui.Text(c, "Paused. Resume to continue indexing.").TextColor(t.TextMuted)
	case st.busy:
		ui.Progress(c, st.fraction)
		ui.Text(c, count(p.FilesDone)+" / "+count(p.FilesTotal)+" files · "+percent(st.fraction)).FontFeatures("tnum")
		ui.Text(c, "ETA "+eta(p.ETA)+" · "+speed(p.FilesPerSec)+" · "+tokens(p.TokensPerSec)).
			TextColor(t.TextMuted).FontFeatures("tnum").MaxLines(2)
		if p.Current != "" {
			ui.Text(c, "Now: "+p.Current).TextColor(t.TextMuted).SingleLine().FontSize(t.Rem(0.85))
		}
	default:
		ui.Text(c, fmt.Sprintf("Everything is indexed. Watching %d folders for changes.", len(a.data.folders))).TextColor(t.TextMuted)
	}
}

// pausedNotice shows a pause reason with the action that lifts it.
func (a *App) pausedNotice(c *ui.Context, text, action string, reason contract.PauseReason) {
	t := c.Theme()
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, text).TextColor(t.TextMuted).Grow(1)
		if ui.Button(c, action).Clicked() {
			a.d.Indexer.Resume(reason)
		}
	})
}

// pauseButton pauses the job, or resumes it when the user paused it.
func (a *App) pauseButton(c *ui.Context) {
	if !a.hasProgress {
		return
	}
	if a.progress.Paused == contract.PauseUser {
		if ui.Button(c, "").Label("Resume indexing").Tooltip("Resume indexing").Children(func() {
			ui.Icon(c, icon("play"))
			ui.Text(c, "Resume")
		}).Clicked() {
			a.d.Indexer.Resume(contract.PauseUser)
		}
		return
	}
	if a.indexStatus().busy && a.progress.Paused == contract.PauseNone {
		if ui.Button(c, "").Label("Pause indexing").Tooltip("Pause indexing").Children(func() {
			ui.Icon(c, icon("pause"))
			ui.Text(c, "Pause")
		}).Clicked() {
			a.d.Indexer.Pause(contract.PauseUser)
		}
	}
}

// rebuildCard shows the progress of a rebuild into a building generation, labeled with its precisions.
func (a *App) rebuildCard(c *ui.Context) {
	if a.rebuild == nil {
		return
	}
	t := c.Theme()
	label := "Rebuilding the index"
	for _, g := range a.data.gens {
		if g.State == contract.GenBuilding {
			label = fmt.Sprintf("Rebuilding the index: text %s, encoders %s", precisionName(g.Space.TextQuant), precisionName(g.Space.EncQuant))
		}
	}
	r := a.rebuild
	ui.Column(c).Gap(t.Space(2)).Padding(t.Space(3)).Radius(8).Background(t.Surface).Children(func() {
		ui.Text(c, label).Bold()
		ui.Progress(c, float64(r.FilesDone)/float64(max(r.FilesTotal, 1)))
		ui.Text(c, count(r.FilesDone)+" / "+count(r.FilesTotal)+" files · the active index keeps serving searches").
			TextColor(t.TextMuted).FontFeatures("tnum")
	})
}

// folderProgress lists the watched folders with their progress while indexing.
func (a *App) folderProgress(c *ui.Context) {
	t := c.Theme()
	if !a.hasProgress || len(a.progress.PerFolder) == 0 {
		return
	}
	ui.Text(c, "Folders").FontSize(t.Rem(1.15)).Bold()
	for _, f := range a.progress.PerFolder {
		frac := float64(f.FilesDone) / float64(max(f.FilesTotal, 1))
		ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, f.Path).SingleLine().Basis(260)
			ui.Progress(c, frac).Grow(1)
			ui.Text(c, count(f.FilesDone)+" / "+count(f.FilesTotal)).FontFeatures("tnum").SingleLine().MinWidth(120)
		})
	}
}

// errorListHeight is the most a list of errors takes; past it the list scrolls within itself.
const errorListHeight = 360

// errorRowHeight is the height of one error row, to size a short list.
const errorRowHeight = 44

// errorList lists the files whose last indexing failed, each with Retry, and Retry all. The rows are a virtual list
// with a selection: Up and Down move it, and Enter retries the selected file (UI guide section 6). Only the rows on
// screen are built, so a folder with thousands of failed files costs what a folder with a few does.
func (a *App) errorList(c *ui.Context) {
	t := c.Theme()
	errs := a.data.errors
	if len(errs) == 0 {
		return
	}
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("Errors (%s)", count(len(errs)))).FontSize(t.Rem(1.15)).Bold().Grow(1)
		if ui.Button(c, "Retry all").Clicked() {
			a.run(func(ctx context.Context) error { return a.d.Indexer.RetryAll(ctx) })
		}
	})
	a.errList.Selected = &a.errSel
	a.errList.Key = func(row int) any { return errs[row].file.ID }
	list := ui.List(c, &a.errList, len(errs), func(i int) {
		e := errs[i]
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon("triangle-alert")).TextColor(t.Danger)
			ui.Text(c, filepath.Base(e.file.Path)).SingleLine().Basis(260)
			ui.Text(c, e.msg).TextColor(t.TextMuted).Grow(1).MaxLines(2)
			if ui.Button(c, "Retry").Clicked() {
				a.retryFile(e.file.ID)
			}
		})
	}).Height(float32(min(len(errs)*errorRowHeight, errorListHeight)))
	if list.Submitted() && a.errSel >= 0 && a.errSel < len(errs) {
		a.retryFile(errs[a.errSel].file.ID)
	}
}

// retryFile queues one failed file for indexing again.
func (a *App) retryFile(id int64) {
	a.run(func(ctx context.Context) error { return a.d.Indexer.Retry(ctx, []int64{id}) })
}

// run runs a command in the background, then reloads the snapshot. A failure is shown on the page it started on,
// with Retry.
func (a *App) run(cmd func(ctx context.Context) error) {
	var again func()
	again = func() { a.bg(a.page, cmd, again) }
	again()
}

// speed formats files per second, or "estimating…" before the first measurement.
func speed(filesPerSec float64) string {
	if filesPerSec <= 0 {
		return "estimating…"
	}
	return fmt.Sprintf("%.1f files/s", filesPerSec)
}

// tokens formats tokens per second, or "estimating…" before the first measurement.
func tokens(perSec float64) string {
	if perSec <= 0 {
		return "estimating…"
	}
	return fmt.Sprintf("%.0f tokens/s", perSec)
}

// precisionName names a precision for the labels: BF16 and Q8_0.
func precisionName(q contract.Quant) string {
	switch q {
	case contract.QuantBF16:
		return "BF16"
	case contract.QuantQ8_0:
		return "Q8_0"
	}
	return string(q)
}
