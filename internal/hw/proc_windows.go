package hw

import (
	"os/exec"
	"syscall"
)

// createNoWindow keeps a console program started by a GUI app from opening a console window.
const createNoWindow = 0x08000000

// hideWindow stops cmd from flashing a console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
