//go:build linux

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The entry must start the binary with --background, quote a path with spaces, and be removed when it is off.
func TestSetAutostartWritesAndRemovesEntry(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	entry := filepath.Join(cfg, "autostart", "lookback.desktop")

	if err := SetAutostart(false); err != nil { // off with no entry is not an error
		t.Fatalf("off without an entry: %v", err)
	}
	if err := SetAutostart(true); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	b, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Exec=\"" + exe + "\" --background\n"; !strings.Contains(string(b), want) {
		t.Fatalf("entry lacks %q:\n%s", want, b)
	}
	if err := SetAutostart(true); err != nil { // on twice rewrites the same entry
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(filepath.Join(cfg, "autostart", ".lookback-*")); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
	if err := SetAutostart(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Fatalf("entry still exists after off: %v", err)
	}
}

// With no XDG_CONFIG_HOME the entry goes to ~/.config/autostart.
func TestSetAutostartDefaultsToHomeConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)
	if err := SetAutostart(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "autostart", "lookback.desktop")); err != nil {
		t.Fatal(err)
	}
}

func TestExecQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/bin/lookback": `"/usr/bin/lookback"`,
		"/a b/lookback":     `"/a b/lookback"`,
		`/a"b$c` + "`d%e":   `"/a\\"b\\$c\\` + "`d%%e\"",
		`/a\b`:              `"/a\\\\b"`,
	} {
		if got := execQuote(in); got != want {
			t.Errorf("execQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
