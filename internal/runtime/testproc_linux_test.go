package runtime

import "syscall"

// alive reports whether a process with this PID exists.
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// killPID kills a process at once, as kill -9 does.
func killPID(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }

// falseBin is a program that exits at once, whatever its arguments.
var falseBin = "/bin/false"
