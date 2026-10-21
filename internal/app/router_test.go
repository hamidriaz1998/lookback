package app

import (
	"reflect"
	"testing"
)

// A command that arrives before readiness must not run until open, then run in arrival order.
func TestRouterQueuesCommandsUntilOpen(t *testing.T) {
	var got []Options
	r := &commandRouter{act: func(o Options) { got = append(got, o) }}
	r.receive(Options{Toggle: true})
	r.receive(Options{})
	if len(got) != 0 {
		t.Fatalf("commands ran before the window existed: %v", got)
	}
	r.open()
	r.receive(Options{Toggle: true})
	want := []Options{{Toggle: true}, {}, {Toggle: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
