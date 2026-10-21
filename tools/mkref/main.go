// Command mkref computes the BF16 reference vectors for the bundled sample, so the test lab can score quality
// without downloading BF16 (SPEC section 6). It runs the CPU llama-server with the BF16 pair, embeds the 8 images
// and 16 chunks the way the lab does, and writes assets/sample/reference.json and reference.bin.
//
// Usage (from the repository root):
//
//	go run ./tools/mkref -server <llama-server> -models <dir with the BF16 pair> -build b11755
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/hamidriaz1998/lookback/assets"
	"github.com/hamidriaz1998/lookback/internal/bench"
	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/runtime"
)

func main() {
	server := flag.String("server", "", "CPU llama-server binary (required)")
	models := flag.String("models", "", "directory with embeddinggemma-2-BF16.gguf and mmproj-embeddinggemma-2-BF16.gguf (required)")
	build := flag.String("build", "b11755", "llama.cpp release tag of -server")
	out := flag.String("out", "assets/sample", "sample directory to write the reference into")
	threads := flag.Int("t", 8, "CPU threads")
	logPath := flag.String("log", filepath.Join(os.TempDir(), "mkref-llama-server.log"), "server log")
	flag.Parse()
	if *server == "" || *models == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*server, *models, *build, *out, *threads, *logPath); err != nil {
		log.Fatal(err)
	}
}

func run(server, models, build, out string, threads int, logPath string) error {
	space := contract.VectorSpace{TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16}
	text, mm, err := runtime.ModelPaths(models, space)
	if err != nil {
		return err
	}
	srv, err := runtime.Start(context.Background(), runtime.Options{
		Files:   runtime.Files{Server: server, Model: text, MMProj: mm},
		Threads: threads,
		LogPath: logPath,
	})
	if err != nil {
		return err
	}
	defer srv.Stop(context.Background())

	ref := assets.Reference{Model: filepath.Base(text), Backend: string(contract.BackendCPU), Build: build}
	images, err := assets.Images()
	if err != nil {
		return err
	}
	for _, img := range images {
		v, _, err := srv.EmbedImage(context.Background(), img.Data, img.MIME)
		if err != nil {
			return fmt.Errorf("%s: %w", img.Name, err)
		}
		ref.Items = append(ref.Items, img.Name)
		ref.Vectors = append(ref.Vectors, v)
	}
	chunks, err := assets.Chunks()
	if err != nil {
		return err
	}
	for _, c := range chunks {
		v, _, err := srv.EmbedTexts(context.Background(), []string{bench.Prompt(c.Name, c.Text)})
		if err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		ref.Items = append(ref.Items, c.Name)
		ref.Vectors = append(ref.Vectors, v[0])
	}
	manifest, bin, err := assets.EncodeReference(ref)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "reference.json"), manifest, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "reference.bin"), bin, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %d reference vectors to %s (model %s, build %s, peak RAM %d MB)\n",
		len(ref.Items), out, ref.Model, build, srv.PeakRAM()>>20)
	return nil
}
