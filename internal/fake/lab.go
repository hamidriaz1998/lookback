package fake

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// labResults are the measured rows of the reference laptop (UI guide section 3.2): the four
// combinations of the test matrix that have a result.
var labResults = []contract.LabResult{
	{Combo: vulkanBF16Q8, Bench: contract.BenchResult{ImageMS: 2410, TextTokensPerSec: 312, PeakRAM: 900 << 20, VRAM: 1200 << 20, Quality: fp(0.996)}},
	{Combo: contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16},
		Bench: contract.BenchResult{ImageMS: 2683, TextTokensPerSec: 298, PeakRAM: 1100 << 20, VRAM: 1600 << 20, Quality: fp(1.0)}},
	{Combo: contract.Combo{Backend: contract.BackendCPU, TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0},
		Bench: contract.BenchResult{ImageMS: 4759, TextTokensPerSec: 160, PeakRAM: 2000 << 20, Quality: fp(0.996)}},
	{Combo: contract.Combo{Backend: contract.BackendCPU, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16},
		Bench: contract.BenchResult{ImageMS: 4850, TextTokensPerSec: 158, PeakRAM: 2100 << 20, Quality: fp(1.0)}},
}

// labInstalled returns the two installed combinations of the fixture, with their lab rows.
func labInstalled() []contract.Installed {
	installed := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	var out []contract.Installed
	for _, r := range labResults {
		if r.Combo == vulkanBF16Q8 || r.Combo == (contract.Combo{Backend: contract.BackendCPU, TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0}) {
			b := r.Bench
			out = append(out, contract.Installed{Combo: r.Combo, InstalledAt: installed, Bench: &b})
		}
	}
	return out
}

// fp returns a pointer to v.
func fp(v float64) *float64 { return &v }

// Lab is a contract.Lab that replays the fixture results. A run emits one step every 200 ms.
type Lab struct {
	streams *stream[contract.LabStep]
}

// NewLab returns a Lab with the fixture results. Its streams close when ctx ends.
func NewLab(ctx context.Context) *Lab {
	l := &Lab{streams: newStream(
		func(s contract.LabStep) string { return s.Combo.ID() },
		func(s contract.LabStep) bool { return s.State == contract.LabDone || s.State == contract.LabFailed },
	)}
	go func() {
		<-ctx.Done()
		l.streams.close()
	}()
	return l
}

// Run tests combination c. It fails at once for a combination without a fixture result, and for
// more than 10 extra files. The extra files add steps but do not change the sample's numbers.
func (l *Lab) Run(ctx context.Context, c contract.Combo, extra []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(extra) > 10 {
		return errors.New("lab: at most 10 extra files")
	}
	var row *contract.LabResult
	for i := range labResults {
		if labResults[i].Combo == c {
			row = &labResults[i]
		}
	}
	if row == nil {
		return fmt.Errorf("lab: no fixture result for %s", c.ID())
	}
	steps := []string{}
	for i := 1; i <= 8; i++ {
		steps = append(steps, fmt.Sprintf("Embedding image %d of 8", i))
	}
	steps = append(steps, "Embedding 16 text chunks")
	for i := range extra {
		steps = append(steps, fmt.Sprintf("Embedding your file %d of %d", i+1, len(extra)))
	}
	go func() {
		if !l.send(ctx, contract.LabStep{Combo: c, State: contract.LabStarting, Step: "Starting " + c.ID()}) {
			return
		}
		time.Sleep(200 * time.Millisecond)
		for _, s := range steps {
			if !l.send(ctx, contract.LabStep{Combo: c, State: contract.LabEmbedding, Step: s}) {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		result := *row
		l.send(ctx, contract.LabStep{Combo: c, State: contract.LabDone, Step: "Done", Result: &result})
	}()
	return nil
}

// send publishes a step and reports false when ctx ends first.
func (l *Lab) send(ctx context.Context, s contract.LabStep) bool {
	if ctx.Err() != nil {
		return false
	}
	l.streams.publish(s)
	return true
}

// Subscribe returns a stream of the steps of all runs.
func (l *Lab) Subscribe(ctx context.Context) <-chan contract.LabStep {
	return l.streams.subscribe(ctx)
}

// Recommend returns the fastest result whose quality is at least 0.99. Results with n/a quality do not qualify.
func (l *Lab) Recommend(results []contract.LabResult) (contract.Combo, bool) {
	var best contract.LabResult
	found := false
	for _, r := range results {
		q := r.Bench.Quality
		if q == nil || *q < 0.99 {
			continue
		}
		if !found || r.Bench.ImageMS < best.Bench.ImageMS {
			best, found = r, true
		}
	}
	return best.Combo, found
}
