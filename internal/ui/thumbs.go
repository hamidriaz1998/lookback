package ui

import (
	"context"
	"image"
	"runtime"
	"sync"

	"github.com/egoist/mygo/ui"
)

// thumbCacheMax is how many bitmaps the view keeps. The least recently drawn go first.
const thumbCacheMax = 400

// thumbKey identifies one decoded picture: a file at one size.
type thumbKey struct {
	path string
	edge int
}

// thumbEntry is a decoded picture, ready to draw. A file the media cannot decode keeps err, so the view
// does not ask for it again.
type thumbEntry struct {
	bm   *ui.Bitmap
	err  error
	used uint64
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
	tick     uint64
}

func newThumbs(media Media) *thumbs {
	t := &thumbs{
		media:   media,
		cache:   map[thumbKey]*thumbEntry{},
		pending: map[thumbKey]bool{},
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
	for i := 0; i < max(2, runtime.NumCPU()/2); i++ {
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
// entries past the cap.
func (t *thumbs) finish(key thumbKey, img image.Image, err error) {
	delete(t.pending, key)
	e := &thumbEntry{used: t.tick, err: err}
	if err == nil {
		e.bm = ui.NewBitmap(img)
	}
	t.cache[key] = e
	for len(t.cache) > thumbCacheMax {
		var oldest thumbKey
		oldestUse := ^uint64(0)
		for k, v := range t.cache {
			if v.used < oldestUse {
				oldest, oldestUse = k, v.used
			}
		}
		delete(t.cache, oldest)
	}
}
