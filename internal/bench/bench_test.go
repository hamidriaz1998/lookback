package bench

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/runtime"
)

// recordingIndexer counts Pause and Resume calls for PauseLab.
type recordingIndexer struct {
	contract.Indexer // only Pause and Resume are used
	mu               sync.Mutex
	pauses           int
	resumes          int
	labHeld          bool
}

func (x *recordingIndexer) Pause(r contract.PauseReason) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if r == contract.PauseLab {
		x.pauses++
		x.labHeld = true
	}
}
func (x *recordingIndexer) Resume(r contract.PauseReason) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if r == contract.PauseLab {
		x.resumes++
		x.labHeld = false
	}
}

// failingFiles resolves every combination to a server binary that exits at once, so each run ends in failed
// quickly and the queue order is what the test observes.
func failingFiles(c contract.Combo) (runtime.Files, error) {
	return runtime.Files{Server: "/bin/false", Model: "m", MMProj: "p"}, nil
}

func combo(q contract.Quant) contract.Combo {
	return contract.Combo{Backend: contract.BackendCPU, TextQuant: q, EncQuant: q}
}

// collect reads steps until every run in want has a terminal step, and returns the order of terminal steps.
func collect(t *testing.T, ch <-chan contract.LabStep, want int) []contract.RunID {
	t.Helper()
	var order []contract.RunID
	deadline := time.After(60 * time.Second)
	for len(order) < want {
		select {
		case s, ok := <-ch:
			if !ok {
				t.Fatalf("stream closed after %d terminal steps, want %d", len(order), want)
			}
			switch s.State {
			case contract.LabDone, contract.LabFailed, contract.LabCanceled:
				order = append(order, s.RunID)
			}
		case <-deadline:
			t.Fatalf("timed out with %d terminal steps", len(order))
		}
	}
	return order
}

func TestRecommendPicksFastestAboveBar(t *testing.T) {
	q := func(v float64) *float64 { return &v }
	results := []contract.LabResult{
		{Combo: combo(contract.QuantBF16), Bench: contract.BenchResult{ImageMS: 4000, Quality: q(1.0)}},
		{Combo: contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16},
			Bench: contract.BenchResult{ImageMS: 2000, Quality: q(0.995)}},
		{Combo: contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantQ8_0},
			Bench: contract.BenchResult{ImageMS: 1000, Quality: q(0.98)}},
		{Combo: contract.Combo{Backend: contract.BackendCUDA12}, Bench: contract.BenchResult{ImageMS: 500}},
	}
	got, ok := (&Lab{}).Recommend(results)
	if !ok || got.Backend != contract.BackendVulkan || got.TextQuant != contract.QuantBF16 {
		t.Fatalf("Recommend = %+v, %v; want the fastest with quality >= 0.99", got, ok)
	}
}

func TestRunsFormOneFIFOQueueAndHoldThePause(t *testing.T) {
	ix := &recordingIndexer{}
	life, end := context.WithCancel(context.Background())
	defer end()
	l := New(life, ix, nil, failingFiles, t.TempDir()+"/log", 1)
	ch := l.Subscribe(context.Background())
	var ids []contract.RunID
	for _, q := range []contract.Quant{contract.QuantBF16, contract.QuantQ8_0, contract.QuantBF16} {
		id, err := l.Run(context.Background(), combo(q), nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	order := collect(t, ch, 3)
	for i := range ids {
		if order[i] != ids[i] {
			t.Fatalf("terminal order %v, want the queue order %v", order, ids)
		}
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.pauses != 1 || ix.resumes != 1 || ix.labHeld {
		t.Fatalf("pause calls: %d pauses, %d resumes; want one of each (held=%v)", ix.pauses, ix.resumes, ix.labHeld)
	}
}

// gatedFiles blocks the resolution of the first combination until release is closed, and records the order in
// which combinations start.
type gatedFiles struct {
	mu        sync.Mutex
	started   []contract.Quant
	gateCombo contract.Combo
	resolves  map[string]int
	entered   chan struct{}
	gate      chan struct{}
}

func (g *gatedFiles) resolve(c contract.Combo) (runtime.Files, error) {
	g.mu.Lock()
	g.resolves[c.ID()]++
	// Run resolves once for validation; only its second lookup is the active run.
	active := g.resolves[c.ID()] == 2
	if active {
		g.started = append(g.started, c.TextQuant)
	}
	g.mu.Unlock()
	if active && c == g.gateCombo {
		select {
		case <-g.entered:
		default:
			close(g.entered)
		}
		<-g.gate
	}
	return failingFiles(c)
}

func TestCanceledQueuedRunNeverStartsAndLaterRunsWait(t *testing.T) {
	ix := &recordingIndexer{}
	life, end := context.WithCancel(context.Background())
	defer end()
	gateCombo := combo(contract.QuantBF16)
	g := &gatedFiles{gateCombo: gateCombo, resolves: make(map[string]int), entered: make(chan struct{}), gate: make(chan struct{})}
	l := New(life, ix, nil, g.resolve, t.TempDir()+"/log", 1)
	ch := l.Subscribe(context.Background())

	a, err := l.Run(context.Background(), combo(contract.QuantBF16), nil)
	if err != nil {
		t.Fatal(err)
	}
	bctx, cancelB := context.WithCancel(context.Background())
	b, err := l.Run(bctx, combo(contract.QuantQ8_0), nil)
	if err != nil {
		t.Fatal(err)
	}
	cCombo := contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16}
	c, err := l.Run(context.Background(), cCombo, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-g.entered
	cancelB()
	canceled := time.After(60 * time.Second)
waitCanceled:
	for {
		select {
		case s := <-ch:
			if s.RunID == b {
				if s.State != contract.LabCanceled {
					t.Fatalf("queued run terminal step: %+v, want canceled", s)
				}
				break waitCanceled
			}
			if s.State == contract.LabDone || s.State == contract.LabFailed || s.State == contract.LabCanceled {
				t.Fatalf("run %d finished while active run %d is held", s.RunID, a)
			}
		case <-canceled:
			t.Fatal("timed out waiting for queued run cancellation")
		}
	}
	close(g.gate)
	order := collect(t, ch, 2)
	if order[0] != a || order[1] != c {
		t.Fatalf("order %v: A %d and C %d must follow in order", order, a, c)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.started) != 2 {
		t.Fatalf("starts %v: the canceled run must not start", g.started)
	}
}

func TestLifetimeEndCancelsActiveAndQueuedRuns(t *testing.T) {
	ix := &recordingIndexer{}
	life, end := context.WithCancel(context.Background())
	g := &gatedFiles{gateCombo: combo(contract.QuantBF16), resolves: make(map[string]int), entered: make(chan struct{}), gate: make(chan struct{})}
	l := New(life, ix, nil, g.resolve, t.TempDir()+"/log", 1)
	ch := l.Subscribe(context.Background())
	a, _ := l.Run(context.Background(), combo(contract.QuantBF16), nil)
	b, _ := l.Run(context.Background(), combo(contract.QuantQ8_0), nil)
	<-g.entered
	end()
	close(g.gate)
	got := map[contract.RunID]contract.LabState{}
	deadline := time.After(60 * time.Second)
	for len(got) < 2 {
		select {
		case s, ok := <-ch:
			if !ok {
				t.Fatalf("stream closed with terminal steps %v", got)
			}
			if s.State == contract.LabCanceled || s.State == contract.LabFailed || s.State == contract.LabDone {
				got[s.RunID] = s.State
			}
		case <-deadline:
			t.Fatalf("timed out; terminal steps %v", got)
		}
	}
	if got[a] != contract.LabCanceled || got[b] != contract.LabCanceled {
		t.Fatalf("terminal states %v, want canceled for both runs (%d, %d)", got, a, b)
	}
	if _, err := l.Run(context.Background(), combo(contract.QuantBF16), nil); !errors.Is(err, contract.ErrStopped) {
		t.Fatalf("Run after lifetime end: %v", err)
	}
}

func TestUnknownComboIsRefused(t *testing.T) {
	life, end := context.WithCancel(context.Background())
	defer end()
	l := New(life, &recordingIndexer{}, nil, func(contract.Combo) (runtime.Files, error) {
		return runtime.Files{}, contract.ErrUnknownCombo
	}, t.TempDir()+"/log", 1)
	if _, err := l.Run(context.Background(), combo(contract.QuantBF16), nil); !errors.Is(err, contract.ErrUnknownCombo) {
		t.Fatalf("got %v, want ErrUnknownCombo", err)
	}
}

func TestPromptFollowsTheDocumentFormat(t *testing.T) {
	if got := Prompt("chunk-01.txt", "hello"); got != "title: chunk-01.txt | text: hello" {
		t.Fatalf("prompt %q", got)
	}
}

func TestMedianAndQuality(t *testing.T) {
	if m := median([]float64{5, 1, 3}); m != 3 {
		t.Fatalf("median %v", m)
	}
	if m := median([]float64{4, 1, 3, 2}); m != 2.5 {
		t.Fatalf("even median %v", m)
	}
	if c := cosine([]float32{1, 0}, []float32{0, 1}); c != 0 {
		t.Fatalf("orthogonal cosine %v", c)
	}
}
