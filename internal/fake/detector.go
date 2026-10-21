package fake

import (
	"context"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Detector is a contract.Detector with the reference laptop: Ryzen 7 5700U, 16 threads, 21 GB RAM,
// and a Radeon RENOIR iGPU with 12970 MiB total and 6800 MiB free. It has no NVIDIA GPU.
type Detector struct{}

// NewDetector returns the fake detector.
func NewDetector() *Detector { return &Detector{} }

// Detect returns the reference laptop's hardware.
func (*Detector) Detect(ctx context.Context) (contract.Hardware, error) {
	if err := ctx.Err(); err != nil {
		return contract.Hardware{}, err
	}
	return contract.Hardware{
		CPU:          "AMD Ryzen 7 5700U",
		Threads:      16,
		RAMTotal:     21 << 30,
		RAMAvailable: 14 << 30,
		GPUs: []contract.GPU{{
			Name: "AMD Radeon Graphics", Vendor: "amd", Driver: "Mesa RADV",
			VRAMTotal: 12970 << 20, VRAMFree: 6800 << 20,
		}},
	}, nil
}

// Probe returns the --list-devices result for backend b on the reference laptop.
func (*Detector) Probe(ctx context.Context, b contract.Backend) (contract.BackendProbe, error) {
	if err := ctx.Err(); err != nil {
		return contract.BackendProbe{}, err
	}
	switch b {
	case contract.BackendCPU:
		return contract.BackendProbe{Backend: b, Candidate: true}, nil
	case contract.BackendVulkan:
		return contract.BackendProbe{Backend: b, Candidate: true, Devices: []contract.Device{{
			Name: "AMD Radeon Graphics (RADV RENOIR)", VRAMTotal: 12970 << 20, VRAMFree: 6800 << 20,
		}}}, nil
	default:
		return contract.BackendProbe{Backend: b, Reason: "no NVIDIA GPU"}, nil
	}
}
