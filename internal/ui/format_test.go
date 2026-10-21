package ui

import (
	"testing"
	"time"
)

func TestCount(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1284: "1,284", 1234567: "1,234,567"} {
		if got := count(in); got != want {
			t.Errorf("count(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestSizeAndRate(t *testing.T) {
	cases := map[int64]string{
		512:           "512 bytes",
		4_100_000:     "4.1 MB",
		1_600_000_000: "1.6 GB",
		982_000_000:   "982 MB",
	}
	for in, want := range cases {
		if got := size(in); got != want {
			t.Errorf("size(%d) = %q, want %q", in, got, want)
		}
	}
	if got := rate(4_100_000); got != "4.1 MB/s" {
		t.Errorf("rate = %q", got)
	}
}

func TestETA(t *testing.T) {
	cases := map[time.Duration]string{
		-1:                              "estimating…",
		30 * time.Second:                "less than a minute",
		37 * time.Minute:                "about 37 min",
		3*time.Hour + 20*time.Minute:    "about 3 h 20 min",
		89*time.Minute + 40*time.Second: "about 90 min",
	}
	for in, want := range cases {
		if got := eta(in); got != want {
			t.Errorf("eta(%v) = %q, want %q", in, got, want)
		}
	}
}
