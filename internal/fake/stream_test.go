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
	r := NewRuntime()
	fast := r.Subscribe(ctx)
	slow := r.Subscribe(ctx) // not read until the end
	for i := 0; i < 80; i++ {
		if err := r.Use(ctx, vulkanBF16Q8); err != nil {
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
	r := NewRuntime()
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
