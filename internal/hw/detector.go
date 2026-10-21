// Package hw detects the CPU, RAM and GPUs of this computer, and parses the device list that
// llama-server --list-devices prints. Its results decide the backend candidates (SPEC section 5).
package hw

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Minimum Linux driver versions for the CUDA builds (SPEC section 5). CUDA 12.8 GA needs >=570.26;
// CUDA 13.x minor-version compatibility needs >=580. CUDA 13.4 features and newly enabled platforms can
// require R615 or newer: https://docs.nvidia.com/cuda/cuda-toolkit-release-notes/index.html. These baselines
// are not exercised on the reference laptop, which has no NVIDIA GPU. The catalog (WP1) must carry the same values.
const (
	minDriverCUDA12 = "570.26"
	minDriverCUDA13 = "580"
)

// Binaries returns the llama-server path of backend b on disk, or an error when its build is not downloaded.
type Binaries func(b contract.Backend) (string, error)

// Detector is a contract.Detector. Detect reads the hardware from the OS; Probe runs the backend's
// llama-server --list-devices.
type Detector struct {
	binary Binaries
}

// NewDetector returns a Detector that finds backend builds with binary.
func NewDetector(binary Binaries) *Detector { return &Detector{binary: binary} }

// Probe runs llama-server --list-devices for backend b. A backend is a candidate when its probe lists at
// least one device. The CPU backend is always a candidate. CUDA also needs an NVIDIA driver that is new enough.
func (d *Detector) Probe(ctx context.Context, b contract.Backend) (contract.BackendProbe, error) {
	out := contract.BackendProbe{Backend: b}
	if b == contract.BackendCPU {
		out.Candidate = true
		return out, nil
	}
	if b == contract.BackendCUDA12 || b == contract.BackendCUDA13 {
		drv := nvidiaDriver()
		if drv == "" {
			out.Reason = "no NVIDIA driver"
			return out, nil
		}
		min := minDriverCUDA13
		if b == contract.BackendCUDA12 {
			min = minDriverCUDA12
		}
		ok, err := DriverAtLeast(drv, min)
		if err != nil {
			return out, err
		}
		if !ok {
			out.Reason = fmt.Sprintf("NVIDIA driver %s is older than %s", drv, min)
			return out, nil
		}
	}
	bin, err := d.binary(b)
	if err != nil {
		return out, err
	}
	devs, err := listDevices(ctx, bin)
	if err != nil {
		return out, err
	}
	out.Devices = devs
	out.Candidate = len(devs) > 0
	if !out.Candidate {
		out.Reason = "the build lists no devices"
	}
	return out, nil
}

// listDevices runs bin --list-devices with a 20 s limit and parses its output.
func listDevices(ctx context.Context, bin string) ([]contract.Device, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "--list-devices")
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("hw: %s --list-devices: %w: %s", bin, err, buf.String())
	}
	return ParseListDevices(buf.String())
}

// PrecisionFor applies the SPEC section 5 rule: BF16 for both parts when the device has at least 4 GB free
// VRAM, or, on the CPU backend, at least 16 GB of RAM. Otherwise Q8_0 for both.
func PrecisionFor(b contract.Backend, vramFree, ramTotal int64) contract.Quant {
	const gb = 1 << 30
	if b == contract.BackendCPU {
		if ramTotal >= 16*gb {
			return contract.QuantBF16
		}
		return contract.QuantQ8_0
	}
	if vramFree >= 4*gb {
		return contract.QuantBF16
	}
	return contract.QuantQ8_0
}
