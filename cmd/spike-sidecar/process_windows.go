//go:build windows

package main

import "os/exec"

// setProcessGroup does nothing on Windows. The spike kills the process directly; the app
// will put the child in a job object so that it dies with the app (SPEC section 8).
func setProcessGroup(cmd *exec.Cmd) {}

// signalGroup kills the server process. Windows has no SIGTERM for console-less children.
func signalGroup(cmd *exec.Cmd, kill bool) {
	_ = cmd.Process.Kill()
}
