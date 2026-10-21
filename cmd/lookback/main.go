package main

import (
	"log"
	"os"

	"github.com/hamidriaz1998/lookback/internal/app"
)

func main() {
	opts, err := app.ParseArgs(os.Args[1:])
	if err != nil {
		log.Fatalf("lookback: %v", err)
	}
	// nil: the indexer is wired in WP9, so pause reasons are tracked but not yet applied.
	if err := app.Run(opts, app.DefaultConfig(), nil); err != nil {
		log.Fatalf("lookback: %v", err)
	}
}
