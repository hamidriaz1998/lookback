// Package app wires the packages together and runs the application lifecycle: single instance,
// tray, global hotkey and power events. It sets TURSO_GO_CACHE_DIR to <data root>/lib before the
// database opens (SPEC section 2). It holds wiring only, no business logic.
package app
