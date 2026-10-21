// Package bench runs the combination test lab. It measures image latency, text tokens per second, peak RAM, VRAM
// and quality against the BF16 references, and recommends the fastest combination whose quality is at least 0.99
// (SPEC section 6).
package bench

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hamidriaz1998/lookback/assets"
	"github.com/hamidriaz1998/lookback/internal/chunk"
	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/extract"
	"github.com/hamidriaz1998/lookback/internal/runtime"
)

// Limits from SPEC section 6.
const (
	maxExtra     = 10
	textBatch    = 8        // chunks per request (SPEC section 9)
	qualityBar   = 0.99     // Recommend needs at least this quality
	maxPickBytes = 50 << 20 // largest user-picked file, the index's own limit (SPEC section 9)
)

// embedder is the part of the sidecar that a run uses. *runtime.Server has these methods.
type embedder interface {
	EmbedTexts(ctx context.Context, prompts []string) ([][]float32, int, error)
	EmbedImage(ctx context.Context, img []byte, mime string) ([]float32, int, error)
	Tokenize(ctx context.Context, text string) ([]contract.Token, error)
}

// Prompt returns the document prompt of a chunk: "title: <file name> | text: <chunk>" (SPEC section 9).
func Prompt(name, text string) string {
	return "title: " + name + " | text: " + text
}

// Lab is a contract.Lab. Runs form one FIFO queue. Each run starts its own temporary llama-server, separate from
// the runtime's sidecars, and stops it before the run's terminal step (SPEC section 6, step 1).
type Lab struct {
	life    context.Context
	ix      contract.Indexer
	det     contract.Detector // for VRAM before and while loaded; nil leaves VRAM at 0
	files   func(contract.Combo) (runtime.Files, error)
	logPath string
	threads int
	streams *runtime.Stream[contract.LabStep]

	mu     sync.Mutex
	nextID contract.RunID
	queue  []*run // waiting runs, first to start first
	active *run   // the run that holds the lab, or nil
	closed bool   // the lifetime ended, so no run starts
	runs   sync.WaitGroup

	// picks holds the vectors of the latest BF16 run of picked files, keyed by the files' paths, sizes and times.
	// A later run of the same files compares against it; with none, its quality is n/a (SPEC section 6).
	// ponytail: one remembered set; per-set references if the lab must compare several picks at once.
	picksKey  string
	picksVecs [][]float32
}

// run is one queued or running test.
type run struct {
	id       contract.RunID
	combo    contract.Combo
	extra    []string
	ctx      context.Context
	cancel   context.CancelFunc
	ready    chan struct{} // closed when the run is promoted
	promoted bool          // guarded by Lab.mu
}

// New returns a Lab. files resolves the files of a combination, and returns contract.ErrUnknownCombo for one it
// cannot test. When life ends, no run starts, every run emits its terminal step, and the streams close.
func New(life context.Context, ix contract.Indexer, det contract.Detector,
	files func(contract.Combo) (runtime.Files, error), logPath string, threads int) *Lab {
	if threads == 0 {
		threads = 4
	}
	l := &Lab{
		life: life, ix: ix, det: det, files: files, logPath: logPath, threads: threads,
		streams: runtime.NewStream(
			func(s contract.LabStep) string { return fmt.Sprint(s.RunID) },
			func(s contract.LabStep) bool {
				return s.State == contract.LabDone || s.State == contract.LabFailed || s.State == contract.LabCanceled
			},
		),
	}
	go func() {
		<-life.Done()
		l.shutdown()
	}()
	return l
}

// Run queues a test of combination c with up to 10 user-picked files. It returns the run's ID at once.
func (l *Lab) Run(ctx context.Context, c contract.Combo, extra []string) (contract.RunID, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if l.life.Err() != nil {
		return 0, contract.ErrStopped
	}
	if len(extra) > maxExtra {
		return 0, fmt.Errorf("bench: at most %d extra files", maxExtra)
	}
	for _, p := range extra {
		if _, err := checkPick(p); err != nil {
			return 0, err
		}
		if _, ok := extract.KindOf(p); !ok {
			return 0, fmt.Errorf("%s: %w", filepath.Base(p), extract.ErrUnsupported)
		}
	}
	if _, err := l.files(c); err != nil {
		return 0, err
	}
	rctx, cancel := context.WithCancel(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		cancel()
		return 0, contract.ErrStopped
	}
	r := &run{id: l.nextID, combo: c, extra: extra, ctx: rctx, cancel: cancel, ready: make(chan struct{})}
	l.nextID++
	l.runs.Add(1)
	go l.runOne(r)
	if l.active == nil {
		l.ix.Pause(contract.PauseLab)
		l.giveLocked(r)
	} else {
		l.queue = append(l.queue, r)
	}
	return r.id, nil
}

func (l *Lab) giveLocked(r *run) {
	l.active = r
	r.promoted = true
	close(r.ready)
}

// releaseLocked passes the lab to the next queued run, or frees it and resumes the indexer.
func (l *Lab) releaseLocked() {
	if len(l.queue) == 0 {
		l.active = nil
		l.ix.Resume(contract.PauseLab)
		return
	}
	next := l.queue[0]
	l.queue = l.queue[1:]
	l.giveLocked(next)
}

func (l *Lab) dequeueLocked(r *run) {
	for i, q := range l.queue {
		if q == r {
			l.queue = append(l.queue[:i], l.queue[i+1:]...)
			return
		}
	}
}

// runOne waits for its turn, executes when it holds the lab, and passes the lab on. A run canceled while it waits
// leaves the queue and emits canceled.
func (l *Lab) runOne(r *run) {
	defer l.runs.Done()
	defer r.cancel()
	select {
	case <-r.ready:
	case <-r.ctx.Done():
	}
	l.mu.Lock()
	if !r.promoted {
		l.dequeueLocked(r)
		l.mu.Unlock()
		l.emit(r, contract.LabStep{State: contract.LabCanceled, Step: "Canceled"})
		return
	}
	l.mu.Unlock()
	last := l.execute(r)
	l.emit(r, last)
	l.mu.Lock()
	l.releaseLocked()
	l.mu.Unlock()
}

func (l *Lab) emit(r *run, s contract.LabStep) {
	s.RunID, s.Combo = r.id, r.combo
	l.streams.Publish(s)
}

// execute measures one run and returns its terminal step. The temporary server stops before it returns.
func (l *Lab) execute(r *run) contract.LabStep {
	fail := func(err error) contract.LabStep {
		if r.ctx.Err() != nil {
			return contract.LabStep{State: contract.LabCanceled, Step: "Canceled"}
		}
		return contract.LabStep{State: contract.LabFailed, Step: "Failed", Err: err.Error()}
	}
	l.emit(r, contract.LabStep{State: contract.LabStarting, Step: "Starting " + r.combo.ID()})
	files, err := l.files(r.combo)
	if err != nil {
		return fail(err)
	}
	gpu := r.combo.Backend != contract.BackendCPU
	vramBefore := l.vramFree(r.ctx, r.combo.Backend)
	srv, err := runtime.Start(r.ctx, runtime.Options{Files: files, GPU: gpu, Threads: l.threads, LogPath: l.logPath})
	if err != nil {
		return fail(err)
	}
	defer srv.Stop(context.Background())
	vramLoaded := l.vramFree(r.ctx, r.combo.Backend)
	vram := int64(0)
	if gpu && vramBefore > 0 && vramLoaded > 0 {
		vram = vramBefore - vramLoaded
	}

	res, err := l.measure(r, srv)
	if err != nil {
		return fail(err)
	}
	res.PeakRAM = srv.PeakRAM()
	res.VRAM = vram
	result := contract.LabResult{RunID: r.id, Combo: r.combo, Bench: res}
	return contract.LabStep{State: contract.LabDone, Step: "Done", Result: &result}
}

// measure embeds the sample and the user's files, and returns the numbers of the test table. The picked files go
// through the same extraction and chunking as the index (SPEC section 9), and their vectors and timings join the
// sample's.
func (l *Lab) measure(r *run, srv embedder) (contract.BenchResult, error) {
	var res contract.BenchResult
	images, err := assets.Images()
	if err != nil {
		return res, err
	}
	chunks, err := assets.Chunks()
	if err != nil {
		return res, err
	}
	ref, err := assets.LoadReference()
	if err != nil {
		return res, err
	}
	var vecs [][]float32
	var msPerImage []float64
	for i, img := range images {
		l.emit(r, contract.LabStep{State: contract.LabEmbedding, Step: fmt.Sprintf("Embedding image %d of %d", i+1, len(images))})
		t0 := time.Now()
		v, _, err := srv.EmbedImage(r.ctx, img.Data, img.MIME)
		if err != nil {
			return res, fmt.Errorf("image %s: %w", img.Name, err)
		}
		msPerImage = append(msPerImage, float64(time.Since(t0))/float64(time.Millisecond))
		vecs = append(vecs, v)
	}
	l.emit(r, contract.LabStep{State: contract.LabEmbedding, Step: fmt.Sprintf("Embedding %d text chunks", len(chunks))})
	var prompts []string
	for _, c := range chunks {
		prompts = append(prompts, Prompt(c.Name, c.Text))
	}
	out, tokens, textWall, err := embedTexts(r.ctx, srv, prompts)
	if err != nil {
		return res, fmt.Errorf("text chunks: %w", err)
	}
	vecs = append(vecs, out...)

	var userVecs [][]float32
	var key strings.Builder
	reg := extract.NewRegistry()
	defer reg.Close()
	for i, p := range r.extra {
		l.emit(r, contract.LabStep{State: contract.LabEmbedding, Step: fmt.Sprintf("Embedding your file %d of %d", i+1, len(r.extra))})
		name := filepath.Base(p)
		fi, err := checkPick(p)
		if err != nil {
			return res, err
		}
		fmt.Fprintf(&key, "%s|%d|%d\n", p, fi.Size(), fi.ModTime().UnixNano())
		units, err := extractPick(r.ctx, reg, p, fi)
		if err != nil {
			return res, fmt.Errorf("%s: %w", name, err)
		}
		var texts []string
		for _, u := range units {
			if u.Image != nil {
				mime := "image/jpeg"
				if bytes.HasPrefix(u.Image, []byte("\x89PNG\r\n\x1a\n")) {
					mime = "image/png"
				}
				t0 := time.Now()
				v, _, err := srv.EmbedImage(r.ctx, u.Image, mime)
				if err != nil {
					return res, fmt.Errorf("%s: %w", name, err)
				}
				msPerImage = append(msPerImage, float64(time.Since(t0))/float64(time.Millisecond))
				userVecs = append(userVecs, v)
				continue
			}
			wins, err := chunk.Split(r.ctx, u.Text, Prompt(name, ""), srv.Tokenize)
			if err != nil {
				return res, fmt.Errorf("%s: %w", name, err)
			}
			for _, w := range wins {
				texts = append(texts, Prompt(name, u.Text[w.Start:w.End]))
			}
		}
		out, n, wall, err := embedTexts(r.ctx, srv, texts)
		if err != nil {
			return res, fmt.Errorf("%s: %w", name, err)
		}
		userVecs = append(userVecs, out...)
		tokens += n
		textWall += wall
	}

	res.ImageMS = median(msPerImage)
	if textWall > 0 {
		res.TextTokensPerSec = float64(tokens) / textWall.Seconds()
	}
	if len(r.extra) == 0 {
		q, err := quality(vecs, ref)
		if err != nil {
			return res, err
		}
		res.Quality = &q
		return res, nil
	}
	// With picked files, quality is their own: the vectors against a BF16 run of the same files.
	if r.combo.TextQuant == contract.QuantBF16 && r.combo.EncQuant == contract.QuantBF16 {
		l.keepPicks(key.String(), userVecs)
	}
	base, ok := l.picksReference(key.String())
	switch {
	case !ok:
		res.QualityReason = "needs a BF16 run of these files first"
	case len(userVecs) == 0:
		res.QualityReason = "the picked files have no text or images to compare"
	default:
		q, err := quality(userVecs, assets.Reference{Vectors: base})
		if err != nil {
			return res, err
		}
		res.Quality = &q
	}
	return res, nil
}

// keepPicks remembers the vectors of a BF16 run of the picked files.
func (l *Lab) keepPicks(key string, vecs [][]float32) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.picksKey, l.picksVecs = key, vecs
}

// picksReference returns the remembered BF16 vectors when they belong to the same picked files.
func (l *Lab) picksReference(key string) ([][]float32, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.picksVecs == nil || l.picksKey != key {
		return nil, false
	}
	return l.picksVecs, true
}

// embedTexts sends prompts in batches of textBatch (SPEC section 9). It returns their vectors, the prompt tokens
// and the wall time of the requests.
func embedTexts(ctx context.Context, srv embedder, prompts []string) ([][]float32, int, time.Duration, error) {
	var vecs [][]float32
	var tokens int
	var wall time.Duration
	for start := 0; start < len(prompts); start += textBatch {
		end := min(start+textBatch, len(prompts))
		t0 := time.Now()
		out, n, err := srv.EmbedTexts(ctx, prompts[start:end])
		wall += time.Since(t0)
		if err != nil {
			return nil, 0, 0, err
		}
		tokens += n
		vecs = append(vecs, out...)
	}
	return vecs, tokens, wall, nil
}

// extractPick returns the units of a picked file through the registry, the same extractors the index uses. fi is
// the file's checked info.
func extractPick(ctx context.Context, reg *extract.Registry, p string, fi os.FileInfo) ([]contract.Unit, error) {
	kind, ok := extract.KindOf(p)
	if !ok {
		return nil, extract.ErrUnsupported
	}
	f := contract.FileRec{Path: p, Kind: kind, Size: fi.Size(), MTime: fi.ModTime().UnixNano()}
	var units []contract.Unit
	err := reg.Extract(ctx, f, func(u contract.Unit) error {
		units = append(units, u)
		return nil
	})
	return units, err
}

// checkPick returns the info of a user-picked file. It must be a regular file of at most maxPickBytes. Stat does
// not open the file, so a FIFO, a device or a socket is refused without blocking.
func checkPick(p string) (os.FileInfo, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", filepath.Base(p))
	}
	if fi.Size() > maxPickBytes {
		return nil, fmt.Errorf("%s: %d bytes, over the %d-byte limit", filepath.Base(p), fi.Size(), int64(maxPickBytes))
	}
	return fi, nil
}

// vramFree returns the free VRAM of the first device of backend b, or 0 when it is unknown.
func (l *Lab) vramFree(ctx context.Context, b contract.Backend) int64 {
	if l.det == nil || b == contract.BackendCPU {
		return 0
	}
	p, err := l.det.Probe(ctx, b)
	if err != nil || len(p.Devices) == 0 {
		return 0
	}
	return p.Devices[0].VRAMFree
}

// quality is the mean cosine similarity between the run's vectors and the BF16 reference, item by item.
// Items are the images then the chunks, the order of assets.LoadReference.
func quality(vecs [][]float32, ref assets.Reference) (float64, error) {
	if len(vecs) != len(ref.Vectors) {
		return 0, fmt.Errorf("bench: %d vectors, reference has %d", len(vecs), len(ref.Vectors))
	}
	var sum float64
	for i := range vecs {
		c := cosine(vecs[i], ref.Vectors[i])
		if math.IsNaN(c) || math.IsInf(c, 0) {
			// A zero or non-finite vector has no cosine; a NaN mean would pass the quality bar.
			return 0, fmt.Errorf("bench: item %d has a zero or non-finite vector", i)
		}
		sum += c
	}
	return sum / float64(len(vecs)), nil
}

// cosine returns the cosine similarity of a and b.
func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 0 {
		return (s[m-1] + s[m]) / 2
	}
	return s[m]
}

// Subscribe streams the steps of all runs, keyed by RunID.
func (l *Lab) Subscribe(ctx context.Context) <-chan contract.LabStep {
	return l.streams.Subscribe(ctx)
}

// Recommend returns the fastest result whose quality is at least 0.99. Results with n/a quality do not qualify.
func (l *Lab) Recommend(results []contract.LabResult) (contract.Combo, bool) {
	var best contract.LabResult
	found := false
	for _, r := range results {
		q := r.Bench.Quality
		if q == nil || math.IsNaN(*q) || math.IsInf(*q, 0) || *q < qualityBar {
			continue
		}
		if !found || r.Bench.ImageMS < best.Bench.ImageMS {
			best, found = r, true
		}
	}
	return best.Combo, found
}

// shutdown runs when the lifetime ends: no run starts, every run is canceled and emits its terminal step, and then
// the streams close.
func (l *Lab) shutdown() {
	l.mu.Lock()
	l.closed = true
	for _, r := range l.queue {
		r.cancel()
	}
	if l.active != nil {
		l.active.cancel()
	}
	l.mu.Unlock()
	l.runs.Wait()
	l.streams.Close()
}
