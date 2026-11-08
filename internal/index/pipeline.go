package index

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/hamidriaz1998/lookback/internal/chunk"
	"github.com/hamidriaz1998/lookback/internal/contract"
)

// embedBatch is the number of text prompts per EmbedTexts request (SPEC section 9).
const embedBatch = 8

// errAbort marks a file whose work ended because its session or the runtime went away. Such a file gets no
// error row, so it is not reported as broken. Nothing requeues it until the next start: sessions close only when
// the lifetime ends, or after the single worker has handed a rebuild over, and the startup rescan then finds the
// file without a row (contract.NeedsWork).
var errAbort = errors.New("index: session ended")

// process runs stat, extract, chunk, embed and commit for one file of job j (SPEC section 11). It stats the
// file again before it commits, and queues it again when it changed while it ran.
func (x *Indexer) process(ctx context.Context, j *job, id int64) {
	x.mu.Lock()
	f, ok := x.files[id]
	var fo contract.Folder
	if ok {
		if fs, has := x.folders[f.FolderID]; has {
			fo = fs.f
		} else {
			ok = false
		}
	}
	x.mu.Unlock()
	if !ok {
		return
	}
	// The file may have changed or become excluded since it was queued, so the exclusions apply again here.
	info, ok := eligible(fo, f.Kind, f.Path)
	if !ok {
		if ctx.Err() == nil {
			_ = x.forget(ctx, id)
		}
		return
	}
	size, mtime := info.Size(), info.ModTime().UnixNano()
	if size != f.Size || mtime != f.MTime {
		if !x.updateFile(ctx, f, size, mtime) {
			return
		}
		f.Size, f.MTime = size, mtime
	}

	x.mu.Lock()
	x.setCurrentLocked(j, f.Path)
	x.mu.Unlock()
	res := x.embedFile(ctx, j, f)
	x.mu.Lock()
	x.setCurrentLocked(j, "")
	x.mu.Unlock()
	if ctx.Err() != nil || res.aborted {
		return
	}

	info2, ok := eligible(fo, f.Kind, f.Path)
	if !ok {
		if ctx.Err() == nil {
			_ = x.forget(ctx, id)
		}
		return
	}
	if info2.Size() != size || info2.ModTime().UnixNano() != mtime {
		// changed while it ran: store the new stat and queue it again
		x.updateFile(ctx, f, info2.Size(), info2.ModTime().UnixNano())
		x.mu.Lock()
		x.enqueueLocked(id)
		x.mu.Unlock()
		return
	}

	idx := contract.FileIndex{FileID: id, Gen: j.gen, Size: size, MTime: mtime, Status: res.status,
		Error: res.msg, Tokens: res.tokens, Ms: res.dur.Milliseconds()}
	if err := x.store.CommitFile(ctx, idx, res.items); err != nil {
		if ctx.Err() != nil {
			return
		}
		idx.Status, idx.Error, idx.Tokens = contract.StatusError, err.Error(), 0
		if err := x.store.CommitFile(ctx, idx, nil); err != nil {
			return
		}
	}

	x.mu.Lock()
	defer x.mu.Unlock()
	j.rows[id] = idx
	j.items++
	j.tokens += float64(res.tokens)
	if res.textDur > 0 && res.textTokens > 0 {
		x.textRate.add(res.textTokens / res.textDur.Seconds())
	}
	if res.imageDur > 0 && res.imageTokens > 0 {
		x.imageRate.add(res.imageTokens / res.imageDur.Seconds())
	}
}

// updateFile stores the new size and mtime of f. It reports false when the store refused the write.
func (x *Indexer) updateFile(ctx context.Context, f contract.FileRec, size, mtime int64) bool {
	f.Size, f.MTime = size, mtime
	if err := x.store.SaveFile(ctx, &f); err != nil {
		return false
	}
	x.mu.Lock()
	x.files[f.ID] = f
	x.mu.Unlock()
	return true
}

func (x *Indexer) setCurrentLocked(j *job, path string) { j.curr = path }

// result is the outcome of embedding one file.
type result struct {
	items                   []contract.Item
	status                  contract.Status
	msg                     string
	tokens                  int64
	dur                     time.Duration
	textTokens, imageTokens float64
	textDur, imageDur       time.Duration
	aborted                 bool // the session or the runtime ended; write nothing
}

// embedFile extracts f and embeds every unit through the job's session. Any failure but an ended session
// becomes an error row for the file.
func (x *Indexer) embedFile(ctx context.Context, j *job, f contract.FileRec) result {
	start := time.Now()
	var r result
	err := x.ex.Extract(ctx, f, func(u contract.Unit) error {
		if u.Image != nil {
			t0 := time.Now()
			out, err := retry(ctx, j.sess, func() (embedOut, error) {
				vec, n, err := j.sess.EmbedImage(ctx, u.Image, imageMIME(u.Image))
				return embedOut{vecs: [][]float32{vec}, tokens: n}, err
			})
			if err != nil {
				return err
			}
			r.imageDur += time.Since(t0)
			r.imageTokens += float64(out.tokens)
			r.tokens += int64(out.tokens)
			r.items = append(r.items, contract.Item{Gen: j.gen, FileID: f.ID, Unit: u.Unit, Start: -1, End: -1,
				Modality: "image", Snippet: u.Snippet, Vector: out.vecs[0]})
			return nil
		}
		t0 := time.Now()
		items, n, err := x.embedText(ctx, j, f, u)
		if err != nil {
			return err
		}
		r.textDur += time.Since(t0)
		r.textTokens += float64(n)
		r.tokens += int64(n)
		r.items = append(r.items, items...)
		return nil
	})
	r.dur = time.Since(start)
	r.status = contract.StatusDone
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, errAbort) {
			r.aborted = true
			return r
		}
		r.status = contract.StatusError
		r.msg = err.Error()
		r.items = nil
		r.tokens = 0
	}
	return r
}

// embedText chunks one text unit with the title prefix and embeds the windows in batches of embedBatch.
func (x *Indexer) embedText(ctx context.Context, j *job, f contract.FileRec, u contract.Unit) ([]contract.Item, int64, error) {
	prefix := "title: " + filepath.Base(f.Path) + " | text: "
	tok := func(ctx context.Context, text string) ([]contract.Token, error) {
		return retry(ctx, j.sess, func() ([]contract.Token, error) {
			t, err := j.sess.Tokenize(ctx, text)
			return t, err
		})
	}
	wins, err := chunk.Split(ctx, u.Text, prefix, tok)
	if err != nil {
		return nil, 0, err
	}
	base := u.Start
	if base < 0 {
		base = 0
	}
	var items []contract.Item
	var tokens int64
	for i := 0; i < len(wins); i += embedBatch {
		batch := wins[i:min(i+embedBatch, len(wins))]
		prompts := make([]string, len(batch))
		for k, w := range batch {
			prompts[k] = prefix + u.Text[w.Start:w.End]
		}
		out, err := retry(ctx, j.sess, func() (embedOut, error) {
			vecs, n, err := j.sess.EmbedTexts(ctx, prompts)
			return embedOut{vecs: vecs, tokens: n}, err
		})
		if err != nil {
			return nil, 0, err
		}
		if len(out.vecs) != len(batch) {
			return nil, 0, fmt.Errorf("index: %d vectors for %d prompts", len(out.vecs), len(batch))
		}
		tokens += int64(out.tokens)
		for k, w := range batch {
			items = append(items, contract.Item{Gen: j.gen, FileID: f.ID, Unit: u.Unit,
				Start: base + w.Start, End: base + w.End, Modality: "text",
				Snippet: snippet(u.Text[w.Start:w.End]), Vector: out.vecs[k]})
		}
	}
	return items, tokens, nil
}

type embedOut struct {
	vecs   [][]float32
	tokens int
}

// retry runs fn and, while the session is restarting, waits for it to be ready and runs fn again (SPEC section 8).
// An ended session returns errAbort.
func retry[T any](ctx context.Context, sess contract.Session, fn func() (T, error)) (T, error) {
	for {
		v, err := fn()
		if err == nil || !(errors.Is(err, contract.ErrRestarting) || errors.Is(err, contract.ErrNotReady)) {
			return v, err
		}
		if rerr := sess.Ready(ctx); rerr != nil {
			var zero T
			if ctx.Err() != nil {
				return zero, ctx.Err()
			}
			return zero, fmt.Errorf("%w: %w", errAbort, rerr)
		}
	}
}

// snippet returns the first 300 characters of s, whitespace collapsed to single spaces (SPEC section 9).
func snippet(s string) string {
	out := []rune{}
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			space = true
			continue
		}
		if space && len(out) > 0 {
			out = append(out, ' ')
		}
		space = false
		out = append(out, r)
		if len(out) == 300 {
			break
		}
	}
	return string(out)
}

// imageMIME names the type of a PNG or JPEG the extractor produced.
func imageMIME(img []byte) string {
	if bytes.HasPrefix(img, []byte{0xFF, 0xD8, 0xFF}) {
		return "image/jpeg"
	}
	return "image/png"
}
