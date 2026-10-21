//go:build linux

package watch

import (
	"encoding/binary"

	"golang.org/x/sys/unix"
)

func unixInit() (int, error) {
	return unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
}

func closeFD(fd int) { unix.Close(fd) }

// drain reads and discards every queued event.
func drain(fd int) {
	buf := make([]byte, 64*1024)
	for {
		if n, err := unix.Read(fd, buf); err != nil || n <= 0 {
			return
		}
	}
}

// overflowEvent is one inotify event with IN_Q_OVERFLOW and no name.
func overflowEvent() []byte {
	b := make([]byte, unix.SizeofInotifyEvent)
	binary.NativeEndian.PutUint32(b[0:], uint32(0xFFFFFFFF))
	binary.NativeEndian.PutUint32(b[4:], unix.IN_Q_OVERFLOW)
	return b
}
