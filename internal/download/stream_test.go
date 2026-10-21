package download

import (
	"context"
	"testing"
	"time"
)

type streamTestValue struct {
	key      string
	value    string
	terminal bool
}

func TestLiveTerminalEventsAreRetained(t *testing.T) {
	s := newStream(func(v streamTestValue) string { return v.key }, func(v streamTestValue) bool { return v.terminal })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := s.subscribe(ctx)
	s.publish(streamTestValue{key: "done", value: "done", terminal: true})
	s.publish(streamTestValue{key: "failed", value: "failed", terminal: true})
	seen := map[string]int{}
	deadline := time.After(time.Second)
	for seen["done"]+seen["failed"] < 2 {
		select {
		case v, ok := <-ch:
			if !ok {
				t.Fatal("stream closed before live terminal events arrived")
			}
			if !v.terminal {
				continue
			}
			seen[v.key]++
		case <-deadline:
			t.Fatal("live terminal events were lost")
		}
	}
	if seen["done"] != 1 || seen["failed"] != 1 {
		t.Fatalf("terminal deliveries = %v", seen)
	}
	s.close()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("unexpected value after live terminal events")
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not close")
	}
}
