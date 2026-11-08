//go:build !windows

package ui

// isNetworkPath reports whether path is on a network share. Only Windows has UNC paths and mapped drives, so this
// is always false here.
func isNetworkPath(string) bool { return false }
