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
	spaceA     = fixtureSpace
	spaceB     = contract.VectorSpace{TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantQ8_0, ModelRev: modelRev}
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

// spaceState returns the state the status reports for space s, and whether s is open.
func spaceState(st contract.RuntimeStatus, s contract.VectorSpace) (contract.RuntimeState, bool) {
	for _, sp := range st.Spaces {
		if sp.Space == s {
			return sp.State, true
		}
	}
	return "", false
}

// A stop after Use leaves the sidecar stopped: the start Use began does not publish Ready (review finding 4).
func TestUseThenStopStaysStopped(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime(ctx)
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
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
	if _, _, err := sess.EmbedTexts(ctx, []string{"q"}); !errors.Is(err, contract.ErrNotReady) {
		t.Fatalf("embed after Stop: %v, want ErrNotReady", err)
	}
}

// Canceling the ctx of a Use that is waiting abandons its restart: the sidecar is stopped, not ready.
func TestUseCancelAbandonsRestart(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime(ctx)
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	useCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- r.Use(useCtx, cpuBackend) }()
	time.Sleep(transitionTime / 10)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Use after cancel: %v, want context.Canceled", err)
	}
	if _, _, err := sess.EmbedTexts(ctx, []string{"q"}); !errors.Is(err, contract.ErrNotReady) {
		t.Fatalf("embed after the abandoned restart: %v, want ErrNotReady", err)
	}
	time.Sleep(3 * transitionTime / 2)
	if st, _ := spaceState(r.Status(), spaceA); st != contract.RuntimeStopped {
		t.Fatalf("after cancel the state is %s, want stopped", st)
	}
}

// Competing transitions: a Use that a later Use supersedes returns ErrRestarting, the latest backend wins, and
// once the backend has switched to the CPU no start of the superseded backend publishes Ready (review finding 4).
func TestCompetingTransitionsKeepTheLatest(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime(ctx)
	if _, err := r.Session(ctx, spaceA); err != nil {
		t.Fatal(err)
	}
	events := r.Subscribe(ctx)
	first := make(chan error, 1)
	go func() { first <- r.Use(ctx, contract.BackendVulkan) }()
	deadline := time.Now().Add(time.Second)
	for r.Status().State != contract.RuntimeRestarting {
		if time.Now().After(deadline) {
			t.Fatal("the first Use never started a restart")
		}
		time.Sleep(time.Millisecond)
	}
	if err := r.Use(ctx, cpuBackend); err != nil {
		t.Fatal(err)
	}
	if err := <-first; !errors.Is(err, contract.ErrRestarting) {
		t.Fatalf("superseded Use: %v, want ErrRestarting", err)
	}
	waitState(t, r, contract.RuntimeReady)
	// Read the stream for a while after the switch: a late Ready of the superseded backend would show here.
	window := time.After(3 * transitionTime / 2)
	switched := false
	for done := false; !done; {
		select {
		case st, ok := <-events:
			if !ok {
				t.Fatal("stream closed")
			}
			if st.Backend == cpuBackend {
				switched = true
			}
			if switched && st.State == contract.RuntimeReady && st.Backend == contract.BackendVulkan {
				t.Fatal("the superseded start published Ready after the switch")
			}
		case <-window:
			done = true
		}
	}
	if !switched {
		t.Fatal("the switch to the CPU never reached the status stream")
	}
	if st := r.Status(); st.Backend != cpuBackend {
		t.Fatalf("final backend %s, want %s", st.Backend, cpuBackend)
	}
}

// Requests while the sidecar is not ready fail: ErrNotReady when stopped, ErrRestarting while a restart runs,
// and they work again once Use returns (review finding 4).
func TestRequestsWhileNotReady(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime(ctx)
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
	r := NewRuntime(ctx)
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
	r := NewRuntime(ctx)
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
	r := NewRuntime(ctx)
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

// A backend switch restarts the sidecars without changing their space, so the vectors that come back before
// and after the switch are the same (review finding 1, search side).
func TestBackendSwitchKeepsSpace(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime(ctx)
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
			if r.Backend() != cpuBackend {
				t.Fatalf("backend %s after the switch", r.Backend())
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

// A handover while the old search session is still open: the status lists both spaces with their own states,
// the old session keeps embedding in its space, and closing it leaves only the building space (review finding 1).
func TestHandoverWithOldSearchSession(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime(ctx)
	oldSearch, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	building, err := r.Session(ctx, spaceB)
	if err != nil {
		t.Fatal(err)
	}
	st := r.Status()
	if len(st.Spaces) != 2 || st.Spaces[0].Space != spaceA || st.Spaces[1].Space != spaceB || st.State != contract.RuntimeReady {
		t.Fatalf("status during the handover: %+v", st)
	}
	vecA, _, err := oldSearch.EmbedTexts(ctx, []string{"query"})
	if err != nil || oldSearch.Space() != spaceA {
		t.Fatalf("old search session after the handover: %v", err)
	}
	vecB, _, _ := building.EmbedTexts(ctx, []string{"query"})
	if reflect.DeepEqual(vecA, vecB) {
		t.Fatal("old and new spaces produced one vector")
	}
	_ = oldSearch.Close()
	st = r.Status()
	if len(st.Spaces) != 1 || st.Spaces[0].Space != spaceB {
		t.Fatalf("after the old session closed: %+v", st.Spaces)
	}
	_ = building.Close()
}

// Opening the building space while the active space's session is open: Status lists both spaces, each with its
// own state, while the building space starts (review finding 1).
func TestOpeningBuildingSpaceKeepsOwnStates(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime(ctx)
	active, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan contract.Session, 1)
	go func() {
		sess, err := r.Session(ctx, spaceB)
		if err != nil {
			t.Error(err)
		}
		opened <- sess
	}()
	time.Sleep(transitionTime / 4)
	st := r.Status()
	if state, ok := spaceState(st, spaceA); !ok || state != contract.RuntimeReady {
		t.Fatalf("active space: %s, open %v; want ready", state, ok)
	}
	if state, ok := spaceState(st, spaceB); !ok || state != contract.RuntimeStarting {
		t.Fatalf("building space: %s, open %v; want starting", state, ok)
	}
	if st.State != contract.RuntimeStarting {
		t.Fatalf("runtime state %s, want the worst state, starting", st.State)
	}
	building := <-opened
	if st := r.Status(); st.State != contract.RuntimeReady || len(st.Spaces) != 2 {
		t.Fatalf("after the building space opened: %+v", st)
	}
	_ = active.Close()
	_ = building.Close()
}

// Session returns only when its space is ready: a cold building space embeds at once, with no ErrRestarting.
func TestColdSpaceSessionIsReady(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime(ctx)
	start := time.Now()
	sess, err := r.Session(ctx, spaceB)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < transitionTime/2 {
		t.Fatal("Session returned before the cold sidecar could start")
	}
	if _, _, err := sess.EmbedTexts(ctx, []string{"query"}); err != nil {
		t.Fatalf("embed on a session returned by Session: %v", err)
	}
	_ = sess.Close()
}

// ctx bounds only the acquisition of each handle. Canceling one acquisition releases its reference, and the
// other handle, which holds the same cold space, keeps working (review finding 2).
func TestHandlesWithDifferentAcquisitionContexts(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime(ctx)
	cancelled, cancel := context.WithCancel(ctx)
	first := make(chan error, 1)
	go func() {
		_, err := r.Session(cancelled, spaceB)
		first <- err
	}()
	second := make(chan contract.Session, 1)
	secondErr := make(chan error, 1)
	go func() {
		sess, err := r.Session(ctx, spaceB)
		second <- sess
		secondErr <- err
	}()
	time.Sleep(transitionTime / 10)
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquisition: %v, want context.Canceled", err)
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("the other acquisition failed: %v", err)
	}
	sess := <-second
	if _, _, err := sess.EmbedTexts(ctx, []string{"query"}); err != nil {
		t.Fatalf("the other handle does not embed: %v", err)
	}
	if _, ok := spaceState(r.Status(), spaceB); !ok {
		t.Fatal("the space closed while another handle holds it")
	}
	_ = sess.Close()
	if _, ok := spaceState(r.Status(), spaceB); ok {
		t.Fatal("the space is still open after its last handle closed")
	}
}

// A canceled acquisition that is alone stops the sidecar it started, and it leaves no open space behind.
func TestCanceledAcquisitionStopsItsSidecar(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime(ctx)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.Session(canceled, spaceB); !errors.Is(err, context.Canceled) {
		t.Fatalf("Session with a done ctx: %v", err)
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, transitionTime/10)
	defer cancelWait()
	if _, err := r.Session(waitCtx, spaceB); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Session with a short ctx: %v, want DeadlineExceeded", err)
	}
	if st := r.Status(); len(st.Spaces) != 0 || st.State != contract.RuntimeStopped {
		t.Fatalf("a canceled acquisition left %+v open", st.Spaces)
	}
}

// Ready waits through a backend switch and returns once the sidecar is ready again (review finding 2).
func TestReadyAfterBackendSwitch(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime(ctx)
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Ready(ctx); err != nil {
		t.Fatalf("Ready on a ready session: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- r.Use(ctx, cpuBackend) }()
	time.Sleep(transitionTime / 10)
	if err := sess.Ready(ctx); err != nil {
		t.Fatalf("Ready after the switch: %v", err)
	}
	if st, _ := spaceState(r.Status(), spaceA); st != contract.RuntimeReady {
		t.Fatalf("Ready returned while the space is %s", st)
	}
	if err := <-done; err != nil {
		t.Fatalf("Use: %v", err)
	}
	if _, _, err := sess.EmbedTexts(ctx, []string{"q"}); err != nil {
		t.Fatalf("embed after Ready: %v", err)
	}
}

// Ready ends with ErrStopped when the runtime's lifetime ends, and with ctx's error when ctx ends first.
func TestReadyEndsWithLifetimeOrContext(t *testing.T) {
	bg := context.Background()
	life, endLife := context.WithCancel(bg)
	r := NewRuntime(life)
	sess, err := r.Session(bg, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(bg); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(bg, transitionTime/10)
	defer cancel()
	if err := sess.Ready(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Ready on a stopped sidecar with a short ctx: %v", err)
	}
	endLife()
	if err := sess.Ready(bg); !errors.Is(err, contract.ErrStopped) {
		t.Fatalf("Ready after the lifetime ended: %v, want ErrStopped", err)
	}
	if _, err := r.Session(bg, spaceB); !errors.Is(err, contract.ErrStopped) {
		t.Fatalf("Session after the lifetime ended: %v, want ErrStopped", err)
	}
}

// The status streams close when the lifetime ends, and Stop does not end the lifetime: a later Session starts
// the sidecar again (pending decision 2).
func TestLifetimeClosesStreamsAndStopKeepsIt(t *testing.T) {
	bg := context.Background()
	life, endLife := context.WithCancel(bg)
	r := NewRuntime(life)
	events := r.Subscribe(bg)
	if err := r.Stop(bg); err != nil {
		t.Fatal(err)
	}
	sess, err := r.Session(bg, spaceA)
	if err != nil {
		t.Fatalf("Session after Stop: %v", err)
	}
	_ = sess.Close()
	endLife()
	timeout := time.After(time.Second)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("the status stream did not close with the lifetime")
		}
	}
}

// The status of two spaces has one worst state: a fallback to the CPU shows on both spaces, and Retry starts them
// on the GPU again.
func TestFallbackAndRetryCoverEverySpace(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime(ctx)
	a, _ := r.Session(ctx, spaceA)
	b, _ := r.Session(ctx, spaceB)
	r.FallBackToCPU()
	st := r.Status()
	if st.State != contract.RuntimeCPUFallback || st.Backend != contract.BackendCPU {
		t.Fatalf("after the fallback: %s on %s", st.State, st.Backend)
	}
	for _, sp := range st.Spaces {
		if sp.State != contract.RuntimeCPUFallback || sp.Err == "" {
			t.Fatalf("space %v after the fallback: %s %q", sp.Space, sp.State, sp.Err)
		}
	}
	if err := r.RetryGPU(ctx); err != nil {
		t.Fatal(err)
	}
	waitState(t, r, contract.RuntimeReady)
	if r.Backend() != contract.BackendVulkan {
		t.Fatalf("backend after RetryGPU: %s", r.Backend())
	}
	_ = a.Close()
	_ = b.Close()
}
