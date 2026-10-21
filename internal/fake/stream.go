package fake

import (
	"context"
	"sync"
)

// stream fans one service's updates out to its subscribers (contract: Streams). Each subscriber has its
// own channel. It starts with the current snapshot, one value per key, and then gets every later update
// in the order the state changed. A reader that falls behind gets the newest value per key: an unread
// update is replaced by a newer one with the same key. A terminal value is never replaced or dropped while
// the service and subscription are live. Pending values may be discarded when either stops.
type stream[T any] struct {
	mu       sync.Mutex
	key      func(T) string
	terminal func(T) bool
	keys     []string     // keys in first-seen order
	snap     map[string]T // the latest value per key
	subs     map[*subscriber[T]]struct{}
	closed   bool
	done     chan struct{}
}

// subscriber holds the values a reader has not taken yet, in order.
type subscriber[T any] struct {
	queue []entry[T]
	wake  chan struct{} // holds at most one signal that the queue grew
}

type entry[T any] struct {
	key string
	v   T
}

// newStream returns a stream that keys each value with key and treats values that terminal reports as
// terminal.
func newStream[T any](key func(T) string, terminal func(T) bool) *stream[T] {
	return &stream[T]{key: key, terminal: terminal, snap: map[string]T{}, subs: map[*subscriber[T]]struct{}{}, done: make(chan struct{})}
}

// publish sends v to every subscriber. It does nothing after close.
func (s *stream[T]) publish(v T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	k := s.key(v)
	if _, seen := s.snap[k]; !seen {
		s.keys = append(s.keys, k)
	}
	s.snap[k] = v
	for sub := range s.subs {
		sub.push(k, v, s.terminal)
	}
}

// push queues v for a subscriber. It removes an unread non-terminal value with the same key first. The
// caller holds the stream's lock.
func (sub *subscriber[T]) push(k string, v T, terminal func(T) bool) {
	kept := sub.queue[:0]
	for _, e := range sub.queue {
		if e.key == k && !terminal(e.v) {
			continue
		}
		kept = append(kept, e)
	}
	sub.queue = append(kept, entry[T]{key: k, v: v})
	select {
	case sub.wake <- struct{}{}:
	default:
	}
}

// subscribe returns a channel that gets the snapshot and then the updates while ctx and the service live.
// Stopping either may discard queued values so the channel can close promptly.
func (s *stream[T]) subscribe(ctx context.Context) <-chan T {
	ch := make(chan T)
	sub := &subscriber[T]{wake: make(chan struct{}, 1)}
	s.mu.Lock()
	for _, k := range s.keys {
		sub.queue = append(sub.queue, entry[T]{key: k, v: s.snap[k]})
	}
	if s.closed {
		close(ch)
	} else {
		s.subs[sub] = struct{}{}
		go s.pump(ctx, sub, ch)
	}
	s.mu.Unlock()
	return ch
}

// pump sends a subscriber's queue on ch until ctx or the service ends.
func (s *stream[T]) pump(ctx context.Context, sub *subscriber[T], ch chan T) {
	defer close(ch)
	defer func() {
		s.mu.Lock()
		delete(s.subs, sub)
		s.mu.Unlock()
	}()
	for {
		s.mu.Lock()
		if len(sub.queue) == 0 {
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			select {
			case <-sub.wake:
				continue
			case <-ctx.Done():
				return
			case <-s.done:
				return
			}
		}
		e := sub.queue[0]
		sub.queue = sub.queue[1:]
		s.mu.Unlock()
		select {
		case ch <- e.v:
		case <-ctx.Done():
			return
		case <-s.done:
			return
		}
	}
}

// close stops delivery pumps without waiting for subscribers to read.
func (s *stream[T]) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.done)
}
