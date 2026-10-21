// Command spike-sidecar is spike 3 of M0. It starts llama-server with the EmbeddingGemma files,
// waits for /health, embeds one text and one image, prints the vector sizes and token usage,
// and stops the server. Run it with: go run ./cmd/spike-sidecar
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultServer = "/home/hamid/Desktop/AI/embeddingGemma_test/llama-cpu/llama-b11755/llama-server"
	defaultModels = "/home/hamid/Desktop/AI/embeddingGemma_test/models"
	healthTimeout = 120 * time.Second
	stopTimeout   = 10 * time.Second
)

func main() {
	var (
		server  = flag.String("server", defaultServer, "path to llama-server")
		model   = flag.String("m", filepath.Join(defaultModels, "embeddinggemma-2-BF16.gguf"), "text model (GGUF)")
		mmproj  = flag.String("mmproj", filepath.Join(defaultModels, "mmproj-embeddinggemma-2-BF16.gguf"), "encoder file (GGUF)")
		threads = flag.Int("t", 8, "CPU threads")
		ctxSize = flag.Int("c", 8192, "context size")
		logPath = flag.String("log", "llama-server.log", "file for llama-server output")
		image   = flag.String("image", "", "PNG or JPEG to embed (default: first ~/Pictures/screenshot* file)")
	)
	flag.Parse()
	if err := run(*server, *model, *mmproj, *threads, *ctxSize, *logPath, *image); err != nil {
		log.Fatal(err)
	}
}

func run(server, model, mmproj string, threads, ctxSize int, logPath, imagePath string) error {
	port, err := freePort()
	if err != nil {
		return err
	}
	key, err := randomHex(32)
	if err != nil {
		return err
	}
	if imagePath == "" {
		if imagePath, err = firstScreenshot(); err != nil {
			return err
		}
	}
	img, err := os.ReadFile(imagePath)
	if err != nil {
		return err
	}

	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logFile.Close()

	args := []string{
		"-m", model, "--mmproj", mmproj, "--embeddings", "--pooling", "mean",
		"-c", strconv.Itoa(ctxSize), "-t", strconv.Itoa(threads),
		"--host", "127.0.0.1", "--port", strconv.Itoa(port), "--api-key", key,
	}
	cmd := exec.Command(server, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	setProcessGroup(cmd)
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start llama-server: %w", err)
	}
	srv := &sidecar{cmd: cmd, done: make(chan struct{})}
	go func() {
		srv.waitErr = cmd.Wait()
		close(srv.done)
	}()
	defer srv.stop()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Minute}
	if err := waitHealthy(client, base, key, srv); err != nil {
		return err
	}
	fmt.Printf("health: ok after %s (port %d, log %s)\n", time.Since(start).Round(time.Millisecond), port, logPath)

	// Text query, with the prompt prefix from SPEC section 9.
	textStart := time.Now()
	textVec, textTokens, err := embedText(client, base, key, "task: search result | query: test")
	if err != nil {
		return fmt.Errorf("text embedding: %w", err)
	}
	fmt.Printf("text:  dims=%d norm=%.4f prompt_tokens=%d wall=%s\n",
		len(textVec), norm(textVec), textTokens, time.Since(textStart).Round(time.Millisecond))

	// One image, as a data URL in the OpenAI-style content array (SPEC section 8).
	imgStart := time.Now()
	imgVec, imgTokens, err := embedImage(client, base, key, img, mimeFor(imagePath))
	if err != nil {
		return fmt.Errorf("image embedding: %w", err)
	}
	fmt.Printf("image: %s (%d bytes) dims=%d norm=%.4f prompt_tokens=%d wall=%s\n",
		filepath.Base(imagePath), len(img), len(imgVec), norm(imgVec), imgTokens, time.Since(imgStart).Round(time.Millisecond))
	if len(textVec) != 768 || len(imgVec) != 768 {
		return fmt.Errorf("expected 768 dimensions, got text %d and image %d", len(textVec), len(imgVec))
	}
	return nil
}

// freePort asks the kernel for a free loopback port and releases it.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// randomHex returns n random bytes as hex.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// firstScreenshot returns the first screenshot* PNG in ~/Pictures, by name.
func firstScreenshot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(filepath.Join(home, "Pictures"))
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(strings.ToLower(n), "screenshot") && strings.EqualFold(filepath.Ext(n), ".png") {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return "", errors.New("no screenshot*.png in ~/Pictures; pass -image")
	}
	sort.Strings(names)
	return filepath.Join(home, "Pictures", names[0]), nil
}

// mimeFor returns the image MIME type for a file name.
func mimeFor(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".jpg") || strings.EqualFold(filepath.Ext(path), ".jpeg") {
		return "image/jpeg"
	}
	return "image/png"
}

// sidecar is a running llama-server. done closes when the process has exited, and waitErr is set before that.
type sidecar struct {
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error
}

// waitHealthy polls GET /health until it returns 200, the server exits, or healthTimeout passes.
func waitHealthy(client *http.Client, base, key string, srv *sidecar) error {
	deadline := time.Now().Add(healthTimeout)
	for {
		select {
		case <-srv.done:
			return fmt.Errorf("llama-server exited before it was healthy: %v", srv.waitErr)
		default:
		}
		req, err := http.NewRequest(http.MethodGet, base+"/health", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := client.Do(req)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && bytes.Contains(body, []byte(`"ok"`)) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("llama-server did not become healthy in time")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// embeddingResponse is the part of the /v1/embeddings reply that the spike reads.
type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// embedText posts one text input and returns its vector and the prompt token count.
func embedText(client *http.Client, base, key, text string) ([]float32, int, error) {
	body := map[string]any{"input": []string{text}}
	return postEmbedding(client, base, key, body)
}

// embedImage posts one image as a data URL and returns its vector and the prompt token count.
func embedImage(client *http.Client, base, key string, img []byte, mime string) ([]float32, int, error) {
	url := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img)
	body := map[string]any{"input": []any{map[string]any{"content": []any{
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}},
	}}}}
	return postEmbedding(client, base, key, body)
}

// postEmbedding sends body to /v1/embeddings and decodes the first vector.
func postEmbedding(client *http.Client, base, key string, body any) ([]float32, int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequest(http.MethodPost, base+"/v1/embeddings", bytes.NewReader(raw))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var out embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, 0, fmt.Errorf("decode reply (HTTP %d): %w", resp.StatusCode, err)
	}
	if out.Error != nil {
		return nil, 0, fmt.Errorf("server error (HTTP %d): %s", resp.StatusCode, out.Error.Message)
	}
	if len(out.Data) != 1 {
		return nil, 0, fmt.Errorf("HTTP %d: got %d vectors, want 1", resp.StatusCode, len(out.Data))
	}
	return out.Data[0].Embedding, out.Usage.PromptTokens, nil
}

// norm returns the Euclidean length of v. A normalized vector gives 1.
func norm(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

// stop ends llama-server and its process group: SIGTERM, then SIGKILL after stopTimeout.
func (s *sidecar) stop() {
	select {
	case <-s.done:
		return
	default:
	}
	signalGroup(s.cmd, false)
	select {
	case <-s.done:
		fmt.Printf("stop: llama-server exited after SIGTERM (%v)\n", s.waitErr)
	case <-time.After(stopTimeout):
		signalGroup(s.cmd, true)
		<-s.done
		fmt.Printf("stop: llama-server killed after %s (%v)\n", stopTimeout, s.waitErr)
	}
}
