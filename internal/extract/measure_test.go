package extract

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// TestImageCapMeasure measures the 1600 px cap against the real sidecar (PLAN WP4). It runs only when
// LOOKBACK_LLAMA_SERVER (the llama-server binary), LOOKBACK_MODEL_DIR (the folder with the BF16 pair) and
// LOOKBACK_MEASURE_IMAGES (a folder of large screenshots, read only) are set, and it skips under -short.
//
// For each of the largest images it compares the full-size PNG with the capped output: prompt tokens, median
// wall time of the embedding request over three runs, and the cosine similarity of the two vectors.
func TestImageCapMeasure(t *testing.T) {
	if testing.Short() {
		t.Skip("sidecar measurement skipped under -short")
	}
	server := os.Getenv("LOOKBACK_LLAMA_SERVER")
	modelDir := os.Getenv("LOOKBACK_MODEL_DIR")
	imageDir := os.Getenv("LOOKBACK_MEASURE_IMAGES")
	if server == "" || modelDir == "" || imageDir == "" {
		t.Skip("set LOOKBACK_LLAMA_SERVER, LOOKBACK_MODEL_DIR and LOOKBACK_MEASURE_IMAGES to measure")
	}

	base, key := startSidecar(t, server, modelDir)
	client := &http.Client{Timeout: 5 * time.Minute}

	paths := largestPNGs(t, imageDir, 4)
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}

		var capped []byte
		extractTime := medianDuration(3, func() {
			capped = nil
			err := Image{}.Extract(context.Background(), contract.FileRec{ID: 1, Path: p, Kind: contract.KindImage},
				func(u contract.Unit) error { capped = u.Image; return nil })
			if err != nil {
				t.Fatal(err)
			}
		})
		capCfg, _, _ := image.DecodeConfig(bytes.NewReader(capped))

		fullVec, fullTok, fullTime := embedMedian(t, client, base, key, raw, "image/png")
		capVec, capTok, capTime := embedMedian(t, client, base, key, capped, "image/png")

		t.Logf("%s: full %dx%d %.2f MB, %d tokens, %.2f s | capped %dx%d %.0f KB, %d tokens, %.2f s | extract %.0f ms | cosine %.4f",
			filepath.Base(p), cfg.Width, cfg.Height, float64(len(raw))/1e6, fullTok, fullTime.Seconds(),
			capCfg.Width, capCfg.Height, float64(len(capped))/1e3, capTok, capTime.Seconds(),
			float64(extractTime.Microseconds())/1000, cosine(fullVec, capVec))
	}
}

// startSidecar runs llama-server with the BF16 pair and returns its base URL and API key. It stops the
// server when the test ends.
func startSidecar(t *testing.T, server, modelDir string) (string, string) {
	t.Helper()
	port := freePort(t)
	raw := make([]byte, 16)
	rand.Read(raw)
	key := hex.EncodeToString(raw)
	logFile, err := os.Create(filepath.Join(t.TempDir(), "llama.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(server,
		"-m", filepath.Join(modelDir, "embeddinggemma-2-BF16.gguf"),
		"--mmproj", filepath.Join(modelDir, "mmproj-embeddinggemma-2-BF16.gguf"),
		"--embeddings", "--pooling", "mean", "-c", "8192", "-t", "8",
		"--host", "127.0.0.1", "--port", strconv.Itoa(port), "--api-key", key)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		logFile.Close()
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(120 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodGet, base+"/health", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return base, key
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("sidecar not healthy after 120 s")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// freePort returns a free TCP port on 127.0.0.1.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// largestPNGs returns the n largest screenshot PNG files in dir, by size, largest first.
func largestPNGs(t *testing.T, dir string, n int) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "screenshot*.png"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(matches, func(i, j int) bool {
		a, _ := os.Stat(matches[i])
		b, _ := os.Stat(matches[j])
		return a.Size() > b.Size()
	})
	if len(matches) > n {
		matches = matches[:n]
	}
	if len(matches) == 0 {
		t.Fatalf("no screenshot PNG files in %s", dir)
	}
	return matches
}

// embedMedian embeds img three times and returns the vector, the prompt tokens and the median time.
func embedMedian(t *testing.T, client *http.Client, base, key string, img []byte, mime string) ([]float32, int, time.Duration) {
	t.Helper()
	var vec []float32
	var tokens int
	d := medianDuration(3, func() {
		vec, tokens = embedImageOnce(t, client, base, key, img, mime)
	})
	return vec, tokens, d
}

// embedImageOnce posts one image as a data URL to /v1/embeddings.
func embedImageOnce(t *testing.T, client *http.Client, base, key string, img []byte, mime string) ([]float32, int) {
	t.Helper()
	url := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img)
	body, _ := json.Marshal(map[string]any{"input": []any{map[string]any{"content": []any{
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}},
	}}}})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/embeddings", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 1 {
		t.Fatalf("sidecar returned %d vectors", len(out.Data))
	}
	return out.Data[0].Embedding, out.Usage.PromptTokens
}

// medianDuration runs f n times and returns the median duration.
func medianDuration(n int, f func()) time.Duration {
	ds := make([]time.Duration, n)
	for i := range ds {
		start := time.Now()
		f()
		ds[i] = time.Since(start)
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	return ds[n/2]
}

// cosine returns the cosine similarity of two vectors.
func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
