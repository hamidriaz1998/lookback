package app

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// TestWriteAutostartSetsAndRemovesTheRunValue checks the value that Start at login writes, on a key of its own so
// that the test never touches the real Run key.
func TestWriteAutostartSetsAndRemovesTheRunValue(t *testing.T) {
	key := `Software\LookbackTest\` + t.Name()
	t.Cleanup(func() {
		registry.DeleteKey(registry.CURRENT_USER, key)
		registry.DeleteKey(registry.CURRENT_USER, `Software\LookbackTest`)
	})
	read := func() (string, error) {
		k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE)
		if err != nil {
			return "", err
		}
		defer k.Close()
		v, _, err := k.GetStringValue(runValueName)
		return v, err
	}

	if err := writeAutostart(key, `C:\Program Files\Lookback app\Lookback.exe`, true); err != nil {
		t.Fatal(err)
	}
	want := `"C:\Program Files\Lookback app\Lookback.exe" --background`
	if got, err := read(); err != nil || got != want {
		t.Fatalf("value = %q, %v; want %q", got, err, want)
	}
	if err := writeAutostart(key, `C:\Other\Lookback.exe`, true); err != nil { // on twice replaces the value
		t.Fatal(err)
	}
	if got, _ := read(); got != `"C:\Other\Lookback.exe" --background` {
		t.Fatalf("value after a second on = %q", got)
	}
	if err := writeAutostart(key, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := read(); !errors.Is(err, registry.ErrNotExist) {
		t.Fatalf("value after off: err = %v, want not exist", err)
	}
	if err := writeAutostart(key, "", false); err != nil { // off twice is not an error
		t.Fatal(err)
	}
	if err := writeAutostart(`Software\LookbackTest\missing`, "", false); err != nil { // off with no key at all
		t.Fatal(err)
	}
}
