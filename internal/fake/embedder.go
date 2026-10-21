package fake

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Embedder is a deterministic contract.Embedder. Vectors come from a hash of the input, so
// equal inputs give equal vectors. Tokens are one per word, a rough stand-in for the real tokenizer.
type Embedder struct{}

// NewEmbedder returns an Embedder.
func NewEmbedder() *Embedder { return &Embedder{} }

// EmbedTexts returns one vector per prompt and the prompt token count, about two tokens per word.
func (e *Embedder) EmbedTexts(ctx context.Context, prompts []string) ([][]float32, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	vecs := make([][]float32, len(prompts))
	tokens := 0
	for i, p := range prompts {
		vecs[i] = unitVector(hashSeed(p))
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
	return unitVector(hashSeed(string(img))), 256 + len(img)/64, nil
}

// Tokenize returns one token per whitespace-separated word, with its byte range.
func (e *Embedder) Tokenize(ctx context.Context, text string) ([]contract.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var toks []contract.Token
	start := -1
	for i, r := range text {
		if unicode.IsSpace(r) {
			if start >= 0 {
				toks = append(toks, wordToken(text, start, i))
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		toks = append(toks, wordToken(text, start, len(text)))
	}
	return toks, nil
}

// wordToken returns the token for text[start:end].
func wordToken(text string, start, end int) contract.Token {
	piece := text[start:end]
	return contract.Token{ID: int32(hashSeed(piece) % 256000), Piece: piece, Start: start, End: end}
}
