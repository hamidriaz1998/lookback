package hw

import (
	"fmt"
	"strings"
)

// nvidiaDisplayVersion turns the driver version that Windows stores for an adapter into the version that NVIDIA
// prints on its downloads: the last five digits, as 560.94 for 32.0.15.6094 (SPEC "Hardware detection on
// Windows"). It returns "" when the version has fewer than five digits.
func nvidiaDisplayVersion(driverVersion string) string {
	digits := strings.ReplaceAll(driverVersion, ".", "")
	if len(digits) < 5 || strings.Trim(digits, "0123456789") != "" {
		return ""
	}
	last := digits[len(digits)-5:]
	return fmt.Sprintf("%s.%s", last[:3], last[3:])
}

// pciVendor maps the vendor part of a PCI matching device id, such as "PCI\VEN_10DE&DEV_2504&SUBSYS_...", to the
// vendor names of contract.GPU (SPEC section 5). ok is false for an id that is not on the PCI bus.
func pciVendor(matchingDeviceID string) (vendor string, ok bool) {
	id := strings.ToUpper(matchingDeviceID)
	if !strings.HasPrefix(id, `PCI\VEN_`) || len(id) < len(`PCI\VEN_`)+4 {
		return "", false
	}
	switch id[len(`PCI\VEN_`) : len(`PCI\VEN_`)+4] {
	case "10DE":
		return "nvidia", true
	case "1002":
		return "amd", true
	case "8086":
		return "intel", true
	}
	return "other", true
}
