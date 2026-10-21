package download

import (
	"sync"

	"github.com/egoist/mygo"
)

// Network reports whether the computer is online. Downloads pause while it is not.
type Network interface {
	// Online reports the current state.
	Online() bool
	// OnChanged calls fn when the state changes.
	OnChanged(fn func(online bool))
}

// mygoNetwork adapts mygo.Network. The app calls MygoNetwork once it is ready.
type mygoNetwork struct {
	mu     sync.Mutex
	online bool
	fns    []func(bool)
}

// MygoNetwork returns the Network that mygo.Network reports.
func MygoNetwork() Network {
	n := &mygoNetwork{online: true}
	mygo.Network.OnChanged(func(s mygo.NetworkStatus) { n.set(s.Online) })
	mygo.App.WhenReady(func() { n.set(mygo.Network.Status().Online) })
	return n
}

func (n *mygoNetwork) Online() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.online
}

func (n *mygoNetwork) OnChanged(fn func(bool)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.fns = append(n.fns, fn)
}

func (n *mygoNetwork) set(online bool) {
	n.mu.Lock()
	if online == n.online {
		n.mu.Unlock()
		return
	}
	n.online = online
	fns := append([]func(bool){}, n.fns...)
	n.mu.Unlock()
	for _, fn := range fns {
		fn(online)
	}
}
