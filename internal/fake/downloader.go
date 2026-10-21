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

// download is one file's state in the fake downloader.
type download struct {
	asset       contract.Asset
	state       contract.FileState
	done        int64
	resumedFrom int64
	resumedAt   time.Time
	err         string
	verifying   bool
}

// Downloader is a contract.Downloader that moves bytes on a timer. Its fixture is the reference
// laptop's Vulkan BF16/BF16 install: everything is verified except the BF16 encoder, which is at
// 612 MB of 982 MB after a restart.
type Downloader struct {
	mu     sync.Mutex
	files  map[string]*download
	order  []string
	ch     chan contract.DownloadProgress
	paused bool
	off    bool
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
		df := &download{asset: a, state: contract.FileDone, done: a.Bytes}
		if a.Name == "mmproj-embeddinggemma-2-BF16.gguf" {
			df.state = contract.FileDownloading
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

// tick moves the files on by one tick.
func (d *Downloader) tick() {
	d.mu.Lock()
	defer d.mu.Unlock()
	step := int64(fakeBytesPerSec * downloadTick.Seconds())
	for _, name := range d.order {
		df := d.files[name]
		switch df.state {
		case contract.FileQueued:
			df.state = contract.FileDownloading
		case contract.FileDownloading:
			if d.off {
				df.state = contract.FileOffline
				break
			}
			df.done += step
			if df.done >= df.asset.Bytes {
				df.done = df.asset.Bytes
				df.state = contract.FileVerifying
			}
		case contract.FileVerifying:
			df.state = contract.FileDone
		}
		if !df.resumedAt.IsZero() && time.Since(df.resumedAt) > resumeBannerFor {
			df.resumedFrom, df.resumedAt = 0, time.Time{}
		}
		d.publishLocked(df)
	}
}

// publishLocked sends an update for df. It drops the update when 256 are unread. The caller holds d.mu.
func (d *Downloader) publishLocked(df *download) {
	speed := 0.0
	if df.state == contract.FileDownloading {
		speed = fakeBytesPerSec
	}
	select {
	case d.ch <- contract.DownloadProgress{
		Key: df.asset.Key(), Name: df.asset.Name, State: df.state, Done: df.done, Total: df.asset.Bytes,
		BytesPerSec: speed, ResumedFrom: df.resumedFrom, Err: df.err,
	}:
	default:
	}
}

// Ensure queues the assets that are not verified on disk. It checks every asset before it queues any, so a
// conflict queues nothing. A tracked key with the same descriptor is left as it is.
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
			d.add(&download{asset: a, state: contract.FileQueued})
		}
	}
	return nil
}

// Pause pauses one file.
func (d *Downloader) Pause(key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	df, ok := d.files[key]
	if !ok {
		return contract.ErrUnknownAsset
	}
	if df.state == contract.FileDownloading || df.state == contract.FileQueued {
		df.state = contract.FilePaused
		d.publishLocked(df)
	}
	return nil
}

// Resume resumes one paused file from the bytes it has, and shows "Resumed at" for a few seconds.
func (d *Downloader) Resume(key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	df, ok := d.files[key]
	if !ok {
		return contract.ErrUnknownAsset
	}
	if df.state == contract.FilePaused {
		df.state = contract.FileDownloading
		df.resumedFrom, df.resumedAt = df.done, time.Now()
		d.publishLocked(df)
	}
	return nil
}

// PauseAll pauses every file that is queued or downloading.
func (d *Downloader) PauseAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paused = true
	for _, name := range d.order {
		df := d.files[name]
		if df.state == contract.FileDownloading || df.state == contract.FileQueued {
			df.state = contract.FilePaused
			d.publishLocked(df)
		}
	}
}

// ResumeAll resumes every paused file.
func (d *Downloader) ResumeAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paused = false
	for _, name := range d.order {
		df := d.files[name]
		if df.state == contract.FilePaused {
			df.state = contract.FileDownloading
			df.resumedFrom, df.resumedAt = df.done, time.Now()
			d.publishLocked(df)
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
	df, ok := d.files[key]
	if !ok {
		return contract.ErrUnknownAsset
	}
	if df.state == contract.FileFailed {
		df.state, df.done, df.err = contract.FileQueued, 0, ""
		d.publishLocked(df)
	}
	return nil
}

// SetOffline marks every unfinished file offline, or resumes them. The fake's network check uses it.
func (d *Downloader) SetOffline(off bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.off = off
	for _, name := range d.order {
		df := d.files[name]
		if df.state == contract.FileDownloading && off {
			df.state = contract.FileOffline
			d.publishLocked(df)
		}
		if df.state == contract.FileOffline && !off {
			df.state = contract.FileDownloading
			d.publishLocked(df)
		}
	}
}

// Progress returns the stream of updates.
func (d *Downloader) Progress() <-chan contract.DownloadProgress { return d.ch }
