package bench

import "testing"

// mkfifo skips the test: Windows has no named pipes in the file system for the tests to put in a picked folder.
func mkfifo(t *testing.T, dir string) string {
	t.Helper()
	t.Skip("no FIFOs on Windows")
	return ""
}
