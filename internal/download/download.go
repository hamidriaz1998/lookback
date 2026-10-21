// Package download fetches pinned files with resume (Range), SHA-256 verification, atomic rename,
// backoff and offline pause. It unpacks runtime archives into the runtimes folder and refuses paths
// that leave it (SPEC section 7).
package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

const (
	retryMin     = time.Second
	retryMax     = 60 * time.Second
	progressGap  = 250 * time.Millisecond // at most 4 updates per second per file
	resumeBanner = 5 * time.Second        // how long "Resumed at" stays visible
	copyBuf      = 128 << 10
	// idleTimeout cuts a connection that sends no bytes for this long; the cut counts as transient.
	idleTimeout = 30 * time.Second
)

// errTransient marks a failure that the transfer retries with backoff.
var errTransient = errors.New("transient network error")

// Config says where downloads go and how the app learns about the network.
type Config struct {
	Dir     string  // app data folder: downloads/, models/ and runtimes/ go under it
	Network Network // nil means the computer is always online
}

// Downloader is a contract.Downloader. Each file that moves bytes has one goroutine; the streams, the
// user's pause (per file and Pause all) and the connection are kept apart, and the visible state comes
// from all of them (see visibleLocked).
type Downloader struct {
	cfg       Config
	ctx       context.Context
	mu        sync.Mutex
	files     map[string]*file
	order     []string
	allPaused bool // PauseAll is in effect: files ensured or retried now start paused
	offline   bool // the connection is down
	stopped   bool // the lifetime ctx ended
	wg        sync.WaitGroup
	streams   *stream[contract.DownloadProgress]
}

var _ contract.Downloader = (*Downloader)(nil)

// file is one tracked asset. phase is queued, downloading, verifying, done or failed; pause and
// connection are not phases.
type file struct {
	asset       contract.Asset
	phase       contract.FileState
	userPaused  bool
	done        int64
	rate        float64
	lastHave    int64
	lastPub     time.Time
	resumedFrom int64
	resumedAt   time.Time
	err         string
	cancel      context.CancelFunc // non-nil while a goroutine runs this file
}

// New starts a downloader that stops when ctx ends. It resumes every download that a previous run left
// unfinished, so a crash or kill loses nothing (SPEC section 7).
func New(ctx context.Context, cfg Config) (*Downloader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, sub := range []string{"downloads", "models", "runtimes"} {
		if err := os.MkdirAll(filepath.Join(cfg.Dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	d := &Downloader{cfg: cfg, ctx: ctx, files: map[string]*file{}, streams: newStream(
		func(p contract.DownloadProgress) string { return p.Key },
		func(p contract.DownloadProgress) bool {
			return p.State == contract.FileDone || p.State == contract.FileFailed
		},
	)}
	if err := d.resumeFromDisk(); err != nil {
		return nil, err
	}
	d.offline = cfg.Network != nil && !cfg.Network.Online()
	d.mu.Lock()
	for _, k := range d.order {
		d.startLocked(d.files[k])
	}
	d.mu.Unlock()
	if cfg.Network != nil {
		cfg.Network.OnChanged(func(online bool) { d.setOffline(!online) })
	}
	go func() {
		<-ctx.Done()
		d.shutdown()
	}()
	return d, nil
}

// resumeFromDisk tracks every .part.json left by an earlier run. A download whose verified file is already
// installed only loses its leftovers.
func (d *Downloader) resumeFromDisk() error {
	metas, err := filepath.Glob(filepath.Join(d.cfg.Dir, "downloads", "*.part.json"))
	if err != nil {
		return err
	}
	for _, meta := range metas {
		var a contract.Asset
		if err := readJSON(meta, &a); err != nil || a.SHA256 == "" {
			// A meta file we cannot read is ours and stale: drop it, the part restarts from its bytes.
			os.Remove(meta)
			continue
		}
		if _, err := os.Stat(d.donePath(a)); err == nil {
			removeIfExists(d.partPath(a), meta)
			continue
		}
		d.add(&file{asset: a, phase: contract.FileQueued})
	}
	return nil
}

// Ensure queues the assets that are not yet verified on disk. A batch with a conflict queues nothing.
func (d *Downloader) Ensure(ctx context.Context, assets []contract.Asset) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return contract.ErrStopped
	}
	batch := map[string]contract.Asset{}
	for _, a := range assets {
		if a.SHA256 == "" {
			return fmt.Errorf("download: %s has no SHA-256 pin", a.Name)
		}
		if prev, ok := batch[a.Key()]; ok && prev != a {
			return contract.ErrConflict
		}
		batch[a.Key()] = a
		if f, ok := d.files[a.Key()]; ok && f.asset != a {
			return contract.ErrConflict
		}
	}
	for _, a := range assets {
		if _, ok := d.files[a.Key()]; ok {
			continue
		}
		f := &file{asset: a, phase: contract.FileQueued, userPaused: d.allPaused}
		if d.installed(a) {
			f.phase, f.done = contract.FileDone, a.Bytes
		}
		d.add(f)
		d.publishLocked(f)
		d.startLocked(f)
	}
	return nil
}

// Pause pauses one file by key. A file that is queued or downloading stops; other phases are left alone.
func (d *Downloader) Pause(key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	f, ok := d.files[key]
	if !ok {
		return contract.ErrUnknownAsset
	}
	if f.phase == contract.FileQueued || f.phase == contract.FileDownloading {
		f.userPaused = true
		d.stopLocked(f)
		d.publishLocked(f)
	}
	return nil
}

// Resume resumes one paused file from the bytes it has.
func (d *Downloader) Resume(key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	f, ok := d.files[key]
	if !ok {
		return contract.ErrUnknownAsset
	}
	if f.userPaused {
		f.userPaused = false
		d.startLocked(f)
		d.publishLocked(f)
	}
	return nil
}

// PauseAll pauses every queued or downloading file, and files ensured or retried from now on.
func (d *Downloader) PauseAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.allPaused = true
	for _, k := range d.order {
		if f := d.files[k]; f.phase == contract.FileQueued || f.phase == contract.FileDownloading {
			f.userPaused = true
			d.stopLocked(f)
			d.publishLocked(f)
		}
	}
}

// ResumeAll clears every user pause and PauseAll's hold on new files.
func (d *Downloader) ResumeAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.allPaused = false
	for _, k := range d.order {
		if f := d.files[k]; f.userPaused {
			f.userPaused = false
			d.startLocked(f)
			d.publishLocked(f)
		}
	}
}

// Retry queues a failed file again, from zero.
func (d *Downloader) Retry(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	f, ok := d.files[key]
	if !ok {
		return contract.ErrUnknownAsset
	}
	if f.phase != contract.FileFailed {
		return nil
	}
	f.phase, f.done, f.err, f.resumedFrom, f.resumedAt = contract.FileQueued, 0, "", 0, time.Time{}
	f.userPaused = d.allPaused
	d.publishLocked(f)
	d.startLocked(f)
	return nil
}

// Subscribe streams updates for every file, starting with the current state of each.
func (d *Downloader) Subscribe(ctx context.Context) <-chan contract.DownloadProgress {
	return d.streams.subscribe(ctx)
}

// setOffline records the connection state. Going offline stops every transfer; coming back online restarts
// the files the user has not paused.
func (d *Downloader) setOffline(off bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped || d.offline == off {
		return
	}
	d.offline = off
	for _, k := range d.order {
		f := d.files[k]
		if off {
			d.stopLocked(f)
		} else {
			d.startLocked(f)
		}
		d.publishLocked(f)
	}
}

// shutdown ends every transfer, waits for the goroutines, and closes the streams.
func (d *Downloader) shutdown() {
	d.mu.Lock()
	d.stopped = true
	for _, f := range d.files {
		if f.cancel != nil {
			f.cancel()
		}
	}
	d.mu.Unlock()
	d.wg.Wait()
	d.streams.close()
}

// add tracks f under its key.
func (d *Downloader) add(f *file) {
	d.files[f.asset.Key()] = f
	d.order = append(d.order, f.asset.Key())
}

// startLocked starts a goroutine for f when it is queued or downloading, the connection is up, the user has
// not paused it, and no goroutine runs it yet. The caller holds d.mu.
func (d *Downloader) startLocked(f *file) {
	if d.stopped || d.offline || f.userPaused || f.cancel != nil {
		return
	}
	if f.phase != contract.FileQueued && f.phase != contract.FileDownloading {
		return
	}
	ctx, cancel := context.WithCancel(d.ctx)
	f.cancel = cancel
	d.wg.Add(1)
	go d.run(ctx, f)
}

// stopLocked stops the goroutine of f. The goroutine clears f.cancel when it ends, and then starts again if
// the file is still wanted. The caller holds d.mu.
func (d *Downloader) stopLocked(f *file) {
	if f.cancel != nil {
		f.cancel()
	}
}

// visibleLocked returns the state the user sees: done, verifying and failed show their phase; otherwise the
// connection wins over the user's pause. The caller holds d.mu.
func (d *Downloader) visibleLocked(f *file) contract.FileState {
	switch f.phase {
	case contract.FileDone, contract.FileVerifying, contract.FileFailed:
		return f.phase
	}
	if d.offline {
		return contract.FileOffline
	}
	if f.userPaused {
		return contract.FilePaused
	}
	return f.phase
}

// publishLocked sends f's current state. The caller holds d.mu.
func (d *Downloader) publishLocked(f *file) {
	st := d.visibleLocked(f)
	speed := 0.0
	if st == contract.FileDownloading {
		speed = f.rate
	}
	d.streams.publish(contract.DownloadProgress{
		Key: f.asset.Key(), Name: f.asset.Name, State: st, Done: f.done, Total: f.asset.Bytes,
		BytesPerSec: speed, ResumedFrom: f.resumedFrom, Err: f.err,
	})
}

// setPhase changes f's phase and publishes it.
func (d *Downloader) setPhase(f *file, phase contract.FileState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f.phase = phase
	d.publishLocked(f)
}

// progress records that have bytes are on disk and publishes at most every progressGap.
func (d *Downloader) progress(f *file, have int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f.done = have
	now := time.Now()
	if now.Sub(f.lastPub) < progressGap {
		return
	}
	if !f.lastPub.IsZero() {
		f.rate = float64(have-f.lastHave) / now.Sub(f.lastPub).Seconds()
	}
	f.lastHave, f.lastPub = have, now
	if !f.resumedAt.IsZero() && now.Sub(f.resumedAt) > resumeBanner {
		f.resumedFrom, f.resumedAt = 0, time.Time{}
	}
	d.publishLocked(f)
}

// run downloads, verifies and installs f, then records the outcome. A goroutine that a pause, the connection
// or the shutdown stopped records nothing: the controller decides what comes next.
func (d *Downloader) run(ctx context.Context, f *file) {
	defer d.wg.Done()
	err := d.fetch(ctx, f)
	d.mu.Lock()
	defer d.mu.Unlock()
	stopped := ctx.Err() != nil // read before cancel, which would make it non-nil
	cancel := f.cancel
	f.cancel = nil
	cancel()
	switch {
	case err == nil:
		f.phase, f.done, f.err = contract.FileDone, f.asset.Bytes, ""
	case stopped:
		// Stopped on purpose; startLocked below restarts the file if it is still wanted.
	default:
		f.phase, f.err = contract.FileFailed, err.Error()
		removeIfExists(d.partPath(f.asset), d.partPath(f.asset)+".json")
	}
	d.publishLocked(f)
	d.startLocked(f)
}

// fetch runs the transfer and verification. A checksum mismatch deletes the part and retries once from zero.
func (d *Downloader) fetch(ctx context.Context, f *file) error {
	a := f.asset
	part := d.partPath(a)
	for mismatch := 1; ; mismatch++ {
		if err := d.transfer(ctx, f, part); err != nil {
			return err
		}
		d.setPhase(f, contract.FileVerifying)
		ok, err := hashMatches(part, a.SHA256)
		if err != nil {
			return err
		}
		if ok {
			return d.install(a, part)
		}
		removeIfExists(part, part+".json")
		if mismatch == 2 {
			return fmt.Errorf("checksum mismatch: the file is not %s", a.SHA256)
		}
		d.mu.Lock()
		f.done, f.lastHave, f.lastPub = 0, 0, time.Time{}
		d.mu.Unlock()
	}
}

// transfer fills part to the pinned size. It writes the meta file before the first byte and retries
// transient errors with exponential backoff.
func (d *Downloader) transfer(ctx context.Context, f *file, part string) error {
	if err := writeJSON(part+".json", f.asset); err != nil {
		return err
	}
	backoff := retryMin
	for {
		err := d.attempt(ctx, f, part)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !errors.Is(err, errTransient) {
			return err
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff = min(2*backoff, retryMax)
	}
}

// attempt makes one request. It resumes from the bytes on disk with Range; a 200 answer means the server
// ignored Range, so the part restarts from zero.
func (d *Downloader) attempt(ctx context.Context, f *file, part string) error {
	a := f.asset
	size := fileSize(part)
	if size > a.Bytes {
		removeIfExists(part)
		size = 0
	}
	if size == a.Bytes {
		return nil
	}
	actx, acancel := context.WithCancel(ctx)
	defer acancel()
	idle := time.AfterFunc(idleTimeout, acancel)
	defer idle.Stop()
	req, err := http.NewRequestWithContext(actx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	if size > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(size, 10)+"-")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", errTransient, err)
	}
	defer resp.Body.Close()
	flags := os.O_WRONLY | os.O_CREATE
	switch {
	case resp.StatusCode == http.StatusPartialContent && size > 0:
	case resp.StatusCode == http.StatusOK:
		size = 0
		flags |= os.O_TRUNC
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		removeIfExists(part)
		return fmt.Errorf("%w: the server rejected the resume range", errTransient)
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("%w: HTTP %s", errTransient, resp.Status)
	default:
		return fmt.Errorf("download %s: HTTP %s", a.Name, resp.Status)
	}
	out, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	if _, err := out.Seek(size, io.SeekStart); err != nil {
		out.Close()
		return err
	}
	d.begin(f, size)
	n, copyErr := d.copy(f, out, resp.Body, size, func() { idle.Reset(idleTimeout) })
	closeErr := out.Close()
	have := size + n
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case copyErr != nil:
		return fmt.Errorf("%w: %w", errTransient, copyErr)
	case closeErr != nil:
		return closeErr
	case have < a.Bytes:
		return fmt.Errorf("%w: the connection ended at %d of %d bytes", errTransient, have, a.Bytes)
	case have > a.Bytes:
		removeIfExists(part)
		return fmt.Errorf("download %s: %d bytes, the catalog pins %d", a.Name, have, a.Bytes)
	}
	return nil
}

// begin publishes the start of a transfer that resumes at from bytes.
func (d *Downloader) begin(f *file, from int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f.phase, f.done, f.lastHave, f.lastPub = contract.FileDownloading, from, from, time.Time{}
	if from > 0 {
		f.resumedFrom, f.resumedAt = from, time.Now()
	}
	d.publishLocked(f)
}

// copy writes body to w and reports progress. It returns the bytes written.
func (d *Downloader) copy(f *file, w io.Writer, body io.Reader, start int64, touch func()) (int64, error) {
	buf := make([]byte, copyBuf)
	var n int64
	for {
		nr, rerr := body.Read(buf)
		touch()
		if nr > 0 {
			if _, err := w.Write(buf[:nr]); err != nil {
				return n, err
			}
			n += int64(nr)
			d.progress(f, start+n)
		}
		if rerr == io.EOF {
			return n, nil
		}
		if rerr != nil {
			return n, rerr
		}
	}
}

// install moves a verified part into place: a model file is renamed to models/, a runtime archive is
// unpacked into runtimes/. Then it writes the done marker and removes the part.
func (d *Downloader) install(a contract.Asset, part string) error {
	if a.Archive {
		if err := unpack(part, a.Name, filepath.Join(d.cfg.Dir, "runtimes", runtimeFolder(a))); err != nil {
			return err
		}
	} else if err := os.Rename(part, d.modelPath(a)); err != nil {
		return err
	}
	if err := writeJSON(d.donePath(a), a); err != nil {
		return err
	}
	removeIfExists(part, part+".json")
	return nil
}

// installed reports whether a is on disk: its done marker exists and, for a model, so does its file.
func (d *Downloader) installed(a contract.Asset) bool {
	if _, err := os.Stat(d.donePath(a)); err != nil {
		return false
	}
	return a.Archive || fileSize(d.modelPath(a)) >= 0
}

func (d *Downloader) partPath(a contract.Asset) string {
	return filepath.Join(d.cfg.Dir, "downloads", a.SHA256+".part")
}

func (d *Downloader) donePath(a contract.Asset) string {
	return filepath.Join(d.cfg.Dir, "downloads", a.SHA256+".done")
}

func (d *Downloader) modelPath(a contract.Asset) string {
	return filepath.Join(d.cfg.Dir, "models", a.Name)
}

// runtimeFolder names the folder of a runtime archive: its release tag and backend, such as b11755-vulkan.
func runtimeFolder(a contract.Asset) string {
	u, err := url.Parse(a.URL)
	tag := "unknown"
	if err == nil {
		tag = path.Base(path.Dir(u.Path))
	}
	return tag + "-" + string(a.Backend)
}

// hashMatches reports whether the SHA-256 of the file at p is want.
func hashMatches(p, want string) (bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	return strings.EqualFold(hex.EncodeToString(h.Sum(nil)), want), nil
}

// fileSize returns the size of p, or -1 when it does not exist.
func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return -1
	}
	return st.Size()
}

func removeIfExists(paths ...string) {
	for _, p := range paths {
		os.Remove(p)
	}
}

// writeJSON writes v to p atomically.
func writeJSON(p string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
