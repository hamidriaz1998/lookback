package ui

import (
	"context"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// newTestHarness is the harness with downloads that verify at once, and a detector and lab that tests may
// replace. The returned downloader holds or releases verifications.
func newTestHarness(t *testing.T, setup func(*fake.Store) contract.Store, det contract.Detector, tweak func(*Deps), hold bool) (*harness, *instantDownloader) {
	t.Helper()
	var dl *instantDownloader
	themes := func(context.Context) []contract.ThemeSource {
		return []contract.ThemeSource{fixedTheme("light", false), fixedTheme("dark", true)}
	}
	h := newHarnessTweak(t, setup, themes, false, func(d *Deps) {
		dl = newInstantDownloader(d.Downloader)
		if g, ok := det.(*gatedDetector); ok {
			dl.built = g.markBuilt
		}
		dl.setHold(hold)
		d.Downloader = dl
		if det != nil {
			d.Detector = det
		}
		if tweak != nil {
			tweak(d)
		}
	})
	return h, dl
}

// newFirstRunHarness is newTestHarness for a first run, with the onboarding flow showing.
func newFirstRunHarness(t *testing.T, det contract.Detector, tweak func(*Deps)) (*harness, *instantDownloader) {
	t.Helper()
	return newTestHarness(t, firstRun(t), det, tweak, false)
}

// ptr returns a pointer to v.
func ptr(v float64) *float64 { return &v }

// waitDetection waits until the first run has its hardware and probes.
func waitDetection(h *harness) {
	h.wait("detection", func() bool { return h.app.onb != nil && !h.app.onb.detecting })
}

// hasGen reports whether the store has a generation in the vector space of the given precisions.
func hasGen(t *testing.T, s contract.Store, text, enc contract.Quant) bool {
	t.Helper()
	gens, err := s.ListGenerations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range gens {
		if g.Space.TextQuant == text && g.Space.EncQuant == enc {
			return true
		}
	}
	return false
}

// bareApp returns an App that has no window, for the pure parts of the onboarding flow.
func bareApp(t *testing.T, o *onboarding) *App {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a := New(Deps{Lab: fake.NewLab(ctx, fake.NewIndexer(fake.NewStore(), fake.NewRuntime(ctx)))})
	a.onb = o
	return a
}

// TestRecommendedPrecisionFollowsMemory checks SPEC section 5: BF16 needs 4 GB free on the backend's device, or
// 15 GiB of RAM on the CPU. Below those, both parts are Q8_0.
func TestRecommendedPrecisionFollowsMemory(t *testing.T) {
	gpu := func(free int64) map[contract.Backend]contract.BackendProbe {
		return map[contract.Backend]contract.BackendProbe{
			contract.BackendVulkan: {Backend: contract.BackendVulkan, Candidate: true, Devices: []contract.Device{{VRAMFree: free}}},
			contract.BackendCPU:    {Backend: contract.BackendCPU, Candidate: true},
		}
	}
	cpuOnly := map[contract.Backend]contract.BackendProbe{
		contract.BackendCPU: {Backend: contract.BackendCPU, Candidate: true},
	}
	cases := []struct {
		name   string
		probes map[contract.Backend]contract.BackendProbe
		hw     contract.Hardware
		want   contract.Combo
	}{
		{"vulkan with 2 GB free", gpu(2 << 30), contract.Hardware{RAMTotal: 21 << 30}, contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantQ8_0}},
		{"vulkan with 6.8 GB free", gpu(6800 << 20), contract.Hardware{RAMTotal: 21 << 30}, contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16}},
		{"cpu with 8 GB RAM", cpuOnly, contract.Hardware{RAMTotal: 8 << 30}, contract.Combo{Backend: contract.BackendCPU, TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantQ8_0}},
		{"cpu on a 16 GB machine", cpuOnly, contract.Hardware{RAMTotal: 15600 << 20}, contract.Combo{Backend: contract.BackendCPU, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := bareApp(t, &onboarding{probes: c.probes, backends: []contract.Backend{contract.BackendVulkan, contract.BackendCPU}, hw: c.hw, hwDone: true})
			got, ok := a.recommendedCombo()
			if !ok || got != c.want {
				t.Fatalf("recommended %+v (ok %v), want %+v", got, ok, c.want)
			}
		})
	}
}

// TestRecommendedBackendFollowsMeasurement checks that a measured result decides the backend, so a faster CUDA
// candidate beats Vulkan, and a slower one does not.
func TestRecommendedBackendFollowsMeasurement(t *testing.T) {
	probes := map[contract.Backend]contract.BackendProbe{
		contract.BackendCUDA13: {Backend: contract.BackendCUDA13, Candidate: true, Devices: []contract.Device{{VRAMFree: 8 << 30}}},
		contract.BackendVulkan: {Backend: contract.BackendVulkan, Candidate: true, Devices: []contract.Device{{VRAMFree: 6800 << 20}}},
		contract.BackendCPU:    {Backend: contract.BackendCPU, Candidate: true},
	}
	backends := []contract.Backend{contract.BackendCUDA13, contract.BackendVulkan, contract.BackendCPU}
	bf := func(b contract.Backend) contract.Combo {
		return contract.Combo{Backend: b, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16}
	}
	measured := func(a *App, b contract.Backend, imageMS float64) {
		id := contract.RunID(len(a.labOrder) + 1)
		r := contract.LabResult{RunID: id, Combo: bf(b), Bench: contract.BenchResult{ImageMS: imageMS, Quality: ptr(1)}}
		a.labSteps[id] = contract.LabStep{RunID: id, Combo: bf(b), State: contract.LabDone, Result: &r}
		a.labOrder = append(a.labOrder, id)
	}
	cases := []struct {
		name     string
		measures map[contract.Backend]float64
		want     contract.Backend
	}{
		{"faster cuda wins", map[contract.Backend]float64{contract.BackendVulkan: 2683, contract.BackendCUDA13: 900}, contract.BackendCUDA13},
		{"slower cuda loses", map[contract.Backend]float64{contract.BackendVulkan: 2683, contract.BackendCUDA13: 3000}, contract.BackendVulkan},
		{"unmeasured prefers cuda", nil, contract.BackendCUDA13},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := bareApp(t, &onboarding{probes: probes, backends: backends, hwDone: true, hw: contract.Hardware{RAMTotal: 21 << 30}})
			for _, b := range []contract.Backend{contract.BackendVulkan, contract.BackendCUDA13} {
				if ms, ok := c.measures[b]; ok {
					measured(a, b, ms)
				}
			}
			got, ok := a.recommendedCombo()
			if !ok || got.Backend != c.want {
				t.Fatalf("recommended %v (ok %v), want backend %v", got.Backend, ok, c.want)
			}
		})
	}
}

// TestProbeWaitsForRuntimeDownload checks that detection downloads the runtime builds before it probes: a probe
// fails until its build is on disk, so the recommended backend shows only when the downloads came first.
func TestProbeWaitsForRuntimeDownload(t *testing.T) {
	det := newGatedDetector()
	h, _ := newFirstRunHarness(t, det, nil)
	waitDetection(h)
	h.wantText("Vulkan · BF16 · BF16")
	if len(det.probed) == 0 {
		t.Fatal("no backend was probed after its build was downloaded")
	}
}

// TestProbesHoldUntilBuildsVerified checks that the probes wait while a runtime build is still downloading.
func TestProbesHoldUntilBuildsVerified(t *testing.T) {
	det := newGatedDetector()
	h, dl := newTestHarness(t, firstRun(t), det, nil, true)
	h.pump(0)
	h.wantText("Downloading the runtime builds to test the backends…")
	if len(det.probed) != 0 {
		t.Fatal("probes ran before the builds were verified")
	}
	dl.release()
	waitDetection(h)
	h.wantText("Vulkan · BF16 · BF16")
}

// TestOpeningLabQueuesNoRuns checks that the test lab runs only the combinations the user checks.
func TestOpeningLabQueuesNoRuns(t *testing.T) {
	var lab *countingLab
	h, _ := newFirstRunHarness(t, nil, func(d *Deps) {
		lab = &countingLab{Lab: d.Lab}
		d.Lab = lab
	})
	waitDetection(h)
	h.app.onb.step = 2
	for i := 0; i < 20; i++ {
		h.pump(0)
		time.Sleep(10 * time.Millisecond)
	}
	if n := lab.count(); n != 0 {
		t.Fatalf("opening the lab started %d runs, want none", n)
	}
	if len(h.app.labSteps) != 0 {
		t.Fatalf("opening the lab produced %d lab steps", len(h.app.labSteps))
	}
	h.wantText("Choose a combination to continue.")
}

// TestUncheckedRunIsCanceledAndIndexerResumes checks that unchecking a combination cancels its run, and that
// the indexer is resumed once the lab's queue is empty.
func TestUncheckedRunIsCanceledAndIndexerResumes(t *testing.T) {
	var lab *countingLab
	h, _ := newFirstRunHarness(t, nil, func(d *Deps) {
		lab = &countingLab{Lab: d.Lab}
		d.Lab = lab
	})
	waitDetection(h)
	h.app.onb.step = 2
	cb := contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0}
	h.app.setTest(cb, true)
	h.wait("the lab run to start", func() bool { return lab.count() == 1 })
	run := lab.runs[0]
	h.wait("the indexer to pause for the lab", func() bool { return h.app.progress.Paused == contract.PauseLab })
	h.app.setTest(cb, false)
	if run.Err() == nil {
		t.Fatal("unchecking did not cancel the run")
	}
	h.wait("the indexer to resume", func() bool { return h.app.progress.Paused == contract.PauseNone })
}

// TestLeavingLabReleasesPause checks that leaving the lab cancels its open runs and resumes the indexer.
func TestLeavingLabReleasesPause(t *testing.T) {
	var lab *countingLab
	h, _ := newFirstRunHarness(t, nil, func(d *Deps) {
		lab = &countingLab{Lab: d.Lab}
		d.Lab = lab
	})
	waitDetection(h)
	h.app.onb.step = 2
	h.app.setTest(contract.Combo{Backend: contract.BackendCPU, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16}, true)
	h.wait("the lab run to start", func() bool { return lab.count() == 1 })
	run := lab.runs[0]
	h.wait("the indexer to pause for the lab", func() bool { return h.app.progress.Paused == contract.PauseLab })
	h.app.leaveLab()
	if run.Err() == nil {
		t.Fatal("leaving the lab did not cancel its run")
	}
	h.wait("the indexer to resume", func() bool { return h.app.progress.Paused == contract.PauseNone })
}

// heldLabHarness is a first run at step 2 whose downloads are held once the probes are done, so a test can check
// a combination before its files verify. The lab rejects a combination whose files are not verified.
func heldLabHarness(t *testing.T) (*harness, *countingLab, *instantDownloader) {
	t.Helper()
	var lab *countingLab
	h, dl := newTestHarness(t, firstRun(t), nil, func(d *Deps) {
		lab = &countingLab{Lab: d.Lab}
		d.Lab = lab
		idl := d.Downloader.(*instantDownloader)
		cat, os := d.Catalog, d.OS
		lab.ready = func(c contract.Combo) bool { return idl.verifiedCombo(cat, os, c) }
	}, false)
	waitDetection(h)
	h.app.onb.step = 2
	return h, lab, dl
}

// heldCombo is the combination the held-download tests check: its files are not verified until released.
var heldCombo = contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0}

// settle pumps the app for a moment, so a run that should not start has time to.
func settle(h *harness) {
	for i := 0; i < 20; i++ {
		h.pump(0)
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHeldDownloadDelaysLabRun checks SPEC section 6: a checked combination is tested only once its files are
// verified. No run starts while its files download, and exactly one starts when they verify.
func TestHeldDownloadDelaysLabRun(t *testing.T) {
	h, lab, dl := heldLabHarness(t)
	dl.setHold(true)
	h.app.setTest(heldCombo, true)
	settle(h)
	if n := lab.count(); n != 0 {
		t.Fatalf("checking a combination started %d runs before its files verified, want none", n)
	}
	dl.release()
	h.wait("the lab run to start", func() bool { return lab.count() >= 1 })
	h.wait("the test to finish", func() bool {
		s, ok := h.app.labRow(heldCombo)
		return ok && s.State == contract.LabDone
	})
	settle(h)
	if n := lab.count(); n != 1 {
		t.Fatalf("the combination ran %d times, want exactly one", n)
	}
	if n := lab.rejections(); n != 0 {
		t.Fatalf("the lab rejected %d runs whose files had verified", n)
	}
}

// TestUncheckedBeforeVerificationNeverRuns checks that a combination unchecked while its files download is not
// tested once they verify.
func TestUncheckedBeforeVerificationNeverRuns(t *testing.T) {
	h, lab, dl := heldLabHarness(t)
	dl.setHold(true)
	h.app.setTest(heldCombo, true)
	h.app.setTest(heldCombo, false)
	dl.release()
	settle(h)
	if n := lab.count(); n != 0 {
		t.Fatalf("an unchecked combination started %d runs, want none", n)
	}
}

// TestLeavingLabBeforeVerificationNeverRuns checks that a combination checked while its files download is not
// tested once they verify, when the user has left the lab meanwhile.
func TestLeavingLabBeforeVerificationNeverRuns(t *testing.T) {
	h, lab, dl := heldLabHarness(t)
	dl.setHold(true)
	h.app.setTest(heldCombo, true)
	h.app.leaveLab()
	dl.release()
	settle(h)
	if n := lab.count(); n != 0 {
		t.Fatalf("a combination left behind started %d runs, want none", n)
	}
}

// TestModelsChoiceRebuildsIndex checks that a combination picked from Models goes through the precision switch:
// the confirmation first, then a rebuild into a generation with that vector space, and the chosen backend.
func TestModelsChoiceRebuildsIndex(t *testing.T) {
	h, _ := newTestHarness(t, nil, nil, nil, false)
	h.app.startLab()
	waitDetection(h)
	cb := contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16}
	h.app.useLab(cb)
	h.wait("the precision switch to be asked", func() bool { return h.app.models.switchOpen && h.app.models.switchTo != nil })
	if hasGen(t, h.store, contract.QuantBF16, contract.QuantBF16) {
		t.Fatal("a generation was built before the switch was confirmed")
	}
	h.app.rebuildTo(*h.app.models.switchTo)
	h.wait("a generation in the chosen space", func() bool { return hasGen(t, h.store, contract.QuantBF16, contract.QuantBF16) })
	h.wait("the runtime on the chosen backend", func() bool { return h.app.d.Runtime.Backend() == contract.BackendVulkan })
}

// TestFirstRunChoiceSetsVectorSpace checks that the first run's choice is saved, that Start indexing waits for
// its files to verify, and that finishing builds the index in the chosen vector space.
func TestFirstRunChoiceSetsVectorSpace(t *testing.T) {
	h, dl := newFirstRunHarness(t, nil, nil)
	waitDetection(h)
	// The fixture index is BF16 text with Q8_0 encoders, so this choice is another vector space.
	cb := contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantBF16}
	dl.setHold(true)
	h.app.pickCombo(cb)
	h.app.onb.step = 3
	h.pump(0)
	if h.app.pickReady() {
		t.Fatal("the choice is ready while its files still download")
	}
	h.wantText("Waiting for the files of")
	dl.release()
	h.wait("the files to verify", h.app.pickReady)
	h.wait("the choice saved", func() bool {
		v, _, _ := h.store.GetSetting(context.Background(), "onboard_combo")
		return v == cb.ID()
	})
	h.app.finishOnboarding()
	h.wait("a generation in the chosen space", func() bool { return hasGen(t, h.store, contract.QuantQ8_0, contract.QuantBF16) })
	h.wait("the runtime on the chosen backend", func() bool { return h.app.d.Runtime.Backend() == contract.BackendVulkan })
}

// TestLabRowShowsEachMetricUnderItsHeading checks that a finished test row draws every metric in its own column:
// image ms, tokens/s, RAM, VRAM and quality, in that order, with the real quality value.
func TestLabRowShowsEachMetricUnderItsHeading(t *testing.T) {
	h, _ := newFirstRunHarness(t, nil, nil)
	waitDetection(h)
	h.app.onb.step = 2
	h.pump(0)
	cb := h.app.onb.combos[0]
	res := contract.LabResult{RunID: 1, Combo: cb, Bench: contract.BenchResult{
		ImageMS: 1111, TextTokensPerSec: 2222, PeakRAM: 3 << 30, VRAM: 4 << 30, Quality: ptr(0.995),
	}}
	h.app.labOrder = append(h.app.labOrder, 1)
	h.app.labSteps[1] = contract.LabStep{RunID: 1, Combo: cb, State: contract.LabDone, Result: &res}
	h.pump(0)
	want := []string{count(1111), count(2222), size(3 << 30), size(4 << 30), "0.995"}
	texts := h.tt.Texts()
	for i := 0; i+len(want) <= len(texts); i++ {
		match := true
		for j, w := range want {
			if texts[i+j] != w {
				match = false
				break
			}
		}
		if match {
			return
		}
	}
	t.Fatalf("the row does not show %q in its columns in order; texts: %q", want, texts)
}
