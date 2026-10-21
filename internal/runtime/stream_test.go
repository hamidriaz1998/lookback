package runtime

import (
	"context"
	"testing"
	"time"
)

func TestStreamCloseUnblocksUnreadSubscriber(t *testing.T) {
	s := NewStream(func(string) string { return "run" }, func(v string) bool { return v == "done" })
	s.Publish("done")
	ch := s.Subscribe(context.Background())
	waitForBlockedPump(t, s)
	s.Close()
	waitForStreamClose(t, ch)
	if ch := s.Subscribe(context.Background()); !streamClosed(ch) {
		t.Fatal("subscription to a closed stream should close immediately")
	}
}

func TestStreamCancellationUnblocksUnreadSubscriber(t *testing.T) {
	s := NewStream(func(int) string { return "run" }, func(int) bool { return false })
	s.Publish(1)
	ctx, cancel := context.WithCancel(context.Background())
	ch := s.Subscribe(ctx)
	waitForBlockedPump(t, s)
	cancel()
	waitForStreamClose(t, ch)
	s.Close()
}

func TestStreamKeepsTerminalValueWhileLive(t *testing.T) {
	s := NewStream(func(string) string { return "run" }, func(v string) bool { return v == "done" })
	s.Publish("initial")
	ch := s.Subscribe(context.Background())
	waitForBlockedPump(t, s)
	s.Publish("done")
	s.Publish("progress")
	for _, want := range []string{"initial", "done", "progress"} {
		select {
		case got := <-ch:
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
	s.Close()
	waitForStreamClose(t, ch)
}

func waitForBlockedPump[T any](t *testing.T, s *Stream[T]) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		blocked := false
		for sub := range s.subs {
			if len(sub.queue) == 0 {
				blocked = true
				break
			}
		}
		s.mu.Unlock()
		if blocked {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("stream pump did not take the queued value")
}

func waitForStreamClose[T any](t *testing.T, ch <-chan T) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("stream channel did not close promptly")
		}
	}
}

func streamClosed[T any](ch <-chan T) bool {
	select {
	case _, ok := <-ch:
		return !ok
	default:
		return false
	}
}
