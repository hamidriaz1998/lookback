// Package index runs the indexing pipeline: the queue, stat, extract, chunk, embed and commit steps,
// generations, pause reasons, progress and the token-weighted ETA (SPEC section 11).
package index

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/watch"
)

// Indexer implements contract.Indexer over a Store, an Extractor and a Runtime. It has one worker goroutine,
// which embeds one file at a time, so the sessions it uses are never shared by two files at once.
type Indexer struct {
	store   contract.Store
	ex      contract.Extractor
	rt      contract.Runtime
	initial contract.VectorSpace // the space of the first generation, when the store has none

	scanMu sync.Mutex // one folder scan at a time (watchers and the startup rescan both scan)

	mu          sync.Mutex
	started     bool
	life        context.Context
	wake        chan struct{} // closed and replaced on every change the worker waits for
	reasons     map[contract.PauseReason]bool
	folders     map[int64]*folderState
	files       map[int64]contract.FileRec
	active      *job
	rebuild     *job
	rebuildBusy bool // a Rebuild call is opening its session
	textRate    ewma // tokens per second, text
	imageRate   ewma // tokens per second, images
	actStream   *stream
	rebStream   *stream
}

type folderState struct {
	f      contract.Folder
	cancel context.CancelFunc // stops the folder's watcher
}

// New returns an Indexer. initial is the vector space of the first generation, used when the store has no
// generation yet (the first run).
func New(store contract.Store, ex contract.Extractor, rt contract.Runtime, initial contract.VectorSpace) *Indexer {
	return &Indexer{
		store:     store,
		ex:        ex,
		rt:        rt,
		initial:   initial,
		wake:      make(chan struct{}),
		reasons:   map[contract.PauseReason]bool{},
		folders:   map[int64]*folderState{},
		files:     map[int64]contract.FileRec{},
		actStream: newStream(),
		rebStream: newStream(),
	}
}

// Start loads the store, opens the active generation's session, and starts the worker, the progress ticker and
// the startup rescan. If the store has no active generation, it starts a rebuild into a new one (or resumes a
// building one from an earlier run), so the first index is a rebuild.
func (x *Indexer) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	x.mu.Lock()
	if x.started {
		x.mu.Unlock()
		return errors.New("index: Start may be called once")
	}
	x.life = ctx
	x.mu.Unlock()

	folders, err := x.store.ListFolders(ctx)
	if err != nil {
		return err
	}
	gens, err := x.store.ListGenerations(ctx)
	if err != nil {
		return err
	}
	var act, bld *contract.Generation
	for i := range gens {
		switch gens[i].State {
		case contract.GenActive:
			act = &gens[i]
		case contract.GenBuilding:
			bld = &gens[i]
		}
	}
	var sessions []contract.Session // closed again when Start fails
	ok := false
	defer func() {
		if !ok {
			for _, s := range sessions {
				_ = s.Close()
			}
		}
	}()

	x.mu.Lock()
	for _, fo := range folders {
		x.folders[fo.ID] = &folderState{f: fo}
		fl, err := x.store.ListFiles(ctx, fo.ID)
		if err != nil {
			x.mu.Unlock()
			return err
		}
		for _, f := range fl {
			x.files[f.ID] = f
		}
	}
	x.mu.Unlock()

	var active, rebuild *job
	if act != nil {
		sess, err := x.rt.Session(ctx, act.Space)
		if err != nil {
			return err
		}
		sessions = append(sessions, sess)
		rows, err := x.store.ListFileIndex(ctx, act.ID)
		if err != nil {
			return err
		}
		active = newJob(act.ID, act.Space, sess, rows)
	}
	if bld == nil && act == nil {
		if x.initial == (contract.VectorSpace{}) {
			return errors.New("index: no generation and no initial vector space")
		}
		bld = &contract.Generation{Space: x.initial, State: contract.GenBuilding, CreatedAt: time.Now()}
		if err := x.store.NewGeneration(ctx, bld); err != nil {
			return err
		}
	}
	if bld != nil {
		sess, err := x.rt.Session(ctx, bld.Space)
		if err != nil {
			return err
		}
		sessions = append(sessions, sess)
		rows, err := x.store.ListFileIndex(ctx, bld.ID)
		if err != nil {
			return err
		}
		rebuild = newJob(bld.ID, bld.Space, sess, rows)
	}
	x.mu.Lock()
	x.active, x.rebuild = active, rebuild
	x.queueNeedingWorkLocked()
	x.started = true
	x.mu.Unlock()
	ok = true

	go x.work(ctx)
	go x.tick(ctx)
	go x.startup(ctx)
	return nil
}

// queueNeedingWorkLocked queues every file that needs work in the active and the rebuild job.
func (x *Indexer) queueNeedingWorkLocked() {
	for _, id := range x.sortedFileIDsLocked() {
		x.enqueueLocked(id)
	}
}

func (x *Indexer) sortedFileIDsLocked() []int64 {
	ids := make([]int64, 0, len(x.files))
	for id := range x.files {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	return ids
}

// enqueueLocked queues file id in every job for which it needs work (contract.NeedsWork) and wakes the worker.
// The caller holds x.mu.
func (x *Indexer) enqueueLocked(id int64) {
	f, ok := x.files[id]
	if !ok {
		return
	}
	for _, j := range []*job{x.active, x.rebuild} {
		if j == nil {
			continue
		}
		var row *contract.FileIndex
		if r, has := j.rows[id]; has {
			row = &r
		}
		if contract.NeedsWork(f, row) {
			j.push(id)
		}
	}
	x.signalLocked()
}

// signalLocked wakes the worker. The caller holds x.mu.
func (x *Indexer) signalLocked() {
	close(x.wake)
	x.wake = make(chan struct{})
}

// live reports whether a command may run: Start has been called and the lifetime has not ended.
func (x *Indexer) live() error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.started {
		return contract.ErrNotStarted
	}
	if x.life.Err() != nil {
		return contract.ErrStopped
	}
	return nil
}

// Pause stops the job for reason. Reasons stack: the job runs when none is set.
func (x *Indexer) Pause(reason contract.PauseReason) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.reasons[reason] = true
	x.signalLocked()
}

// Resume lifts the pause for reason.
func (x *Indexer) Resume(reason contract.PauseReason) {
	x.mu.Lock()
	defer x.mu.Unlock()
	delete(x.reasons, reason)
	x.signalLocked()
}

// bound returns a context that ends when ctx ends or the Indexer lifetime ends, so a command that is already
// running stops with the Indexer. The caller calls the returned cancel function.
func (x *Indexer) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	x.mu.Lock()
	life := x.life
	x.mu.Unlock()
	c, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(life, cancel)
	return c, func() { stop(); cancel() }
}

// Retry queues the files for indexing again in the active generation, after clearing their rows.
func (x *Indexer) Retry(ctx context.Context, fileIDs []int64) error {
	if err := x.live(); err != nil {
		return err
	}
	ctx, cancel := x.bound(ctx)
	defer cancel()
	x.mu.Lock()
	for _, id := range fileIDs {
		if _, ok := x.files[id]; !ok {
			x.mu.Unlock()
			return contract.ErrUnknownFile
		}
	}
	act := x.active
	x.mu.Unlock()
	if act == nil {
		return contract.ErrUnknownGen
	}
	for _, id := range fileIDs {
		if err := x.store.ClearFileIndex(ctx, id, act.gen); err != nil {
			return err
		}
		x.mu.Lock()
		delete(act.rows, id)
		act.push(id)
		x.signalLocked()
		x.mu.Unlock()
	}
	return nil
}

// RetryAll queues every file with an error row in the active generation.
func (x *Indexer) RetryAll(ctx context.Context) error {
	if err := x.live(); err != nil {
		return err
	}
	x.mu.Lock()
	act := x.active
	var ids []int64
	if act != nil {
		for id, row := range act.rows {
			if row.Status == contract.StatusError {
				ids = append(ids, id)
			}
		}
	}
	x.mu.Unlock()
	if act == nil {
		return contract.ErrUnknownGen
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	return x.Retry(ctx, ids)
}

// Rescan walks one folder and queues the files that need work, as the startup rescan does.
func (x *Indexer) Rescan(ctx context.Context, folderID int64) error {
	if err := x.live(); err != nil {
		return err
	}
	x.mu.Lock()
	fs, ok := x.folders[folderID]
	x.mu.Unlock()
	if !ok {
		return contract.ErrUnknownFolder
	}
	ctx, cancel := x.bound(ctx)
	defer cancel()
	return x.scanFolder(ctx, fs.f)
}

// Rebuild opens a session for the new precisions, creates a building generation, and queues every file in it.
// The active generation keeps serving until the building one is complete, and then the worker activates it.
func (x *Indexer) Rebuild(ctx context.Context, text, enc contract.Quant) (int64, error) {
	if err := x.live(); err != nil {
		return 0, err
	}
	ctx, cancel := x.bound(ctx)
	defer cancel()
	x.mu.Lock()
	if x.rebuild != nil || x.rebuildBusy {
		x.mu.Unlock()
		return 0, contract.ErrRebuildBusy
	}
	x.rebuildBusy = true
	space := contract.VectorSpace{TextQuant: text, EncQuant: enc, ModelRev: x.initial.ModelRev}
	if x.active != nil {
		space.ModelRev = x.active.space.ModelRev
	}
	x.mu.Unlock()

	fail := func(err error) (int64, error) {
		x.mu.Lock()
		x.rebuildBusy = false
		x.mu.Unlock()
		return 0, err
	}
	sess, err := x.rt.Session(ctx, space)
	if err != nil {
		return fail(err)
	}
	// The lifetime may have ended while the session opened. Checking here, under x.mu, means shutdown either
	// sees the job this call installs or this call sees the ended lifetime.
	if ctx.Err() != nil || x.life.Err() != nil {
		_ = sess.Close()
		return fail(contract.ErrStopped)
	}
	g := &contract.Generation{Space: space, State: contract.GenBuilding, CreatedAt: time.Now()}
	if err := x.store.NewGeneration(ctx, g); err != nil {
		_ = sess.Close()
		return fail(err)
	}
	x.mu.Lock()
	if x.life.Err() != nil {
		x.rebuildBusy = false
		x.mu.Unlock()
		_ = sess.Close()
		return 0, contract.ErrStopped
	}
	x.rebuild = newJob(g.ID, space, sess, nil)
	x.rebuildBusy = false
	x.queueNeedingWorkLocked()
	x.mu.Unlock()
	return g.ID, nil
}

// Subscribe streams snapshots of the active generation's job.
func (x *Indexer) Subscribe(ctx context.Context) <-chan contract.Progress {
	return x.actStream.subscribe(ctx)
}

// SubscribeRebuild streams snapshots of the building generation. It is quiet when no rebuild runs, and its
// last value, the one that finishes the rebuild, is terminal.
func (x *Indexer) SubscribeRebuild(ctx context.Context) <-chan contract.Progress {
	return x.rebStream.subscribe(ctx)
}

// AddFolder saves a new watched folder, starts its watcher and scans it. The watcher runs until the lifetime
// ends or RemoveFolder is called.
func (x *Indexer) AddFolder(ctx context.Context, f *contract.Folder) error {
	if err := x.live(); err != nil {
		return err
	}
	if err := x.store.SaveFolder(ctx, f); err != nil {
		return err
	}
	x.mu.Lock()
	x.folders[f.ID] = &folderState{f: *f}
	x.mu.Unlock()
	werr := x.watchFolder(*f)
	if err := x.scanFolder(ctx, *f); err != nil {
		return err
	}
	return werr
}

// RemoveFolder stops watching a folder, drops its files from the queues, and deletes it from the store.
func (x *Indexer) RemoveFolder(ctx context.Context, id int64) error {
	if err := x.live(); err != nil {
		return err
	}
	x.mu.Lock()
	fs, ok := x.folders[id]
	if !ok {
		x.mu.Unlock()
		return contract.ErrUnknownFolder
	}
	delete(x.folders, id)
	if fs.cancel != nil {
		fs.cancel()
	}
	for fid, f := range x.files {
		if f.FolderID == id {
			delete(x.files, fid)
			if x.active != nil {
				delete(x.active.rows, fid)
			}
			if x.rebuild != nil {
				delete(x.rebuild.rows, fid)
			}
		}
	}
	x.mu.Unlock()
	return x.store.DeleteFolder(ctx, id)
}

// watchFolder starts the watcher of fo. Its changes trigger a scan of the folder.
func (x *Indexer) watchFolder(fo contract.Folder) error {
	x.mu.Lock()
	life := x.life
	x.mu.Unlock()
	ctx, cancel := context.WithCancel(life)
	err := watch.Watch(ctx, fo.Path, func(p string, isDir bool) bool { return excludedPath(fo, p, isDir) },
		func() {
			x.mu.Lock()
			fs, ok := x.folders[fo.ID]
			x.mu.Unlock()
			if ok {
				_ = x.scanFolder(life, fs.f)
			}
		})
	if err != nil {
		cancel()
		return err
	}
	x.mu.Lock()
	if fs, ok := x.folders[fo.ID]; ok {
		fs.cancel = cancel
	} else {
		cancel()
	}
	x.mu.Unlock()
	return nil
}

// startup starts the watchers and scans every folder once (SPEC section 13, the startup rescan).
func (x *Indexer) startup(ctx context.Context) {
	x.mu.Lock()
	var list []contract.Folder
	for _, fs := range x.folders {
		list = append(list, fs.f)
	}
	x.mu.Unlock()
	sort.Slice(list, func(a, b int) bool { return list[a].ID < list[b].ID })
	for _, fo := range list {
		if ctx.Err() != nil {
			return
		}
		// A folder that cannot be watched or scanned (for example, it was removed) keeps its rows until the
		// user removes it; its error is shown when the Folders screen reads the store.
		_ = x.watchFolder(fo)
		_ = x.scanFolder(ctx, fo)
	}
}

// tick publishes the progress of both jobs every 250 ms, the SPEC section 11 maximum rate.
func (x *Indexer) tick(ctx context.Context) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			x.publish()
		}
	}
}

// publish sends a snapshot of each running job. The worker publishes the terminal rebuild value under the same
// lock, so the two never interleave.
func (x *Indexer) publish() {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.active != nil {
		x.actStream.publish(x.snapshotLocked(x.active), false)
	}
	if x.rebuild != nil {
		x.rebStream.publish(x.snapshotLocked(x.rebuild), false)
	}
}

// work is the worker loop: it embeds queued files, finishes rebuilds, and waits while paused or idle.
func (x *Indexer) work(ctx context.Context) {
	defer x.shutdown()
	for ctx.Err() == nil {
		x.mu.Lock()
		ch := x.wake
		if x.pauseReason() != contract.PauseNone {
			x.mu.Unlock()
			idle(ctx, ch)
			continue
		}
		j, id, ok := x.nextLocked()
		x.mu.Unlock()
		if ok {
			x.process(ctx, j, id)
			continue
		}
		if x.finishRebuild(ctx) {
			continue
		}
		idle(ctx, ch)
	}
}

func idle(ctx context.Context, ch chan struct{}) {
	select {
	case <-ch:
	case <-ctx.Done():
	}
}

// nextLocked pops the next file: the active job's queue first, then the rebuild's (SPEC section 11).
func (x *Indexer) nextLocked() (*job, int64, bool) {
	for _, j := range []*job{x.active, x.rebuild} {
		if j == nil {
			continue
		}
		if id, ok := j.pop(); ok {
			return j, id, true
		}
	}
	return nil, 0, false
}

// shutdown ends the streams and closes the sessions when the lifetime ends.
func (x *Indexer) shutdown() {
	x.mu.Lock()
	act, reb := x.active, x.rebuild
	x.mu.Unlock()
	x.actStream.close()
	x.rebStream.close()
	for _, j := range []*job{act, reb} {
		if j != nil && j.sess != nil {
			_ = j.sess.Close()
		}
	}
}

// finishRebuild queues the files that the building generation still lacks. When none is missing, it activates
// the generation, retires the old one, and publishes the terminal rebuild snapshot. It reports whether it did
// something, so the worker does not wait.
func (x *Indexer) finishRebuild(ctx context.Context) bool {
	x.mu.Lock()
	reb := x.rebuild
	if reb == nil || len(reb.queue) > 0 {
		x.mu.Unlock()
		return false
	}
	var missing []int64
	for _, id := range x.sortedFileIDsLocked() {
		var row *contract.FileIndex
		if r, has := reb.rows[id]; has {
			row = &r
		}
		if contract.NeedsWork(x.files[id], row) {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		for _, id := range missing {
			reb.push(id)
		}
		x.mu.Unlock()
		return true
	}
	gen := reb.gen
	x.mu.Unlock()

	err := x.activate(ctx, gen)
	if err != nil {
		if ctx.Err() != nil {
			return false
		}
		select { // ponytail: a fixed 500 ms retry; the store only refuses here when a file changed under us
		case <-ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
		return true
	}

	// Promote the rebuild job itself. Every file that still needs work in the new generation is queued again,
	// so work queued during activation (in the old job or the rebuild) is kept.
	x.mu.Lock()
	old := x.active
	p := x.snapshotLocked(reb)
	p.FilesDone = p.FilesTotal
	p.ETA = 0
	x.rebStream.publish(p, true)
	x.active = reb
	x.rebuild = nil
	reb.begun, reb.items, reb.tokens, reb.curr = time.Now(), 0, 0, ""
	for _, id := range x.sortedFileIDsLocked() {
		var row *contract.FileIndex
		if r, has := reb.rows[id]; has {
			row = &r
		}
		if contract.NeedsWork(x.files[id], row) {
			reb.push(id)
		}
	}
	x.mu.Unlock()

	if old != nil {
		if old.sess != nil {
			_ = old.sess.Close()
		}
		_ = x.store.DeleteItems(ctx, old.gen)
	}
	return true
}

// activate checks that gen is complete and makes it the active generation (SPEC section 10).
func (x *Indexer) activate(ctx context.Context, gen int64) error {
	ok, err := x.store.GenerationComplete(ctx, gen)
	if err != nil {
		return err
	}
	if !ok {
		return contract.ErrIncomplete
	}
	return x.store.ActivateGeneration(ctx, gen)
}
