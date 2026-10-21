package fake

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// transitionTime is how long a sidecar takes to start or restart. The Models screen shows the transitions.
const transitionTime = 200 * time.Millisecond

// Runtime is a contract.Runtime that does not start a process. It keeps one open vector space per
// Session, at most two, and each space has its own deterministic vectors (see Session).
//
// Transitions are serialized under mu. Each starts a new epoch. A transition that is not finished when
// the next one starts, when Stop runs, or when its ctx ends, is abandoned: its completion does nothing.
type Runtime struct {
	mu      sync.Mutex
	status  contract.RuntimeStatus
	epoch   uint64             // increments on every transition; a request that began earlier fails with ErrRestarting
	cancel  context.CancelFunc // ends the pending transition, if any
	spaces  map[contract.VectorSpace]int
	changed chan struct{} // closed and replaced when the status changes
	streams *stream[contract.RuntimeStatus]
}

// NewRuntime returns a Runtime that is ready on the reference laptop's Vulkan combination.
func NewRuntime() *Runtime {
	r := &Runtime{
		status: contract.RuntimeStatus{
			State: contract.RuntimeReady,
			Combo: vulkanBF16Q8,
			Since: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC),
		},
		spaces:  map[contract.VectorSpace]int{},
		changed: make(chan struct{}),
		streams: newStream(
			func(contract.RuntimeStatus) string { return "" },
			func(st contract.RuntimeStatus) bool { return st.State == contract.RuntimeStopped || st.State == contract.RuntimeFailed },
		),
	}
	r.streams.publish(r.status)
	return r
}

// vulkanBF16Q8 is the recommended combination on the reference laptop.
var vulkanBF16Q8 = contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0}

// Active returns the combination of the sidecar.
func (r *Runtime) Active() contract.Combo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status.Combo
}

// Status returns the current status.
func (r *Runtime) Status() contract.RuntimeStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// Subscribe returns a stream of status changes.
func (r *Runtime) Subscribe(ctx context.Context) <-chan contract.RuntimeStatus {
	return r.streams.subscribe(ctx)
}

// setLocked stores st, publishes it and wakes the waiters. The caller holds r.mu.
func (r *Runtime) setLocked(st contract.RuntimeStatus) {
	r.status = st
	r.streams.publish(st)
	close(r.changed)
	r.changed = make(chan struct{})
}

// startLocked begins a transition that shows st, then becomes done after transitionTime. ctx bounds the
// transition: when it ends before done, the transition is abandoned and the sidecar is stopped. The caller
// holds r.mu.
func (r *Runtime) startLocked(ctx context.Context, st, done contract.RuntimeStatus) uint64 {
	r.epoch++
	ep := r.epoch
	if r.cancel != nil {
		r.cancel()
	}
	tctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.setLocked(st)
	timer := time.AfterFunc(transitionTime, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.epoch == ep && tctx.Err() == nil {
			done.Since = time.Now()
			r.setLocked(done)
			cancel()
		}
	})
	go func() {
		<-tctx.Done()
		timer.Stop()
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.epoch == ep && (r.status.State == contract.RuntimeStarting || r.status.State == contract.RuntimeRestarting) {
			r.setLocked(contract.RuntimeStatus{State: contract.RuntimeStopped, Combo: st.Combo, Message: "Start canceled", Since: time.Now()})
		}
	}()
	return ep
}

// stopLocked ends the pending transition and sets st. It is the only way a transition ends early. The
// caller holds r.mu.
func (r *Runtime) stopLocked(st contract.RuntimeStatus) {
	r.epoch++
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.setLocked(st)
}

// readyState reports whether a sidecar in state s can embed.
func readyState(s contract.RuntimeState) bool {
	return s == contract.RuntimeReady || s == contract.RuntimeCPUFallback
}

// Use makes c's backend the backend of every sidecar and restarts the open ones. It returns when every
// open session is ready again, or when ctx ends. With no session open it returns at once and the restart
// runs in the background. A Use that a later Use or Stop supersedes returns ErrRestarting, and Stop
// during the wait returns ErrStopped.
func (r *Runtime) Use(ctx context.Context, c contract.Combo) error {
	r.mu.Lock()
	if err := ctx.Err(); err != nil {
		r.mu.Unlock()
		return err
	}
	st := contract.RuntimeRestarting
	if r.status.State == contract.RuntimeStopped || r.status.State == contract.RuntimeFailed {
		st = contract.RuntimeStarting
	}
	ep := r.startLocked(ctx,
		contract.RuntimeStatus{State: st, Combo: c, Message: "Starting " + c.ID(), Since: time.Now()},
		contract.RuntimeStatus{State: contract.RuntimeReady, Combo: c},
	)
	open := len(r.spaces) > 0
	r.mu.Unlock()
	if !open {
		return nil
	}
	return r.await(ctx, ep)
}

// await waits until transition ep finishes, or returns the reason it did not.
func (r *Runtime) await(ctx context.Context, ep uint64) error {
	for {
		r.mu.Lock()
		if r.epoch != ep {
			r.mu.Unlock()
			return contract.ErrRestarting
		}
		st := r.status
		ch := r.changed
		r.mu.Unlock()
		switch {
		case readyState(st.State):
			return nil
		case st.State == contract.RuntimeStopped && ctx.Err() != nil:
			return ctx.Err()
		case st.State == contract.RuntimeStopped || st.State == contract.RuntimeFailed:
			return contract.ErrStopped
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// RetryGPU restarts the GPU backend after a CPU fallback. It is an error in any other state.
func (r *Runtime) RetryGPU(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.State != contract.RuntimeCPUFallback {
		return errors.New("runtime: not on the CPU fallback")
	}
	gpu := r.status.Combo
	gpu.Backend = contract.BackendVulkan
	r.startLocked(ctx,
		contract.RuntimeStatus{State: contract.RuntimeRestarting, Combo: gpu, Message: "Restarting Vulkan", Since: time.Now()},
		contract.RuntimeStatus{State: contract.RuntimeReady, Combo: gpu},
	)
	return nil
}

// FallBackToCPU moves the sidecar to the CPU build after two GPU failures (SPEC section 8, Q23).
// The UI fake and the tests use it to show the fallback state.
func (r *Runtime) FallBackToCPU() {
	r.mu.Lock()
	defer r.mu.Unlock()
	cpu := r.status.Combo
	cpu.Backend = contract.BackendCPU
	r.stopLocked(contract.RuntimeStatus{
		State: contract.RuntimeCPUFallback, Combo: cpu, Since: time.Now(),
		Message: "The GPU backend failed twice. Lookback is using the CPU.",
	})
}

// Stop stops the sidecar. It ends a pending transition, and waiting Use calls return ErrRestarting.
// Sessions stay open, but they fail with ErrNotReady until a Use starts the sidecar again.
func (r *Runtime) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked(contract.RuntimeStatus{State: contract.RuntimeStopped, Combo: r.status.Combo, Since: time.Now()})
	return nil
}

// Session opens a session for vector space s on the runtime's backend. Opening a space that is already
// open shares its sidecar. A third space returns ErrTooManySpaces.
func (r *Runtime) Session(ctx context.Context, s contract.VectorSpace) (contract.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, open := r.spaces[s]; !open && len(r.spaces) >= 2 {
		return nil, contract.ErrTooManySpaces
	}
	r.spaces[s]++
	return &session{rt: r, space: s, emb: &Embedder{salt: spaceSalt(s)}}, nil
}

// spaceSalt returns the offset that makes the vectors of space s differ from those of every other space.
func spaceSalt(s contract.VectorSpace) uint64 {
	return hashSeed(fmt.Sprintf("%s|%s|%s", s.TextQuant, s.EncQuant, s.ModelRev))
}

// session is a contract.Session on a Runtime. Its vectors come from an Embedder salted by its space, so
// a vector from one space never equals a vector of another for the same input. It embeds only while its
// sidecar is ready, and a request that is in flight during a restart fails with ErrRestarting.
type session struct {
	rt     *Runtime
	space  contract.VectorSpace
	emb    *Embedder
	closed bool // guarded by rt.mu
}

// Space returns the vector space of the session.
func (s *session) Space() contract.VectorSpace { return s.space }

// Close releases the session. The sidecar of its space stops counting toward the limit when no session
// of that space is open.
func (s *session) Close() error {
	s.rt.mu.Lock()
	defer s.rt.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.rt.spaces[s.space] <= 1 {
		delete(s.rt.spaces, s.space)
	} else {
		s.rt.spaces[s.space]--
	}
	return nil
}

// begin checks that the session can embed now and returns the epoch the request runs in.
func (s *session) begin(ctx context.Context) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.rt.mu.Lock()
	defer s.rt.mu.Unlock()
	switch {
	case s.closed:
		return 0, contract.ErrNotReady
	case readyState(s.rt.status.State):
		return s.rt.epoch, nil
	case s.rt.status.State == contract.RuntimeStarting || s.rt.status.State == contract.RuntimeRestarting:
		return 0, contract.ErrRestarting
	default:
		return 0, contract.ErrNotReady
	}
}

// end reports whether a request that began in epoch ep finished in the same sidecar run.
func (s *session) end(ep uint64) error {
	s.rt.mu.Lock()
	defer s.rt.mu.Unlock()
	if s.rt.epoch != ep {
		return contract.ErrRestarting
	}
	return nil
}

// EmbedTexts embeds prompts in the session's space.
func (s *session) EmbedTexts(ctx context.Context, prompts []string) ([][]float32, int, error) {
	ep, err := s.begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	vecs, n, err := s.emb.EmbedTexts(ctx, prompts)
	if err != nil {
		return nil, 0, err
	}
	if err := s.end(ep); err != nil {
		return nil, 0, err
	}
	return vecs, n, nil
}

// EmbedImage embeds one image in the session's space.
func (s *session) EmbedImage(ctx context.Context, img []byte, mime string) ([]float32, int, error) {
	ep, err := s.begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	vec, n, err := s.emb.EmbedImage(ctx, img, mime)
	if err != nil {
		return nil, 0, err
	}
	if err := s.end(ep); err != nil {
		return nil, 0, err
	}
	return vec, n, nil
}

// Tokenize splits text with the session's tokenizer.
func (s *session) Tokenize(ctx context.Context, text string) ([]contract.Token, error) {
	ep, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	toks, err := s.emb.Tokenize(ctx, text)
	if err != nil {
		return nil, err
	}
	if err := s.end(ep); err != nil {
		return nil, err
	}
	return toks, nil
}
