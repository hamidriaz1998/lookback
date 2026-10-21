// Package search embeds the query, runs the store search, groups hits by file, applies the filename
// boost and finds similar files (SPEC section 12).
package search

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// PageSize is how many results Run and Similar return (SPEC section 10: the top 60).
const PageSize = 60

// candidates is how many files Run takes from the store before the filename boost reorders them (SPEC
// section 10 searches the 300 nearest items). A file just outside the top 60 by similarity can still rank
// in the top 60 once boosted, so the boost is applied to this wider set, not to the first 60.
const candidates = 300

// The filename boost (SPEC section 10): each query word of 3 or more letters found in the file name or the
// last two path segments adds boostPerWord, up to boostCap.
const (
	boostPerWord = 0.04
	boostCap     = 0.12
	minWordLen   = 3
)

// queryPrefix is the query prompt of the embedding model (SPEC section 8).
const queryPrefix = "task: search result | query: "

// ErrNoActive is returned when no generation is active, so there is nothing to search.
var ErrNoActive = errors.New("search: no active generation")

// ErrGenerationChanged means the active vector space changed while the query was being embedded or searched.
// The caller should acquire a session for the new active space and retry the query.
var ErrGenerationChanged = errors.New("search: active generation changed; retry")

// Result is one file in a result list. ItemID, Unit, Modality and Snippet describe the file's best item.
type Result struct {
	FileID   int64
	Path     string
	Kind     contract.Kind
	ItemID   int64
	Unit     int
	Modality string
	Snippet  string
	Score    float64 // cosine similarity, plus the filename boost for Run
}

// Searcher runs queries and similarity searches over the active generation.
type Searcher struct {
	store contract.Store
}

// New returns a Searcher over st.
func New(st contract.Store) *Searcher { return &Searcher{store: st} }

// Run embeds query through sess and returns the top PageSize files of the active generation, best first.
// sess must be a session for the active generation's vector space. kinds, when not empty, limits the files.
// An empty query returns no results and does not use the session.
func (s *Searcher) Run(ctx context.Context, sess contract.Session, query string, kinds []contract.Kind) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	gen, err := s.active(ctx)
	if err != nil {
		return nil, err
	}
	if sess.Space() != gen.Space {
		return nil, fmt.Errorf("search: session space %+v is not the active generation's space %+v", sess.Space(), gen.Space)
	}
	vec, err := embedQuery(ctx, sess, queryPrefix+query)
	if err != nil {
		return nil, err
	}
	hits, err := s.store.Search(ctx, gen.ID, vec, kinds, candidates)
	if err != nil {
		return nil, err
	}
	current, err := s.active(ctx)
	if err != nil {
		return nil, err
	}
	if current.ID != gen.ID {
		return nil, ErrGenerationChanged
	}
	words := queryWords(query)
	out := make([]Result, 0, len(hits))
	for _, h := range hits {
		r := fromHit(h)
		r.Score += boost(words, h.Path)
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].FileID < out[j].FileID
	})
	if len(out) > PageSize {
		out = out[:PageSize]
	}
	return out, nil
}

// Similar returns the PageSize files nearest to item itemID, which must be an item of the active generation.
// It uses the stored vector, so it needs no session. It returns contract.ErrStaleItem for an item of another
// generation; the caller drops that seed.
func (s *Searcher) Similar(ctx context.Context, itemID int64, kinds []contract.Kind) ([]Result, error) {
	gen, err := s.active(ctx)
	if err != nil {
		return nil, err
	}
	hits, err := s.store.Similar(ctx, gen.ID, itemID, kinds, PageSize)
	if err != nil {
		return nil, err
	}
	out := make([]Result, len(hits))
	for i, h := range hits {
		out[i] = fromHit(h)
	}
	return out, nil
}

// active returns the active generation, or ErrNoActive.
func (s *Searcher) active(ctx context.Context) (contract.Generation, error) {
	gens, err := s.store.ListGenerations(ctx)
	if err != nil {
		return contract.Generation{}, err
	}
	for _, g := range gens {
		if g.State == contract.GenActive {
			return g, nil
		}
	}
	return contract.Generation{}, ErrNoActive
}

// embedQuery embeds one prompt. When the sidecar is restarting it waits for the session to be ready and
// tries once more.
func embedQuery(ctx context.Context, sess contract.Session, prompt string) ([]float32, error) {
	vecs, _, err := sess.EmbedTexts(ctx, []string{prompt})
	if errors.Is(err, contract.ErrRestarting) {
		if err := sess.Ready(ctx); err != nil {
			return nil, err
		}
		vecs, _, err = sess.EmbedTexts(ctx, []string{prompt})
	}
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("search: embedder returned %d vectors for one query", len(vecs))
	}
	return vecs[0], nil
}

// fromHit converts a store hit to a result, without boost.
func fromHit(h contract.Hit) Result {
	return Result{
		FileID: h.FileID, Path: h.Path, Kind: h.Kind, ItemID: h.ItemID, Unit: h.Unit,
		Modality: h.Modality, Snippet: h.Snippet, Score: h.Score,
	}
}

// queryWords returns the distinct lowercase words of q that have at least minWordLen letters. Words are the
// runs of letters between other characters.
func queryWords(q string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if utf8.RuneCountInString(w) < minWordLen || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// boost returns the filename boost of path for the query words: boostPerWord for each word found in the
// file name or the last two path segments (case-insensitive), at most boostCap.
func boost(words []string, path string) float64 {
	segs := strings.Split(filepath.ToSlash(path), "/")
	tail := segs[len(segs)-1]
	if len(segs) >= 2 {
		tail = segs[len(segs)-2] + "/" + tail
	}
	tail = strings.ToLower(tail)
	n := 0
	for _, w := range words {
		if strings.Contains(tail, w) {
			n++
		}
	}
	return math.Min(float64(n)*boostPerWord, boostCap)
}
