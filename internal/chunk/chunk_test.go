package chunk

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

const testPrefix = "title: notes.txt | text: "

// mixedText returns n words that mix ASCII, two-byte, three-byte and four-byte characters, so that
// byte-fallback tokens split characters.
func mixedText(n int) string {
	words := []string{"alpha", "héllo", "wörld", "日本語", "emoji😀", "beta", "naïve", "ß"}
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(words[i%len(words)])
		if i%5 == 4 {
			b.WriteString(".\n")
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// checkWindows verifies SPEC section 9 on the windows of text: coverage, character starts, progress,
// overlap and the prompt cap.
func checkWindows(t *testing.T, text, prefix string, tok Tokenizer, wins []Window) {
	t.Helper()
	ctx := context.Background()
	if len(wins) == 0 {
		t.Fatal("no windows")
	}
	if wins[0].Start != 0 {
		t.Fatalf("first window starts at %d, want 0", wins[0].Start)
	}
	if last := wins[len(wins)-1]; last.End != len(text) {
		t.Fatalf("last window ends at %d, want %d", last.End, len(text))
	}
	all, err := tok(ctx, text)
	if err != nil {
		t.Fatal(err)
	}
	for k, w := range wins {
		if w.End <= w.Start {
			t.Fatalf("window %d is empty or backwards: %+v", k, w)
		}
		if !utf8.ValidString(text[w.Start:w.End]) {
			t.Fatalf("window %d is not valid UTF-8", k)
		}
		if w.Start < len(text) && !utf8.RuneStart(text[w.Start]) {
			t.Fatalf("window %d starts inside a character", k)
		}
		if w.End < len(text) && !utf8.RuneStart(text[w.End]) {
			t.Fatalf("window %d ends inside a character", k)
		}
		n, err := count(ctx, tok, prefix+text[w.Start:w.End])
		if err != nil {
			t.Fatal(err)
		}
		if n > MaxTokens {
			t.Fatalf("window %d prompt has %d tokens, cap is %d", k, n, MaxTokens)
		}
		if k == 0 {
			continue
		}
		prev := wins[k-1]
		if w.Start <= prev.Start {
			t.Fatalf("window %d does not advance: start %d after %d", k, w.Start, prev.Start)
		}
		if w.Start > prev.End {
			t.Fatalf("gap between window %d and %d: %d > %d", k-1, k, w.Start, prev.End)
		}
		shared := 0
		for _, tk := range all {
			if tk.Start >= w.Start && tk.Start < prev.End {
				shared++
			}
		}
		if prev.End < len(text) && shared < Overlap {
			t.Fatalf("windows %d and %d share %d tokens, want at least %d", k-1, k, shared, Overlap)
		}
	}
}

func TestSplitByteFallbackRegression(t *testing.T) {
	tok := fake.NewByteFallbackEmbedder().Tokenize
	text := mixedText(3000)
	wins, err := Split(context.Background(), text, testPrefix, tok)
	if err != nil {
		t.Fatal(err)
	}
	if len(wins) < 5 {
		t.Fatalf("got %d windows for %d bytes, expected several", len(wins), len(text))
	}
	checkWindows(t, text, testPrefix, tok, wins)
}

func TestSplitWindowsAreBroadlyFull(t *testing.T) {
	// Each window before the last should fill the budget: 512 tokens less the prefix and a little slack.
	tok := fake.NewByteFallbackEmbedder().Tokenize
	text := mixedText(2000)
	wins, err := Split(context.Background(), text, testPrefix, tok)
	if err != nil {
		t.Fatal(err)
	}
	for k, w := range wins[:len(wins)-1] {
		n, _ := count(context.Background(), tok, text[w.Start:w.End])
		if n < 400 {
			t.Fatalf("window %d holds only %d tokens; windows should fill the cap", k, n)
		}
	}
}

func TestSplitPlainASCIIAndSmallInputs(t *testing.T) {
	ctx := context.Background()
	tok := fake.NewEmbedder().Tokenize
	var b strings.Builder
	for i := 0; i < 4000; i++ {
		b.WriteString("word")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteByte(' ')
	}
	text := b.String()
	wins, err := Split(ctx, text, testPrefix, tok)
	if err != nil {
		t.Fatal(err)
	}
	checkWindows(t, text, testPrefix, tok, wins)

	short, err := Split(ctx, "hi there", testPrefix, tok)
	if err != nil || len(short) != 1 || short[0] != (Window{0, 8}) {
		t.Fatalf("short text: got %v, %v; want one window 0..8", short, err)
	}
	if w, err := Split(ctx, "", testPrefix, tok); err != nil || w != nil {
		t.Fatalf("empty text: got %v, %v; want no windows", w, err)
	}
	if _, err := Split(ctx, "x", strings.Repeat("p ", 600), tok); err != ErrPrefixTooLong {
		t.Fatalf("long prefix: err %v, want ErrPrefixTooLong", err)
	}
}

func TestSplitTerminatesOnOneCharacterTokens(t *testing.T) {
	// Every character is its own token, so windows advance one character at a time near the overlap.
	tok := func(_ context.Context, s string) ([]contract.Token, error) {
		var toks []contract.Token
		for i := range s {
			toks = append(toks, contract.Token{Piece: s[i:], Start: i, End: i + utf8.RuneLen([]rune(s[i:])[0])})
		}
		return toks, nil
	}
	text := strings.Repeat("aé日😀", 400)
	wins, err := Split(context.Background(), text, "", tok)
	if err != nil {
		t.Fatal(err)
	}
	checkWindows(t, text, "", tok, wins)
}
