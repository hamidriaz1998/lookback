//go:build !linux

package contract

// DefaultHotkey is the default global shortcut that shows or hides the window (SPEC section 14, Q35).
// Win+. is the emoji panel on Windows, so the default there is Ctrl+Alt+Space.
const DefaultHotkey = "Ctrl+Alt+Space"
