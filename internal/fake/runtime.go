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

// Runtime is a contract.Runtime that does not start a process. Each open vector space has its own sidecar
// state, and a space is open while it has sessions. A session embeds with vectors salted by its space (see
// session).
//
// Every state change of a space starts a new epoch on it. A transition that is not finished when a newer one
// starts, when Stop runs, when the space closes, or when its restart ctx ends, does nothing when its timer fires.
type Runtime struct {
	life    context.Context
	mu      sync.Mutex
	backend contract.Backend
	open    []*space      // open spaces, in the order they were opened
	changed chan struct{} // closed and replaced when the status changes
	streams *stream[contract.RuntimeStatus]
}

// space is the sidecar of one open vector space.
type space struct {
	key   contract.VectorSpace
	refs  int // open sessions
	state contract.RuntimeState
	err   string
	epoch uint64      // increments on every transition; a request that began earlier fails with ErrRestarting
	timer *time.Timer // ends the pending start or restart, if any
}

// NewRuntime returns a Runtime whose lifetime is life. It starts with no open space, so every state is stopped,
// and its backend is the reference laptop's Vulkan.
func NewRuntime(life context.Context) *Runtime {
	r := &Runtime{
		life:    life,
		backend: contract.BackendVulkan,
		changed: make(chan struct{}),
		streams: newStream(
			func(contract.RuntimeStatus) string { return "" },
			func(contract.RuntimeStatus) bool { return false },
		),
	}
	r.streams.publish(r.statusLocked())
	go func() {
		<-life.Done()
		r.shutdown()
	}()
	return r
}

// vulkanBF16Q8 is the recommended combination on the reference laptop.
var vulkanBF16Q8 = contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0}

// Backend returns the backend of every sidecar.
func (r *Runtime) Backend() contract.Backend {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.backend
}

// Status returns the current status.
func (r *Runtime) Status() contract.RuntimeStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statusLocked()
}

// Subscribe returns a stream of status changes.
func (r *Runtime) Subscribe(ctx context.Context) <-chan contract.RuntimeStatus {
	return r.streams.subscribe(ctx)
}

// rank orders states from best to worst. The worst state of the open spaces is the runtime's state.
func rank(s contract.RuntimeState) int {
	switch s {
	case contract.RuntimeReady:
		return 0
	case contract.RuntimeStarting:
		return 1
	case contract.RuntimeRestarting:
		return 2
	case contract.RuntimeCPUFallback:
		return 3
	case contract.RuntimeStopped:
		return 4
	default:
		return 5 // failed
	}
}

// statusLocked returns the status. The caller holds r.mu.
func (r *Runtime) statusLocked() contract.RuntimeStatus {
	st := contract.RuntimeStatus{Backend: r.backend, State: contract.RuntimeStopped}
	for i, sp := range r.open {
		st.Spaces = append(st.Spaces, contract.SpaceStatus{Space: sp.key, State: sp.state, Err: sp.err})
		if i == 0 || rank(sp.state) > rank(st.State) {
			st.State = sp.state
		}
	}
	return st
}

// setLocked sets the state of sp and publishes the status, which wakes the waiters. The caller holds r.mu.
func (r *Runtime) setLocked(sp *space, st contract.RuntimeState, msg string) {
	sp.state, sp.err = st, msg
	r.publishLocked()
}

// publishLocked publishes the status and wakes the waiters. The caller holds r.mu.
func (r *Runtime) publishLocked() {
	r.streams.publish(r.statusLocked())
	close(r.changed)
	r.changed = make(chan struct{})
}

// supersedeLocked ends the pending transition of sp, so its timer does nothing. The caller holds r.mu.
func supersedeLocked(sp *space) {
	sp.epoch++
	if sp.timer != nil {
		sp.timer.Stop()
		sp.timer = nil
	}
}

// transitionLocked starts a transition of sp to st. After transitionTime sp becomes ready, unless a newer
// transition or a stop ends first. It returns the epoch of the transition. The caller holds r.mu.
func (r *Runtime) transitionLocked(sp *space, st contract.RuntimeState) uint64 {
	supersedeLocked(sp)
	ep := sp.epoch
	r.setLocked(sp, st, "")
	sp.timer = time.AfterFunc(transitionTime, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if sp.epoch == ep {
			r.setLocked(sp, contract.RuntimeReady, "")
		}
	})
	return ep
}

// stopLocked stops the sidecar of sp and ends its pending transition. The caller holds r.mu.
func (r *Runtime) stopLocked(sp *space) {
	supersedeLocked(sp)
	r.setLocked(sp, contract.RuntimeStopped, "")
}

// isOpenLocked reports whether sp is still open. The caller holds r.mu.
func (r *Runtime) isOpenLocked(sp *space) bool {
	for _, o := range r.open {
		if o == sp {
			return true
		}
	}
	return false
}

// releaseLocked drops one session of sp. The last session closes the space and stops its sidecar. The caller
// holds r.mu.
func (r *Runtime) releaseLocked(sp *space) {
	sp.refs--
	if sp.refs > 0 {
		return
	}
	supersedeLocked(sp)
	for i, o := range r.open {
		if o == sp {
			r.open = append(r.open[:i], r.open[i+1:]...)
			break
		}
	}
	r.publishLocked()
}

// readyState reports whether a sidecar in state s can embed.
func readyState(s contract.RuntimeState) bool {
	return s == contract.RuntimeReady || s == contract.RuntimeCPUFallback
}

// Use makes b the backend of every sidecar and restarts the open ones. It returns when every open session is
// ready again, when ctx ends, or when a later Use or Stop supersedes it (ErrRestarting). When ctx ends first,
// the sidecars that are not ready yet stop.
func (r *Runtime) Use(ctx context.Context, b contract.Backend) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	if r.life.Err() != nil {
		r.mu.Unlock()
		return contract.ErrStopped
	}
	r.backend = b
	type restart struct {
		sp *space
		ep uint64
	}
	var waits []restart
	for _, sp := range r.open {
		st := contract.RuntimeRestarting
		if sp.state == contract.RuntimeStopped || sp.state == contract.RuntimeFailed {
			st = contract.RuntimeStarting
		}
		waits = append(waits, restart{sp, r.transitionLocked(sp, st)})
	}
	r.publishLocked()
	r.mu.Unlock()

	for {
		r.mu.Lock()
		done := true
		for _, w := range waits {
			if !r.isOpenLocked(w.sp) {
				continue
			}
			if w.sp.epoch != w.ep {
				r.mu.Unlock()
				return contract.ErrRestarting
			}
			if !readyState(w.sp.state) {
				done = false
			}
		}
		if done {
			r.mu.Unlock()
			return nil
		}
		ch := r.changed
		r.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			r.mu.Lock()
			for _, w := range waits {
				if r.isOpenLocked(w.sp) && w.sp.epoch == w.ep && !readyState(w.sp.state) {
					r.stopLocked(w.sp)
				}
			}
			r.mu.Unlock()
			return ctx.Err()
		case <-r.life.Done():
			return contract.ErrStopped
		}
	}
}

// RetryGPU restarts the GPU backend after a CPU fallback. It is an error when no sidecar is on the fallback.
func (r *Runtime) RetryGPU(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.life.Err() != nil {
		return contract.ErrStopped
	}
	var fallen []*space
	for _, sp := range r.open {
		if sp.state == contract.RuntimeCPUFallback {
			fallen = append(fallen, sp)
		}
	}
	if len(fallen) == 0 {
		return errors.New("runtime: not on the CPU fallback")
	}
	r.backend = contract.BackendVulkan
	for _, sp := range fallen {
		r.transitionLocked(sp, contract.RuntimeRestarting)
	}
	r.publishLocked()
	return nil
}

// FallBackToCPU moves every sidecar to the CPU build after two GPU failures (SPEC section 8, Q23).
// The UI fake and the tests use it to show the fallback state.
func (r *Runtime) FallBackToCPU() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backend = contract.BackendCPU
	for _, sp := range r.open {
		supersedeLocked(sp)
		r.setLocked(sp, contract.RuntimeCPUFallback, "The GPU backend failed twice. Lookback is using the CPU.")
	}
	r.publishLocked()
}

// Stop stops every sidecar. It ends pending restarts, and waiting Use calls return ErrRestarting. Sessions
// stay open and fail with ErrNotReady until Use or Session starts the sidecar again.
func (r *Runtime) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.life.Err() != nil {
		return contract.ErrStopped
	}
	for _, sp := range r.open {
		r.stopLocked(sp)
	}
	return nil
}

// shutdown runs when the lifetime ends: every sidecar stops and the streams close.
func (r *Runtime) shutdown() {
	r.mu.Lock()
	for _, sp := range r.open {
		r.stopLocked(sp)
	}
	r.mu.Unlock()
	r.streams.close()
}

// Session returns a session for vector space s once its sidecar is ready. ctx bounds only this call.
func (r *Runtime) Session(ctx context.Context, s contract.VectorSpace) (contract.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.life.Err() != nil {
		r.mu.Unlock()
		return nil, contract.ErrStopped
	}
	var sp *space
	for _, o := range r.open {
		if o.key == s {
			sp = o
		}
	}
	if sp == nil {
		if len(r.open) >= 2 {
			r.mu.Unlock()
			return nil, contract.ErrTooManySpaces
		}
		sp = &space{key: s, state: contract.RuntimeStopped}
		r.open = append(r.open, sp)
	}
	sp.refs++
	if sp.state == contract.RuntimeStopped || sp.state == contract.RuntimeFailed {
		r.transitionLocked(sp, contract.RuntimeStarting)
	}
	r.mu.Unlock()

	sess := &session{rt: r, sp: sp, emb: &Embedder{salt: spaceSalt(s)}}
	if err := r.waitReady(ctx, sp, true); err != nil {
		_ = sess.Close()
		return nil, err
	}
	return sess, nil
}

// waitReady waits until sp is ready. With acquire, a stop or failure ends the wait with ErrNotReady. Otherwise
// it waits through them, until a Use starts the sidecar again. It also returns when ctx ends, or with ErrStopped
// when the lifetime ends.
func (r *Runtime) waitReady(ctx context.Context, sp *space, acquire bool) error {
	for {
		r.mu.Lock()
		if r.life.Err() != nil {
			r.mu.Unlock()
			return contract.ErrStopped
		}
		if !r.isOpenLocked(sp) {
			r.mu.Unlock()
			return contract.ErrNotReady
		}
		st := sp.state
		ch := r.changed
		r.mu.Unlock()
		switch {
		case readyState(st):
			return nil
		case acquire && (st == contract.RuntimeStopped || st == contract.RuntimeFailed):
			return contract.ErrNotReady
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		case <-r.life.Done():
			return contract.ErrStopped
		}
	}
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
	sp     *space
	emb    *Embedder
	closed bool // guarded by rt.mu
}

// Space returns the vector space of the session.
func (s *session) Space() contract.VectorSpace { return s.sp.key }

// Ready waits until the session's sidecar is ready again.
func (s *session) Ready(ctx context.Context) error {
	s.rt.mu.Lock()
	closed := s.closed
	s.rt.mu.Unlock()
	if closed {
		return contract.ErrNotReady
	}
	return s.rt.waitReady(ctx, s.sp, false)
}

// Close releases the session. It is idempotent.
func (s *session) Close() error {
	s.rt.mu.Lock()
	defer s.rt.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.rt.releaseLocked(s.sp)
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
	case readyState(s.sp.state):
		return s.sp.epoch, nil
	case s.sp.state == contract.RuntimeStarting || s.sp.state == contract.RuntimeRestarting:
		return 0, contract.ErrRestarting
	default:
		return 0, contract.ErrNotReady
	}
}

// end reports whether a request that began in epoch ep finished in the same sidecar run.
func (s *session) end(ep uint64) error {
	s.rt.mu.Lock()
	defer s.rt.mu.Unlock()
	if s.sp.epoch != ep {
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
