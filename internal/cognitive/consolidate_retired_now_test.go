package cognitive

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

var errBoom = errors.New("the store cannot be read")

const (
	dawnClaim = "The ferry to the island leaves the harbor at dawn"
	noonClaim = "The ferry to the island leaves the harbor at noon since the timetable changed"
)

// lockedRing is a ring writer that two goroutines may write, as the
// facility's two writers (a pass and a retirement) do.
type lockedRing struct {
	mu sync.Mutex
	rw mockRingWriter
}

func (l *lockedRing) RingSection(level ring.RingLevel, name string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rw.RingSection(level, name)
}

func (l *lockedRing) SetRingSection(level ring.RingLevel, name, content string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rw.SetRingSection(level, name, content)
}

func (l *lockedRing) workingTruth() string { return l.RingSection(ring.Ring3, "working_truth") }

// retire removes a belief from the store's current set, as the projection
// does when the retiring event materializes.
func retire(st *mockStore, id string) {
	kept := st.beliefs[:0:0]
	for _, b := range st.beliefs {
		if b.ID != id {
			kept = append(kept, b)
		}
	}
	st.beliefs = kept
}

func ferryStore() *mockStore {
	return &mockStore{
		beliefs: []store.Belief{
			{ID: "b_dawn", Statement: dawnClaim, Ring: 3},
			{ID: "b_noon", Statement: noonClaim, Ring: 3},
		},
		standings: map[string]string{"b_dawn": "confirmed", "b_noon": "new"},
	}
}

// gatedLLM answers only when released, so a test can retire a belief while a
// render is in flight; entered says the call has been made.
type gatedLLM struct {
	entered chan struct{}
	release chan struct{}
	reply   string
}

func newGatedLLM(reply string) *gatedLLM {
	return &gatedLLM{entered: make(chan struct{}, 1), release: make(chan struct{}), reply: reply}
}

func (g *gatedLLM) wait(ctx context.Context) error {
	g.entered <- struct{}{}
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *gatedLLM) ChatSimple(ctx context.Context, _, _ string) (string, string, error) {
	if err := g.wait(ctx); err != nil {
		return "", "", err
	}
	return g.reply, "gated", nil
}

func (g *gatedLLM) ChatStructured(ctx context.Context, _, _ string, _ wire.ToolDefinition) (string, string, bool, error) {
	if err := g.wait(ctx); err != nil {
		return "", "", false, err
	}
	return g.reply, "gated", false, nil
}

// A retirement rewrites Ring 3 at once, from the store, with no model call.
func TestARetiredBeliefLeavesRing3AtOnce(t *testing.T) {
	st := ferryStore()
	rw := &lockedRing{}
	llm := &mockLLM{}
	c := NewConsolidate(st, llm, &mockLedger{st: st}, rw, ConsolidateConfig{Threshold: 3})
	rw.SetRingSection(ring.Ring3, "working_truth", "You believe the ferry to the island leaves the harbor at dawn.")

	retire(st, "b_dawn")
	c.BeliefRetired()

	got := rw.workingTruth()
	if strings.Contains(got, "dawn") {
		t.Fatalf("Ring 3 still states the retired claim: %q", got)
	}
	if !strings.Contains(got, noonClaim) {
		t.Fatalf("Ring 3 does not state the claim that replaced it: %q", got)
	}
	if llm.calls != 0 {
		t.Fatalf("a retirement asked the model %d time(s): the rewrite is a render of the record, not a model's word", llm.calls)
	}
}

// The last belief working truth held can be the one retired: Ring 3 is then
// emptied, not left stating it because there is nothing to render.
func TestTheLastRetiredBeliefLeavesRing3Empty(t *testing.T) {
	st := &mockStore{beliefs: []store.Belief{{ID: "b_dawn", Statement: dawnClaim, Ring: 3}}}
	rw := &lockedRing{}
	c := NewConsolidate(st, &mockLLM{}, &mockLedger{st: st}, rw, ConsolidateConfig{Threshold: 3})
	rw.SetRingSection(ring.Ring3, "working_truth", "You believe the ferry to the island leaves the harbor at dawn.")

	retire(st, "b_dawn")
	c.BeliefRetired()

	if got := rw.workingTruth(); got != "" {
		t.Fatalf("Ring 3 still holds text after its only belief was retired: %q", got)
	}
}

// A render begun before the retirement finishes after it, and its prose still
// states the claim: it must not be written over the rewrite.
func TestARenderInFlightDoesNotWriteOverARetirement(t *testing.T) {
	st := ferryStore()
	rw := &lockedRing{}
	llm := newGatedLLM("You believe the ferry to the island leaves the harbor at dawn.")
	c := NewConsolidate(st, llm, &mockLedger{st: st}, rw, ConsolidateConfig{Threshold: 3})

	done := make(chan struct{})
	go func() { defer close(done); c.writeRing3(context.Background(), c.cfg()) }()
	<-llm.entered // the render has read the beliefs and is with the model

	retire(st, "b_dawn")
	c.BeliefRetired()
	close(llm.release)
	<-done

	if got := rw.workingTruth(); strings.Contains(got, "dawn") || !strings.Contains(got, noonClaim) {
		t.Fatalf("a render begun before the retirement wrote over it: %q", got)
	}
}

// retiringLedger is the door: it materializes a supersession into the store
// and tells the facility, as the app's live-append hook does.
type retiringLedger struct {
	*mockLedger
	st *mockStore
	c  *ConsolidateFacility
}

func (l *retiringLedger) Append(et ledger.EventType, r int, payload any, model string) (*ledger.Event, error) {
	evt, err := l.mockLedger.Append(et, r, payload, model)
	if err == nil && et == ledger.EventBeliefSupersede {
		retire(l.st, payload.(map[string]any)["old_id"].(string))
		l.c.BeliefRetired()
	}
	return evt, err
}

func passStore() *mockStore {
	st := ferryStore()
	st.unprocessedCnt = 3
	st.experiences = []store.Experience{
		{ID: "e1", Content: "the ferry left at noon", Raw: 1},
		{ID: "e2", Content: "noon again", Raw: 1},
		{ID: "e3", Content: "the timetable changed", Raw: 1},
	}
	return st
}

const supersedeEnvelope = `{"operations":[{"op":"supersede","old_id":"b_dawn","new_id":"b_noon","reason":"the timetable changed"}],` +
	`"ring3_view":"You believe the ferry leaves the harbor at noon."}`

// A pass that retires beliefs of its own still writes its own view: its
// retirements are its own decisions, already in the view the model wrote.
func TestAPassThatRetiresItsOwnBeliefsStillWritesItsView(t *testing.T) {
	st := passStore()
	rw := &lockedRing{}
	lg := &retiringLedger{mockLedger: &mockLedger{st: st}, st: st}
	c := NewConsolidate(st, &mockLLM{override: supersedeEnvelope}, lg, rw, ConsolidateConfig{Threshold: 3})
	lg.c = c

	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rw.workingTruth(); got != "You believe the ferry leaves the harbor at noon." {
		t.Fatalf("the pass's own view did not stand: %q", got)
	}
}

// A retirement that comes from outside while the pass is with the model
// leaves the pass's view describing beliefs that have changed: the render
// of the record stands instead.
func TestARetirementDuringAPassDiscardsItsView(t *testing.T) {
	st := passStore()
	st.beliefs = append(st.beliefs, store.Belief{ID: "b_ferry_old", Statement: "The ferry is late on Mondays", Ring: 3})
	rw := &lockedRing{}
	llm := newGatedLLM(`{"operations":[{"op":"supersede","old_id":"b_dawn","new_id":"b_noon"}],` +
		`"ring3_view":"You believe the ferry is late on Mondays and leaves at noon."}`)
	lg := &retiringLedger{mockLedger: &mockLedger{st: st}, st: st}
	c := NewConsolidate(st, llm, lg, rw, ConsolidateConfig{Threshold: 3})
	lg.c = c

	done := make(chan error, 1)
	go func() { done <- c.Execute(context.Background()) }()
	<-llm.entered // the pass has built its evidence and is with the model

	retire(st, "b_ferry_old") // the identity retires a belief the model's view will still state
	c.BeliefRetired()
	close(llm.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if got := rw.workingTruth(); strings.Contains(got, "late on Mondays") {
		t.Fatalf("the pass's view stated a belief retired while it was being written: %q", got)
	}
}

// At boot: the persisted working truth was written at a moment, and a
// retirement the record holds from that moment on has not been rewritten.
func TestBootRewritesARing3OlderThanARetirement(t *testing.T) {
	retiredAt := time.Date(2026, 9, 29, 10, 0, 0, 500_000_000, time.UTC)
	for _, tc := range []struct {
		name      string
		writtenAt time.Time
		retired   []store.RetiredBelief
		err       error
		rewritten bool
	}{
		{"written before the retirement", retiredAt.Add(-time.Second), []store.RetiredBelief{{ID: "b_dawn", RetiredAt: retiredAt.Format(time.RFC3339Nano)}}, nil, true},
		{"written after the retirement", retiredAt.Add(time.Second), []store.RetiredBelief{{ID: "b_dawn", RetiredAt: retiredAt.Format(time.RFC3339Nano)}}, nil, false},
		{"nothing was ever retired", retiredAt, nil, nil, false},
		{"the record of retirements cannot be read", retiredAt, nil, errBoom, true},
		{"a retirement time that cannot be read", retiredAt, []store.RetiredBelief{{ID: "b_dawn", RetiredAt: "yesterday"}}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := ferryStore()
			st.retiredView, st.retiredErr = tc.retired, tc.err
			rw := &lockedRing{}
			rw.SetRingSection(ring.Ring3, "working_truth", "stale prose")
			c := NewConsolidate(st, &mockLLM{}, &mockLedger{st: st}, rw, ConsolidateConfig{Threshold: 3})

			c.ReconcileRing3(tc.writtenAt)

			if rewritten := rw.workingTruth() != "stale prose"; rewritten != tc.rewritten {
				t.Fatalf("rewritten=%v, want %v: %q", rewritten, tc.rewritten, rw.workingTruth())
			}
		})
	}
}
