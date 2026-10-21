package chunk

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// MaxTokens is the cap on each embedded prompt, prefix included (SPEC section 9).
const MaxTokens = 512

// Overlap is the minimum number of tokens two consecutive windows share (SPEC section 9).
const Overlap = 64

// Tokenizer returns the tokens of text with their byte ranges. Embedder.Tokenize has this shape.
type Tokenizer func(ctx context.Context, text string) ([]contract.Token, error)

// Window is a byte range [Start, End) of the text that Split was given.
type Window struct {
	Start, End int
}

// ErrPrefixTooLong is returned when the prompt prefix alone fills the token cap.
var ErrPrefixTooLong = errors.New("chunk: prompt prefix leaves no room in the 512-token cap")

// ErrCharTooLong is returned when one character, with the prefix, is over the token cap.
var ErrCharTooLong = errors.New("chunk: one character with the prompt prefix is over the 512-token cap")

// Split cuts text into windows for embedding. Each window's prompt is prefix followed by the window's
// text, and every prompt is at most MaxTokens tokens. Consecutive windows share at least Overlap tokens.
// Windows start and end on UTF-8 character starts, so each one is valid UTF-8 and the windows cover every
// byte of text. Every window advances by at least one character. Empty text has no windows.
func Split(ctx context.Context, text, prefix string, tok Tokenizer) ([]Window, error) {
	if text == "" {
		return nil, nil
	}
	toks, err := tok(ctx, text)
	if err != nil {
		return nil, err
	}
	prefixToks, err := tok(ctx, prefix)
	if err != nil {
		return nil, err
	}
	budget := MaxTokens - len(prefixToks)
	if budget < 1 {
		return nil, ErrPrefixTooLong
	}

	var wins []Window
	start, i := 0, 0 // i is the first token that ends after start
	for {
		for i < len(toks) && toks[i].End <= start {
			i++
		}
		j := min(len(toks), i+budget)
		var end int
		for {
			end = cutEnd(text, toks, start, i, j)
			n, err := count(ctx, tok, prefix+text[start:end])
			if err != nil {
				return nil, err
			}
			if n <= MaxTokens {
				break
			}
			if end <= start+charLen(text, start) {
				return nil, ErrCharTooLong
			}
			j-- // shrink the window by one token and check the prompt again
		}
		wins = append(wins, Window{Start: start, End: end})
		if end == len(text) {
			return wins, nil
		}

		// The next window starts at the character start at or before the token that lies Overlap
		// tokens before this window's last token. If that would not move forward, step one character.
		inWin := i
		for inWin < len(toks) && toks[inWin].Start < end {
			inWin++
		}
		next := start + charLen(text, start)
		if k := inWin - Overlap; k >= 0 && k < len(toks) {
			if s := charStart(text, toks[k].Start); s > start {
				next = s
			}
		}
		start = next
	}
}

// cutEnd returns the end of a window that holds tokens i..j-1, moved back to a character start. A
// window never ends before its first character, and the last window ends at len(text).
func cutEnd(text string, toks []contract.Token, start, i, j int) int {
	if j >= len(toks) {
		return len(text)
	}
	end := start
	if j > i {
		end = toks[j-1].End
	}
	if end > len(text) {
		end = len(text)
	}
	if end < len(text) {
		end = charStart(text, end)
	}
	if end <= start {
		end = start + charLen(text, start)
	}
	return end
}

// charStart returns the largest character start at or before p.
func charStart(text string, p int) int {
	for p > 0 && p < len(text) && !utf8.RuneStart(text[p]) {
		p--
	}
	return p
}

// charLen returns the byte length of the character at p, or 0 at the end of text.
func charLen(text string, p int) int {
	if p >= len(text) {
		return 0
	}
	_, n := utf8.DecodeRuneInString(text[p:])
	return n
}

// count returns the number of tokens of s.
func count(ctx context.Context, tok Tokenizer, s string) (int, error) {
	toks, err := tok(ctx, s)
	return len(toks), err
}
