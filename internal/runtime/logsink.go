package runtime

import (
	"os"
	"sync"
)

// logSink is the log file of one path, shared by every llama-server that writes to it. A child writes through a
// pipe into the sink, so the sink rotates the file at logRotateSize even while a child runs (SPEC section 8).
// The file stays open while a child uses the sink and closes when the last one is done, so that the folder can
// be deleted afterwards (Windows cannot delete an open file).
type logSink struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
	refs int // children that use the sink; guarded by logSinks
}

// logSinks holds one sink per path while it is in use.
var logSinks = struct {
	sync.Mutex
	m map[string]*logSink
}{m: map[string]*logSink{}}

// logSinkFor returns the shared sink of path, opened in append mode.
func logSinkFor(path string) (*logSink, error) {
	logSinks.Lock()
	defer logSinks.Unlock()
	if s := logSinks.m[path]; s != nil {
		s.refs++
		return s, nil
	}
	s := &logSink{path: path, refs: 1}
	if err := s.open(); err != nil {
		return nil, err
	}
	logSinks.m[path] = s
	return s, nil
}

// release ends one child's use of the sink. The last release closes the file. Call it once per logSinkFor, after
// the child has exited and its output is copied (cmd.Wait has returned), or when the child failed to start.
func (s *logSink) release() {
	logSinks.Lock()
	defer logSinks.Unlock()
	if s.refs--; s.refs > 0 {
		return
	}
	delete(logSinks.m, s.path)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.f.Close()
}

// open opens the file in append mode and reads its size.
func (s *logSink) open() error {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	s.f, s.size = f, fi.Size()
	return nil
}

// Write appends p, first moving the file to path.1 when p would take it past logRotateSize.
func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.size > 0 && s.size+int64(len(p)) > logRotateSize {
		s.f.Close()
		_ = os.Rename(s.path, s.path+".1") // on failure the file keeps appending, and the next write tries again
		if err := s.open(); err != nil {
			return 0, err
		}
	}
	n, err := s.f.Write(p)
	s.size += int64(n)
	return n, err
}
