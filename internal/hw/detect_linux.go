//go:build linux

package hw

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Detect reads the CPU, RAM and GPUs from /proc and /sys (SPEC section 5).
func (*Detector) Detect(ctx context.Context) (contract.Hardware, error) {
	if err := ctx.Err(); err != nil {
		return contract.Hardware{}, err
	}
	cpuText, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return contract.Hardware{}, err
	}
	cpu, err := ParseCPUInfo(string(cpuText))
	if err != nil {
		return contract.Hardware{}, err
	}
	memText, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return contract.Hardware{}, err
	}
	total, avail, err := ParseMeminfo(string(memText))
	if err != nil {
		return contract.Hardware{}, err
	}
	return contract.Hardware{
		CPU:          cpu.Model,
		Threads:      cpu.Threads,
		RAMTotal:     total,
		RAMAvailable: avail,
		GPUs:         pciGPUs(),
		NvidiaDriver: nvidiaDriver(),
	}, nil
}

// PhysicalCores returns the number of physical cores, which sets llama-server's -t (SPEC section 8).
func PhysicalCores() (int, error) {
	text, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return 0, err
	}
	info, err := ParseCPUInfo(string(text))
	if err != nil {
		return 0, err
	}
	return info.PhysicalCores, nil
}

// nvidiaDriver returns the NVIDIA kernel driver version, or "" when none is loaded.
func nvidiaDriver() string {
	text, err := os.ReadFile("/proc/driver/nvidia/version")
	if err != nil {
		return ""
	}
	return ParseNvidiaDriver(string(text))
}

// pciGPUs lists the display controllers (PCI class 0x03xxxx) with their vendor and VRAM from amdgpu.
func pciGPUs() []contract.GPU {
	devs, _ := filepath.Glob("/sys/bus/pci/devices/*")
	var gpus []contract.GPU
	for _, d := range devs {
		class := readTrim(filepath.Join(d, "class"))
		if !strings.HasPrefix(class, "0x03") {
			continue
		}
		g := contract.GPU{Vendor: vendorName(readTrim(filepath.Join(d, "vendor")))}
		id := readTrim(filepath.Join(d, "device"))
		g.Name = g.Vendor + " GPU " + id
		if drv, err := filepath.EvalSymlinks(filepath.Join(d, "driver")); err == nil {
			g.Driver = filepath.Base(drv)
		}
		if v, ok := readInt(filepath.Join(d, "mem_info_vram_total")); ok {
			g.VRAMTotal = v
			used, _ := readInt(filepath.Join(d, "mem_info_vram_used"))
			g.VRAMFree = v - used
		}
		gpus = append(gpus, g)
	}
	return gpus
}

// vendorName maps a PCI vendor ID to the vendor names of contract.GPU (SPEC section 5).
func vendorName(id string) string {
	switch id {
	case "0x10de":
		return "nvidia"
	case "0x1002":
		return "amd"
	case "0x8086":
		return "intel"
	}
	return "other"
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readInt(path string) (int64, bool) {
	v, err := strconv.ParseInt(readTrim(path), 10, 64)
	return v, err == nil
}
