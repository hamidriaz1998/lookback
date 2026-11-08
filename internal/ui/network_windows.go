package ui

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// isNetworkPath reports whether path is on a network share: a UNC path, or a drive letter mapped to a share.
func isNetworkPath(path string) bool {
	if hasUNCPrefix(path) {
		return true
	}
	vol := filepath.VolumeName(path)
	if len(vol) != 2 {
		return false
	}
	root, err := windows.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return false
	}
	return windows.GetDriveType(root) == windows.DRIVE_REMOTE
}
