package hw

import (
	"bufio"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// deviceLine matches one device line of llama-server --list-devices, such as
// "Vulkan0: AMD Radeon Graphics (RADV RENOIR) (12970 MiB, 10384 MiB free)". The device name can contain
// parentheses, so the match anchors on the trailing "(N MiB, M MiB free)".
var deviceLine = regexp.MustCompile(`^\s*[A-Za-z0-9_-]+\d*:\s+(.+?)\s+\((\d+) MiB,\s*(\d+) MiB free\)\s*$`)

// ParseListDevices returns the devices in the output of llama-server --list-devices. Output with
// "(none)" in the device list gives no devices and no error. Output without a device header is an error.
func ParseListDevices(out string) ([]contract.Device, error) {
	if !strings.Contains(out, "Available devices:") {
		return nil, errors.New("hw: no device list in llama-server output")
	}
	var devs []contract.Device
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "Available devices:") || strings.TrimSpace(line) == "(none)" {
			continue
		}
		m := deviceLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		total, _ := strconv.ParseInt(m[2], 10, 64)
		free, _ := strconv.ParseInt(m[3], 10, 64)
		devs = append(devs, contract.Device{Name: m[1], VRAMTotal: total << 20, VRAMFree: free << 20})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return devs, nil
}

// CPUInfo is what /proc/cpuinfo says about the processor.
type CPUInfo struct {
	Model         string
	Threads       int // logical processors
	PhysicalCores int // unique (physical id, core id) pairs
}

// ParseCPUInfo reads the text of /proc/cpuinfo.
func ParseCPUInfo(text string) (CPUInfo, error) {
	var info CPUInfo
	cores := map[string]bool{}
	var phys, core string
	sc := bufio.NewScanner(strings.NewReader(text))
	flush := func() {
		if phys != "" || core != "" {
			cores[phys+"/"+core] = true
		}
		phys, core = "", ""
	}
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			flush()
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			info.Threads++
			flush()
		case "model name":
			if info.Model == "" {
				info.Model = v
			}
		case "physical id":
			phys = v
		case "core id":
			core = v
		}
	}
	flush()
	if info.Threads == 0 {
		return info, errors.New("hw: no processor in /proc/cpuinfo")
	}
	info.PhysicalCores = len(cores)
	if info.PhysicalCores == 0 {
		info.PhysicalCores = info.Threads
	}
	return info, nil
}

// ParseMeminfo returns MemTotal and MemAvailable in bytes from /proc/meminfo.
func ParseMeminfo(text string) (total, avail int64, err error) {
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		v, perr := strconv.ParseInt(f[1], 10, 64)
		if perr != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total = v << 10
		case "MemAvailable:":
			avail = v << 10
		}
	}
	if total == 0 {
		return 0, 0, errors.New("hw: no MemTotal in /proc/meminfo")
	}
	return total, avail, nil
}

var versionRE = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// ParseNvidiaDriver returns the driver version in the first line of /proc/driver/nvidia/version, such as
// "580.82.07", or "" when there is none.
func ParseNvidiaDriver(text string) string {
	first, _, _ := strings.Cut(text, "\n")
	if !strings.Contains(first, "NVRM") && !strings.Contains(first, "NVIDIA") {
		return ""
	}
	return versionRE.FindString(first)
}

// DriverAtLeast reports whether driver version v (dotted numbers) is at least min.
func DriverAtLeast(v, min string) (bool, error) {
	a, err := parseVersion(v)
	if err != nil {
		return false, err
	}
	b, err := parseVersion(min)
	if err != nil {
		return false, err
	}
	for i := 0; i < len(b) || i < len(a); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y, nil
		}
	}
	return true, nil
}

func parseVersion(v string) ([]int, error) {
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("hw: bad driver version %q: %w", v, err)
		}
		out = append(out, n)
	}
	return out, nil
}
