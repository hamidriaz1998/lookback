package watch

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

const (
	// filter is the set of changes that report a burst: names, sizes, times and attributes.
	filter = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME |
		windows.FILE_NOTIFY_CHANGE_SIZE | windows.FILE_NOTIFY_CHANGE_LAST_WRITE |
		windows.FILE_NOTIFY_CHANGE_ATTRIBUTES | windows.FILE_NOTIFY_CHANGE_CREATION
	shareAll       = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE
	readAttributes = 0x0080 // FILE_READ_ATTRIBUTES
	retryEvery     = 200 * time.Millisecond
	bufSize        = 64 * 1024
)

// fileID identifies a directory by volume and file index, so that a directory replaced at the same path is
// told apart from the one that was watched.
type fileID struct {
	vol, hi, lo uint32
}

// Watch watches root and every directory under it that skip does not exclude. skip gets an absolute path
// and whether it is a directory. onChange runs once per burst of events, quiet for one second, on the watch
// goroutine. Watch returns once the watch is set; the goroutine ends and releases its handles when ctx ends.
//
// The watch is a single recursive ReadDirectoryChangesW on the root, so directories created later are covered
// without extra handles. Skipped paths are dropped per event instead. If the root is renamed, deleted or
// replaced, the handle is released and the root is opened again at the same path once it exists, and each
// of those steps makes a burst.
func Watch(ctx context.Context, root string, skip func(path string, isDir bool) bool, onChange func()) error {
	return watchWithBuffer(ctx, root, skip, onChange, bufSize)
}

// watchWithBuffer is Watch with the size of the read buffer given, so that a test can overflow a small one.
func watchWithBuffer(ctx context.Context, root string, skip func(path string, isDir bool) bool, onChange func(), size int) error {
	ev, err := windows.CreateEvent(nil, 1, 0, nil) // manual reset: ResetEvent before each read
	if err != nil {
		return err
	}
	w := &watcher{root: root, skip: skip, ev: ev, h: windows.InvalidHandle, buf: make([]byte, size)}
	if err := w.open(); err != nil {
		windows.CloseHandle(ev)
		return err
	}
	go w.run(ctx, onChange)
	return nil
}

type watcher struct {
	root    string
	skip    func(string, bool) bool
	h       windows.Handle // the root directory, or InvalidHandle while the root is not watched
	id      fileID         // the identity of the root that h was opened on
	ev      windows.Handle
	ov      windows.Overlapped // kept in the heap-allocated watcher for the life of a pending read
	buf     []byte             // the one buffer the read fills; parsed before the next read is armed
	ret     uint32
	pending bool
	dirty   bool
	first   time.Time // the burst's first event
	last    time.Time // the burst's latest event
}

func (w *watcher) run(ctx context.Context, onChange func()) {
	defer w.stop()
	var nextCheck time.Time
	for ctx.Err() == nil {
		now := time.Now()
		if !now.Before(nextCheck) {
			nextCheck = now.Add(retryEvery)
			w.checkRoot(now)
		}
		if w.pending {
			if ev, _ := windows.WaitForSingleObject(w.ev, 100); ev == windows.WAIT_OBJECT_0 {
				w.complete(time.Now())
			}
		} else {
			time.Sleep(20 * time.Millisecond) // the root is not watched; checkRoot retries every retryEvery
		}
		now = time.Now()
		if w.dirty && (now.Sub(w.last) >= quiet || now.Sub(w.first) >= maxWait) {
			w.dirty = false
			onChange()
		}
	}
}

// checkRoot opens the root when it is not watched. When it is watched, it checks that the path still names the
// directory that is held: a rename or replacement at the path moves the held handle away from it.
func (w *watcher) checkRoot(now time.Time) {
	if w.h == windows.InvalidHandle {
		if w.open() == nil {
			w.markDirty(now) // the root was recreated; changes made while it was gone are missed
		}
		return
	}
	if id, err := pathID(w.root); err != nil || id != w.id {
		w.closeDir()
		w.markDirty(now)
	}
}

// open opens the root for a recursive watch and arms the first read.
func (w *watcher) open() error {
	p, err := windows.UTF16PtrFromString(w.root)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY, shareAll, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		windows.CloseHandle(h)
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		windows.CloseHandle(h)
		return windows.ERROR_DIRECTORY
	}
	w.h = h
	w.id = fileID{info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow}
	if err := w.arm(); err != nil {
		w.closeDir()
		return err
	}
	return nil
}

// arm starts an overlapped read into w.buf. ERROR_IO_PENDING is the normal outcome.
func (w *watcher) arm() error {
	windows.ResetEvent(w.ev)
	w.ov = windows.Overlapped{HEvent: w.ev}
	err := windows.ReadDirectoryChanges(w.h, &w.buf[0], uint32(len(w.buf)), true, filter, &w.ret, &w.ov, 0)
	if err != nil && err != windows.ERROR_IO_PENDING {
		return err
	}
	w.pending = true
	return nil
}

// complete collects the finished read, parses it, and arms the next one. A failed read means the root is gone,
// and the handle is released so that checkRoot opens the root again later.
func (w *watcher) complete(now time.Time) {
	w.pending = false
	var n uint32
	err := windows.GetOverlappedResult(w.h, &w.ov, &n, false)
	switch {
	case err == windows.ERROR_NOTIFY_ENUM_DIR:
		w.markDirty(now) // the buffer overflowed, so events were lost
	case err != nil:
		w.closeDir()
		w.markDirty(now)
		return
	case n == 0:
		w.markDirty(now) // an empty completion is the overflow case too
	default:
		w.parse(w.buf[:n], now)
	}
	if err := w.arm(); err != nil {
		w.closeDir()
		w.markDirty(now)
	}
}

// closeDir cancels a pending read, waits for it to finish, and closes the root handle. The buffer and the
// Overlapped are not reused until the wait returns.
func (w *watcher) closeDir() {
	if w.h == windows.InvalidHandle {
		return
	}
	if w.pending {
		windows.CancelIoEx(w.h, &w.ov)
		var n uint32
		windows.GetOverlappedResult(w.h, &w.ov, &n, true)
		w.pending = false
	}
	windows.CloseHandle(w.h)
	w.h = windows.InvalidHandle
}

func (w *watcher) stop() {
	w.closeDir()
	windows.CloseHandle(w.ev)
}

func (w *watcher) markDirty(now time.Time) {
	if !w.dirty {
		w.dirty = true
		w.first = now
	}
	w.last = now
}

// parse reads the FILE_NOTIFY_INFORMATION records in b. Each record is a 12-byte header (next offset, action,
// name length in bytes) followed by a UTF-16 name relative to the root. A record that is not skipped marks the
// burst dirty.
func (w *watcher) parse(b []byte, now time.Time) {
	for off := 0; off+12 <= len(b); {
		next := int(binary.LittleEndian.Uint32(b[off:]))
		nameLen := int(binary.LittleEndian.Uint32(b[off+8:]))
		end := off + 12 + nameLen
		if end > len(b) {
			end = len(b)
		}
		u := make([]uint16, (end-off-12)/2)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(b[off+12+2*i:])
		}
		if name := string(utf16.Decode(u)); name != "" && !w.skipped(name) {
			w.markDirty(now)
		}
		if next == 0 {
			break
		}
		off += next
	}
}

// skipped reports whether the event at rel (relative to the root) is under a skipped directory, or is itself
// skipped. Each ancestor between the root and the path is asked with isDir true. The path itself is asked with
// whether it is a directory now; a deleted path counts as a file.
func (w *watcher) skipped(rel string) bool {
	if w.skip == nil {
		return false
	}
	parts := strings.Split(rel, `\`)
	p := w.root
	for _, part := range parts[:len(parts)-1] {
		p = filepath.Join(p, part)
		if w.skip(p, true) {
			return true
		}
	}
	p = filepath.Join(p, parts[len(parts)-1])
	isDir := false
	if fi, err := os.Stat(p); err == nil {
		isDir = fi.IsDir()
	}
	return w.skip(p, isDir)
}

// pathID returns the identity of the directory at path, or an error if there is no directory there.
func pathID(path string) (fileID, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fileID{}, err
	}
	h, err := windows.CreateFile(p, readAttributes, shareAll, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fileID{}, err
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return fileID{}, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fileID{}, windows.ERROR_DIRECTORY
	}
	return fileID{info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow}, nil
}
