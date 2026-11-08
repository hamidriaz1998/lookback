package runtime

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// alive reports whether a process with this PID is still running.
func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}

// killPID kills a process at once (TerminateProcess).
func killPID(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

// falseBin is a program that exits at once, whatever its arguments: hostname.exe rejects them.
var falseBin = filepath.Join(os.Getenv("SystemRoot"), "System32", "hostname.exe")
