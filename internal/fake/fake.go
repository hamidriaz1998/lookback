// Package fake holds in-memory implementations of the interfaces in internal/contract.
// The UI builds against them before the real packages exist. They are deterministic:
// the same calls give the same results, so screenshots and tests do not drift.
// The fakes share one fixture: the Store's files, items and rows, and what the Indexer does to them.
package fake

import "github.com/hamidriaz1998/lookback/internal/contract"

// Compile-time checks that every fake satisfies its interface.
var (
	_ contract.Store       = (*Store)(nil)
	_ contract.Embedder    = (*Embedder)(nil)
	_ contract.Extractor   = (*Extractor)(nil)
	_ contract.Indexer     = (*Indexer)(nil)
	_ contract.ThemeSource = (*ThemeSource)(nil)
	_ contract.Catalog     = (*Catalog)(nil)
	_ contract.Downloader  = (*Downloader)(nil)
	_ contract.Detector    = (*Detector)(nil)
	_ contract.Lab         = (*Lab)(nil)
	_ contract.Runtime     = (*Runtime)(nil)
)
