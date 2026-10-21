package fake

import (
	"context"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// wait returns the next value of ch, or fails the test after one second.
func wait[T any](t *testing.T, ch <-chan T) (T, bool) {
	t.Helper()
	select {
	case v, ok := <-ch:
		return v, ok
	case <-time.After(time.Second):
		t.Fatal("no value within one second")
	}
	var zero T
	return zero, false
}

// A late subscriber first gets the current snapshot of every file (review finding 6).
func TestLateSubscriberGetsSnapshot(t *testing.T) {
	d := newDownloader()
	d.tick()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := d.Subscribe(ctx)
	got := map[string]contract.FileState{}
	for len(got) < len(d.order) {
		p, ok := wait(t, ch)
		if !ok {
			t.Fatal("channel closed before the snapshot")
		}
		got[p.Key] = p.State
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, key := range d.order {
		if want := d.state(d.files[key]); got[key] != want {
			t.Fatalf("snapshot of %s is %s, want %s", d.files[key].asset.Name, got[key], want)
		}
	}
}

// Two subscribers both get a terminal state, and a slow reader still gets the terminal state after many
// non-terminal updates (review finding 6).
func TestTerminalStateReachesEveryReader(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime(context.Background())
	fast := r.Subscribe(ctx)
	slow := r.Subscribe(ctx) // not read until the end
	for i := 0; i < 80; i++ {
		if err := r.Use(ctx, contract.BackendVulkan); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	for name, ch := range map[string]<-chan contract.RuntimeStatus{"fast": fast, "slow": slow} {
		seen := false
		for !seen {
			st, ok := wait(t, ch)
			if !ok {
				t.Fatalf("%s reader: channel closed before Stopped", name)
			}
			seen = st.State == contract.RuntimeStopped
		}
	}
}

// Canceling the subscriber's context closes its channel.
func TestSubscriptionClosesWithItsContext(t *testing.T) {
	r := NewRuntime(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	ch := r.Subscribe(ctx)
	if _, ok := wait(t, ch); !ok {
		t.Fatal("no snapshot before cancel")
	}
	cancel()
	for {
		if _, ok := wait(t, ch); !ok {
			return
		}
	}
}

type streamTestEvent struct {
	key   string
	value string
	done  bool
}

func TestLiveTerminalEventsAreRetained(t *testing.T) {
	s := newStream(func(e streamTestEvent) string { return e.key }, func(e streamTestEvent) bool { return e.done })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := s.subscribe(ctx)
	s.publish(streamTestEvent{key: "a", value: "done-a", done: true})
	s.publish(streamTestEvent{key: "b", value: "failed-b", done: true})
	got := map[string]string{}
	for len(got) < 2 {
		e, ok := wait(t, ch)
		if !ok {
			t.Fatal("stream closed before both live terminal events arrived")
		}
		if !e.done {
			t.Fatalf("got non-terminal event while draining: %+v", e)
		}
		got[e.key] = e.value
	}
	if got["a"] != "done-a" || got["b"] != "failed-b" {
		t.Fatalf("terminal events = %v", got)
	}
	s.close()
	if _, ok := wait(t, ch); ok {
		t.Fatal("channel remained open after close")
	}
}

func TestUnreadSubscriptionIsRemovedOnServiceClose(t *testing.T) {
	s := newStream(func(e streamTestEvent) string { return e.key }, func(e streamTestEvent) bool { return e.done })
	ctx := context.Background()
	ch := s.subscribe(ctx)
	s.publish(streamTestEvent{key: "a", value: "queued"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		var sub *subscriber[streamTestEvent]
		for current := range s.subs {
			sub = current
		}
		blocked := sub != nil && len(sub.queue) == 0
		s.mu.Unlock()
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pump did not block on the unread update")
		}
		time.Sleep(time.Millisecond)
	}
	s.publish(streamTestEvent{key: "a", value: "terminal", done: true})
	s.mu.Lock()
	queuedTerminal := false
	for sub := range s.subs {
		for _, e := range sub.queue {
			queuedTerminal = queuedTerminal || e.v.done
		}
	}
	s.mu.Unlock()
	if !queuedTerminal {
		t.Fatal("terminal update was not retained while the stream was live")
	}
	s.close()
	for {
		s.mu.Lock()
		n := len(s.subs)
		s.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pump remained after service close")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("unread channel delivered after it closed")
		}
	case <-time.After(time.Second):
		t.Fatal("unread subscription channel did not close")
	}
}
