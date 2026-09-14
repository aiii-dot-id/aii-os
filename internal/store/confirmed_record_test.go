package store

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
type anchorFixture struct {
	t      *testing.T
	s      *Store
	events []ledger.Event
}

func newAnchorFixture(t *testing.T) *anchorFixture {
	f := &anchorFixture{t: t, s: testStore(t)}
	f.mat(ledger.EventRing0Genesis, 0, map[string]interface{}{"name": "S"})
	f.mat(ledger.EventBeliefUpsert, 3, map[string]interface{}{"id": "b", "statement": "stand", "ring": 3, "confidence": 0.5})
	f.mat(ledger.EventExperienceCreate, 3, map[string]interface{}{"id": "x1", "content": "obs", "category": "observation"})
	f.mat(ledger.EventExperienceCreate, 3, map[string]interface{}{"id": "x2", "content": "obs", "category": "observation"})
	return f
}

func (f *anchorFixture) event(et ledger.EventType, ring int, payload map[string]interface{}) ledger.Event {
	b, _ := json.Marshal(payload)
	return ledger.Event{Seq: uint64(len(f.events) + 1), Type: et, Ring: ring, Timestamp: "2026-09-04T10:00:00Z", Payload: b}
}

func (f *anchorFixture) mat(et ledger.EventType, ring int, payload map[string]interface{}) {
	f.t.Helper()
	evt := f.event(et, ring, payload)
	if err := f.s.Materialize(&evt); err != nil {
		f.t.Fatalf("materialize %s: %v", et, err)
	}
	f.events = append(f.events, evt)
}

func (f *anchorFixture) run(input string, confirmed ...map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"inputs": []string{input}, "outputs": []uint64{}, "confirmed": confirmed}
}

func (f *anchorFixture) setTicks(n int64) {
	f.t.Helper()
	f.s.mu.Lock()
	_, err := f.s.db.Exec(`UPDATE identity_lifetime SET lifetime_ticks = ? WHERE singleton_id = 'current'`, n)
	f.s.mu.Unlock()
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *anchorFixture) anchor(id string) int64 {
	f.t.Helper()
	var n int64
	f.s.mu.RLock()
	err := f.s.db.QueryRow(`SELECT confirmed_at_ticks FROM beliefs WHERE id = ?`, id).Scan(&n)
	f.s.mu.RUnlock()
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

// .
// .
// .
// .
// .
func TestTheRunMarkerStampsTheConfirmedAnchorAndReplayKeepsIt(t *testing.T) {
	f := newAnchorFixture(t)
	f.setTicks(120)
	f.mat(ledger.EventConsolidationRun, 3, f.run("x1", map[string]interface{}{"id": "b", "ticks": 100}))
	if got := f.anchor("b"); got != 100 {
		t.Fatalf("anchor after the marker = %d, want 100", got)
	}
	// .
	f.mat(ledger.EventConsolidationRun, 3, f.run("x2", map[string]interface{}{"id": "b", "ticks": 115}))
	if got := f.anchor("b"); got != 100 {
		t.Fatalf("a second crossing moved the anchor to %d", got)
	}
	// .
	// .
	// .
	if err := f.s.ReplayAll(EventSlice(f.events)); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got := f.anchor("b"); got != 100 {
		t.Fatalf("anchor after replay = %d, want 100", got)
	}
}

// .
// .
// .
func TestACrossingRefusesAGhostBeliefAndAZeroTick(t *testing.T) {
	f := newAnchorFixture(t)
	f.setTicks(120)
	ghost := f.event(ledger.EventConsolidationRun, 3, f.run("x1", map[string]interface{}{"id": "nobody", "ticks": 100}))
	if err := f.s.Materialize(&ghost); err == nil || !strings.Contains(err.Error(), "no such belief") {
		t.Fatalf("a ghost belief was not refused: %v", err)
	}
	zero := f.event(ledger.EventConsolidationRun, 3, f.run("x1", map[string]interface{}{"id": "b", "ticks": 0}))
	if err := f.s.Materialize(&zero); err == nil || !strings.Contains(err.Error(), "positive tick") {
		t.Fatalf("a zero tick was not refused: %v", err)
	}
	// .
	var raw int
	f.s.mu.RLock()
	err := f.s.db.QueryRow(`SELECT raw FROM experiences WHERE id = 'x1'`).Scan(&raw)
	f.s.mu.RUnlock()
	if err != nil || raw != 1 {
		t.Fatalf("a refused marker consumed its input: raw=%d err=%v", raw, err)
	}
}

// .
// .
// .
// .
// .
func TestTheAnchorNeverLiesInTheClocksFuture(t *testing.T) {
	f := newAnchorFixture(t)
	f.setTicks(30)
	f.mat(ledger.EventConsolidationRun, 3, f.run("x1", map[string]interface{}{"id": "b", "ticks": 100}))
	if got := f.anchor("b"); got != 30 {
		t.Fatalf("anchor ahead of a tick-30 clock = %d, want 30", got)
	}
}
