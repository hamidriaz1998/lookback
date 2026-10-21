package theme

import (
	"context"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Omarchy is Linux only. On Windows it returns nil, which means that the source is not listed.
func Omarchy(ctx context.Context) contract.ThemeSource { return nil }
