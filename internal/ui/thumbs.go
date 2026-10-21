package ui

import (
	"context"
	"image"
	"sync"

	"github.com/egoist/mygo/ui"
)

// The bitmaps the view keeps, by bytes. Grid tiles and previews have separate budgets, so a run of previews
// cannot push the visible tiles out. The least recently drawn entries go first.
const (
	tileBudget    = 64 << 20 // bytes of grid tile bitmaps
	previewBudget = 96 << 20 // bytes of preview bitmaps
	entryOverhead = 1 << 10  // charged for an entry without a bitmap, such as a failed decode
	// decodeWorkers bounds the decodes at once. Each full-size decode holds its pixels in memory, so more
	// workers multiply the peak.
	decodeWorkers = 2
)

// cacheClass names the budget an entry is charged to.
type cacheClass bool

const (
	classPreview cacheClass = false
	classTile    cacheClass = true
)

// classOf returns the budget of a key: tiles decode at tileEdgeMax or less.
func classOf(key thumbKey) cacheClass {
	if key.edge <= tileEdgeMax {
		return classTile
	}
	return classPreview
}

// budgetOf returns the byte budget of a class.
func budgetOf(c cacheClass) int64 {
	if c == classTile {
		return tileBudget
	}
	return previewBudget
}

// thumbKey identifies one decoded picture: a file at one size.
type thumbKey struct {
	path string
	edge int
}

// thumbEntry is a decoded picture, ready to draw. A file the media cannot decode keeps err, so the view
// does not ask for it again.
type thumbEntry struct {
	bm    *ui.Bitmap
	err   error
	used  uint64
	class cacheClass
	bytes int64
}

// thumbs decodes pictures on a worker pool and keeps the bitmaps for the view. The view calls get on the
// main thread. The workers send each finished picture back through dispatch, which runs on the main
// thread (Window.Update). The queue is last in, first out, so the tiles on screen decode before the ones
// the user scrolled past.
type thumbs struct {
	media Media

	mu    sync.Mutex
	cond  *sync.Cond
	queue []thumbKey

	// Main thread only.
	dispatch func(func())
	cache    map[thumbKey]*thumbEntry
	pending  map[thumbKey]bool
	bytes    map[cacheClass]int64 // bytes charged to each class
	tick     uint64
}

func newThumbs(media Media) *thumbs {
	t := &thumbs{
		media:   media,
		cache:   map[thumbKey]*thumbEntry{},
		pending: map[thumbKey]bool{},
		bytes:   map[cacheClass]int64{},
	}
	t.cond = sync.NewCond(&t.mu)
	return t
}

// start runs the workers until ctx ends. dispatch must run its function on the main thread.
func (t *thumbs) start(ctx context.Context, dispatch func(func())) {
	t.dispatch = dispatch
	context.AfterFunc(ctx, func() {
		t.mu.Lock()
		t.queue = nil
		t.mu.Unlock()
		t.cond.Broadcast()
	})
	for i := 0; i < decodeWorkers; i++ {
		go t.worker(ctx)
	}
}

// get returns the picture for key when it is decoded. Otherwise it queues the decode and returns ok false.
// The view draws a placeholder, and the result shows on a later frame.
func (t *thumbs) get(key thumbKey) (bm *ui.Bitmap, err error, ok bool) {
	t.tick++
	if e, hit := t.cache[key]; hit {
		e.used = t.tick
		return e.bm, e.err, true
	}
	if t.pending[key] {
		return nil, nil, false
	}
	t.pending[key] = true
	t.mu.Lock()
	t.queue = append(t.queue, key)
	t.mu.Unlock()
	t.cond.Signal()
	return nil, nil, false
}

func (t *thumbs) worker(ctx context.Context) {
	for {
		t.mu.Lock()
		for len(t.queue) == 0 && ctx.Err() == nil {
			t.cond.Wait()
		}
		if ctx.Err() != nil {
			t.mu.Unlock()
			return
		}
		key := t.queue[len(t.queue)-1]
		t.queue = t.queue[:len(t.queue)-1]
		t.mu.Unlock()

		img, err := t.media.Image(ctx, key.path, key.edge)
		if ctx.Err() != nil {
			return
		}
		t.dispatch(func() { t.finish(key, img, err) })
	}
}

// finish runs on the main thread. It makes the bitmap, stores it, and drops the least recently used
// entries of its class past the class budget.
func (t *thumbs) finish(key thumbKey, img image.Image, err error) {
	delete(t.pending, key)
	e := &thumbEntry{used: t.tick, err: err, class: classOf(key), bytes: entryOverhead}
	if err == nil {
		e.bm = ui.NewBitmap(img)
		b := img.Bounds()
		e.bytes = int64(b.Dx()) * int64(b.Dy()) * 4
	}
	if old, ok := t.cache[key]; ok {
		t.bytes[old.class] -= old.bytes
	}
	t.cache[key] = e
	t.bytes[e.class] += e.bytes
	for t.bytes[e.class] > budgetOf(e.class) {
		var oldest thumbKey
		oldestUse := ^uint64(0)
		for k, v := range t.cache {
			if v.class == e.class && v.used < oldestUse {
				oldest, oldestUse = k, v.used
			}
		}
		t.bytes[e.class] -= t.cache[oldest].bytes
		delete(t.cache, oldest)
	}
}
