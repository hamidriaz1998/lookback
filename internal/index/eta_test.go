package index

import (
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// TestETAMath checks the token-weighted ETA (SPEC section 11, Q27) with known rates.
func TestETAMath(t *testing.T) {
	var text, img ewma
	text.add(100) // 1000 tokens at 100 tokens/s: 10 s
	img.add(50)   // 500 tokens at 50 tokens/s: 10 s
	d, ok := etaFor(1000, 500, text, img)
	if !ok || d != 20*time.Second {
		t.Fatalf("etaFor = %v, %v; want 20s, true", d, ok)
	}
	// With no image rate, images use the text rate: 1000/100 + 500/100 = 15 s.
	var noImg ewma
	if d, ok := etaFor(1000, 500, text, noImg); !ok || d != 15*time.Second {
		t.Fatalf("etaFor without an image rate = %v, %v; want 15s, true", d, ok)
	}
	// No rate at all and work left: no estimate.
	var none ewma
	if _, ok := etaFor(1, 0, none, none); ok {
		t.Fatal("etaFor with no rates and work left should not estimate")
	}
	// Moving average: the first sample is the value, later ones move it by alpha (0.1).
	var e ewma
	e.add(100)
	e.add(200)
	if e.v < 109.99 || e.v > 110.01 {
		t.Fatalf("ewma after 100 then 200 = %v, want 110", e.v)
	}
}

// TestSnippet checks the 300-character, whitespace-collapsed snippet (SPEC section 9).
func TestSnippet(t *testing.T) {
	if got := snippet("  a \n\t b  "); got != "a b" {
		t.Fatalf("snippet = %q", got)
	}
	long := make([]byte, 0, 400)
	for i := 0; i < 400; i++ {
		long = append(long, 'x')
	}
	if got := snippet(string(long)); len(got) != 300 {
		t.Fatalf("snippet length = %d, want 300", len(got))
	}
}

// TestStreamKeepsTerminal checks that a terminal value is kept while a reader is slow, and that
// non-terminal values with the same generation coalesce.
func TestStreamKeepsTerminal(t *testing.T) {
	s := newStream()
	sub := &subscriber{wake: make(chan struct{}, 1)}
	s.subs[sub] = struct{}{}
	s.publish(contract.Progress{Gen: 1, FilesDone: 1}, false)
	s.publish(contract.Progress{Gen: 1, FilesDone: 2}, false)
	s.publish(contract.Progress{Gen: 1, FilesDone: 3, FilesTotal: 3}, true)
	s.publish(contract.Progress{Gen: 1, FilesDone: 4}, false)
	if len(sub.pending) != 2 {
		t.Fatalf("pending = %d entries, want 2 (coalesced snapshot, then terminal)", len(sub.pending))
	}
	if !sub.pending[0].terminal && sub.pending[0].p.FilesDone != 2 {
		t.Fatalf("first pending = %+v, want the coalesced value", sub.pending[0].p)
	}
	found := false
	for _, e := range sub.pending {
		if e.terminal && e.p.FilesDone == 3 {
			found = true
		}
	}
	if !found {
		t.Fatal("terminal value was dropped")
	}
}
