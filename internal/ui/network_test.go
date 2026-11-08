package ui

import (
	"runtime"
	"testing"
)

// TestHasUNCPrefix checks the pure UNC test, which decides network paths on Windows. The inputs are spelled with
// \x5c escapes, the one backslash.
func TestHasUNCPrefix(t *testing.T) {
	const bs = "\x5c"
	unc := bs + bs
	tests := []struct {
		path string
		want bool
	}{
		{unc + `server\share\dir\f.txt`, true},
		{`//server/share/dir`, true},
		{`C:\Users\Me`, false},
		{unc + `?` + bs + `C:\Users\Me`, false},
		{unc + `.` + bs + `C:`, false},
		{"/home/me", false},
		{bs + `Users`, false},
	}
	for _, tt := range tests {
		if got := hasUNCPrefix(tt.path); got != tt.want {
			t.Errorf("hasUNCPrefix(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// TestIsNetworkPathUNC checks that a UNC path is a network folder on Windows. Off Windows nothing is.
func TestIsNetworkPathUNC(t *testing.T) {
	const bs = "\x5c"
	want := runtime.GOOS == "windows"
	if got := isNetworkPath(bs + bs + `server` + bs + `share` + bs + `dir` + bs + `f.txt`); got != want {
		t.Errorf("isNetworkPath(UNC) = %v, want %v", got, want)
	}
	if isNetworkPath(t.TempDir()) {
		t.Error("a local temporary folder is not a network folder")
	}
}
