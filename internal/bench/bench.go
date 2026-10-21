// Package bench runs the combination test lab. It measures image latency, text tokens per second, peak RAM, VRAM
// and quality against the BF16 references, and recommends the fastest combination whose quality is at least 0.99
// (SPEC section 6).
package bench

import (
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
	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/runtime"
)

// Limits from SPEC section 6.
const (
	maxExtra       = 10
	textBatch      = 8    // chunks per request (SPEC section 9)
	qualityBar     = 0.99 // Recommend needs at least this quality
	extraTextBytes = 8192 // bytes read from a user-picked text file
)

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

// measure embeds the sample and the user's files, and returns the numbers of the test table.
func (l *Lab) measure(r *run, srv *runtime.Server) (contract.BenchResult, error) {
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
	var tokens int
	var textWall time.Duration
	for start := 0; start < len(chunks); start += textBatch {
		end := min(start+textBatch, len(chunks))
		var prompts []string
		for _, c := range chunks[start:end] {
			prompts = append(prompts, Prompt(c.Name, c.Text))
		}
		t0 := time.Now()
		out, n, err := srv.EmbedTexts(r.ctx, prompts)
		textWall += time.Since(t0)
		if err != nil {
			return res, fmt.Errorf("text chunks: %w", err)
		}
		tokens += n
		vecs = append(vecs, out...)
	}
	for i, p := range r.extra {
		l.emit(r, contract.LabStep{State: contract.LabEmbedding, Step: fmt.Sprintf("Embedding your file %d of %d", i+1, len(r.extra))})
		if err := embedUserFile(r.ctx, srv, p); err != nil {
			return res, fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
	}

	res.ImageMS = median(msPerImage)
	if textWall > 0 {
		res.TextTokensPerSec = float64(tokens) / textWall.Seconds()
	}
	q, err := quality(vecs, ref)
	if err != nil {
		return res, err
	}
	res.Quality = &q
	return res, nil
}

// embedUserFile embeds one picked file, for its timing only: images as images, anything else as its first bytes.
func embedUserFile(ctx context.Context, srv *runtime.Server, p string) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png":
		_, _, err = srv.EmbedImage(ctx, b, "image/png")
	case ".jpg", ".jpeg":
		_, _, err = srv.EmbedImage(ctx, b, "image/jpeg")
	default:
		if len(b) > extraTextBytes {
			b = b[:extraTextBytes]
		}
		_, _, err = srv.EmbedTexts(ctx, []string{Prompt(filepath.Base(p), string(b))})
	}
	return err
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
		sum += cosine(vecs[i], ref.Vectors[i])
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
		if q == nil || *q < qualityBar {
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
