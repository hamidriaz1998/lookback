package runtime

import (
	"os"
	"sync"
)

// logSink is the log file of one path, shared by every llama-server that writes to it. A child writes through a
// pipe into the sink, so the sink rotates the file at logRotateSize even while a child runs (SPEC section 8).
type logSink struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

// logSinks holds one sink per path. ponytail: sinks stay open for the process lifetime; a handful of paths.
var logSinks = struct {
	sync.Mutex
	m map[string]*logSink
}{m: map[string]*logSink{}}

// logSinkFor returns the shared sink of path, opened in append mode.
func logSinkFor(path string) (*logSink, error) {
	logSinks.Lock()
	defer logSinks.Unlock()
	if s := logSinks.m[path]; s != nil {
		return s, nil
	}
	s := &logSink{path: path}
	if err := s.open(); err != nil {
		return nil, err
	}
	logSinks.m[path] = s
	return s, nil
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
