package app

// commandRouter runs the commands that second instances send. They can arrive before the window exists
// (the first instance is still starting), so they wait until open is called, then run in arrival order.
// Both receive and open run on the main thread, so the router needs no lock.
type commandRouter struct {
	ready   bool
	pending []Options
	act     func(Options)
}

// receive runs o now, or queues it until open.
func (r *commandRouter) receive(o Options) {
	if !r.ready {
		r.pending = append(r.pending, o)
		return
	}
	r.act(o)
}

// open marks the window as built and runs the queued commands in order.
func (r *commandRouter) open() {
	r.ready = true
	for _, o := range r.pending {
		r.act(o)
	}
	r.pending = nil
}
