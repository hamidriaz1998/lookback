//go:build !linux && !windows

package runtime

import (
	"context"
	"errors"
	"time"
)

// process is not implemented outside Linux yet (SPEC: Windows phase: job object with kill-on-close).
type process struct {
	pid     int
	exited  chan struct{}
	waitErr error
}

func startProcess(o Options, port int, key string) (*process, error) {
	return nil, errors.ErrUnsupported
}

func (p *process) bindCollision() bool { return false }

func (p *process) terminate(ctx context.Context, grace time.Duration) {}

func (p *process) peakRSS() int64 { return 0 }

func trimLogAll(path string) string { return "" }
