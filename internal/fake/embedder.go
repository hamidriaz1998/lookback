package fake

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Embedder is a deterministic contract.Embedder. Vectors come from a hash of the input, so equal
// inputs give equal vectors. Tokens are rough: one per word, about two per word in the prompt count.
type Embedder struct {
	// ByteFallback makes Tokenize split every non-ASCII rune into one token per byte, as llama.cpp does
	// for characters outside its vocabulary. WP4 uses it to test chunk cuts inside a character.
	ByteFallback bool

	salt uint64 // mixed into every vector; sessions of different vector spaces use different salts
}

// NewEmbedder returns an Embedder that tokenizes by word.
func NewEmbedder() *Embedder { return &Embedder{} }

// NewByteFallbackEmbedder returns an Embedder whose Tokenize emits byte-fallback tokens for non-ASCII runes.
func NewByteFallbackEmbedder() *Embedder { return &Embedder{ByteFallback: true} }

// EmbedTexts returns one vector per prompt and the prompt token count, about two tokens per word.
func (e *Embedder) EmbedTexts(ctx context.Context, prompts []string) ([][]float32, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	vecs := make([][]float32, len(prompts))
	tokens := 0
	for i, p := range prompts {
		vecs[i] = unitVector(hashSeed(p) ^ e.salt)
		tokens += 2*len(strings.Fields(p)) + 4
	}
	return vecs, tokens, nil
}

// EmbedImage returns a vector for img and an estimate of its image tokens.
func (e *Embedder) EmbedImage(ctx context.Context, img []byte, mime string) ([]float32, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if len(img) == 0 {
		return nil, 0, errors.New("fake embedder: empty image")
	}
	return unitVector(hashSeed(string(img)) ^ e.salt), 256 + len(img)/64, nil
}

// Tokenize splits text into tokens with byte ranges. Words are tokens. With ByteFallback, each
// non-ASCII rune becomes one token per byte, and the pieces of those tokens are raw bytes.
func (e *Embedder) Tokenize(ctx context.Context, text string) ([]contract.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var toks []contract.Token
	start := -1
	emitWord := func(end int) {
		if start >= 0 {
			toks = append(toks, wordToken(text, start, end))
			start = -1
		}
	}
	for i, r := range text {
		switch {
		case unicode.IsSpace(r):
			emitWord(i)
		case e.ByteFallback && r >= utf8.RuneSelf:
			emitWord(i)
			for b := 0; b < utf8.RuneLen(r); b++ {
				toks = append(toks, wordToken(text, i+b, i+b+1))
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	emitWord(len(text))
	return toks, nil
}

// wordToken returns the token for text[start:end].
func wordToken(text string, start, end int) contract.Token {
	piece := text[start:end]
	return contract.Token{ID: int32(hashSeed(piece) % 256000), Piece: piece, Start: start, End: end}
}
