package fake

import (
	"context"
	"sync"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

const (
	fakeBytesPerSec = 4.1e6 // the speed the UI guide shows
	resumeBannerFor = 5 * time.Second
	downloadTick    = 250 * time.Millisecond
)

// download is one file's state in the fake downloader. Its phase is its progress: queued, downloading,
// verifying, done or failed. The user's pause and the connection are separate flags, and the visible
// state is derived from both (see state).
type download struct {
	asset       contract.Asset
	phase       contract.FileState
	userPaused  bool // paused by the user, per file or with PauseAll; only ResumeAll or Resume clears it
	done        int64
	resumedFrom int64
	resumedAt   time.Time
	err         string
}

// Downloader is a contract.Downloader that moves bytes on a timer. Its fixture is the reference
// laptop's Vulkan BF16/BF16 install: everything is verified except the BF16 encoder, which is at
// 612 MB of 982 MB after a restart.
//
// Pause and connection are independent. The user's pause is kept when the connection changes: a file
// the user paused stays paused after the network comes back. While offline the display shows offline,
// and a paused file shows paused again once it is back online.
type Downloader struct {
	mu     sync.Mutex
	files  map[string]*download
	order  []string
	ch     chan contract.DownloadProgress
	paused bool // PauseAll is in effect: files ensured or retried now start paused
	off    bool // the connection is down
}

// NewDownloader returns the fixture downloader. It stops when ctx ends.
func NewDownloader(ctx context.Context) *Downloader {
	d := newDownloader()
	go d.run(ctx)
	return d
}

// newDownloader returns the fixture downloader without its timer, so tests can call tick themselves.
func newDownloader() *Downloader {
	d := &Downloader{files: map[string]*download{}, ch: make(chan contract.DownloadProgress, 256)}
	now := time.Now()
	for _, a := range mustAssets(contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16}) {
		df := &download{asset: a, phase: contract.FileDone, done: a.Bytes}
		if a.Name == "mmproj-embeddinggemma-2-BF16.gguf" {
			df.phase = contract.FileDownloading
			df.done = 612 * mb
			df.resumedFrom = 612 * mb
			df.resumedAt = now
		}
		d.add(df)
	}
	return d
}

// mustAssets returns the catalog's assets for c on Linux. The fixture combination always exists.
func mustAssets(c contract.Combo) []contract.Asset {
	a, err := NewCatalog().Assets("linux", c)
	if err != nil {
		panic(err)
	}
	return a
}

// add stores a download, keeping the order of insertion.
func (d *Downloader) add(df *download) {
	d.files[df.asset.Key()] = df
	d.order = append(d.order, df.asset.Key())
}

// state returns the state the user sees for df. Done, verifying and failed files show their phase.
// While the connection is down, a downloading or queued file shows offline. Otherwise a user pause shows
// paused. The caller holds d.mu.
func (d *Downloader) state(df *download) contract.FileState {
	switch df.phase {
	case contract.FileDone, contract.FileVerifying, contract.FileFailed:
		return df.phase
	}
	if d.off {
		return contract.FileOffline
	}
	if df.userPaused {
		return contract.FilePaused
	}
	return df.phase
}

// runnable reports whether df moves bytes now: it is queued or downloading, the connection is up and the
// user has not paused it. The caller holds d.mu.
func (d *Downloader) runnable(df *download) bool {
	return !d.off && !df.userPaused && (df.phase == contract.FileQueued || df.phase == contract.FileDownloading)
}

// run advances every downloading file once per tick and publishes the changes.
func (d *Downloader) run(ctx context.Context) {
	ticker := time.NewTicker(downloadTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.tick()
		}
	}
}

// tick moves the files on by one tick and publishes each file.
func (d *Downloader) tick() {
	d.mu.Lock()
	defer d.mu.Unlock()
	step := int64(fakeBytesPerSec * downloadTick.Seconds())
	for _, name := range d.order {
		df := d.files[name]
		if d.runnable(df) {
			switch df.phase {
			case contract.FileQueued:
				df.phase = contract.FileDownloading
			case contract.FileDownloading:
				df.done += step
				if df.done >= df.asset.Bytes {
					df.done = df.asset.Bytes
					df.phase = contract.FileVerifying
				}
			}
		} else if df.phase == contract.FileVerifying {
			df.phase = contract.FileDone
		}
		if !df.resumedAt.IsZero() && time.Since(df.resumedAt) > resumeBannerFor {
			df.resumedFrom, df.resumedAt = 0, time.Time{}
		}
		d.publishLocked(df)
	}
}

// publishLocked sends an update for df. It drops the update when 256 are unread. The caller holds d.mu.
func (d *Downloader) publishLocked(df *download) {
	st := d.state(df)
	speed := 0.0
	if st == contract.FileDownloading {
		speed = fakeBytesPerSec
	}
	select {
	case d.ch <- contract.DownloadProgress{
		Key: df.asset.Key(), Name: df.asset.Name, State: st, Done: df.done, Total: df.asset.Bytes,
		BytesPerSec: speed, ResumedFrom: df.resumedFrom, Err: df.err,
	}:
	default:
	}
}

// Ensure queues the assets that are not verified on disk. It checks every asset before it queues any, so a
// conflict queues nothing. A tracked key with the same descriptor is left as it is. A new file starts
// paused while PauseAll is in effect.
func (d *Downloader) Ensure(ctx context.Context, assets []contract.Asset) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, a := range assets {
		if df, ok := d.files[a.Key()]; ok && df.asset != a {
			return contract.ErrConflict
		}
	}
	for _, a := range assets {
		if _, ok := d.files[a.Key()]; !ok {
			df := &download{asset: a, phase: contract.FileQueued, userPaused: d.paused}
			d.add(df)
			d.publishLocked(df)
		}
	}
	return nil
}

// Pause pauses one file. A queued or downloading file pauses whatever the connection is.
func (d *Downloader) Pause(key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	df, ok := d.files[key]
	if !ok {
		return contract.ErrUnknownAsset
	}
	if df.phase == contract.FileDownloading || df.phase == contract.FileQueued {
		df.userPaused = true
		d.publishLocked(df)
	}
	return nil
}

// Resume resumes one paused file from the bytes it has, and shows "Resumed at" for a few seconds. While the
// connection is down the file shows offline and moves once it is back.
func (d *Downloader) Resume(key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	df, ok := d.files[key]
	if !ok {
		return contract.ErrUnknownAsset
	}
	if df.userPaused {
		d.resumeLocked(df)
		d.publishLocked(df)
	}
	return nil
}

// resumeLocked clears the user's pause on df and starts the resume banner. The caller holds d.mu.
func (d *Downloader) resumeLocked(df *download) {
	df.userPaused = false
	df.resumedFrom, df.resumedAt = df.done, time.Now()
}

// PauseAll pauses every file that is queued or downloading, and files ensured or retried from now on.
func (d *Downloader) PauseAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paused = true
	for _, name := range d.order {
		df := d.files[name]
		if df.phase == contract.FileDownloading || df.phase == contract.FileQueued {
			df.userPaused = true
			d.publishLocked(df)
		}
	}
}

// ResumeAll clears every user pause, and PauseAll's hold on new files.
func (d *Downloader) ResumeAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paused = false
	for _, name := range d.order {
		df := d.files[name]
		if df.userPaused {
			d.resumeLocked(df)
			d.publishLocked(df)
		}
	}
}

// Retry queues a failed file again, from zero. The file starts paused while PauseAll is in effect.
func (d *Downloader) Retry(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	df, ok := d.files[key]
	if !ok {
		return contract.ErrUnknownAsset
	}
	if df.phase == contract.FileFailed {
		df.phase, df.done, df.err, df.userPaused = contract.FileQueued, 0, "", d.paused
		d.publishLocked(df)
	}
	return nil
}

// SetOffline marks the connection down or up. Unfinished files show offline while it is down. Coming back
// up does not clear a user's pause.
func (d *Downloader) SetOffline(off bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.off = off
	for _, name := range d.order {
		d.publishLocked(d.files[name])
	}
}

// Progress returns the stream of updates.
func (d *Downloader) Progress() <-chan contract.DownloadProgress { return d.ch }
