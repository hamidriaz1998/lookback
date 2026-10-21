package theme

import (
	"context"
	"fmt"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Manager holds the active ThemeSource and emits a change whenever the active theme changes:
// on SetActive, and when the active source's own Changed fires.
type Manager struct {
	ctx     context.Context
	changed chan struct{}

	mu      sync.Mutex
	sources []contract.ThemeSource
	active  contract.ThemeSource
	stop    context.CancelFunc // stops the forwarder of the active source
}

// NewManager returns a manager over sources, with the source whose ID is activeID selected. ctx
// ends the forwarders of the sources.
func NewManager(ctx context.Context, sources []contract.ThemeSource, activeID string) (*Manager, error) {
	m := &Manager{ctx: ctx, changed: make(chan struct{}, 1), sources: sources}
	for _, s := range sources {
		if s.ID() == activeID {
			m.setActiveLocked(s)
			return m, nil
		}
	}
	return nil, fmt.Errorf("theme: no source with ID %q", activeID)
}

// Sources returns the sources in the order given to NewManager.
func (m *Manager) Sources() []contract.ThemeSource {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]contract.ThemeSource(nil), m.sources...)
}

// Active returns the selected source.
func (m *Manager) Active() contract.ThemeSource {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

// SetActive selects the source with the given ID and emits a change.
func (m *Manager) SetActive(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sources {
		if s.ID() == id {
			m.setActiveLocked(s)
			return nil
		}
	}
	return fmt.Errorf("theme: no source with ID %q", id)
}

// Theme returns the active theme built from base (see contract.ThemeSource).
func (m *Manager) Theme(base *ui.Theme) *ui.Theme {
	return m.Active().Theme(base)
}

// Changes sends when the active theme changes. Sends coalesce: a receiver that is slow sees one
// signal for several changes, and should read the theme again when it receives one.
func (m *Manager) Changes() <-chan struct{} { return m.changed }

// setActiveLocked makes s the active source, moves the forwarder to it and emits a change. m.mu
// must be held.
func (m *Manager) setActiveLocked(s contract.ThemeSource) {
	if m.stop != nil {
		m.stop()
	}
	m.active = s
	ctx, stop := context.WithCancel(m.ctx)
	m.stop = stop
	if ch := s.Changed(); ch != nil {
		go m.forward(ctx, ch)
	}
	m.notify()
}

func (m *Manager) forward(ctx context.Context, ch <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			m.notify()
		}
	}
}

func (m *Manager) notify() {
	select {
	case m.changed <- struct{}{}:
	default:
	}
}
