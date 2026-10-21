package download

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

const mib = 1 << 20

// testServer serves one file. stallAt > 0 holds each response that starts before it at that offset until
// the client goes away, which is how a test kills a transfer mid-file.
type testServer struct {
	*httptest.Server
	content     []byte
	ignoreRange bool
	stallAt     int64
	fail503     int // the first N requests answer 503

	mu     sync.Mutex
	ranges []string // the Range header of each request, "" when absent
}

func newTestServer(t *testing.T, content []byte) *testServer {
	t.Helper()
	ts := &testServer{content: content}
	ts.Server = httptest.NewServer(http.HandlerFunc(ts.serve))
	t.Cleanup(ts.Close)
	return ts
}

func (ts *testServer) serve(w http.ResponseWriter, r *http.Request) {
	ts.mu.Lock()
	ts.ranges = append(ts.ranges, r.Header.Get("Range"))
	n := len(ts.ranges)
	ts.mu.Unlock()
	if n <= ts.fail503 {
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	var start int64
	if rng := r.Header.Get("Range"); rng != "" && !ts.ignoreRange {
		start, _ = strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-"), 10, 64)
	}
	code := http.StatusOK
	if start > 0 {
		code = http.StatusPartialContent
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(ts.content)-int(start)))
	w.WriteHeader(code)
	off := start
	for off < int64(len(ts.content)) {
		end := min(off+64<<10, int64(len(ts.content)))
		if ts.stallAt > 0 && off < ts.stallAt && end > ts.stallAt {
			end = ts.stallAt
		}
		if _, err := w.Write(ts.content[off:end]); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		off = end
		if ts.stallAt > 0 && off == ts.stallAt && start < ts.stallAt {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Minute):
				return
			}
		}
	}
}

func (ts *testServer) requests() []string {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]string(nil), ts.ranges...)
}

func sumHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(1)).Read(b)
	return b
}

// modelAsset is a model file pinned to content and served at url.
func modelAsset(url string, content []byte) contract.Asset {
	return contract.Asset{Name: "model-test.gguf", URL: url, Bytes: int64(len(content)),
		SHA256: sumHex(content), Part: contract.PartText, Quant: contract.QuantBF16}
}

// waitFor subscribes and returns the first state of key that pred accepts.
func waitFor(t *testing.T, d *Downloader, key string, pred func(contract.DownloadProgress) bool) contract.DownloadProgress {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for p := range d.Subscribe(ctx) {
		if p.Key == key && pred(p) {
			return p
		}
	}
	t.Fatalf("timed out waiting for %s", key)
	return contract.DownloadProgress{}
}

func isState(s contract.FileState) func(contract.DownloadProgress) bool {
	return func(p contract.DownloadProgress) bool { return p.State == s }
}

// waitBytes polls until the file at p has exactly n bytes.
func waitBytes(t *testing.T, p string, n int64) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for fileSize(p) != n {
		if time.Now().After(deadline) {
			t.Fatalf("%s has %d bytes, want %d", p, fileSize(p), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// stop ends a downloader and waits until its goroutines have finished and its streams are closed.
func stop(t *testing.T, cancel context.CancelFunc, d *Downloader) {
	t.Helper()
	cancel()
	ctx, c := context.WithTimeout(context.Background(), 30*time.Second)
	defer c()
	for range d.Subscribe(ctx) {
	}
	if ctx.Err() != nil {
		t.Fatal("downloader did not stop")
	}
}

func partOf(dir string, a contract.Asset) string {
	return filepath.Join(dir, "downloads", a.SHA256+".part")
}

// Acceptance: a download killed mid-file resumes from the same byte.
func TestKillAndResumeFromSameByte(t *testing.T) {
	content := randomBytes(4 * mib)
	ts := newTestServer(t, content)
	ts.stallAt = mib
	a := modelAsset(ts.URL+"/model-test.gguf", content)
	dir := t.TempDir()

	ctx1, cancel1 := context.WithCancel(context.Background())
	d1, err := New(ctx1, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := d1.Ensure(context.Background(), []contract.Asset{a}); err != nil {
		t.Fatal(err)
	}
	waitBytes(t, partOf(dir, a), mib)
	stop(t, cancel1, d1)
	killedAt := fileSize(partOf(dir, a))
	if killedAt != mib {
		t.Fatalf("killed at %d bytes, want %d", killedAt, mib)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	d2, err := New(ctx2, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := d2.Ensure(context.Background(), []contract.Asset{a}); err != nil {
		t.Fatal(err)
	}
	done := waitFor(t, d2, a.Key(), isState(contract.FileDone))

	reqs := ts.requests()
	if len(reqs) != 2 || reqs[1] != "bytes="+strconv.FormatInt(killedAt, 10)+"-" {
		t.Fatalf("requests = %q, want the second to resume at %d", reqs, killedAt)
	}
	if done.ResumedFrom != killedAt {
		t.Errorf("ResumedFrom = %d, want %d", done.ResumedFrom, killedAt)
	}
	got, err := os.ReadFile(d2.modelPath(a))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("installed file differs from the pinned content: %v", err)
	}
}

// Acceptance: a server that ignores Range (answers 200) makes the downloader start the file again.
func TestServerIgnoresRangeRestarts(t *testing.T) {
	content := randomBytes(2 * mib)
	ts := newTestServer(t, content)
	ts.ignoreRange = true
	a := modelAsset(ts.URL+"/model-test.gguf", content)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	junk := bytes.Repeat([]byte{0xAA}, mib)
	if err := os.WriteFile(partOf(dir, a), junk, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := New(ctx, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Ensure(context.Background(), []contract.Asset{a}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, d, a.Key(), isState(contract.FileDone))

	if reqs := ts.requests(); len(reqs) != 1 || reqs[0] != "bytes="+strconv.Itoa(mib)+"-" {
		t.Fatalf("requests = %q, want one Range request from %d", reqs, mib)
	}
	got, err := os.ReadFile(d.modelPath(a))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("file after a 200 answer differs from the pinned content: %v", err)
	}
}

// Acceptance: a wrong checksum deletes the part, retries once from zero, then reports the error.
func TestWrongChecksumRetriesOnceThenFails(t *testing.T) {
	pinned := randomBytes(mib)
	served := bytes.Repeat([]byte{7}, mib)
	ts := newTestServer(t, served)
	a := modelAsset(ts.URL+"/model-test.gguf", pinned)
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := New(ctx, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Ensure(context.Background(), []contract.Asset{a}); err != nil {
		t.Fatal(err)
	}
	failed := waitFor(t, d, a.Key(), isState(contract.FileFailed))

	if !strings.Contains(failed.Err, "checksum mismatch") {
		t.Errorf("Err = %q, want a checksum mismatch", failed.Err)
	}
	reqs := ts.requests()
	if len(reqs) != 2 || reqs[0] != "" || reqs[1] != "" {
		t.Errorf("requests = %q, want two full requests", reqs)
	}
	if fileSize(partOf(dir, a)) != -1 {
		t.Error("the part was not deleted")
	}
	if fileSize(d.modelPath(a)) != -1 {
		t.Error("a file that failed its checksum was installed")
	}
}

func TestOfflineDuringChecksumVerificationRestarts(t *testing.T) {
	content := randomBytes(32 * mib)
	wrongPin := append([]byte(nil), content...)
	wrongPin[0] ^= 0xff
	ts := newTestServer(t, content)
	net := &fakeNet{online: true}
	a := modelAsset(ts.URL+"/model-test.gguf", wrongPin)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := New(ctx, Config{Dir: t.TempDir(), Network: net})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Ensure(context.Background(), []contract.Asset{a}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, d, a.Key(), isState(contract.FileVerifying))
	net.set(false)
	deadline := time.Now().Add(10 * time.Second)
	for {
		d.mu.Lock()
		f := d.files[a.Key()]
		ready := f.cancel == nil && f.phase == contract.FileQueued
		d.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("canceled verification did not return to a restartable phase")
		}
		time.Sleep(time.Millisecond)
	}
	net.set(true)
	waitFor(t, d, a.Key(), isState(contract.FileFailed))
	if len(ts.requests()) < 3 {
		t.Fatalf("requests = %d, want interrupted transfer and checksum recovery", len(ts.requests()))
	}
}

func TestRecoveredOfflineDownloadPublishesSavedBytes(t *testing.T) {
	content := randomBytes(2 * mib)
	ts := newTestServer(t, content)
	a := modelAsset(ts.URL+"/model-test.gguf", content)
	dir := t.TempDir()
	part := partOf(dir, a)
	if err := os.MkdirAll(filepath.Dir(part), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part, content[:mib], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(part+".json", a); err != nil {
		t.Fatal(err)
	}
	net := &fakeNet{online: false}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := New(ctx, Config{Dir: dir, Network: net})
	if err != nil {
		t.Fatal(err)
	}
	got := waitFor(t, d, a.Key(), func(p contract.DownloadProgress) bool { return true })
	if got.State != contract.FileOffline || got.Done != mib {
		t.Fatalf("snapshot = %+v, want offline at %d bytes", got, mib)
	}
	if err := d.Ensure(context.Background(), []contract.Asset{a}); err != nil {
		t.Fatal(err)
	}
	if len(ts.requests()) != 0 {
		t.Fatal("offline recovery started a request")
	}
}

func TestShutdownClosesUnreadSubscription(t *testing.T) {
	content := randomBytes(64 << 10)
	ts := newTestServer(t, content)
	a := modelAsset(ts.URL+"/model-test.gguf", content)
	ctx, cancel := context.WithCancel(context.Background())
	d, err := New(ctx, Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	subCtx, subCancel := context.WithCancel(context.Background())
	defer subCancel()
	ch := d.Subscribe(subCtx)
	if err := d.Ensure(context.Background(), []contract.Asset{a}); err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.After(time.Second)
	for {
		d.streams.mu.Lock()
		closed := d.streams.closed
		d.streams.mu.Unlock()
		if closed {
			break
		}
		select {
		case <-deadline:
			t.Fatal("downloader shutdown blocked on the unread subscription")
		case <-time.After(time.Millisecond):
		}
	}
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("unread subscription did not close after downloader shutdown")
		}
	}
}

// Acceptance: an archive with ../ entries is refused, and nothing lands outside the runtime folder.
func TestArchiveWithParentPathIsRefused(t *testing.T) {
	entries := map[string][]byte{
		"llama-b11755-bin-ubuntu-x64.tar.gz": tarGz(t, map[string]string{"llama-server": "x", "../escape.txt": "bad"}),
		"llama-b11755-bin-win-cpu-x64.zip":   zipOf(t, map[string]string{"llama-server.exe": "x", "../escape.txt": "bad"}),
	}
	for name, body := range entries {
		t.Run(name, func(t *testing.T) {
			ts := newTestServer(t, body)
			a := contract.Asset{Name: name, URL: ts.URL + "/b11755/" + name, Bytes: int64(len(body)),
				SHA256: sumHex(body), Part: contract.PartRuntime, Backend: contract.BackendCPU, Archive: true}
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d, err := New(ctx, Config{Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if err := d.Ensure(context.Background(), []contract.Asset{a}); err != nil {
				t.Fatal(err)
			}
			failed := waitFor(t, d, a.Key(), isState(contract.FileFailed))
			if !strings.Contains(failed.Err, "leaves the runtime folder") {
				t.Errorf("Err = %q, want the path refusal", failed.Err)
			}
			for _, p := range []string{filepath.Join(dir, "runtimes", "escape.txt"), filepath.Join(dir, "escape.txt")} {
				if _, err := os.Stat(p); err == nil {
					t.Errorf("%s was written", p)
				}
			}
		})
	}
}

// Transient 503 answers retry after backoff and then succeed.
func TestTransientErrorRetriesWithBackoff(t *testing.T) {
	content := randomBytes(64 << 10)
	ts := newTestServer(t, content)
	ts.fail503 = 1
	a := modelAsset(ts.URL+"/model-test.gguf", content)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := New(ctx, Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Ensure(context.Background(), []contract.Asset{a}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, d, a.Key(), isState(contract.FileDone))
	if n := len(ts.requests()); n != 2 {
		t.Fatalf("requests = %d, want 2 (one 503, one success)", n)
	}
}

// Pause is the user's flag and offline is the connection's; neither clears the other.
func TestPauseAndConnectionAreIndependent(t *testing.T) {
	content := randomBytes(4 * mib)
	ts := newTestServer(t, content)
	ts.stallAt = mib
	a := modelAsset(ts.URL+"/model-test.gguf", content)
	net := &fakeNet{online: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := New(ctx, Config{Dir: t.TempDir(), Network: net})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Ensure(context.Background(), []contract.Asset{a}); err != nil {
		t.Fatal(err)
	}
	waitBytes(t, partOf(d.cfg.Dir, a), mib)

	if err := d.Pause(a.Key()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, d, a.Key(), isState(contract.FilePaused))
	net.set(false)
	waitFor(t, d, a.Key(), isState(contract.FileOffline))
	net.set(true)
	waitFor(t, d, a.Key(), isState(contract.FilePaused))
	if err := d.Resume(a.Key()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, d, a.Key(), isState(contract.FileDone))
}

// Ensure is idempotent, and a key held with another descriptor is a conflict that queues nothing.
func TestEnsureConflictsAndIdempotence(t *testing.T) {
	content := randomBytes(64 << 10)
	ts := newTestServer(t, content)
	a := modelAsset(ts.URL+"/model-test.gguf", content)
	other := a
	other.Name = "other.gguf"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := New(ctx, Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Ensure(context.Background(), []contract.Asset{a, other}); !errors.Is(err, contract.ErrConflict) {
		t.Fatalf("in-batch conflict: err = %v", err)
	}
	if len(d.files) != 0 {
		t.Fatal("a conflicting batch queued files")
	}
	if err := d.Ensure(context.Background(), []contract.Asset{a}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, d, a.Key(), isState(contract.FileDone))
	if err := d.Ensure(context.Background(), []contract.Asset{a}); err != nil {
		t.Fatal(err)
	}
	if err := d.Ensure(context.Background(), []contract.Asset{other}); !errors.Is(err, contract.ErrConflict) {
		t.Fatalf("tracked-key conflict: err = %v", err)
	}
	if n := len(ts.requests()); n != 1 {
		t.Fatalf("requests = %d, want 1: a repeated Ensure downloads nothing", n)
	}
}

func TestModelRevisionsKeepSameNamedPins(t *testing.T) {
	old, current := []byte("old revision"), []byte("current revision")
	oldServer, currentServer := newTestServer(t, old), newTestServer(t, current)
	a := modelAsset(oldServer.URL+"/resolve/rev-a/shared.gguf", old)
	b := modelAsset(currentServer.URL+"/resolve/rev-b/shared.gguf", current)
	a.Name, b.Name = "shared.gguf", "shared.gguf"
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := New(ctx, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Ensure(context.Background(), []contract.Asset{a, b}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, d, a.Key(), isState(contract.FileDone))
	waitFor(t, d, b.Key(), isState(contract.FileDone))
	for _, tc := range []struct {
		a    contract.Asset
		want []byte
	}{{a, old}, {b, current}} {
		got, err := os.ReadFile(d.modelPath(tc.a))
		if err != nil || !bytes.Equal(got, tc.want) {
			t.Fatalf("model %s = %q, %v", tc.a.URL, got, err)
		}
	}
	stop(t, cancel, d)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	d, err = New(ctx, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Ensure(context.Background(), []contract.Asset{a}); err != nil {
		t.Fatal(err)
	}
	if p := waitFor(t, d, a.Key(), isState(contract.FileDone)); p.Done != a.Bytes {
		t.Fatalf("restored revision progress = %d", p.Done)
	}
	if got, err := os.ReadFile(d.modelPath(a)); err != nil || !bytes.Equal(got, old) {
		t.Fatalf("old revision after restart = %q, %v", got, err)
	}
}

type fakeNet struct {
	mu     sync.Mutex
	online bool
	fns    []func(bool)
}

func (n *fakeNet) Online() bool { n.mu.Lock(); defer n.mu.Unlock(); return n.online }
func (n *fakeNet) OnChanged(fn func(bool)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.fns = append(n.fns, fn)
}
func (n *fakeNet) set(online bool) {
	n.mu.Lock()
	n.online = online
	fns := append([]func(bool){}, n.fns...)
	n.mu.Unlock()
	for _, fn := range fns {
		fn(online)
	}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		io.WriteString(tw, body)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, body)
	}
	zw.Close()
	return buf.Bytes()
}
