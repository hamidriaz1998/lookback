package fake

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

var (
	spaceA = fixtureSpace
	spaceB = contract.VectorSpace{TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantQ8_0, ModelRev: modelRev}
	cpuBackend = contract.BackendCPU
)

// waitState polls until the runtime reports state, or fails the test after a second.
func waitState(t *testing.T, r *Runtime, state contract.RuntimeState) contract.RuntimeStatus {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if st := r.Status(); st.State == state {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("runtime state is %s, want %s", r.Status().State, state)
	return contract.RuntimeStatus{}
}

// Use then Stop leaves the sidecar stopped: the pending start does not publish Ready (review finding 4).
func TestUseThenStopStaysStopped(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime()
	if err := r.Use(ctx, cpuBackend); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * transitionTime / 2)
	if st := r.Status(); st.State != contract.RuntimeStopped {
		t.Fatalf("after Stop the state is %s, want stopped", st.State)
	}
}

// Canceling the context of Use abandons its start, so the sidecar does not become ready.
func TestUseThenCancelAbandonsStart(t *testing.T) {
	r := NewRuntime()
	ctx, cancel := context.WithCancel(context.Background())
	if err := r.Use(ctx, cpuBackend); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitState(t, r, contract.RuntimeStopped)
	time.Sleep(3 * transitionTime / 2)
	if st := r.Status(); st.State != contract.RuntimeStopped {
		t.Fatalf("after cancel the state is %s, want stopped", st.State)
	}
}

// Competing transitions: a superseded start never publishes Ready, and the latest one wins.
func TestCompetingTransitionsKeepTheLatest(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime()
	events := r.Subscribe(ctx)
	if err := r.Use(ctx, contract.BackendVulkan); err != nil {
		t.Fatal(err)
	}
	if err := r.Use(ctx, cpuBackend); err != nil {
		t.Fatal(err)
	}
	waitState(t, r, contract.RuntimeReady)
	time.Sleep(3 * transitionTime / 2)
	if st := r.Status(); st.State != contract.RuntimeReady || st.Combo.Backend != cpuBackend {
		t.Fatalf("final status %s on %s, want ready on %s", st.State, st.Combo.ID(), cpuBackend)
	}
	timeout := time.After(time.Second)
	for {
		select {
		case st, ok := <-events:
			if !ok {
				t.Fatal("stream closed")
			}
			if st.State == contract.RuntimeReady && st.Combo.Backend == contract.BackendVulkan {
				t.Fatal("the superseded start published Ready")
			}
		case <-timeout:
			return
		}
	}
}

// Requests while the sidecar is not ready fail: ErrNotReady when stopped, ErrRestarting while a restart
// runs, and they work again once Use returns (review finding 4).
func TestRequestsWhileNotReady(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime()
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sess.EmbedTexts(ctx, []string{"q"}); !errors.Is(err, contract.ErrNotReady) {
		t.Fatalf("embed while stopped: %v, want ErrNotReady", err)
	}
	if _, _, err := sess.EmbedImage(ctx, []byte("png"), "image/png"); !errors.Is(err, contract.ErrNotReady) {
		t.Fatalf("image while stopped: %v, want ErrNotReady", err)
	}
	if _, err := sess.Tokenize(ctx, "q"); !errors.Is(err, contract.ErrNotReady) {
		t.Fatalf("tokenize while stopped: %v, want ErrNotReady", err)
	}

	done := make(chan error, 1)
	go func() { done <- r.Use(ctx, cpuBackend) }()
	sawRestart := false
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Use: %v", err)
			}
			if !sawRestart {
				t.Fatal("no request saw the start in progress")
			}
			if _, _, err := sess.EmbedTexts(ctx, []string{"q"}); err != nil {
				t.Fatalf("embed after Use returned: %v", err)
			}
			return
		default:
		}
		if _, _, err := sess.EmbedTexts(ctx, []string{"q"}); errors.Is(err, contract.ErrRestarting) {
			sawRestart = true
		}
		time.Sleep(time.Millisecond)
	}
}

// Stop during a Use that waits for open sessions ends the wait with ErrRestarting, not a success.
func TestStopSupersedesWaitingUse(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime()
	if _, err := r.Session(ctx, spaceA); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- r.Use(ctx, cpuBackend) }()
	time.Sleep(20 * time.Millisecond)
	if err := r.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, contract.ErrRestarting) {
		t.Fatalf("Use superseded by Stop: %v, want ErrRestarting", err)
	}
}

// A session embeds in its own space: the same text gives different vectors in two spaces, and the same
// vector in one space, on any backend (review finding 1).
func TestSessionsHaveTheirOwnSpace(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime()
	a, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Session(ctx, spaceB)
	if err != nil {
		t.Fatal(err)
	}
	if a.Space() != spaceA || b.Space() != spaceB {
		t.Fatal("a session reports the wrong space")
	}
	va, _, _ := a.EmbedTexts(ctx, []string{"query"})
	vb, _, _ := b.EmbedTexts(ctx, []string{"query"})
	if reflect.DeepEqual(va, vb) {
		t.Fatal("two spaces produced the same vector for one input")
	}
}

// At most two spaces are open, and opening an open space shares its sidecar (review finding 1).
func TestThirdSpaceIsRefused(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime()
	a1, _ := r.Session(ctx, spaceA)
	a2, _ := r.Session(ctx, spaceA)
	b, err := r.Session(ctx, spaceB)
	if err != nil {
		t.Fatal(err)
	}
	third := contract.VectorSpace{TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantBF16, ModelRev: modelRev}
	if _, err := r.Session(ctx, third); !errors.Is(err, contract.ErrTooManySpaces) {
		t.Fatalf("third space: %v, want ErrTooManySpaces", err)
	}
	a3, err := r.Session(ctx, spaceA) // an open space shares its sidecar, so it is not a third
	if err != nil {
		t.Fatalf("an open space refused: %v", err)
	}
	_ = a1.Close()
	_ = a2.Close()
	_ = a2.Close() // closing twice changes nothing
	if _, err := r.Session(ctx, third); !errors.Is(err, contract.ErrTooManySpaces) {
		t.Fatalf("space A closed while a session still holds it: %v", err)
	}
	_ = a3.Close()
	if _, err := r.Session(ctx, third); err != nil {
		t.Fatalf("third space after space A closed: %v", err)
	}
	_ = b.Close()
}

// A backend switch restarts the sidecars without changing their space, so the vectors that come back
// before and after the switch are the same (review finding 1, search side).
func TestBackendSwitchKeepsSpace(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime()
	s, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	before, _, _ := s.EmbedTexts(ctx, []string{"query"})
	other, _ := r.Session(ctx, spaceB)
	otherVec, _, _ := other.EmbedTexts(ctx, []string{"query"})

	done := make(chan error, 1)
	go func() { done <- r.Use(ctx, cpuBackend) }()
	restarts := 0
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			if restarts == 0 {
				t.Fatal("no request overlapped the switch")
			}
			if r.Active().Backend != cpuBackend {
				t.Fatalf("active %s after the switch", r.Active().ID())
			}
			after, _, err := s.EmbedTexts(ctx, []string{"query"})
			if err != nil || !reflect.DeepEqual(after, before) || reflect.DeepEqual(after, otherVec) {
				t.Fatalf("vector after the switch changed space: err %v", err)
			}
			return
		default:
		}
		vec, _, err := s.EmbedTexts(ctx, []string{"query"})
		switch {
		case errors.Is(err, contract.ErrRestarting):
			restarts++
		case err != nil:
			t.Fatalf("embed during the switch: %v", err)
		case !reflect.DeepEqual(vec, before):
			t.Fatal("a vector during the switch is not from the session's space")
		}
		time.Sleep(time.Millisecond)
	}
}
