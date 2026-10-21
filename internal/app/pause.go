package app

import (
	"sync"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// pauser is the part of contract.Indexer that pauses use.
type pauser interface {
	Pause(reason contract.PauseReason)
	Resume(reason contract.PauseReason)
}

// pauses forwards pause reasons to the indexer. The indexer stacks pauses per reason (contract.Indexer),
// so this type sends only transitions: setting a reason that is already in that state does nothing.
type pauses struct {
	mu     sync.Mutex
	target pauser // nil until WP9 wires the indexer; the reasons are then only tracked
	active map[contract.PauseReason]bool
}

func newPauses(target pauser) *pauses {
	return &pauses{target: target, active: map[contract.PauseReason]bool{}}
}

// set turns reason on or off. It reports whether the state changed.
func (p *pauses) set(reason contract.PauseReason, on bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active[reason] == on {
		return false
	}
	p.active[reason] = on
	if p.target != nil {
		if on {
			p.target.Pause(reason)
		} else {
			p.target.Resume(reason)
		}
	}
	return true
}

// has reports whether reason is on.
func (p *pauses) has(reason contract.PauseReason) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active[reason]
}
