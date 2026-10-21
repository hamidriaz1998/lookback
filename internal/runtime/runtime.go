package runtime

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/hw"
)

// Config is what a Runtime needs from the app.
type Config struct {
	Life    context.Context // the runtime's lifetime; its end stops the sidecars and closes the streams
	Backend contract.Backend
	// Files returns the files that a sidecar of backend b for vector space s runs with. The app resolves
	// downloaded paths here; the CPU fallback asks with contract.BackendCPU.
	Files   func(b contract.Backend, s contract.VectorSpace) (Files, error)
	LogPath string // logs/llama-server.log
	Threads int    // -t; 0 means the physical core count
}

// Runtime is a contract.Runtime. It keeps one llama-server per open vector space (SPEC section 8).
type Runtime struct {
	cfg     Config
	mu      sync.Mutex
	want    contract.Backend // the backend the user chose (a GPU, or the CPU)
	backend contract.Backend // the backend the sidecars run now: CPU after a fallback
	open    []*space         // open spaces, in the order they were opened
	changed chan struct{}    // closed and replaced on every change
	streams *Stream[contract.RuntimeStatus]
}

// space is the sidecar of one open vector space.
type space struct {
	key      contract.VectorSpace
	refs     int // open sessions
	state    contract.RuntimeState
	err      string
	epoch    uint64             // increments on every transition; a request that began earlier fails with ErrRestarting
	srv      *Server            // the running server of this epoch, once ready
	cancel   context.CancelFunc // ends the start of this epoch
	failures int                // process failures since the last Use or RetryGPU
}

// New returns a Runtime. It starts with no open space, so every state is stopped.
func New(cfg Config) *Runtime {
	if cfg.Threads == 0 {
		if n, err := hw.PhysicalCores(); err == nil {
			cfg.Threads = n
		} else {
			cfg.Threads = 4
		}
	}
	r := &Runtime{
		cfg:     cfg,
		want:    cfg.Backend,
		backend: cfg.Backend,
		changed: make(chan struct{}),
		streams: NewStream(
			func(contract.RuntimeStatus) string { return "" },
			func(contract.RuntimeStatus) bool { return false },
		),
	}
	r.streams.Publish(r.statusLocked())
	go func() {
		<-cfg.Life.Done()
		r.shutdown()
	}()
	return r
}

// rank orders states from best to worst; the worst state of the open spaces is the runtime's state.
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

// readyState reports whether a sidecar in state s can embed.
func readyState(s contract.RuntimeState) bool {
	return s == contract.RuntimeReady || s == contract.RuntimeCPUFallback
}

// Backend returns the backend of every sidecar. After a CPU fallback it is the CPU.
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

// Subscribe streams the status on every change, starting with the current one.
func (r *Runtime) Subscribe(ctx context.Context) <-chan contract.RuntimeStatus {
	return r.streams.Subscribe(ctx)
}

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

// publishLocked publishes the status and wakes the waiters. The caller holds r.mu.
func (r *Runtime) publishLocked() {
	r.streams.Publish(r.statusLocked())
	close(r.changed)
	r.changed = make(chan struct{})
}

// startLocked begins a new epoch of sp in state st. It ends the previous epoch's start, and stops its server in
// the background. The start runs under the runtime's lifetime, never under a caller's ctx (SPEC section 8).
func (r *Runtime) startLocked(sp *space, st contract.RuntimeState, msg string) {
	sp.epoch++
	if sp.cancel != nil {
		sp.cancel()
	}
	prev := sp.srv
	sp.srv = nil
	ctx, cancel := context.WithCancel(r.cfg.Life)
	sp.cancel = cancel
	sp.state, sp.err = st, msg
	r.publishLocked()
	go r.boot(ctx, sp, sp.epoch, r.backend, prev)
}

// boot starts the server of one epoch. Its result is kept only when the epoch is still current.
// prev, when set, is the server of the previous epoch: it stops before the new one starts, so two never overlap.
func (r *Runtime) boot(ctx context.Context, sp *space, ep uint64, b contract.Backend, prev *Server) {
	if prev != nil {
		prev.Stop(context.Background())
	}
	files, err := r.cfg.Files(b, sp.key)
	var srv *Server
	if err == nil {
		srv, err = Start(ctx, Options{Files: files, GPU: b != contract.BackendCPU, Threads: r.cfg.Threads, LogPath: r.cfg.LogPath})
	}
	r.mu.Lock()
	if sp.epoch != ep {
		r.mu.Unlock()
		if srv != nil {
			srv.Stop(context.Background())
		}
		return
	}
	if err != nil {
		r.failLocked(sp, err.Error())
		r.mu.Unlock()
		return
	}
	sp.srv = srv
	st := contract.RuntimeReady
	if b == contract.BackendCPU && r.want != contract.BackendCPU {
		st = contract.RuntimeCPUFallback
	}
	sp.state, sp.err = st, ""
	r.publishLocked()
	r.mu.Unlock()
	go r.watch(sp, ep, srv)
}

// watch reports an unexpected exit of the server of epoch ep. A stop or a newer epoch ends the watch quietly.
func (r *Runtime) watch(sp *space, ep uint64, srv *Server) {
	<-srv.Exited()
	r.mu.Lock()
	defer r.mu.Unlock()
	if sp.epoch != ep {
		return
	}
	sp.srv = nil
	r.failLocked(sp, "llama-server exited")
}

// failLocked handles a failure of sp (SPEC section 8, Q23): the first failure restarts the sidecar, the second
// moves every sidecar to the CPU build of the same precisions, and a failure on the CPU ends in state failed.
func (r *Runtime) failLocked(sp *space, msg string) {
	sp.failures++
	if sp.failures <= 1 {
		r.startLocked(sp, contract.RuntimeRestarting, msg)
		return
	}
	if r.backend != contract.BackendCPU {
		r.backend = contract.BackendCPU
		for _, o := range r.open {
			r.startLocked(o, contract.RuntimeRestarting, "The GPU backend failed; indexing continues on the CPU")
		}
		return
	}
	sp.epoch++
	if sp.cancel != nil {
		sp.cancel()
	}
	sp.state, sp.err = contract.RuntimeFailed, msg
	r.publishLocked()
}

// stopLocked stops the sidecar of sp and ends its start. It returns the server to stop outside the lock.
func (r *Runtime) stopLocked(sp *space) *Server {
	sp.epoch++
	if sp.cancel != nil {
		sp.cancel()
		sp.cancel = nil
	}
	srv := sp.srv
	sp.srv = nil
	sp.state, sp.err = contract.RuntimeStopped, ""
	r.publishLocked()
	return srv
}

// isOpenLocked reports whether sp is still open.
func (r *Runtime) isOpenLocked(sp *space) bool {
	for _, o := range r.open {
		if o == sp {
			return true
		}
	}
	return false
}

// releaseLocked drops one session of sp. The last session closes the space and stops its sidecar, and the
// server it returns is stopped by the caller.
func (r *Runtime) releaseLocked(sp *space) *Server {
	sp.refs--
	if sp.refs > 0 {
		return nil
	}
	for i, o := range r.open {
		if o == sp {
			r.open = append(r.open[:i], r.open[i+1:]...)
			break
		}
	}
	return r.stopLocked(sp)
}

// Session returns a session for vector space s once its sidecar is ready. A space that is not open starts its
// sidecar, and opening an open space shares it. ctx bounds only this call (SPEC section 8).
func (r *Runtime) Session(ctx context.Context, s contract.VectorSpace) (contract.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.cfg.Life.Err() != nil {
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
		sp.failures = 0
		r.startLocked(sp, contract.RuntimeStarting, "")
	}
	r.mu.Unlock()

	sess := &session{rt: r, sp: sp}
	if err := r.waitReady(ctx, sp, true); err != nil {
		_ = sess.Close()
		return nil, err
	}
	return sess, nil
}

// waitReady waits until sp is ready. With acquire, a stop or failure ends the wait with ErrNotReady. Otherwise it
// waits through them, until a Use starts the sidecar again. It returns when ctx ends, or with ErrStopped when the
// lifetime ends.
func (r *Runtime) waitReady(ctx context.Context, sp *space, acquire bool) error {
	for {
		r.mu.Lock()
		if r.cfg.Life.Err() != nil {
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
		case <-r.cfg.Life.Done():
			return contract.ErrStopped
		}
	}
}

// Use makes b the backend of every sidecar and restarts the open ones (SPEC section 8). It returns when every
// open session is ready again, when ctx ends (the unfinished sidecars then stop), or ErrRestarting when a later Use
// or Stop supersedes it.
func (r *Runtime) Use(ctx context.Context, b contract.Backend) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	if r.cfg.Life.Err() != nil {
		r.mu.Unlock()
		return contract.ErrStopped
	}
	r.want, r.backend = b, b
	type wait struct {
		sp *space
		ep uint64
	}
	var waits []wait
	for _, sp := range r.open {
		sp.failures = 0
		st := contract.RuntimeRestarting
		if sp.state == contract.RuntimeStopped || sp.state == contract.RuntimeFailed {
			st = contract.RuntimeStarting
		}
		r.startLocked(sp, st, "")
		waits = append(waits, wait{sp, sp.epoch})
	}
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
			if w.sp.state == contract.RuntimeFailed {
				r.mu.Unlock()
				return contract.ErrNotReady
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
			var srvs []*Server
			for _, w := range waits {
				if r.isOpenLocked(w.sp) && w.sp.epoch == w.ep && !readyState(w.sp.state) {
					if srv := r.stopLocked(w.sp); srv != nil {
						srvs = append(srvs, srv)
					}
				}
			}
			r.mu.Unlock()
			for _, srv := range srvs {
				srv.Stop(context.Background())
			}
			return ctx.Err()
		case <-r.cfg.Life.Done():
			return contract.ErrStopped
		}
	}
}

// RetryGPU restarts the GPU backend after a CPU fallback ("Try GPU again").
func (r *Runtime) RetryGPU(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	if r.cfg.Life.Err() != nil {
		r.mu.Unlock()
		return contract.ErrStopped
	}
	fallen := false
	for _, sp := range r.open {
		if sp.state == contract.RuntimeCPUFallback {
			fallen = true
		}
	}
	want := r.want
	r.mu.Unlock()
	if !fallen {
		return errors.New("runtime: not on the CPU fallback")
	}
	return r.Use(ctx, want)
}

// Stop stops every sidecar. It ends pending starts, and sessions fail with ErrNotReady until Use or Session starts
// the sidecar again. The runtime's lifetime continues.
func (r *Runtime) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	if r.cfg.Life.Err() != nil {
		r.mu.Unlock()
		return contract.ErrStopped
	}
	var srvs []*Server
	for _, sp := range r.open {
		if srv := r.stopLocked(sp); srv != nil {
			srvs = append(srvs, srv)
		}
	}
	r.mu.Unlock()
	for _, srv := range srvs {
		srv.Stop(ctx)
	}
	return nil
}

// shutdown runs when the lifetime ends: every server stops and the streams close.
func (r *Runtime) shutdown() {
	r.mu.Lock()
	var srvs []*Server
	for _, sp := range r.open {
		if srv := r.stopLocked(sp); srv != nil {
			srvs = append(srvs, srv)
		}
	}
	r.mu.Unlock()
	for _, srv := range srvs {
		srv.Stop(context.Background())
	}
	r.streams.Close()
}

// session is a contract.Session on a Runtime.
type session struct {
	rt     *Runtime
	sp     *space
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
	if s.closed {
		s.rt.mu.Unlock()
		return nil
	}
	s.closed = true
	srv := s.rt.releaseLocked(s.sp)
	s.rt.mu.Unlock()
	if srv != nil {
		srv.Stop(context.Background())
	}
	return nil
}

// begin returns the server that a request runs on and the epoch it runs in.
func (s *session) begin() (*Server, uint64, error) {
	s.rt.mu.Lock()
	defer s.rt.mu.Unlock()
	switch {
	case s.closed:
		return nil, 0, contract.ErrNotReady
	case readyState(s.sp.state) && s.sp.srv != nil:
		return s.sp.srv, s.sp.epoch, nil
	case s.sp.state == contract.RuntimeStarting || s.sp.state == contract.RuntimeRestarting:
		return nil, 0, contract.ErrRestarting
	default:
		return nil, 0, contract.ErrNotReady
	}
}

// call runs one request on the server of the session. A request cut off by a restart fails with ErrRestarting. A
// server error (HTTP 500 for a bad image, say) is returned as it is: restarts follow process exits only.
func (s *session) call(ctx context.Context, fn func(*Server) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	srv, ep, err := s.begin()
	if err != nil {
		return err
	}
	err = fn(srv)
	if err == nil {
		s.rt.mu.Lock()
		stale := s.sp.epoch != ep
		s.rt.mu.Unlock()
		if stale {
			return contract.ErrRestarting
		}
		return nil
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		select {
		case <-srv.Exited():
			return contract.ErrRestarting
		case <-time.After(500 * time.Millisecond):
		}
	}
	return err
}

// EmbedTexts embeds prompts in the session's space.
func (s *session) EmbedTexts(ctx context.Context, prompts []string) ([][]float32, int, error) {
	var vecs [][]float32
	var n int
	err := s.call(ctx, func(srv *Server) error {
		var e error
		vecs, n, e = srv.EmbedTexts(ctx, prompts)
		return e
	})
	return vecs, n, err
}

// EmbedImage embeds one image in the session's space.
func (s *session) EmbedImage(ctx context.Context, img []byte, mime string) ([]float32, int, error) {
	var vec []float32
	var n int
	err := s.call(ctx, func(srv *Server) error {
		var e error
		vec, n, e = srv.EmbedImage(ctx, img, mime)
		return e
	})
	return vec, n, err
}

// Tokenize splits text with the session's tokenizer.
func (s *session) Tokenize(ctx context.Context, text string) ([]contract.Token, error) {
	var toks []contract.Token
	err := s.call(ctx, func(srv *Server) error {
		var e error
		toks, e = srv.Tokenize(ctx, text)
		return e
	})
	return toks, err
}
