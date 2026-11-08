package hw

import "testing"

func TestNvidiaDisplayVersion(t *testing.T) {
	for in, want := range map[string]string{
		"32.0.15.7283": "572.83",
		"31.0.15.5123": "551.23",
		"32.0.15.6094": "560.94", // the example in the SPEC
		"32.0.15.8105": "581.05",
		"31.0.21.1000": "110.00", // not an NVIDIA scheme, but the rule is only the last five digits
		"1.2":          "",
		"":             "",
		"a.b.c.dddde":  "",
	} {
		if got := nvidiaDisplayVersion(in); got != want {
			t.Errorf("nvidiaDisplayVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPCIVendor(t *testing.T) {
	for _, tc := range []struct {
		id     string
		vendor string
		ok     bool
	}{
		{`PCI\VEN_10DE&DEV_2504&SUBSYS_00000000&REV_a1`, "nvidia", true},
		{`pci\ven_1002&dev_1636&subsys_12341234&rev_c5`, "amd", true},
		{`PCI\VEN_8086&DEV_9A49`, "intel", true},
		{`PCI\VEN_1234&DEV_0001`, "other", true},
		{`root\basicdisplay`, "", false},
		{`PCI\VEN_`, "", false},
		{``, "", false},
	} {
		v, ok := pciVendor(tc.id)
		if v != tc.vendor || ok != tc.ok {
			t.Errorf("pciVendor(%q) = %q, %v; want %q, %v", tc.id, v, ok, tc.vendor, tc.ok)
		}
	}
}
