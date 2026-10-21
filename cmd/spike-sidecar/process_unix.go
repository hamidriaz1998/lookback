//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts llama-server in its own process group, so stopping it also stops its children.
// Pdeathsig kills it when the app dies without running its cleanup (kill -9, a panic). It fires when the
// thread that forked the child exits, so main calls runtime.LockOSThread.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

// signalGroup sends SIGTERM, or SIGKILL when kill is true, to the server's process group.
func signalGroup(cmd *exec.Cmd, kill bool) {
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}
