package fake

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Runtime is a contract.Runtime that does not start a process. It embeds an Embedder, and its
// state changes take 200 ms, so the Models screen can show the transitions.
type Runtime struct {
	*Embedder

	mu      sync.Mutex
	status  contract.RuntimeStatus
	changes chan contract.RuntimeStatus
}

// NewRuntime returns a Runtime that is ready on the reference laptop's Vulkan combination.
func NewRuntime() *Runtime {
	return &Runtime{
		Embedder: NewEmbedder(),
		status: contract.RuntimeStatus{
			State: contract.RuntimeReady,
			Combo: vulkanBF16Q8,
			Since: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC),
		},
		changes: make(chan contract.RuntimeStatus, 64),
	}
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

// Changes returns the stream of status changes. The fake drops a change when 64 are unread.
func (r *Runtime) Changes() <-chan contract.RuntimeStatus { return r.changes }

// set stores st and publishes it.
func (r *Runtime) set(st contract.RuntimeStatus) {
	r.mu.Lock()
	r.status = st
	r.mu.Unlock()
	select {
	case r.changes <- st:
	default:
	}
}

// Use stops the sidecar and starts combination c. It returns ready after 200 ms.
func (r *Runtime) Use(ctx context.Context, c contract.Combo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.set(contract.RuntimeStatus{State: contract.RuntimeStarting, Combo: c, Message: "Starting " + c.ID(), Since: time.Now()})
	time.AfterFunc(200*time.Millisecond, func() {
		r.set(contract.RuntimeStatus{State: contract.RuntimeReady, Combo: c, Since: time.Now()})
	})
	return nil
}

// RetryGPU restarts the GPU backend after a CPU fallback. It is an error in any other state.
func (r *Runtime) RetryGPU(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	state, combo := r.status.State, r.status.Combo
	r.mu.Unlock()
	if state != contract.RuntimeCPUFallback {
		return errors.New("runtime: not on the CPU fallback")
	}
	gpu := combo
	gpu.Backend = contract.BackendVulkan
	r.set(contract.RuntimeStatus{State: contract.RuntimeRestarting, Combo: gpu, Message: "Restarting Vulkan", Since: time.Now()})
	time.AfterFunc(200*time.Millisecond, func() {
		r.set(contract.RuntimeStatus{State: contract.RuntimeReady, Combo: gpu, Since: time.Now()})
	})
	return nil
}

// FallBackToCPU moves the sidecar to the CPU build after two GPU failures (SPEC section 8, Q23).
// The UI fake and the tests use it to show the fallback state.
func (r *Runtime) FallBackToCPU() {
	r.mu.Lock()
	cpu := r.status.Combo
	cpu.Backend = contract.BackendCPU
	r.mu.Unlock()
	r.set(contract.RuntimeStatus{
		State: contract.RuntimeCPUFallback, Combo: cpu, Since: time.Now(),
		Message: "The GPU backend failed twice. Lookback is using the CPU.",
	})
}

// Stop marks the sidecar stopped.
func (r *Runtime) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	combo := r.status.Combo
	r.mu.Unlock()
	r.set(contract.RuntimeStatus{State: contract.RuntimeStopped, Combo: combo, Since: time.Now()})
	return nil
}
