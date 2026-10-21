package app

import (
	"reflect"
	"sort"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// stackIndexer models contract.Indexer's pause stacking: a job runs when no reason is set.
type stackIndexer struct {
	reasons map[contract.PauseReason]bool
	calls   []string
}

func newStackIndexer() *stackIndexer {
	return &stackIndexer{reasons: map[contract.PauseReason]bool{}}
}

func (s *stackIndexer) Pause(r contract.PauseReason) {
	s.reasons[r] = true
	s.calls = append(s.calls, "pause:"+string(r))
}

func (s *stackIndexer) Resume(r contract.PauseReason) {
	delete(s.reasons, r)
	s.calls = append(s.calls, "resume:"+string(r))
}

// held returns the reasons that still pause the job, sorted.
func (s *stackIndexer) held() []string {
	var out []string
	for r := range s.reasons {
		out = append(out, string(r))
	}
	sort.Strings(out)
	return out
}

func setup() (*pauses, *stackIndexer) {
	s := newStackIndexer()
	return newPauses(s), s
}

func TestUserResumeKeepsBatteryPause(t *testing.T) {
	p, s := setup()
	p.set(contract.PauseBattery, true)
	p.set(contract.PauseUser, true)
	p.set(contract.PauseUser, false) // the tray's Resume
	if got, want := s.held(), []string{"battery"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("held = %v, want %v", got, want)
	}
}

func TestSuspendResumeKeepsUserAndBattery(t *testing.T) {
	p, s := setup()
	p.set(contract.PauseUser, true)
	p.set(contract.PauseBattery, true)
	p.set(contract.PauseSuspend, true)
	p.set(contract.PauseSuspend, false)
	if got, want := s.held(), []string{"battery", "user"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("held = %v, want %v", got, want)
	}
}

func TestRepeatedSetSendsOneTransition(t *testing.T) {
	p, s := setup()
	if !p.set(contract.PauseSuspend, true) || p.set(contract.PauseSuspend, true) {
		t.Fatal("second set of the same state reported a change")
	}
	p.set(contract.PauseSuspend, false)
	p.set(contract.PauseSuspend, false)
	if got, want := s.calls, []string{"pause:suspend", "resume:suspend"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestBatteryUnplugAfterSuspendClearsOnlyBattery(t *testing.T) {
	p, s := setup()
	p.set(contract.PauseBattery, true)
	p.set(contract.PauseUser, true)
	p.set(contract.PauseSuspend, true)
	p.set(contract.PauseSuspend, false)
	p.set(contract.PauseBattery, false) // resume re-checks power and finds the charger in
	if got, want := s.held(), []string{"user"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("held = %v, want %v", got, want)
	}
	if p.has(contract.PauseBattery) || !p.has(contract.PauseUser) {
		t.Fatal("tracked state disagrees with the indexer")
	}
}

func TestNilIndexerTracksOnly(t *testing.T) {
	p := newPauses(nil)
	if !p.set(contract.PauseLab, true) || !p.has(contract.PauseLab) {
		t.Fatal("reason not tracked without an indexer")
	}
}
