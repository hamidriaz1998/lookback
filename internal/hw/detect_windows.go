package hw

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

var (
	kernel32                      = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatusEx      = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetLogicalProcessorInfoEx = kernel32.NewProc("GetLogicalProcessorInformationEx")
)

// Detect reads the CPU, RAM and GPUs from Windows (SPEC section 5).
func (*Detector) Detect(ctx context.Context) (contract.Hardware, error) {
	if err := ctx.Err(); err != nil {
		return contract.Hardware{}, err
	}
	threads, _, err := processorCounts()
	if err != nil {
		return contract.Hardware{}, err
	}
	total, avail, err := memory()
	if err != nil {
		return contract.Hardware{}, err
	}
	gpus := registryGPUs()
	return contract.Hardware{
		CPU:          cpuName(),
		Threads:      threads,
		RAMTotal:     total,
		RAMAvailable: avail,
		GPUs:         gpus,
		NvidiaDriver: nvidiaDriverOf(gpus),
	}, nil
}

// PhysicalCores returns the number of physical cores, which sets llama-server's -t (SPEC section 8).
func PhysicalCores() (int, error) {
	_, cores, err := processorCounts()
	return cores, err
}

// nvidiaDriver returns the display version of the first NVIDIA adapter, or "" when there is none.
func nvidiaDriver() string { return nvidiaDriverOf(registryGPUs()) }

func nvidiaDriverOf(gpus []contract.GPU) string {
	for _, g := range gpus {
		if g.Vendor == "nvidia" && g.Driver != "" {
			return g.Driver
		}
	}
	return ""
}

// cpuName reads the processor name from the registry, such as "AMD Ryzen 7 5700U with Radeon Graphics".
func cpuName() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE)
	if err != nil {
		return "unknown CPU"
	}
	defer k.Close()
	name, _, err := k.GetStringValue("ProcessorNameString")
	if err != nil || strings.TrimSpace(name) == "" {
		return "unknown CPU"
	}
	return strings.Join(strings.Fields(name), " ")
}

// memory returns total and available physical memory in bytes (GlobalMemoryStatusEx).
func memory() (total, avail int64, err error) {
	var m struct {
		Length                       uint32
		MemoryLoad                   uint32
		TotalPhys, AvailPhys         uint64
		TotalPageFile, AvailPageFile uint64
		TotalVirtual, AvailVirtual   uint64
		AvailExtendedVirtual         uint64
	}
	m.Length = uint32(unsafe.Sizeof(m))
	r, _, e := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0, 0, fmt.Errorf("hw: GlobalMemoryStatusEx: %w", e)
	}
	return int64(m.TotalPhys), int64(m.AvailPhys), nil
}

// processorCounts returns the logical processor and physical core counts, from
// GetLogicalProcessorInformationEx with RelationProcessorCore. Each record is one core; the bits of its group
// masks are its logical processors.
func processorCounts() (threads, cores int, err error) {
	const relationProcessorCore = 0
	var size uint32
	procGetLogicalProcessorInfoEx.Call(relationProcessorCore, 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 {
		return 0, 0, errors.New("hw: GetLogicalProcessorInformationEx returned no data")
	}
	buf := make([]byte, size)
	r, _, e := procGetLogicalProcessorInfoEx.Call(relationProcessorCore, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return 0, 0, fmt.Errorf("hw: GetLogicalProcessorInformationEx: %w", e)
	}
	// SYSTEM_LOGICAL_PROCESSOR_INFORMATION_EX: Relationship uint32, Size uint32, then PROCESSOR_RELATIONSHIP:
	// Flags, EfficiencyClass, Reserved[20], GroupCount uint16 (at byte 30), GROUP_AFFINITY[] (at byte 32, 16 bytes each).
	for off := 0; off+32 <= int(size); {
		rec := buf[off:]
		recSize := int(*(*uint32)(unsafe.Pointer(&rec[4])))
		if recSize < 32 || off+recSize > int(size) {
			break
		}
		if *(*uint32)(unsafe.Pointer(&rec[0])) == relationProcessorCore {
			cores++
			groups := int(*(*uint16)(unsafe.Pointer(&rec[30])))
			for g := 0; g < groups && 32+16*(g+1) <= recSize; g++ {
				threads += bits.OnesCount64(*(*uint64)(unsafe.Pointer(&rec[32+16*g])))
			}
		}
		off += recSize
	}
	if cores == 0 || threads == 0 {
		return 0, 0, errors.New("hw: no processor cores reported")
	}
	return threads, cores, nil
}

// displayClassKey is the registry class of display adapters. Each adapter that Windows has a driver for is a
// numbered subkey, such as 0000, with its description, driver version and PCI id.
const displayClassKey = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`

// registryGPUs lists the display adapters that sit on the PCI bus from the display class key (SPEC "Hardware
// detection on Windows"). Software adapters such as Microsoft Basic Display have a root\ id and are left out.
// VRAMTotal is the dedicated memory the driver reports; VRAMFree stays 0 (unknown), because llama-server
// --list-devices is the ground truth for free memory (SPEC section 5).
func registryGPUs() []contract.GPU {
	class, err := registry.OpenKey(registry.LOCAL_MACHINE, displayClassKey, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer class.Close()
	names, err := class.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	var gpus []contract.GPU
	for _, name := range names {
		if len(name) != 4 || strings.Trim(name, "0123456789") != "" {
			continue // Configuration, Properties and the like
		}
		k, err := registry.OpenKey(class, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		g, ok := readGPU(k)
		k.Close()
		if ok {
			gpus = append(gpus, g)
		}
	}
	return gpus
}

// readGPU reads one adapter key. ok is false for adapters that are not on the PCI bus.
func readGPU(k registry.Key) (contract.GPU, bool) {
	id, _, _ := k.GetStringValue("MatchingDeviceId")
	vendor, ok := pciVendor(id)
	if !ok {
		return contract.GPU{}, false
	}
	desc, _, _ := k.GetStringValue("DriverDesc")
	version, _, _ := k.GetStringValue("DriverVersion")
	g := contract.GPU{Name: strings.TrimSpace(desc), Vendor: vendor, VRAMTotal: dedicatedMemory(k)}
	if g.Name == "" {
		g.Name = vendor + " GPU"
	}
	if vendor == "nvidia" {
		g.Driver = nvidiaDisplayVersion(version)
	} else {
		g.Driver = version
	}
	return g, true
}

// dedicatedMemory reads HardwareInformation.qwMemorySize, which drivers write as a QWORD or as 8 bytes.
func dedicatedMemory(k registry.Key) int64 {
	if v, _, err := k.GetIntegerValue("HardwareInformation.qwMemorySize"); err == nil {
		return int64(v)
	}
	if b, _, err := k.GetBinaryValue("HardwareInformation.qwMemorySize"); err == nil && len(b) >= 8 {
		return int64(binary.LittleEndian.Uint64(b))
	}
	return 0
}
