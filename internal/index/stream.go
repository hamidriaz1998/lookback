package index

import (
	"context"
	"sync"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// stream fans out snapshots to Subscribe readers (contract: Streams, and docs/ stream-policy: pending values may
// be discarded when a subscriber or the service ends). Each reader has its own pump goroutine. A non-terminal
// snapshot replaces an unread non-terminal one with the same generation. A terminal snapshot is never replaced
// or dropped while the service and the subscription are live. Every pump exits when its ctx or the stream ends,
// and closes its channel.
type stream struct {
	mu     sync.Mutex
	latest *contract.Progress // the newest non-terminal snapshot; new readers start with it
	subs   map[*subscriber]struct{}
	closed bool
	done   chan struct{} // closed when the stream ends
}

type subscriber struct {
	pending []entry
	wake    chan struct{} // holds at most one signal that pending grew
}

type entry struct {
	p        contract.Progress
	terminal bool
}

func newStream() *stream {
	return &stream{subs: map[*subscriber]struct{}{}, done: make(chan struct{})}
}

// subscribe returns a channel that starts with the newest snapshot and then carries later ones. It closes when
// ctx ends or the stream ends.
func (s *stream) subscribe(ctx context.Context) <-chan contract.Progress {
	out := make(chan contract.Progress)
	sub := &subscriber{wake: make(chan struct{}, 1)}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		close(out)
		return out
	}
	if s.latest != nil {
		sub.pending = append(sub.pending, entry{p: *s.latest})
	}
	s.subs[sub] = struct{}{}
	s.mu.Unlock()

	go func() {
		defer close(out)
		defer s.remove(sub)
		for {
			s.mu.Lock()
			if len(sub.pending) == 0 {
				s.mu.Unlock()
				select {
				case <-sub.wake:
					continue
				case <-ctx.Done():
					return
				case <-s.done:
					return
				}
			}
			e := sub.pending[0]
			sub.pending = sub.pending[1:]
			s.mu.Unlock()
			select {
			case out <- e.p:
			case <-ctx.Done():
				return
			case <-s.done:
				return
			}
		}
	}()
	return out
}

func (s *stream) remove(sub *subscriber) {
	s.mu.Lock()
	delete(s.subs, sub)
	s.mu.Unlock()
}

// publish sends p to every reader. A terminal p also clears the latest snapshot, so a reader that subscribes
// after the job ended gets nothing (the rebuild stream is quiet when no rebuild runs).
func (s *stream) publish(p contract.Progress, terminal bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if terminal {
		s.latest = nil
	} else {
		cp := p
		s.latest = &cp
	}
	for sub := range s.subs {
		sub.push(entry{p: p, terminal: terminal})
	}
}

// push queues e. An unread non-terminal entry of the same generation is replaced in place. The caller holds
// the stream lock.
func (sub *subscriber) push(e entry) {
	if !e.terminal {
		for i := range sub.pending {
			if sub.pending[i].terminal && sub.pending[i].p.Gen == e.p.Gen {
				return // the job of this generation has ended; its last value is already queued
			}
		}
		for i := range sub.pending {
			if !sub.pending[i].terminal && sub.pending[i].p.Gen == e.p.Gen {
				sub.pending[i] = e
				sub.signal()
				return
			}
		}
	}
	sub.pending = append(sub.pending, e)
	sub.signal()
}

func (sub *subscriber) signal() {
	select {
	case sub.wake <- struct{}{}:
	default:
	}
}

// close ends the stream: readers' pumps exit and their channels close. Pending values may be discarded.
func (s *stream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.latest = nil
	close(s.done)
	s.subs = map[*subscriber]struct{}{}
}
