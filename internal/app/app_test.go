package app

import (
	"errors"
	"testing"
)

var errHotkeyTest = errors.New("invalid shortcut")

func TestReselectingRestoredHotkeyClearsFailureStatus(t *testing.T) {
	setHotkeyStatus("B", errHotkeyTest)
	t.Cleanup(func() { setHotkeyStatus("", nil) })

	a := &application{hotkey: "A"} // A was restored after B failed.
	if err := a.setHotkey("A"); err != nil {
		t.Fatalf("setHotkey(A) error = %v, want nil", err)
	}
	if acc, err := HotkeyStatus(); acc != "A" || err != nil {
		t.Fatalf("HotkeyStatus() = (%q, %v), want (A, nil)", acc, err)
	}
}
