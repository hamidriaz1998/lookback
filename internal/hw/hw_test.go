package hw

import (
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

func TestParseListDevicesVulkan(t *testing.T) {
	out := "ggml_vulkan: found 1 device\nAvailable devices:\n  Vulkan0: AMD Radeon Graphics (RADV RENOIR) (12970 MiB, 10384 MiB free)\n"
	devs, err := ParseListDevices(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 {
		t.Fatalf("devices %d, want 1", len(devs))
	}
	d := devs[0]
	if d.Name != "AMD Radeon Graphics (RADV RENOIR)" || d.VRAMTotal != 12970<<20 || d.VRAMFree != 10384<<20 {
		t.Fatalf("device %+v", d)
	}
}

func TestParseListDevicesNone(t *testing.T) {
	devs, err := ParseListDevices("Available devices:\n  (none)\n")
	if err != nil || len(devs) != 0 {
		t.Fatalf("(none): %v, %d devices", err, len(devs))
	}
}

func TestParseListDevicesNoHeaderIsAnError(t *testing.T) {
	if _, err := ParseListDevices("segmentation fault"); err == nil {
		t.Fatal("output without a device list was accepted")
	}
}

func TestParseCPUInfoCountsPhysicalCores(t *testing.T) {
	// Two logical processors per core, four cores, one package.
	text := ""
	for i := 0; i < 8; i++ {
		text += "processor\t: " + string(rune('0'+i)) + "\nmodel name\t: AMD Ryzen 7 5700U\n" +
			"physical id\t: 0\ncore id\t: " + string(rune('0'+i/2)) + "\n\n"
	}
	info, err := ParseCPUInfo(text)
	if err != nil {
		t.Fatal(err)
	}
	if info.Threads != 8 || info.PhysicalCores != 4 || info.Model != "AMD Ryzen 7 5700U" {
		t.Fatalf("cpu %+v", info)
	}
}

func TestParseMeminfo(t *testing.T) {
	total, avail, err := ParseMeminfo("MemTotal:       21962048 kB\nMemAvailable:   14680064 kB\n")
	if err != nil {
		t.Fatal(err)
	}
	if total != 21962048<<10 || avail != 14680064<<10 {
		t.Fatalf("total %d avail %d", total, avail)
	}
}

func TestNvidiaDriverVersionAndMinimums(t *testing.T) {
	v := ParseNvidiaDriver("NVRM version: NVIDIA UNIX x86_64 Kernel Module  580.82.07  Mon Aug  4 2025\nGCC version: gcc 15\n")
	if v != "580.82.07" {
		t.Fatalf("driver %q", v)
	}
	cases := []struct {
		drv, min string
		want     bool
	}{
		{"580.82.07", minDriverCUDA13, true},
		{"579.99", minDriverCUDA13, false},
		{"570.26", "570.26", true}, // the Linux CUDA 12 minimum
		{"570.25.9", "570.26", false},
		{"575.1", "570.26", true},
		{"551.61", "551.61", true}, // the Windows CUDA 12 minimum
		{"551.60", "551.61", false},
		{minDriverCUDA12, minDriverCUDA12, true},
	}
	for _, c := range cases {
		got, err := DriverAtLeast(c.drv, c.min)
		if err != nil || got != c.want {
			t.Fatalf("DriverAtLeast(%s, %s) = %v, %v; want %v", c.drv, c.min, got, err, c.want)
		}
	}
}

func TestPrecisionRule(t *testing.T) {
	const gb = 1 << 30
	cases := []struct {
		b         contract.Backend
		vram, ram int64
		want      contract.Quant
	}{
		{contract.BackendVulkan, 6800 << 20, 0, contract.QuantBF16},
		{contract.BackendVulkan, 4 * gb, 0, contract.QuantBF16},
		{contract.BackendVulkan, 4*gb - 1, 0, contract.QuantQ8_0},
		{contract.BackendCPU, 0, 16 * gb, contract.QuantBF16},
		{contract.BackendCPU, 0, 16*gb - 1, contract.QuantQ8_0},
	}
	for _, c := range cases {
		if got := PrecisionFor(c.b, c.vram, c.ram); got != c.want {
			t.Fatalf("PrecisionFor(%s, %d, %d) = %s, want %s", c.b, c.vram, c.ram, got, c.want)
		}
	}
}
