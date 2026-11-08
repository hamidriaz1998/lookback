//go:build !windows

package hw

import "os/exec"

// hideWindow does nothing outside Windows.
func hideWindow(*exec.Cmd) {}
