//go:build !linux

package hw

import (
	"context"
	"errors"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Detect is not implemented outside Linux yet (SPEC: Windows phase).
func (*Detector) Detect(ctx context.Context) (contract.Hardware, error) {
	return contract.Hardware{}, errors.ErrUnsupported
}

// PhysicalCores is not implemented outside Linux yet (SPEC: Windows phase).
func PhysicalCores() (int, error) { return 0, errors.ErrUnsupported }

func nvidiaDriver() string { return "" }

func pciGPUs() []contract.GPU { return nil }
