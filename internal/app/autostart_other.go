//go:build !linux

package app

import "errors"

// SetAutostart is not implemented outside Linux yet (SPEC "Windows phase": the HKCU Run value).
func SetAutostart(on bool) error { return errors.ErrUnsupported }
