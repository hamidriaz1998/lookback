package main

import (
	"context"
	"log"
	"os"

	"github.com/hamidriaz1998/lookback/internal/app"
)

func main() {
	opts, err := app.ParseArgs(os.Args[1:])
	if err != nil {
		log.Fatalf("lookback: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := app.Run(opts, ctx); err != nil {
		log.Fatalf("lookback: %v", err)
	}
}
