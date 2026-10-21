package hw

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Builds on the reference laptop (SPEC section 4 runtimes, unpacked by WP1 into runtimes/).
var realBuilds = map[contract.Backend]string{
	contract.BackendCPU:    "/home/hamid/Desktop/AI/embeddingGemma_test/llama-cpu/llama-b11755/llama-server",
	contract.BackendVulkan: "/home/hamid/Desktop/AI/embeddingGemma_test/llama-vulkan/llama-b11755/llama-server",
}

// TestRealProbeListsTheRadeonThroughVulkan is the reference-laptop check: the Vulkan build lists the iGPU.
func TestRealProbeListsTheRadeonThroughVulkan(t *testing.T) {
	if testing.Short() {
		t.Skip("runs llama-server --list-devices")
	}
	bin := realBuilds[contract.BackendVulkan]
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("no Vulkan build: %v", err)
	}
	d := NewDetector(func(b contract.Backend) (string, error) { return realBuilds[b], nil })
	p, err := d.Probe(context.Background(), contract.BackendVulkan)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Candidate || len(p.Devices) == 0 {
		t.Fatalf("Vulkan probe: %+v", p)
	}
	if !strings.Contains(p.Devices[0].Name, "Radeon") {
		t.Fatalf("Vulkan device %q, want the Radeon iGPU", p.Devices[0].Name)
	}
	t.Logf("Vulkan probe: %+v", p.Devices)
}

// TestRealDetectReadsThisMachine checks the CPU, RAM and GPU reads on the reference laptop.
func TestRealDetectReadsThisMachine(t *testing.T) {
	if testing.Short() {
		t.Skip("reads /proc and /sys")
	}
	h, err := NewDetector(nil).Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.CPU == "" || h.Threads == 0 || h.RAMTotal == 0 {
		t.Fatalf("hardware %+v", h)
	}
	var amd bool
	for _, g := range h.GPUs {
		if g.Vendor == "amd" {
			amd = true
		}
	}
	if !amd {
		t.Fatalf("no AMD GPU in %+v", h.GPUs)
	}
	t.Logf("hardware: %+v", h)
}

// TestRealCPUProbeIsACandidate checks the CPU build needs no device.
func TestRealCPUProbeIsACandidate(t *testing.T) {
	if testing.Short() {
		t.Skip("runs llama-server")
	}
	p, err := NewDetector(func(b contract.Backend) (string, error) { return realBuilds[b], nil }).Probe(context.Background(), contract.BackendCPU)
	if err != nil || !p.Candidate {
		t.Fatalf("CPU probe: %+v, %v", p, err)
	}
}
