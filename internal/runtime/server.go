// Package runtime runs the llama-server sidecar for the active combination: start on a free port with an API
// key, health checks, logs, restart and CPU fallback, and process cleanup. Its client implements
// contract.Embedder (SPEC section 8).
package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Startup and shutdown limits (SPEC section 8).
const (
	startTimeout   = 120 * time.Second // one deadline for the whole startup, across port retries
	healthAttempt  = 2 * time.Second   // each health or key check
	portRetries    = 3                 // starts on a new port after a bind collision
	stopTimeout    = 10 * time.Second  // SIGTERM before SIGKILL
	logRotateSize  = 10 << 20
	embeddingDims  = 768
	requestTimeout = 5 * time.Minute
)

// Files are the files one llama-server runs with.
type Files struct {
	Server string // the llama-server binary of the backend
	Model  string // the text model GGUF
	MMProj string // the encoder GGUF
}

// ModelPaths returns the text and encoder GGUF paths for vector space s in dir (SPEC section 4 names).
func ModelPaths(dir string, s contract.VectorSpace) (text, mmproj string, err error) {
	q := map[contract.Quant]string{contract.QuantQ8_0: "Q8_0", contract.QuantBF16: "BF16"}
	t, ok1 := q[s.TextQuant]
	e, ok2 := q[s.EncQuant]
	if !ok1 || !ok2 {
		return "", "", fmt.Errorf("runtime: unsupported precision in %+v", s)
	}
	return filepath.Join(dir, "embeddinggemma-2-"+t+".gguf"), filepath.Join(dir, "mmproj-embeddinggemma-2-"+e+".gguf"), nil
}

// Options start one llama-server.
type Options struct {
	Files   Files
	GPU     bool   // -ngl 99 for GPU backends
	Threads int    // -t
	CtxSize int    // -c; 0 means 8192
	LogPath string // the log file; rotated at 10 MB
}

// Server is one running llama-server. It implements contract.Embedder. Its process ends when Stop runs or
// when it exits on its own; Exited closes then.
type Server struct {
	base   string
	key    string
	client *http.Client
	proc   *process
}

// Start launches llama-server with o and returns when it answers an authenticated request. The startup has one
// deadline of 120 s. A child that exits ends the wait at once, and a bind collision retries on a new port.
// Health checks use short per-attempt timeouts, and the child must still be alive when Start returns.
// ctx cancels the start, which stops the child it started.
func Start(ctx context.Context, o Options) (*Server, error) {
	if o.CtxSize == 0 {
		o.CtxSize = 8192
	}
	startCtx, cancelStart := context.WithTimeout(ctx, startTimeout)
	defer cancelStart()
	var lastErr error
	for attempt := 0; attempt < portRetries; attempt++ {
		if err := startCtx.Err(); err != nil {
			return nil, fmt.Errorf("runtime: start: %w (last error: %v)", err, lastErr)
		}
		port, err := freePort()
		if err != nil {
			return nil, err
		}
		key, err := randomKey()
		if err != nil {
			return nil, err
		}
		proc, err := startProcess(o, port, key)
		if err != nil {
			return nil, err
		}
		attemptCtx, cancelAttempt := context.WithCancel(startCtx)
		go func() {
			select {
			case <-proc.exited:
				cancelAttempt()
			case <-attemptCtx.Done():
			}
		}()
		srv := &Server{base: fmt.Sprintf("http://127.0.0.1:%d", port), key: key, proc: proc,
			client: &http.Client{Timeout: requestTimeout}}
		err = srv.waitReady(attemptCtx)
		cancelAttempt()
		if err == nil {
			return srv, nil
		}
		srv.Stop(context.Background())
		lastErr = err
		if proc.bindCollision() {
			continue
		}
		return nil, fmt.Errorf("runtime: llama-server: %w", err)
	}
	return nil, fmt.Errorf("runtime: no free port after %d tries: %w", portRetries, lastErr)
}

// waitReady polls until the server answers /health and an authenticated request, and the child is alive.
func (s *Server) waitReady(ctx context.Context) error {
	for {
		select {
		case <-s.proc.exited:
			return fmt.Errorf("exited before it was ready: %v", s.proc.waitErr)
		default:
		}
		if s.checkOnce(ctx) == nil {
			select {
			case <-s.proc.exited:
				return fmt.Errorf("exited before it was ready: %v", s.proc.waitErr)
			default:
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.proc.exited:
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// checkOnce makes one health request and one authenticated request, each with its own short timeout.
// The health reply has to say ok. The authenticated request shows that the listener has our key, because
// llama-server answers /health without one.
func (s *Server) checkOnce(ctx context.Context) error {
	hctx, c1 := context.WithTimeout(ctx, healthAttempt)
	defer c1()
	req, err := http.NewRequestWithContext(hctx, http.MethodGet, s.base+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var h struct {
		Status string `json:"status"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &h) != nil || h.Status != "ok" {
		return fmt.Errorf("health: HTTP %d", resp.StatusCode)
	}
	actx, c2 := context.WithTimeout(ctx, healthAttempt)
	defer c2()
	_, err = s.tokenize(actx, "ready")
	return err
}

// freePort asks the kernel for a free loopback port and releases it. Another process may take it before the
// child binds it; Start then retries on a new port.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// randomKey returns 32 random bytes as hex, the API key for one server run.
func randomKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Exited closes when the server process has ended.
func (s *Server) Exited() <-chan struct{} { return s.proc.exited }

// PID returns the process ID of the server.
func (s *Server) PID() int { return s.proc.pid }

// PeakRAM returns the server's peak resident memory in bytes (VmHWM), or 0 when it is unknown.
func (s *Server) PeakRAM() int64 { return s.proc.peakRSS() }

// Stop ends the server: SIGTERM to its process group, then SIGKILL after stopTimeout, or when ctx ends.
func (s *Server) Stop(ctx context.Context) {
	s.proc.terminate(ctx, stopTimeout)
}

// embeddingResponse is the part of the /v1/embeddings reply that the client reads.
type embeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// EmbedTexts embeds prompts. Vectors come back in input order.
func (s *Server) EmbedTexts(ctx context.Context, prompts []string) ([][]float32, int, error) {
	if len(prompts) == 0 {
		return nil, 0, nil
	}
	resp, err := s.post(ctx, "/v1/embeddings", map[string]any{"input": prompts})
	if err != nil {
		return nil, 0, err
	}
	if len(resp.Data) != len(prompts) {
		return nil, 0, fmt.Errorf("runtime: %d vectors for %d prompts", len(resp.Data), len(prompts))
	}
	vecs := make([][]float32, len(prompts))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(vecs) {
			return nil, 0, fmt.Errorf("runtime: vector index %d out of range", d.Index)
		}
		vecs[d.Index] = d.Embedding
	}
	for i, v := range vecs {
		if len(v) != embeddingDims {
			return nil, 0, fmt.Errorf("runtime: vector %d has %d dimensions, want %d", i, len(v), embeddingDims)
		}
	}
	return vecs, resp.Usage.PromptTokens, nil
}

// EmbedImage embeds one PNG or JPEG image, sent as a data URL (SPEC section 8).
func (s *Server) EmbedImage(ctx context.Context, img []byte, mime string) ([]float32, int, error) {
	url := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img)
	body := map[string]any{"input": []any{map[string]any{"content": []any{
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}},
	}}}}
	resp, err := s.post(ctx, "/v1/embeddings", body)
	if err != nil {
		return nil, 0, err
	}
	if len(resp.Data) != 1 || len(resp.Data[0].Embedding) != embeddingDims {
		return nil, 0, fmt.Errorf("runtime: image reply has %d vectors", len(resp.Data))
	}
	return resp.Data[0].Embedding, resp.Usage.PromptTokens, nil
}

// serverError is an HTTP 5xx answer of llama-server: a failure of the sidecar, not of the request (SPEC section 8).
type serverError struct {
	status int
	msg    string
}

func (e *serverError) Error() string { return e.msg }

// httpFailure returns the error of a reply with status: a serverError for 5xx, and a plain error otherwise.
func httpFailure(status int, msg string) error {
	if status >= 500 {
		return &serverError{status: status, msg: msg}
	}
	return errors.New(msg)
}

// post sends body as JSON and decodes the embeddings reply.
func (s *Server) post(ctx context.Context, path string, body any) (embeddingResponse, error) {
	var out embeddingResponse
	raw, err := json.Marshal(body)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+path, bytes.NewReader(raw))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+s.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, httpFailure(resp.StatusCode, fmt.Sprintf("runtime: decode reply (HTTP %d): %v", resp.StatusCode, err))
	}
	if out.Error != nil {
		return out, httpFailure(resp.StatusCode, fmt.Sprintf("runtime: server error (HTTP %d): %s", resp.StatusCode, out.Error.Message))
	}
	if resp.StatusCode != http.StatusOK {
		return out, httpFailure(resp.StatusCode, fmt.Sprintf("runtime: HTTP %d", resp.StatusCode))
	}
	return out, nil
}

// tokenize calls /tokenize with pieces. It is the authenticated check of waitReady too.
type tokenizeReply struct {
	Tokens []struct {
		ID    int32           `json:"id"`
		Piece json.RawMessage `json:"piece"`
	} `json:"tokens"`
}

func (s *Server) tokenize(ctx context.Context, text string) (tokenizeReply, error) {
	var out tokenizeReply
	raw, err := json.Marshal(map[string]any{"content": text, "with_pieces": true})
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/tokenize", bytes.NewReader(raw))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+s.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, httpFailure(resp.StatusCode, fmt.Sprintf("runtime: tokenize HTTP %d", resp.StatusCode))
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

// Tokenize splits text into tokens with byte ranges. A piece is either a string or an array of bytes (byte
// fallback, which can split a character), and the ranges add up the piece lengths in order.
func (s *Server) Tokenize(ctx context.Context, text string) ([]contract.Token, error) {
	reply, err := s.tokenize(ctx, text)
	if err != nil {
		return nil, err
	}
	toks := make([]contract.Token, 0, len(reply.Tokens))
	pos := 0
	for _, t := range reply.Tokens {
		piece, err := decodePiece(t.Piece)
		if err != nil {
			return nil, err
		}
		toks = append(toks, contract.Token{ID: t.ID, Piece: piece, Start: pos, End: pos + len(piece)})
		pos += len(piece)
	}
	return toks, nil
}

func decodePiece(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var bs []byte
	var ints []int
	if err := json.Unmarshal(raw, &ints); err != nil {
		return "", fmt.Errorf("runtime: piece %s: %w", raw, err)
	}
	for _, v := range ints {
		if v < 0 || v > 255 {
			return "", errors.New("runtime: piece byte out of range")
		}
		bs = append(bs, byte(v))
	}
	return string(bs), nil
}

// argv returns the llama-server arguments (SPEC section 8).
func argv(o Options, port int, key string) []string {
	if o.CtxSize == 0 {
		o.CtxSize = 8192
	}
	args := []string{
		"-m", o.Files.Model, "--mmproj", o.Files.MMProj, "--embeddings", "--pooling", "mean",
		"-c", strconv.Itoa(o.CtxSize), "-b", strconv.Itoa(o.CtxSize), "-ub", strconv.Itoa(o.CtxSize),
		"-t", strconv.Itoa(o.Threads),
		"--host", "127.0.0.1", "--port", strconv.Itoa(port), "--api-key", key,
	}
	if o.GPU {
		args = append(args, "-ngl", "99")
	}
	return args
}

// trimLog returns the last lines of a log for an error message.
func trimLog(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 800 {
		s = s[len(s)-800:]
	}
	return s
}
