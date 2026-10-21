// Command e2e drives the real services headlessly: it seeds a finished first run (CPU, Q8_0/Q8_0) and one
// folder, opens the services the window uses, waits for the indexer to finish, and runs text queries through
// the real sidecar and Searcher. Run it with a data dir whose models and runtimes are already installed:
//
//	go run ./tools/e2e -data-dir DIR -folder DIR "query" ...
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/hamidriaz1998/lookback/internal/app"
	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/search"
	"github.com/hamidriaz1998/lookback/internal/store"
)

func main() {
	dataDir := flag.String("data-dir", "", "data folder with installed models and runtimes")
	folder := flag.String("folder", "", "folder to index")
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st, err := store.Open(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	combo := contract.Combo{Backend: contract.BackendCPU, TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantQ8_0}
	must(st.SetSetting(ctx, "onboarded", "1"))
	must(st.SetSetting(ctx, "onboard_combo", combo.ID()))
	fs, err := st.ListFolders(ctx)
	must(err)
	if len(fs) == 0 {
		must(st.SaveFolder(ctx, &contract.Folder{Path: *folder, AddedAt: time.Now()}))
	}
	st.Close()

	s, err := app.OpenServices(ctx, *dataDir)
	must(err)
	defer s.Close()
	// Choosing a combination in onboarding ensures its files; already-installed files report done at once,
	// which records the combination and starts the indexer.
	assets, err := s.Deps.Catalog.Assets(s.Deps.OS, combo)
	must(err)
	must(s.Deps.Downloader.Ensure(ctx, assets))
	start := time.Now()
	progress := s.Indexer.Subscribe(ctx)
	deadline := time.After(10 * time.Minute)
	for done := false; !done; {
		select {
		case p, ok := <-progress:
			if !ok {
				log.Fatal("progress stream closed")
			}
			fmt.Printf("%6.1fs gen %d: %d/%d files, %d errors, %.2f files/s, eta %v, current %s\n",
				time.Since(start).Seconds(), p.Gen, p.FilesDone, p.FilesTotal, p.Errors, p.FilesPerSec, p.ETA.Round(time.Second), p.Current)
			done = p.FilesTotal > 0 && p.FilesDone+p.Errors >= p.FilesTotal
		case <-deadline:
			log.Fatal("indexing did not finish in 10 minutes")
		}
	}
	fmt.Printf("indexed in %.1fs\n", time.Since(start).Seconds())

	var active *contract.Generation
	for i := 0; i < 60 && active == nil; i++ {
		gens, err := s.Deps.Store.ListGenerations(ctx)
		must(err)
		for _, g := range gens {
			if g.State == contract.GenActive {
				g := g
				active = &g
			}
		}
		if active == nil {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if active == nil {
		log.Fatal("no active generation after indexing")
	}
	sess, err := s.Deps.Runtime.Session(ctx, active.Space)
	must(err)
	defer sess.Close()
	sr := search.New(s.Deps.Store)
	for _, q := range flag.Args() {
		t := time.Now()
		res, err := sr.Run(ctx, sess, q, nil)
		must(err)
		fmt.Printf("\nquery %q: %d results in %v\n", q, len(res), time.Since(t).Round(time.Millisecond))
		for i, r := range res {
			if i == 5 {
				break
			}
			fmt.Printf("  %.3f  %s\n", r.Score, r.Path)
		}
	}
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
