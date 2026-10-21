package watch

import (
	"context"
	"encoding/binary"
	"io/fs"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const (
	quiet   = time.Second     // a burst fires this long after its last event (SPEC section 13)
	maxWait = 5 * time.Second // ponytail: a burst also fires after this, so a busy folder cannot starve the rescan
	mask    = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MODIFY | unix.IN_CLOSE_WRITE | unix.IN_MOVED_FROM |
		unix.IN_MOVED_TO | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_ATTRIB
	// parentMask watches the parent of the root, so that renaming or replacing the root is seen.
	parentMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO
)

// Watch watches root and every directory under it that skip does not exclude. skip gets an absolute path
// and whether it is a directory. onChange runs once per burst of events, quiet for one second, on the watch
// goroutine. Watch returns once the watches are set; the goroutine ends and closes its inotify descriptor when
// ctx ends.
func Watch(ctx context.Context, root string, skip func(path string, isDir bool) bool, onChange func()) error {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return err
	}
	w := &watcher{fd: fd, root: root, skip: skip, dirs: map[int32]string{}, parentWd: -1}
	if err := w.addTree(root); err != nil {
		unix.Close(fd)
		return err
	}
	if wd, err := unix.InotifyAddWatch(fd, filepath.Dir(root), parentMask); err == nil {
		w.parentWd = int32(wd)
	}
	go w.run(ctx, onChange)
	return nil
}

type watcher struct {
	fd       int
	root     string
	skip     func(string, bool) bool
	dirs     map[int32]string // watch descriptor to directory, at or below root
	parentWd int32            // watch on the parent of root, or -1
	dirty    bool             // events arrived that the next burst must report
	rebuild  bool             // the watches may be stale: the next burst watches the tree again
}

// addTree adds a watch to dir and to every directory below it that is not skipped. A directory that cannot
// be watched is skipped, but a failure on the root is an error.
func (w *watcher) addTree(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p != dir && w.skip != nil && w.skip(p, true) {
			return filepath.SkipDir
		}
		wd, err := unix.InotifyAddWatch(w.fd, p, mask)
		if err != nil {
			if p == dir {
				return err
			}
			return nil
		}
		w.dirs[int32(wd)] = p
		return nil
	})
}

// reconcile drops every watch below root and watches the tree again, when a burst found the watches stale. It
// runs before the burst's callback, so the scan that follows covers what was created while the watches were
// stale. A root that is missing now is watched when it appears (see parse).
func (w *watcher) reconcile() {
	if !w.rebuild {
		return
	}
	w.rebuild = false
	for wd := range w.dirs {
		unix.InotifyRmWatch(w.fd, uint32(wd))
	}
	w.dirs = map[int32]string{}
	_ = w.addTree(w.root)
}

func (w *watcher) run(ctx context.Context, onChange func()) {
	defer unix.Close(w.fd)
	buf := make([]byte, 64*1024)
	var first, last time.Time
	for ctx.Err() == nil {
		fds := []unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, 100) // poll so that ctx is checked at least every 100 ms
		if err != nil && err != unix.EINTR {
			return
		}
		now := time.Now()
		if n > 0 {
			if k, err := unix.Read(w.fd, buf); err == nil && k > 0 {
				was := w.dirty
				w.parse(buf[:k])
				if w.dirty {
					if !was {
						first = now
					}
					last = now
				}
			}
		}
		if w.dirty && (now.Sub(last) >= quiet || now.Sub(first) >= maxWait) {
			w.dirty = false
			w.reconcile()
			onChange()
		}
	}
}

// parse reads the inotify events in b. New directories get a watch, and a dropped watch is forgotten. An event
// that may leave the watches stale (a watched directory moved or deleted, a queue overflow, the root renamed or
// replaced) makes the next burst watch the tree again. In the parent of the root only events that name the root
// count, so unrelated siblings do not trigger scans.
func (w *watcher) parse(b []byte) {
	for off := 0; off+unix.SizeofInotifyEvent <= len(b); {
		wd := int32(binary.NativeEndian.Uint32(b[off:]))
		m := binary.NativeEndian.Uint32(b[off+4:])
		n := int(binary.NativeEndian.Uint32(b[off+12:]))
		name := ""
		if n > 0 {
			raw := b[off+unix.SizeofInotifyEvent : off+unix.SizeofInotifyEvent+n]
			for i, c := range raw {
				if c == 0 {
					raw = raw[:i]
					break
				}
			}
			name = string(raw)
		}
		off += unix.SizeofInotifyEvent + n
		if m&unix.IN_Q_OVERFLOW != 0 {
			w.dirty, w.rebuild = true, true
			continue
		}
		if wd == w.parentWd {
			if name == filepath.Base(w.root) {
				w.dirty, w.rebuild = true, true
			}
			continue
		}
		if m&unix.IN_IGNORED != 0 {
			delete(w.dirs, wd)
			continue
		}
		w.dirty = true
		if m&(unix.IN_MOVE_SELF|unix.IN_DELETE_SELF) != 0 {
			w.rebuild = true
		}
		if m&unix.IN_ISDIR != 0 && m&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0 && name != "" {
			if parent, ok := w.dirs[wd]; ok {
				_ = w.addTree(filepath.Join(parent, name)) // a directory that vanished already is not an error
			}
		}
	}
}
