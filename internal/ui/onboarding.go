package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
)

// onboarding is the first-run flow (UI guide sections 3.1 to 3.3). It holds the detection results and the
// test matrix's choices; the services hold everything else.
type onboarding struct {
	step          int // 1 hardware, 2 downloads and test lab, 3 folders
	fromModels    bool
	detecting     bool // the hardware or a backend probe is still pending
	hwDone        bool // detection has returned
	hw            contract.Hardware
	hwErr         string
	runtimes      []contract.Asset   // the runtime builds the probes need; a probe needs its build on disk
	probeBackends []contract.Backend // the backends that are probed once runtimes are verified
	probing       bool
	probed        bool
	probes        map[contract.Backend]contract.BackendProbe
	backends      []contract.Backend
	tests         map[string]context.CancelFunc // the test runs this lab holds, by combination ID
	chosen        map[string]bool               // the combinations checked for testing
	combos        []contract.Combo
	labErr        string
	table         ui.ListState
	sel           int
	extraOpen     bool
	extraText     string
	extra         []string
	skipShow      bool
	offline       bool
}

// setStep moves the first run to step n and saves the step, so a restart returns to it (SPEC section 6). A run from
// Models is not saved: it is not the first run.
func (a *App) setStep(n int) {
	a.onb.step = n
	if !a.onb.fromModels {
		a.setting("onboard_step", strconv.Itoa(n))
	}
}

// restoreOnboarding returns the first run to the step and the chosen combination it was on when the app stopped.
// It runs when the first run starts, not for the test lab from Models.
func (a *App) restoreOnboarding() {
	if n, err := strconv.Atoi(a.data.setting("onboard_step", "")); err == nil && n >= 1 && n <= 3 {
		a.onb.step = n
	}
	if cb, ok := parseCombo(a.data.setting("onboard_combo", "")); ok {
		a.picked = &cb
		a.ensureFiles(cb)
	}
}

// parseCombo reads a combination ID, such as "vulkan/bf16/q8_0", as Combo.ID writes it.
func parseCombo(id string) (contract.Combo, bool) {
	parts := strings.Split(id, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return contract.Combo{}, false
	}
	return contract.Combo{Backend: contract.Backend(parts[0]), TextQuant: contract.Quant(parts[1]), EncQuant: contract.Quant(parts[2])}, true
}

// startOnboarding creates the flow and detects the hardware in the background.
func (a *App) startOnboarding() {
	o := &onboarding{step: 1, detecting: true, probes: map[contract.Backend]contract.BackendProbe{},
		tests: map[string]context.CancelFunc{}, chosen: map[string]bool{}, sel: -1}
	a.onb = o
	ctx := a.ctx
	go func() {
		hw, err := a.d.Detector.Detect(ctx)
		a.dispatch(func() {
			if a.onb != o {
				return
			}
			o.hw, o.hwDone = hw, true
			if err != nil {
				o.hwErr = err.Error()
			}
			a.queueRuntimes()
		})
	}()
}

// queueRuntimes queues the runtime builds that the probes need (SPEC section 6, step 1): the CPU and Vulkan
// builds, and the CUDA builds when an NVIDIA GPU is present. A backend without its NVIDIA GPU gets its reason
// at once. The probes run once these builds are verified (probeWhenReady).
func (a *App) queueRuntimes() {
	o := a.onb
	for _, b := range a.d.Catalog.Backends(a.d.OS) {
		o.backends = append(o.backends, b)
		if isCUDA(b) && !hasNvidia(o.hw) {
			o.probes[b] = contract.BackendProbe{Backend: b, Reason: "no NVIDIA GPU"}
			continue
		}
		as, err := a.d.Catalog.Assets(a.d.OS, contract.Combo{Backend: b, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16})
		if err != nil {
			o.probes[b] = contract.BackendProbe{Backend: b, Reason: err.Error()}
			continue
		}
		o.runtimes = append(o.runtimes, runtimeAssets(as)...)
		o.probeBackends = append(o.probeBackends, b)
	}
	a.ensureRuntimes()
	a.probeWhenReady()
}

// ensureRuntimes queues the runtime builds that the probes need. A failure shows in step 1 with Retry, which
// calls this again.
func (a *App) ensureRuntimes() {
	o := a.onb
	if len(o.runtimes) == 0 {
		return
	}
	ctx, assets := a.ctx, o.runtimes
	go func() {
		if err := a.d.Downloader.Ensure(ctx, assets); err != nil {
			a.dispatch(func() {
				if a.onb == o {
					o.labErr = err.Error()
				}
			})
		}
	}()
}

// runtimeAssets returns the runtime archives among as.
func runtimeAssets(as []contract.Asset) []contract.Asset {
	var out []contract.Asset
	for _, x := range as {
		if x.Part == contract.PartRuntime {
			out = append(out, x)
		}
	}
	return out
}

// isCUDA reports whether b is a CUDA backend.
func isCUDA(b contract.Backend) bool {
	return b == contract.BackendCUDA12 || b == contract.BackendCUDA13
}

// hasNvidia reports whether the hardware has an NVIDIA GPU.
func hasNvidia(hw contract.Hardware) bool {
	for _, g := range hw.GPUs {
		if g.Vendor == "nvidia" {
			return true
		}
	}
	return false
}

// verified reports whether the file of as is on disk and checked (FileDone), not merely queued.
func (a *App) verified(as contract.Asset) bool {
	d, ok := a.downloads[as.Key()]
	return ok && d.State == contract.FileDone
}

// comboReady reports whether every file of cb is verified.
func (a *App) comboReady(cb contract.Combo) bool {
	assets, err := a.d.Catalog.Assets(a.d.OS, cb)
	if err != nil {
		return false
	}
	for _, as := range assets {
		if !a.verified(as) {
			return false
		}
	}
	return true
}

// probeWhenReady starts the --list-devices probes once every runtime build they need is verified. Until then,
// step 1 shows the downloads, with Retry for a failed one. A probe that runs later sees the builds on disk.
func (a *App) probeWhenReady() {
	o := a.onb
	if o == nil || !o.hwDone || o.probing || o.probed {
		return
	}
	for _, as := range o.runtimes {
		if !a.verified(as) {
			return
		}
	}
	o.probing = true
	ctx := a.ctx
	backends := o.probeBackends
	go func() {
		probes := make(map[contract.Backend]contract.BackendProbe, len(backends))
		for _, b := range backends {
			p, err := a.d.Detector.Probe(ctx, b)
			if err != nil {
				p = contract.BackendProbe{Backend: b, Reason: err.Error()}
			}
			probes[b] = p
		}
		a.dispatch(func() {
			if a.onb != o {
				return
			}
			for b, p := range probes {
				o.probes[b] = p
			}
			o.probing, o.probed, o.detecting = false, true, false
			o.combos = a.candidateCombos()
		})
	}()
}

// onboardingView draws the current step, full window, without the sidebar.
func (a *App) onboardingView(c *ui.Context) {
	o := a.onb
	t := c.Theme()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Fill().Padding(t.Space(6)).Gap(t.Space(4)).Children(func() {
			a.noticeView(c)
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
		if !o.hwDone {
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
			switch {
			case g.VRAMFree > 0:
				row("GPU", fmt.Sprintf("%s · %s free", g.Name, size(g.VRAMFree)))
			case g.VRAMTotal > 0:
				row("GPU", fmt.Sprintf("%s · %s", g.Name, size(g.VRAMTotal)))
			default:
				row("GPU", g.Name) // the free memory is unknown until a backend lists the device
			}
		}
	})
	if o.detecting && o.hwDone {
		a.backendStatus(c)
	}
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
			a.setStep(2)
		}
		if ui.PrimaryButton(c, "Continue with recommended").Disabled(o.detecting).Clicked() {
			if rec, ok := a.recommendedCombo(); ok {
				a.pickCombo(rec)
				a.setStep(3)
			}
		}
	})
}

// backendStatus is what step 1 shows while the runtime builds download: a failed one with Retry, and offline.
func (a *App) backendStatus(c *ui.Context) {
	t := c.Theme()
	o := a.onb
	ui.Column(c).Gap(t.Space(2)).Children(func() {
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			ui.Text(c, "Downloading the runtime builds to test the backends…").TextColor(t.TextMuted)
		})
		for _, as := range o.runtimes {
			a.downloadRow(c, as)
		}
		if o.labErr != "" {
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, o.labErr).TextColor(t.Danger).Grow(1)
				if ui.Button(c, "Retry").Clicked() {
					o.labErr = ""
					a.ensureRuntimes()
				}
			})
		}
	})
}

// preferredBackends is the order a backend is chosen in when the lab has not measured any: the CUDA builds
// first, then Vulkan, then the CPU.
var preferredBackends = []contract.Backend{contract.BackendCUDA13, contract.BackendCUDA12, contract.BackendVulkan, contract.BackendCPU}

// Memory thresholds for the recommended precision (SPEC section 5). A 16 GB machine reports a little under
// 16 GiB of MemTotal, so the RAM threshold is 15 GiB.
const (
	bf16FreeVRAM = 4 << 30
	bf16RAM      = 15 << 30
)

// recommendedCombo is the backend and precision for this computer (SPEC section 5). The backend is the fastest
// candidate the lab has measured; before any measurement, preferredBackends decides. The precision follows the
// memory of the backend (recommendedQuant). It returns false when no backend is a candidate.
func (a *App) recommendedCombo() (contract.Combo, bool) {
	o := a.onb
	if o == nil {
		return contract.Combo{}, false
	}
	candidates := map[contract.Backend]bool{}
	for b, p := range o.probes {
		if p.Candidate {
			candidates[b] = true
		}
	}
	if len(candidates) == 0 {
		return contract.Combo{}, false
	}
	backend, ok := a.measuredBackend(candidates)
	for _, b := range append(append([]contract.Backend{}, preferredBackends...), o.backends...) {
		if ok {
			break
		}
		if candidates[b] {
			backend, ok = b, true
		}
	}
	q := recommendedQuant(backend, o.probes[backend], o.hw)
	return contract.Combo{Backend: backend, TextQuant: q, EncQuant: q}, true
}

// measuredBackend returns the backend of the fastest completed lab result (quality 0.99 or more) among the
// candidates, or false when none has been measured.
func (a *App) measuredBackend(candidates map[contract.Backend]bool) (contract.Backend, bool) {
	var results []contract.LabResult
	for _, id := range a.labOrder {
		if s := a.labSteps[id]; s.State == contract.LabDone && s.Result != nil && candidates[s.Combo.Backend] {
			results = append(results, *s.Result)
		}
	}
	best, ok := a.d.Lab.Recommend(results)
	return best.Backend, ok
}

// recommendedQuant is BF16 for both parts when the backend's device has 4 GB or more free VRAM, or, for the
// CPU, 15 GiB or more RAM. Otherwise it is Q8_0 for both.
func recommendedQuant(b contract.Backend, p contract.BackendProbe, hw contract.Hardware) contract.Quant {
	if b == contract.BackendCPU {
		if hw.RAMTotal >= bf16RAM {
			return contract.QuantBF16
		}
		return contract.QuantQ8_0
	}
	var free int64
	for _, d := range p.Devices {
		free = max(free, d.VRAMFree)
	}
	if free >= bf16FreeVRAM {
		return contract.QuantBF16
	}
	return contract.QuantQ8_0
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

// labRow returns the latest lab step of combination c, when one exists: the run with the highest ID, since a
// canceled run can arrive after the run that replaced it.
func (a *App) labRow(c contract.Combo) (contract.LabStep, bool) {
	var out contract.LabStep
	found := false
	for _, id := range a.labOrder {
		if s := a.labSteps[id]; s.Combo.ID() == c.ID() && (!found || s.RunID > out.RunID) {
			out, found = s, true
		}
	}
	return out, found
}

// stepLab is onboarding step 2: the combination matrix with the test results and the downloads (UI guide 3.2).
func (a *App) stepLab(c *ui.Context) {
	t := c.Theme()
	o := a.onb
	ui.Text(c, "Choose combinations to try").FontSize(t.Rem(1.15)).Bold()
	ui.Text(c, "Check a combination to test it on a short sample. Click a row to pick the one to use.").TextColor(t.TextMuted)
	switch {
	case o.detecting && o.hwDone:
		ui.Text(c, "Checking the backends on this computer…").TextColor(t.TextMuted)
	case len(o.combos) == 0:
		ui.Text(c, "No backend is available, so there is nothing to test.").TextColor(t.TextMuted)
	default:
		a.labTable(c, o.combos)
	}
	if o.labErr != "" {
		ui.Text(c, o.labErr).TextColor(t.Danger)
	}
	a.downloadsView(c)
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, "+ Test with my own files").Clicked() {
			o.extraOpen = true
		}
		ui.Spacer(c).Grow(1)
		if ui.Button(c, "Back").Clicked() {
			a.leaveLab()
			if o.fromModels {
				a.onb = nil
				return
			}
			a.setStep(1)
		}
		chosen := a.chosenCombo()
		label := "Use the chosen combination"
		if chosen != nil {
			label = "Use ★ " + comboLabel(*chosen)
		}
		if ui.PrimaryButton(c, label).Disabled(chosen == nil).Clicked() {
			a.useLab(*chosen)
		}
		if chosen == nil {
			ui.Text(c, "Choose a combination to continue.").TextColor(t.TextMuted).SingleLine()
		}
	})
	if a.onb == nil { // Back from Models closed the lab in this frame
		return
	}
	a.extraDialog(c)
}

// labTable draws the matrix: a checkbox, the test result or the live step, and the download size.
func (a *App) labTable(c *ui.Context, combos []contract.Combo) {
	t := c.Theme()
	o := a.onb
	recommended := a.recommendedLabCombo()
	cols := []ui.TableColumn{
		{Title: "Combination", Width: 290}, {Title: "Image ms", Width: 230, Align: ui.End},
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
					a.setTest(cb, chk)
				}
			})
		case 1:
			switch {
			case has && (step.State == contract.LabEmbedding || step.State == contract.LabStarting || step.State == contract.LabQueued):
				ui.Row(c).Gap(t.Space(1)).AlignItems(ui.Center).Justify(ui.End).Children(func() {
					ui.Spinner(c)
					ui.Text(c, step.Step).SingleLine()
				})
			case has && step.State == contract.LabFailed:
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Justify(ui.End).Children(func() {
					ui.Badge(c, "Failed").Tooltip(step.Err)
					if ui.Button(c, "Retry").Clicked() {
						a.retryTest(cb)
					}
				})
			case has && step.State == contract.LabDone && step.Result != nil:
				ui.Text(c, count(int(step.Result.Bench.ImageMS+0.5))).FontFeatures("tnum")
			default:
				ui.Text(c, "waiting").TextColor(t.TextMuted)
			}
		case 2, 3, 4, 5:
			if has && step.State == contract.LabDone && step.Result != nil {
				cell := ui.Text(c, benchCell(&step.Result.Bench, col)).FontFeatures("tnum").SingleLine()
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

// chosenCombo returns the row the user selected, unless its test failed. Nothing is selected at first, so
// Use is off until the user clicks a row.
func (a *App) chosenCombo() *contract.Combo {
	o := a.onb
	if o.sel < 0 || o.sel >= len(o.combos) {
		return nil
	}
	cb := o.combos[o.sel]
	if step, ok := a.labRow(cb); ok && step.State == contract.LabFailed {
		return nil
	}
	return &cb
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
		a.downloadRow(c, as)
	}
}

// assetLabel says which backend or model variant a file is: "Vulkan runtime", "Text model · Q8_0".
func assetLabel(as contract.Asset) string {
	switch as.Part {
	case contract.PartRuntime:
		return strings.ToUpper(string(as.Backend)) + " runtime"
	case contract.PartText:
		return "Text model · " + strings.ToUpper(string(as.Quant))
	case contract.PartEncoder:
		return "Image encoder · " + strings.ToUpper(string(as.Quant))
	}
	return as.Name
}

// downloadRow draws one file: what it is, a progress bar with MiB/GiB done of total and the speed, and
// Pause or Resume. Onboarding steps 1 and 2 share it.
func (a *App) downloadRow(c *ui.Context, as contract.Asset) {
	t := c.Theme()
	d, ok := a.downloads[as.Key()]
	total := max(d.Total, as.Bytes)
	ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, assetLabel(as)).SingleLine().Tooltip(as.Name).Basis(200)
		switch {
		case !ok:
			ui.Text(c, "Queued · "+iec(as.Bytes)).TextColor(t.TextMuted).Grow(1)
		case d.State == contract.FileDone:
			ui.Text(c, "✓ verified · "+iec(total)).Grow(1)
		case d.State == contract.FilePaused:
			ui.Text(c, "Paused · "+iec(d.Done)+" / "+iec(total)).TextColor(t.TextMuted).Grow(1)
		case d.State == contract.FileOffline:
			ui.Text(c, "Offline · "+iec(d.Done)+" / "+iec(total)).TextColor(t.TextMuted).Grow(1)
		case d.State == contract.FileVerifying:
			ui.Row(c).Gap(t.Space(1)).Grow(1).Children(func() {
				ui.Spinner(c)
				ui.Text(c, "Verifying…")
			})
		case d.State == contract.FileFailed:
			ui.Row(c).Gap(t.Space(2)).Grow(1).AlignItems(ui.Center).Children(func() {
				ui.Badge(c, "Download failed").Tooltip(d.Err)
				if ui.Button(c, "Retry").Clicked() {
					_ = a.d.Downloader.Retry(a.ctx, as.Key())
				}
			})
		default:
			ui.Progress(c, float64(d.Done)/float64(max(total, 1))).Grow(1)
			ui.Text(c, fmt.Sprintf("%s / %s · %s", iec(d.Done), iec(total), rateIEC(d.BytesPerSec))).
				FontFeatures("tnum").SingleLine()
		}
		if ok && d.State == contract.FilePaused && ui.Button(c, "").Label("Resume "+as.Name).Children(func() {
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
			ui.Text(c, "Resumed at "+iec(d.ResumedFrom)+".").TextColor(t.TextMuted).FontSize(t.Rem(0.85))
		}
	})
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
					a.restartTests()
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

// setTest checks or unchecks cb for testing. Checking queues the files of cb. Its test run starts once they are
// verified (startReadyTests). Unchecking cancels the run when it has not finished. A canceled run leaves the lab's
// queue, so the indexer resumes once the queue is empty (SPEC section 8). Opening the lab queues nothing.
func (a *App) setTest(cb contract.Combo, on bool) {
	o := a.onb
	id := cb.ID()
	o.chosen[id] = on
	if !on {
		if cancel, ok := o.tests[id]; ok {
			cancel()
			delete(o.tests, id)
		}
		return
	}
	a.ensureFiles(cb)
	if a.comboReady(cb) {
		a.startTest(cb)
	}
}

// startReadyTests starts the test of each checked combination whose files are verified and whose test has not
// started or ended. It runs on every download update, so a checked combination starts exactly once. A failed test
// waits for its Retry button rather than starting again on its own.
func (a *App) startReadyTests() {
	o := a.onb
	if o == nil {
		return
	}
	for _, cb := range o.combos {
		if !o.chosen[cb.ID()] || !a.comboReady(cb) {
			continue
		}
		if step, ok := a.labRow(cb); ok && step.State == contract.LabFailed {
			continue
		}
		a.startTest(cb)
	}
}

// startTest starts the lab run of cb, unless one is open or cb has a finished test. The run is cancelable, and a
// run that the lab refuses leaves the open runs, so the next download update may start it again.
func (a *App) startTest(cb contract.Combo) {
	o := a.onb
	id := cb.ID()
	if _, running := o.tests[id]; running {
		return
	}
	if step, ok := a.labRow(cb); ok && step.State == contract.LabDone {
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	o.tests[id] = cancel
	extra := o.extra
	lab := a.d.Lab
	go func() {
		if _, err := lab.Run(ctx, cb, extra); err != nil {
			a.dispatch(func() {
				if a.onb != o {
					return
				}
				o.labErr = err.Error()
				// A run canceled by the user is no longer in the map, and its error is expected. Any other run that
				// the lab refused is removed, so it can start again.
				if ctx.Err() == nil {
					cancel()
					delete(o.tests, id)
				}
			})
		}
	}()
}

// retryTest starts the test of cb again after it failed. It is unchecked, then checked, so the run uses the files
// on disk now.
func (a *App) retryTest(cb contract.Combo) {
	a.setTest(cb, false)
	a.setTest(cb, true)
}

// restartTests cancels the open runs and queues the unfinished tests of the checked combinations again, so they
// use the files the user just chose. Finished tests keep their results.
func (a *App) restartTests() {
	o := a.onb
	for id, cancel := range o.tests {
		cancel()
		delete(o.tests, id)
	}
	for _, cb := range o.combos {
		if o.chosen[cb.ID()] {
			a.setTest(cb, true)
		}
	}
}

// leaveLab cancels the test runs that are still open, and unchecks the combinations whose tests did not finish,
// including those still waiting for their files. Opening the lab never reaches this, so a lab that is only looked
// at queues nothing.
func (a *App) leaveLab() {
	o := a.onb
	if o == nil {
		return
	}
	for _, cb := range o.combos {
		id := cb.ID()
		if cancel, ok := o.tests[id]; ok {
			cancel()
			delete(o.tests, id)
		}
		if step, ok := a.labRow(cb); !ok || step.State != contract.LabDone {
			o.chosen[id] = false
		}
	}
}

// useLab picks cb from the test lab. The lab's open runs end, and the files of cb are queued. First run goes on
// to step 3, which builds the index once the files are verified. From Models, applyPick makes cb active then.
func (a *App) useLab(cb contract.Combo) {
	a.leaveLab()
	a.pickCombo(cb)
	if a.onb.fromModels {
		a.onb = nil
	} else {
		a.setStep(3)
	}
}

// pickCombo records cb as the combination to use, and queues its files. On first run it is saved as the choice
// (the "onboard_combo" setting), so the step can be restored. The switch to cb happens once its files are
// verified: the first run builds the index in finishOnboarding, and Models asks through makeActive.
func (a *App) pickCombo(cb contract.Combo) {
	if a.onb != nil && !a.onb.fromModels {
		a.setting("onboard_combo", cb.ID())
	}
	a.picked = &cb
	a.ensureFiles(cb)
}

// ensureFiles queues the files of cb that are not verified yet. Asking again changes nothing (SPEC section 7).
func (a *App) ensureFiles(cb contract.Combo) {
	assets, err := a.d.Catalog.Assets(a.d.OS, cb)
	if err != nil {
		a.notice = &notice{page: a.page, msg: err.Error()}
		return
	}
	a.run(func(ctx context.Context) error { return a.d.Downloader.Ensure(ctx, assets) })
}

// applyPick makes the picked combination active once its files are verified, as Models' Use does: a precision
// change asks first. It runs each frame, and does nothing during onboarding.
func (a *App) applyPick() {
	if a.onb != nil || a.picked == nil || !a.comboReady(*a.picked) {
		return
	}
	cb := *a.picked
	a.picked = nil
	a.makeActive(cb)
}

// inActiveSpace reports whether the active generation has the precisions of cb.
func (a *App) inActiveSpace(cb contract.Combo) bool {
	g := a.activeGen()
	return g != nil && g.Space.TextQuant == cb.TextQuant && g.Space.EncQuant == cb.EncQuant
}

// pickReady reports whether a picked combination has all its files verified, so indexing can start.
func (a *App) pickReady() bool {
	return a.picked != nil && a.comboReady(*a.picked)
}

// stepFolders is onboarding step 3: the folders to index, then Start indexing (UI guide section 3.3).
func (a *App) stepFolders(c *ui.Context) {
	t := c.Theme()
	f := &a.folders
	ui.Text(c, "What should Lookback index?").FontSize(t.Rem(1.6)).Bold()
	if len(a.data.folders) == 0 {
		ui.Column(c).Gap(t.Space(3)).Padding(t.Space(6)).Radius(8).Background(t.Surface).Center().Children(func() {
			ui.Text(c, "Add the folders that hold your photos and documents. Lookback indexes what's in them.").TextColor(t.TextMuted)
			if ui.PrimaryButton(c, "").Label("Add folder").Children(func() {
				ui.Icon(c, icon("folder-plus"))
				ui.Text(c, "Add folder")
			}).Clicked() {
				a.addFolder(false)
			}
		})
	} else {
		ui.Column(c).Gap(t.Space(2)).Padding(t.Space(3)).Radius(8).Background(t.Surface).Children(func() {
			for _, fv := range a.data.folders {
				ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, icon("folder")).TextColor(t.TextMuted)
					ui.Column(c).Grow(1).Children(func() {
						ui.Text(c, fv.folder.Path).SingleLine()
						line := countsLine(fv)
						if fv.files == 0 { // nothing is scanned before Start indexing
							line = "Counted when indexing starts"
						}
						ui.Text(c, line).TextColor(t.TextMuted).FontSize(t.Rem(0.85)).FontFeatures("tnum")
						if fv.denied != "" {
							ui.Text(c, "Can't read this folder: "+fv.denied).TextColor(t.Danger).FontSize(t.Rem(0.85))
						}
						if fv.missing {
							ui.Text(c, "Not found. It will be indexed again when it's back.").TextColor(t.Warning).FontSize(t.Rem(0.85))
						}
						if parent := nestedIn(fv.folder.Path, a.data.folders); parent != "" {
							ui.Text(c, "Inside "+parent+". Its files are merged with that folder.").TextColor(t.TextMuted).FontSize(t.Rem(0.85))
						}
						networkNote(c, fv)
					})
					if ui.Button(c, "Edit rules").Clicked() {
						f.editOpen, f.editID, f.editText = true, fv.folder.ID, fv.folder.Ignore
					}
					if ui.Button(c, "").Label("Remove " + fv.folder.Path).Children(func() { ui.Icon(c, icon("x")) }).Clicked() {
						id := fv.folder.ID
						a.run(func(ctx context.Context) error { return a.d.Store.DeleteFolder(ctx, id) })
					}
				})
			}
		})
		if ui.Button(c, "+ Add folder").Clicked() {
			a.addFolder(false)
		}
		a.skippedLine(c)
	}
	if a.picked != nil && !a.pickReady() {
		ui.Text(c, "Downloading the files of "+comboLabel(*a.picked)+". Start indexing unlocks when they are verified.").TextColor(t.TextMuted)
		assets, _ := a.d.Catalog.Assets(a.d.OS, *a.picked)
		for _, as := range assets {
			if !a.verified(as) {
				a.downloadRow(c, as)
			}
		}
	}
	ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
		if ui.Button(c, "Back").Clicked() {
			a.setStep(2)
		}
		if ui.PrimaryButton(c, "Start indexing").Disabled(len(a.data.folders) == 0 || !a.pickReady()).Clicked() {
			a.finishOnboarding()
		}
	})
	a.rulesDialog(c)
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
	if len(a.data.files) == 0 { // no scan yet: the count would read 0 and mislead
		return
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

// finishOnboarding marks the first run done. The chosen combination becomes the index: when the active
// generation has other precisions (or there is none), a rebuild creates the generation for it, and the runtime
// switches to its backend. Then the folders are scanned, and the Search page opens.
func (a *App) finishOnboarding() {
	folders := a.data.folders
	picked := a.picked
	rebuild := picked != nil && !a.inActiveSpace(*picked)
	a.onb, a.picked = nil, nil
	a.search.ranKey = ""
	a.setting("onboarded", "1")
	a.run(func(ctx context.Context) error {
		if picked != nil {
			if rebuild {
				if _, err := a.d.Indexer.Rebuild(ctx, picked.TextQuant, picked.EncQuant); err != nil {
					return err
				}
			}
			if err := a.d.Runtime.Use(ctx, picked.Backend); err != nil {
				return err
			}
		}
		for _, fv := range folders {
			if err := a.d.Indexer.Rescan(ctx, fv.folder.ID); err != nil {
				return err
			}
		}
		return nil
	})
}
