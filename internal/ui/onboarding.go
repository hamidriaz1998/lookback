package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
)

// onboarding is the first-run flow (UI guide sections 3.1 to 3.3). It holds the detection results and the
// test matrix's choices; the services hold everything else.
type onboarding struct {
	step       int // 1 hardware, 2 downloads and test lab, 3 folders
	fromModels bool
	detecting  bool
	hw         contract.Hardware
	hwErr      string
	probes     map[contract.Backend]contract.BackendProbe
	backends   []contract.Backend
	started    bool // the test lab runs for the candidate combinations
	chosen     map[string]bool
	combos     []contract.Combo
	table      ui.ListState
	sel        int
	extraOpen  bool
	extraText  string
	extra      []string
	skipShow   bool
	offline    bool
}

// startOnboarding creates the flow and detects the hardware in the background.
func (a *App) startOnboarding() {
	o := &onboarding{step: 1, detecting: true, probes: map[contract.Backend]contract.BackendProbe{}, chosen: map[string]bool{}, sel: -1}
	a.onb = o
	ctx := a.ctx
	go func() {
		hw, err := a.d.Detector.Detect(ctx)
		backends := a.d.Catalog.Backends(a.d.OS)
		probes := map[contract.Backend]contract.BackendProbe{}
		for _, b := range backends {
			if p, err := a.d.Detector.Probe(ctx, b); err == nil {
				probes[b] = p
			} else {
				probes[b] = contract.BackendProbe{Backend: b, Reason: err.Error()}
			}
		}
		a.dispatch(func() {
			o.detecting = false
			o.hw = hw
			if err != nil {
				o.hwErr = err.Error()
			}
			o.probes, o.backends = probes, backends
		})
	}()
}

// onboardingView draws the current step, full window, without the sidebar.
func (a *App) onboardingView(c *ui.Context) {
	o := a.onb
	t := c.Theme()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Fill().Padding(t.Space(6)).Gap(t.Space(4)).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Lookback").Bold().Grow(1)
				ui.Text(c, fmt.Sprintf("Step %d of 3", o.step)).TextColor(t.TextMuted).FontFeatures("tnum")
			})
			switch o.step {
			case 1:
				a.stepHardware(c)
			case 2:
				a.stepLab(c)
			default:
				a.stepFolders(c)
			}
		})
	})
}

// stepHardware is onboarding step 1 (UI guide section 3.1).
func (a *App) stepHardware(c *ui.Context) {
	t := c.Theme()
	o := a.onb
	ui.Text(c, "Welcome to Lookback").FontSize(t.Rem(1.6)).Bold()
	ui.Text(c, "Lookback finds your images and documents by what's in them. Everything runs on this computer.").TextColor(t.TextMuted)
	ui.Column(c).Gap(t.Space(2)).Padding(t.Space(3)).Radius(8).Background(t.Surface).Children(func() {
		ui.Text(c, "This computer").Bold()
		row := func(label, value string) {
			ui.Row(c).Gap(t.Space(3)).Children(func() {
				ui.Text(c, label).TextColor(t.TextMuted).MinWidth(60)
				ui.Text(c, value).FontFeatures("tnum").Grow(1)
			})
		}
		if o.detecting {
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Text(c, "Checking your GPU…").TextColor(t.TextMuted)
			})
			return
		}
		if o.hwErr != "" {
			ui.Text(c, "Detection failed: "+o.hwErr).TextColor(t.Danger)
		}
		row("CPU", fmt.Sprintf("%s · %d threads", o.hw.CPU, o.hw.Threads))
		row("RAM", size(o.hw.RAMTotal))
		if len(o.hw.GPUs) == 0 {
			row("GPU", "No supported GPU. Lookback will use the CPU.")
		} else {
			g := o.hw.GPUs[0]
			row("GPU", fmt.Sprintf("%s · %s free", g.Name, size(g.VRAMFree)))
		}
	})
	if !o.detecting {
		ui.Column(c).Gap(t.Space(2)).Padding(t.Space(3)).Radius(8).Background(t.Surface).Children(func() {
			ui.Text(c, "Recommended").Bold()
			rec, ok := a.recommendedCombo()
			if !ok {
				ui.Text(c, "No backend is available on this computer.").TextColor(t.TextMuted)
				return
			}
			ui.Text(c, comboLabel(rec)).SingleLine()
			ui.Text(c, "Recommended for this computer.").TextColor(t.TextMuted).FontSize(t.Rem(0.85))
			for _, b := range o.backends {
				if p, ok := o.probes[b]; ok && !p.Candidate && p.Reason != "" {
					ui.Row(c).Gap(t.Space(1)).Children(func() {
						ui.Badge(c, "Warning").Tooltip(p.Reason)
						ui.Text(c, backendName(b)+": unavailable").TextColor(t.TextMuted).FontSize(t.Rem(0.85))
					})
				}
			}
		})
	}
	ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
		if ui.Button(c, "Compare combinations").Clicked() {
			o.step = 2
		}
		if ui.PrimaryButton(c, "Continue with recommended").Disabled(o.detecting).Clicked() {
			if rec, ok := a.recommendedCombo(); ok {
				a.useCombo(rec)
				o.step = 3
			}
		}
	})
}

// recommendedCombo is the fastest candidate backend with BF16 precisions. It returns false when none is a candidate.
func (a *App) recommendedCombo() (contract.Combo, bool) {
	o := a.onb
	for _, b := range []contract.Backend{contract.BackendVulkan, contract.BackendCUDA13, contract.BackendCUDA12, contract.BackendCPU} {
		if p, ok := o.probes[b]; ok && p.Candidate {
			return contract.Combo{Backend: b, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16}, true
		}
	}
	return contract.Combo{}, false
}

// candidateCombos lists the combinations of every candidate backend: BF16 or Q8_0 text by BF16 or Q8_0 encoders.
func (a *App) candidateCombos() []contract.Combo {
	var out []contract.Combo
	for _, b := range a.onb.backends {
		if p, ok := a.onb.probes[b]; !ok || !p.Candidate {
			continue
		}
		for _, tq := range []contract.Quant{contract.QuantBF16, contract.QuantQ8_0} {
			for _, eq := range []contract.Quant{contract.QuantBF16, contract.QuantQ8_0} {
				out = append(out, contract.Combo{Backend: b, TextQuant: tq, EncQuant: eq})
			}
		}
	}
	return out
}

// startLabRuns queues a test run for each candidate combination, once.
func (a *App) startLabRuns() {
	o := a.onb
	if o.started {
		return
	}
	o.started = true
	o.combos = a.candidateCombos()
	ctx := a.ctx
	extra := o.extra
	go func() {
		for _, c := range o.combos {
			if _, err := a.d.Lab.Run(ctx, c, extra); err != nil {
				a.dispatch(func() { a.notice = err.Error() })
			}
		}
	}()
	for _, c := range o.combos {
		o.chosen[c.ID()] = false
	}
}

// labRow returns the latest lab step of combination c, when one exists.
func (a *App) labRow(c contract.Combo) (contract.LabStep, bool) {
	var out contract.LabStep
	found := false
	for _, id := range a.labOrder {
		if s := a.labSteps[id]; s.Combo.ID() == c.ID() {
			out, found = s, true
		}
	}
	return out, found
}

// stepLab is onboarding step 2: the combination matrix with the test results and the downloads (UI guide 3.2).
func (a *App) stepLab(c *ui.Context) {
	t := c.Theme()
	o := a.onb
	if len(o.probes) > 0 {
		a.startLabRuns()
	}
	ui.Text(c, "Choose combinations to try").FontSize(t.Rem(1.15)).Bold()
	ui.Text(c, "Each combination is tested on a short sample. Pick one to use; you can compare others later.").TextColor(t.TextMuted)
	combos := o.combos
	if len(combos) == 0 {
		ui.Text(c, "No backend is available, so there is nothing to test.").TextColor(t.TextMuted)
	} else {
		a.labTable(c, combos)
	}
	a.downloadsView(c)
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, "+ Test with my own files").Clicked() {
			o.extraOpen = true
		}
		ui.Spacer(c).Grow(1)
		if ui.Button(c, "Back").Clicked() {
			if o.fromModels {
				a.onb = nil
				return
			}
			o.step = 1
		}
		chosen := a.chosenCombo()
		label := "Use the chosen combination"
		if chosen != nil {
			label = "Use ★ " + comboLabel(*chosen)
		}
		if ui.PrimaryButton(c, label).Disabled(chosen == nil).Clicked() {
			a.useCombo(*chosen)
			o.step = 3
			if o.fromModels {
				a.onb = nil
			}
		}
		if chosen == nil {
			ui.Text(c, "Choose a combination to continue.").TextColor(t.TextMuted).SingleLine()
		}
	})
	a.extraDialog(c)
}

// labTable draws the matrix: a checkbox, the test result or the live step, and the download size.
func (a *App) labTable(c *ui.Context, combos []contract.Combo) {
	t := c.Theme()
	o := a.onb
	recommended := a.recommendedLabCombo()
	cols := []ui.TableColumn{
		{Title: "Combination", Width: 290}, {Title: "Image ms", Width: 130, Align: ui.End},
		{Title: "Tokens/s", Width: 100, Align: ui.End}, {Title: "RAM", Width: 90, Align: ui.End},
		{Title: "VRAM", Width: 90, Align: ui.End}, {Title: "Quality", Width: 90, Align: ui.End},
		{Title: "Download", Width: 110, Align: ui.End},
	}
	o.table.Selected = &o.sel
	ui.Table(c, &o.table, cols, len(combos), func(row, col int) {
		cb := combos[row]
		id := cb.ID()
		step, has := a.labRow(cb)
		switch col {
		case 0:
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				chk := o.chosen[id]
				label := comboLabel(cb)
				if recommended != nil && recommended.ID() == id {
					label = "★ " + label
				}
				failed := has && step.State == contract.LabFailed
				if ui.Checkbox(c, &chk, label).Disabled(failed).Changed() {
					o.chosen[id] = chk
				}
			})
		case 1:
			switch {
			case has && (step.State == contract.LabEmbedding || step.State == contract.LabStarting || step.State == contract.LabQueued):
				ui.Row(c).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c)
					ui.Text(c, step.Step).SingleLine()
				})
			case has && step.State == contract.LabFailed:
				ui.Badge(c, "Failed").Tooltip(step.Err)
			case has && step.State == contract.LabDone && step.Result != nil:
				ui.Text(c, count(int(step.Result.Bench.ImageMS+0.5))).FontFeatures("tnum")
			default:
				ui.Text(c, "waiting").TextColor(t.TextMuted)
			}
		case 2, 3, 4, 5:
			if has && step.State == contract.LabDone && step.Result != nil {
				cell := ui.Text(c, benchCell(&step.Result.Bench, col-1)).FontFeatures("tnum").SingleLine()
				if col == 5 && step.Result.Bench.Quality == nil {
					cell.Tooltip("BF16 encoders are needed to compare quality on your own files.")
				}
			} else {
				ui.Text(c, "—").TextColor(t.TextMuted)
			}
		case 6:
			ui.Text(c, a.newBytes(cb)).FontFeatures("tnum").SingleLine()
		}
	}).Grow(1)
}

// recommendedLabCombo returns the fastest completed result whose quality passes the lab's threshold.
func (a *App) recommendedLabCombo() *contract.Combo {
	var results []contract.LabResult
	for _, id := range a.labOrder {
		step := a.labSteps[id]
		if step.State == contract.LabDone && step.Result != nil {
			results = append(results, *step.Result)
		}
	}
	combo, ok := a.d.Lab.Recommend(results)
	if !ok {
		return nil
	}
	return &combo
}

// newBytes is the download size a combination adds: its assets that are not yet on disk.
func (a *App) newBytes(cb contract.Combo) string {
	assets, err := a.d.Catalog.Assets(a.d.OS, cb)
	if err != nil {
		return "—"
	}
	var n int64
	for _, as := range assets {
		if d, ok := a.downloads[as.Key()]; ok && d.State == contract.FileDone {
			continue
		}
		n += as.Bytes
	}
	if n == 0 {
		return "shared"
	}
	return size(n)
}

// chosenCombo returns the combination the Use button names: the one picked in the matrix, else the first checked.
func (a *App) chosenCombo() *contract.Combo {
	o := a.onb
	for _, cb := range o.combos {
		if o.chosen[cb.ID()] {
			if step, ok := a.labRow(cb); ok && step.State == contract.LabFailed {
				continue
			}
			c := cb
			return &c
		}
	}
	return nil
}

// downloadsView lists the downloads of the checked combinations, with Pause and Resume per file.
func (a *App) downloadsView(c *ui.Context) {
	t := c.Theme()
	o := a.onb
	o.offline = false
	for _, d := range a.downloads {
		if d.State == contract.FileOffline {
			o.offline = true
			break
		}
	}
	if o.offline {
		ui.Text(c, "Offline. Downloads resume when you're back online.").TextColor(t.Warning)
	}
	var keys []contract.Asset
	seen := map[string]bool{}
	for _, cb := range o.combos {
		if !o.chosen[cb.ID()] {
			continue
		}
		assets, err := a.d.Catalog.Assets(a.d.OS, cb)
		if err != nil {
			continue
		}
		for _, as := range assets {
			if !seen[as.Key()] {
				seen[as.Key()] = true
				keys = append(keys, as)
			}
		}
	}
	if len(keys) == 0 {
		return
	}
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Downloads").Bold().Grow(1)
		if ui.Button(c, "Pause all").Clicked() {
			a.d.Downloader.PauseAll()
		}
		if ui.Button(c, "Resume all").Clicked() {
			a.d.Downloader.ResumeAll()
		}
	})
	for _, as := range keys {
		d, ok := a.downloads[as.Key()]
		ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, as.Name).SingleLine().Basis(260)
			switch {
			case !ok:
				ui.Text(c, "queued").TextColor(t.TextMuted).Grow(1)
			case d.State == contract.FileDone:
				ui.Text(c, "✓ verified").Grow(1)
			case d.State == contract.FilePaused:
				ui.Text(c, "Paused · "+size(d.Done)+" / "+size(max(d.Total, as.Bytes))).TextColor(t.TextMuted).Grow(1)
			case d.State == contract.FileOffline:
				ui.Text(c, "Offline · "+size(d.Done)+" / "+size(max(d.Total, as.Bytes))).TextColor(t.TextMuted).Grow(1)
			case d.State == contract.FileVerifying:
				ui.Row(c).Gap(t.Space(1)).Grow(1).Children(func() {
					ui.Spinner(c)
					ui.Text(c, "Verifying…")
				})
			case d.State == contract.FileFailed:
				ui.Row(c).Gap(t.Space(2)).Grow(1).AlignItems(ui.Center).Children(func() {
					ui.Badge(c, "Checksum failed").Tooltip(d.Err)
					if ui.Button(c, "Retry").Clicked() {
						_ = a.d.Downloader.Retry(a.ctx, as.Key())
					}
				})
			default:
				total := max(d.Total, as.Bytes)
				ui.Progress(c, float64(d.Done)/float64(max(total, 1))).Grow(1)
				ui.Text(c, fmt.Sprintf("%s / %s · %s", size(d.Done), size(total), rate(d.BytesPerSec))).
					FontFeatures("tnum").SingleLine()
			}
			if ok && (d.State == contract.FilePaused) && ui.Button(c, "").Label("Resume "+as.Name).Children(func() {
				ui.Icon(c, icon("play"))
			}).Clicked() {
				_ = a.d.Downloader.Resume(as.Key())
			}
			if ok && (d.State == contract.FileDownloading || d.State == contract.FileQueued) &&
				ui.Button(c, "").Label("Pause "+as.Name).Children(func() {
					ui.Icon(c, icon("pause"))
				}).Clicked() {
				_ = a.d.Downloader.Pause(as.Key())
			}
			if ok && d.ResumedFrom > 0 {
				ui.Text(c, "Resumed at "+size(d.ResumedFrom)+".").TextColor(t.TextMuted).FontSize(t.Rem(0.85))
			}
		})
	}
}

// extraDialog takes up to 10 of the user's own files for the test lab.
func (a *App) extraDialog(c *ui.Context) {
	t := c.Theme()
	o := a.onb
	ui.Modal(c, &o.extraOpen, func() {
		ui.Column(c).Width(480).Gap(t.Space(3)).Children(func() {
			ui.Text(c, "Test with my own files").Bold().FontSize(t.Rem(1.15))
			ui.Text(c, "One path per line, up to 10. Your own files need the BF16 encoders to compare quality.").TextColor(t.TextMuted)
			ui.TextArea(c, &o.extraText).Placeholder("/home/you/Pictures/receipt.png").Lines(3, 10)
			ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					o.extraOpen = false
				}
				if ui.PrimaryButton(c, "Use these files").Clicked() {
					o.extra = pathLines(o.extraText, 10)
					o.extraOpen = false
					o.started = false
					o.combos = nil
					a.startLabRuns()
				}
			})
		})
	})
}

// pathLines splits text into at most n non-empty lines.
func pathLines(text string, n int) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" && len(out) < n {
			out = append(out, l)
		}
	}
	return out
}

// useCombo selects the backend and downloads the files of the combination.
func (a *App) useCombo(cb contract.Combo) {
	assets, err := a.d.Catalog.Assets(a.d.OS, cb)
	if err != nil {
		a.notice = err.Error()
		return
	}
	a.run(func(ctx context.Context) error {
		if err := a.d.Downloader.Ensure(ctx, assets); err != nil {
			return err
		}
		return a.d.Runtime.Use(ctx, cb.Backend)
	})
}

// stepFolders is onboarding step 3: the folders to index, then Start indexing (UI guide section 3.3).
func (a *App) stepFolders(c *ui.Context) {
	t := c.Theme()
	f := &a.folders
	o := a.onb
	ui.Text(c, "What should Lookback index?").FontSize(t.Rem(1.6)).Bold()
	if len(a.data.folders) == 0 {
		ui.Column(c).Gap(t.Space(3)).Padding(t.Space(6)).Radius(8).Background(t.Surface).Center().Children(func() {
			ui.Text(c, "Add the folders that hold your photos and documents. Lookback indexes what's in them.").TextColor(t.TextMuted)
			if ui.PrimaryButton(c, "").Label("Add folder").Children(func() {
				ui.Icon(c, icon("folder-plus"))
				ui.Text(c, "Add folder")
			}).Clicked() {
				f.addOpen, f.addPath = true, ""
			}
		})
	} else {
		ui.Column(c).Gap(t.Space(2)).Padding(t.Space(3)).Radius(8).Background(t.Surface).Children(func() {
			for _, fv := range a.data.folders {
				ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, icon("folder")).TextColor(t.TextMuted)
					ui.Column(c).Grow(1).Children(func() {
						ui.Text(c, fv.folder.Path).SingleLine()
						ui.Text(c, countsLine(fv)).TextColor(t.TextMuted).FontSize(t.Rem(0.85)).FontFeatures("tnum")
						if fv.denied != "" {
							ui.Text(c, "Can't read this folder: "+fv.denied).TextColor(t.Danger).FontSize(t.Rem(0.85))
						}
						if fv.missing {
							ui.Text(c, "Not found. It will be indexed again when it's back.").TextColor(t.Warning).FontSize(t.Rem(0.85))
						}
						if parent := nestedIn(fv.folder.Path, a.data.folders); parent != "" {
							ui.Text(c, "Inside "+parent+". Its files are merged with that folder.").TextColor(t.TextMuted).FontSize(t.Rem(0.85))
						}
					})
					if ui.Button(c, "").Label("Remove " + fv.folder.Path).Children(func() { ui.Icon(c, icon("x")) }).Clicked() {
						id := fv.folder.ID
						a.run(func(ctx context.Context) error { return a.d.Store.DeleteFolder(ctx, id) })
					}
				})
			}
		})
		if ui.Button(c, "+ Add folder").Clicked() {
			f.addOpen, f.addPath = true, ""
		}
		a.skippedLine(c)
	}
	ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
		if ui.Button(c, "Back").Clicked() {
			o.step = 2
		}
		if ui.PrimaryButton(c, "Start indexing").Disabled(len(a.data.folders) == 0).Clicked() {
			a.finishOnboarding()
		}
	})
	a.addFolderDialog(c)
}

// skippedLine counts the files the folders' rules skip, with a list when Show is on.
func (a *App) skippedLine(c *ui.Context) {
	t := c.Theme()
	o := a.onb
	var skipped []contract.FileRec
	for _, fv := range a.data.folders {
		for _, rec := range a.data.filesOf(fv.folder.ID) {
			if ignoreMatches(fv.folder.Ignore, []contract.FileRec{rec}) > 0 {
				skipped = append(skipped, rec)
			}
		}
	}
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("Skipped by rules: %s files", count(len(skipped)))).TextColor(t.TextMuted).Grow(1)
		if ui.Button(c, "").Label("Show skipped files").Children(func() {
			ui.Text(c, map[bool]string{true: "Hide", false: "Show"}[o.skipShow])
		}).Clicked() {
			o.skipShow = !o.skipShow
		}
	})
	if o.skipShow {
		for _, rec := range skipped {
			ui.Text(c, filepath.Base(rec.Path)).TextColor(t.TextMuted).FontSize(t.Rem(0.85)).SingleLine()
		}
	}
}

// finishOnboarding marks the first run done, scans the folders and returns to the Search page.
func (a *App) finishOnboarding() {
	folders := a.data.folders
	a.onb = nil
	a.search.ranKey = ""
	a.setting("onboarded", "1")
	a.run(func(ctx context.Context) error {
		for _, fv := range folders {
			if err := a.d.Indexer.Rescan(ctx, fv.folder.ID); err != nil {
				return err
			}
		}
		return nil
	})
}
