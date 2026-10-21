package watch

import (
	"context"
	"errors"
)

// Watch is not implemented on Windows yet (SPEC "Windows phase"). It returns errors.ErrUnsupported.
func Watch(ctx context.Context, root string, skip func(path string, isDir bool) bool, onChange func()) error {
	return errors.ErrUnsupported
}
