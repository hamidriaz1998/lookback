package bench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/runtime"
)

// fakeServer stands in for the sidecar. It records the text prompts it embeds and tokenizes one token per byte,
// so a prompt's token count equals its byte length. Its vectors point along dimension dim.
type fakeServer struct {
	mu      sync.Mutex
	prompts []string
	images  int
	dim     int
}

func (f *fakeServer) unit() []float32 {
	v := make([]float32, 768)
	v[f.dim] = 1
	return v
}

func (f *fakeServer) EmbedTexts(ctx context.Context, prompts []string) ([][]float32, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, prompts...)
	out := make([][]float32, len(prompts))
	for i := range out {
		out[i] = f.unit()
	}
	return out, len(prompts), nil
}

func (f *fakeServer) EmbedImage(ctx context.Context, img []byte, mime string) ([]float32, int, error) {
	f.mu.Lock()
	f.images++
	f.mu.Unlock()
	return f.unit(), 1, ctx.Err()
}

func (f *fakeServer) Tokenize(ctx context.Context, text string) ([]contract.Token, error) {
	toks := make([]contract.Token, len(text))
	for i := range text {
		toks[i] = contract.Token{ID: int32(text[i]), Piece: text[i : i+1], Start: i, End: i + 1}
	}
	return toks, nil
}

func (f *fakeServer) Stop(context.Context) {}
func (f *fakeServer) PeakRAM() int64       { return 0 }

// userPrompts returns the prompts that embed the picked file name.
func (f *fakeServer) userPrompts(name string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, p := range f.prompts {
		if strings.HasPrefix(p, "title: "+name+" | text: ") {
			out = append(out, p)
		}
	}
	return out
}

// fakeStarts records the options of each server a run starts.
type fakeStarts struct {
	mu   sync.Mutex
	opts []runtime.Options
}

// newFakeLab returns a lab whose servers are fakes. The files name the model, and a BF16 server embeds along another
// dimension than the others, so a picked-file quality from a BF16 reference is not 1. installed says which
// combinations are installed.
func newFakeLab(t *testing.T, installed func(contract.Combo) bool, starts *fakeStarts) *Lab {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	files := func(c contract.Combo) (runtime.Files, error) {
		return runtime.Files{Model: string(c.TextQuant)}, nil
	}
	l := New(ctx, &recordingIndexer{}, nil, files, installed, "", 1)
	l.start = func(_ context.Context, o runtime.Options) (server, error) {
		starts.mu.Lock()
		starts.opts = append(starts.opts, o)
		starts.mu.Unlock()
		srv := &fakeServer{}
		if o.Files.Model == string(contract.QuantBF16) {
			srv.dim = 1
		}
		return srv, nil
	}
	return l
}

func newTestLab(t *testing.T) *Lab {
	t.Helper()
	return newFakeLab(t, func(contract.Combo) bool { return false }, &fakeStarts{})
}

func newRun(extra []string, c contract.Combo) *run {
	return &run{id: 1, combo: c, extra: extra, ctx: context.Background()}
}

func writePick(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRunRefusesPicksThatAreNotBoundedRegularFiles: a directory, a special file, an oversized file or a missing
// path is refused when the run is queued, so no run starts on it.
func TestRunRefusesPicksThatAreNotBoundedRegularFiles(t *testing.T) {
	l := newTestLab(t)
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxPickBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	fifo := mkfifo(t, dir)
	for _, p := range []string{dir, big, fifo, filepath.Join(dir, "missing.txt")} {
		if _, err := l.Run(context.Background(), combo(contract.QuantQ8_0), []string{p}); err == nil {
			t.Errorf("Run accepted %s", filepath.Base(p))
		}
	}
}

// TestMeasureDoesNotBlockOnASpecialFile: a special file that reaches measure (for example by a swap after the run
// was queued) is refused at once. Reading it must not wait for a writer that never comes.
func TestMeasureDoesNotBlockOnASpecialFile(t *testing.T) {
	l := newTestLab(t)
	fifo := mkfifo(t, t.TempDir())
	done := make(chan error, 1)
	go func() {
		_, _, err := l.measure(newRun([]string{fifo}, combo(contract.QuantQ8_0)), &fakeServer{})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("measure accepted a FIFO")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("measure blocked on a FIFO")
	}
}
