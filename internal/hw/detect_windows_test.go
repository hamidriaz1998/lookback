package hw

import (
	"context"
	"testing"
)

// TestDetectWindows checks that detection reports a CPU, memory and consistent core counts on the machine that runs it.
func TestDetectWindows(t *testing.T) {
	h, err := (&Detector{}).Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", h)
	if h.CPU == "" || h.CPU == "unknown CPU" {
		t.Errorf("CPU name = %q", h.CPU)
	}
	if h.RAMTotal < 1<<30 || h.RAMAvailable <= 0 || h.RAMAvailable > h.RAMTotal {
		t.Errorf("RAM total %d, available %d", h.RAMTotal, h.RAMAvailable)
	}
	cores, err := PhysicalCores()
	if err != nil {
		t.Fatal(err)
	}
	if cores < 1 || h.Threads < cores {
		t.Errorf("threads %d, physical cores %d", h.Threads, cores)
	}
	for _, g := range h.GPUs {
		if g.Name == "" || g.Vendor == "" {
			t.Errorf("GPU without a name or vendor: %+v", g)
		}
	}
}
