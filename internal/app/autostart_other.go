//go:build !linux && !windows

package app

import "errors"

// SetAutostart is not implemented outside Linux and Windows.
func SetAutostart(on bool) error { return errors.ErrUnsupported }
