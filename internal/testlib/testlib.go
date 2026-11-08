// Package testlib holds helpers that the tests of several packages share.
package testlib

import (
	"os"
	"path/filepath"
)

// UseSharedTursoLibrary points the Turso driver at one native-library folder per test binary, outside every
// test's temporary folder. The driver extracts its library into the folder named by TURSO_GO_CACHE_DIR and loads
// it for the life of the process. Windows cannot delete a loaded library, so with the library inside t.TempDir()
// the cleanup of the first test that opens a store fails. The folder is kept between runs, which also saves the
// extraction. name keeps the folders of test binaries that run in parallel apart. Call it from TestMain.
func UseSharedTursoLibrary(name string) {
	if os.Getenv("TURSO_GO_CACHE_DIR") != "" {
		return
	}
	dir := filepath.Join(os.TempDir(), "lookback-test-turso", name)
	if err := os.MkdirAll(dir, 0o755); err == nil {
		os.Setenv("TURSO_GO_CACHE_DIR", dir)
	}
}
