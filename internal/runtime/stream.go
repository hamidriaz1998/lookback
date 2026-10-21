package runtime

import (
	"context"
	"sync"
)

// Stream fans one service's updates out to its subscribers (contract: Streams). Each subscriber has its own
// channel. It starts with the current snapshot, one value per key, and then gets every later update in order.
// A reader that falls behind gets the newest value per key: an unread update is replaced by a newer one with
// the same key. A terminal value is not replaced while the stream and subscriber are live. Cancellation or stream
// shutdown may drop queued values so the subscriber's channel and pump can close promptly.
type Stream[T any] struct {
	mu       sync.Mutex
	key      func(T) string
	terminal func(T) bool
	keys     []string
	snap     map[string]T
	subs     map[*subscriber[T]]struct{}
	done     chan struct{}
	closed   bool
}

type subscriber[T any] struct {
	queue []entry[T]
	wake  chan struct{}
}

type entry[T any] struct {
	key string
	v   T
}

// NewStream returns a stream that keys each value with key and treats values that terminal reports as terminal.
func NewStream[T any](key func(T) string, terminal func(T) bool) *Stream[T] {
	return &Stream[T]{key: key, terminal: terminal, snap: map[string]T{}, subs: map[*subscriber[T]]struct{}{}, done: make(chan struct{})}
}

// Publish sends v to every subscriber. It does nothing after Close.
func (s *Stream[T]) Publish(v T) {
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

// Subscribe returns a channel that gets the snapshot and then the updates.
func (s *Stream[T]) Subscribe(ctx context.Context) <-chan T {
	ch := make(chan T)
	sub := &subscriber[T]{wake: make(chan struct{}, 1)}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		close(ch)
		return ch
	}
	for _, k := range s.keys {
		sub.queue = append(sub.queue, entry[T]{key: k, v: s.snap[k]})
	}
	s.subs[sub] = struct{}{}
	s.mu.Unlock()
	go s.pump(ctx, sub, ch)
	return ch
}

func (s *Stream[T]) pump(ctx context.Context, sub *subscriber[T], ch chan T) {
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

// Close ends the stream and interrupts pumps even when their subscribers are not reading.
func (s *Stream[T]) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.done)
}
