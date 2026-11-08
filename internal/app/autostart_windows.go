package app

import (
	"errors"
	"os"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName = "Lookback"
)

// SetAutostart turns Start at login on or off (SPEC section 13). On: the value Lookback under
// HKCU\Software\Microsoft\Windows\CurrentVersion\Run is the quoted path of this executable plus --background.
// Off: the value is removed. Both are idempotent.
func SetAutostart(on bool) error {
	exe := ""
	if on {
		var err error
		if exe, err = os.Executable(); err != nil {
			return err
		}
	}
	return writeAutostart(runKeyPath, exe, on)
}

// writeAutostart sets (on) or deletes (off) the Run value under HKCU\<keyPath>.
func writeAutostart(keyPath, exe string, on bool) error {
	if !on {
		k, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer k.Close()
		if err := k.DeleteValue(runValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(runValueName, `"`+exe+`" --background`)
}
