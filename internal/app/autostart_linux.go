//go:build linux

package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SetAutostart turns Start at login on or off (SPEC section 13). On: the XDG autostart entry
// $XDG_CONFIG_HOME/autostart/lookback.desktop (default ~/.config/autostart) runs this binary with --background.
// Off: the entry is removed. Both are idempotent.
func SetAutostart(on bool) error {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(home, ".config")
	}
	exe := ""
	if on {
		var err error
		if exe, err = os.Executable(); err != nil {
			return err
		}
	}
	return writeAutostart(filepath.Join(dir, "autostart", "lookback.desktop"), exe, on)
}

// writeAutostart writes (on) or removes (off) the autostart entry at path. The write is atomic, so a crash
// never leaves half an entry for the session to read.
func writeAutostart(path, exe string, on bool) error {
	if !on {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	entry := "[Desktop Entry]\nType=Application\nName=Lookback\nComment=Semantic search over your local files\n" +
		"Exec=" + execQuote(exe) + " --background\nIcon=lookback\nTerminal=false\nX-GNOME-Autostart-enabled=true\n"
	tmp, err := os.CreateTemp(filepath.Dir(path), ".lookback-*.desktop")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op after the rename
	if _, err := tmp.WriteString(entry); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// execQuote quotes one Exec argument as the Desktop Entry specification says: double quotes, a backslash before
// ", `, $ and \, and %% for a literal %. The value is then a string, where a literal backslash is written \\, so
// each backslash of the quoting layer is doubled again.
func execQuote(s string) string {
	s = strings.NewReplacer(`\`, `\\\\`, `"`, `\\"`, "`", `\\`+"`", `$`, `\\$`, `%`, `%%`).Replace(s)
	return `"` + s + `"`
}
