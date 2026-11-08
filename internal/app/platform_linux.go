package app

import (
	"os"
	"path/filepath"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// osName is the operating system the app runs on, as the catalog names it.
const osName = contract.OS("linux")

// serverExe is the file name of llama-server inside a runtime archive.
const serverExe = "llama-server"

// DefaultDataDir is the SPEC section 3 data root: $XDG_DATA_HOME/lookback, or ~/.local/share/lookback.
func DefaultDataDir() (string, error) {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "lookback"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "lookback"), nil
}
