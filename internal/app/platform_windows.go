package app

import (
	"os"
	"path/filepath"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// osName is the operating system the app runs on, as the catalog names it.
const osName = contract.OS("windows")

// serverExe is the file name of llama-server inside a runtime archive.
const serverExe = "llama-server.exe"

// DefaultDataDir is the SPEC section 3 data root: %LOCALAPPDATA%\Lookback. os.UserCacheDir returns
// %LOCALAPPDATA% on Windows.
func DefaultDataDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Lookback"), nil
}
