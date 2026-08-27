package cognitive

import (
	"context"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

type fakeOwner struct {
	name string
	runs int
}

func (f *fakeOwner) Name() string { return f.name }
func (f *fakeOwner) OnAlarm(ctx context.Context, alarmID, clock string, deadline int64, payload string) AlarmResult {
	f.runs++
	return AlarmResult{Accepted: true}
}

type fakeRaw struct{ n int }

func (f *fakeRaw) ListRawExperiences(limit int) ([]store.Experience, error) {
	out := make([]store.Experience, 0, f.n)
	for i := 0; i < f.n && i < limit; i++ {
		out = append(out, store.Experience{ID: "e"})
	}
	return out, nil
}

// Metabolism is capacity-gated (R29): material → DREAM runs; no
// material → the material-gated facilities stay quiet. Reflective
// facilities run on wall spacing and no more.
func TestRhythmCapacityGating(t *testing.T) {
	raw := &fakeRaw{n: 0}
	dream := &fakeOwner{name: "dream"}
	consolidate := &fakeOwner{name: "consolidate"}
	selfModel := &fakeOwner{name: "self_model"}
	review := &fakeOwner{name: "identity_review"}
	r := NewRhythm(raw, freeGate(), dream, consolidate, selfModel, review)
	// Reflective spacing satisfied recently → nothing reflective runs.
	r.lastSelfModel = time.Now()
	r.lastReview = time.Now()
	r.lastConsolidate = time.Now()

	res := r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if !res.Accepted {
		t.Fatal("rhythm pass must accept (recurring)")
	}
	if dream.runs != 0 || consolidate.runs != 0 || selfModel.runs != 0 || review.runs != 0 {
		t.Fatalf("no material, no spacing due → nothing runs (got d=%d c=%d s=%d r=%d)",
			dream.runs, consolidate.runs, selfModel.runs, review.runs)
	}

	// Material appears → DREAM runs on the next pass; consolidate still
	// inside spacing stays quiet.
	raw.n = 2
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if dream.runs != 1 {
		t.Fatalf("raw material must run DREAM, got %d", dream.runs)
	}
	if consolidate.runs != 0 {
		t.Fatal("consolidate inside its spacing must wait")
	}

	// Spacing elapsed + material → consolidate runs too.
	r.lastConsolidate = time.Now().Add(-time.Hour)
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if consolidate.runs != 1 {
		t.Fatalf("consolidate with material past spacing must run, got %d", consolidate.runs)
	}

	// Reflective facilities: spacing alone drives them.
	r.lastSelfModel = time.Now().Add(-7 * time.Hour)
	r.lastReview = time.Now().Add(-25 * time.Hour)
	raw.n = 0
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if selfModel.runs != 1 || review.runs != 1 {
		t.Fatalf("reflective facilities past spacing must run (s=%d r=%d)", selfModel.runs, review.runs)
	}
}

// ONE MODE PER DELTA. hasRaw is read once and both facilities used to
// act on it in sequence: DREAM metabolized the raw experiences, then
// CONSOLIDATE ran against a predicate that no longer held. They are
// opposites by design — divergent and convergent — so running both
// across one delta asks the identity to do both to the same material.
func TestOneModePerDelta(t *testing.T) {
	newRhythm := func() (*Rhythm, *fakeOwner, *fakeOwner) {
		dream := &fakeOwner{name: "dream"}
		consolidate := &fakeOwner{name: "consolidate"}
		r := NewRhythm(&fakeRaw{n: 3}, freeGate(), dream, consolidate,
			&fakeOwner{name: "self_model"}, &fakeOwner{name: "identity_review"})
		return r, dream, consolidate
	}

	// Spacing satisfied (lastConsolidate zero): CONSOLIDATE takes the
	// delta and DREAM must not also run on it.
	r, dream, consolidate := newRhythm()
	r.OnAlarm(context.Background(), "a", "wall", 0, "")
	if consolidate.runs != 1 {
		t.Fatalf("consolidate should take the delta when spacing allows, ran %d", consolidate.runs)
	}
	if dream.runs != 0 {
		t.Fatalf("dream ran on a delta consolidate had already taken (%d) — both consumed one predicate", dream.runs)
	}

	// Spacing NOT satisfied: DREAM takes it instead. Material is never
	// left unmetabolized just because consolidate is resting.
	r2, dream2, consolidate2 := newRhythm()
	r2.lastConsolidate = time.Now()
	r2.OnAlarm(context.Background(), "a", "wall", 0, "")
	if dream2.runs != 1 {
		t.Fatalf("dream should take the delta when consolidate is spaced out, ran %d", dream2.runs)
	}
	if consolidate2.runs != 0 {
		t.Fatalf("consolidate ran inside its own spacing window, %d", consolidate2.runs)
	}

	// No material: neither runs.
	dream3 := &fakeOwner{name: "dream"}
	consolidate3 := &fakeOwner{name: "consolidate"}
	r3 := NewRhythm(&fakeRaw{n: 0}, freeGate(), dream3, consolidate3,
		&fakeOwner{name: "self_model"}, &fakeOwner{name: "identity_review"})
	r3.OnAlarm(context.Background(), "a", "wall", 0, "")
	if dream3.runs != 0 || consolidate3.runs != 0 {
		t.Fatalf("material-gated facilities ran with no material: dream=%d consolidate=%d", dream3.runs, consolidate3.runs)
	}
}

// ONE IDENTITY, ONE VOICE held only for the half of the mind the
// operator can see. The facilities each make their own LLM calls and
// nothing on the alarm path took the turn gate, so CONSOLIDATE could be
// distilling beliefs while the operator's turn was mid-tool call — two
// thoughts at once, on one provider, from one identity.

// fakeGate is the turn gate as metabolism sees it: take-or-fail.
type fakeGate struct {
	held  bool
	takes int
	ends  int
}

func freeGate() *fakeGate { return &fakeGate{} }
func heldGate() *fakeGate { return &fakeGate{held: true} }

func (g *fakeGate) TryBeginTurn() bool {
	g.takes++
	if g.held {
		return false
	}
	g.held = true
	return true
}
func (g *fakeGate) EndTurn() { g.ends++; g.held = false }

func TestMetabolismDefersWhileTheIdentityIsInATurn(t *testing.T) {
	dream, consolidate := &fakeOwner{}, &fakeOwner{}
	selfModel, review := &fakeOwner{}, &fakeOwner{}
	gate := heldGate()
	r := NewRhythm(&fakeRaw{n: 3}, gate, dream, consolidate, selfModel, review)

	res := r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")

	if res.Accepted {
		t.Fatal("a deferred pass reported acceptance — the alarm row would be consumed and the metabolism skipped, not deferred")
	}
	for name, o := range map[string]*fakeOwner{
		"dream": dream, "consolidate": consolidate, "self_model": selfModel, "identity_review": review,
	} {
		if o.runs != 0 {
			t.Fatalf("%s thought while the operator's turn was running — one identity, one voice", name)
		}
	}
	if gate.ends != 0 {
		t.Fatal("a pass that never took the gate released it")
	}
}

// Deferral must not advance the spacing timers, or a pass declined
// during a conversation would be silently skipped rather than delayed.
func TestADeferredPassIsNotASkippedPass(t *testing.T) {
	dream, consolidate := &fakeOwner{}, &fakeOwner{}
	selfModel, review := &fakeOwner{}, &fakeOwner{}
	gate := heldGate()
	r := NewRhythm(&fakeRaw{n: 3}, gate, dream, consolidate, selfModel, review)

	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if !r.lastConsolidate.IsZero() || !r.lastSelfModel.IsZero() || !r.lastReview.IsZero() {
		t.Fatal("a deferred pass advanced its spacing timers — the work would be skipped, not deferred")
	}

	// The turn ends; the next pass runs everything it was holding.
	gate.held = false
	res := r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if !res.Accepted {
		t.Fatalf("the pass after the turn did not run: %+v", res)
	}
	if selfModel.runs != 1 || review.runs != 1 {
		t.Fatalf("the deferred work did not happen on the next pass: self_model=%d review=%d", selfModel.runs, review.runs)
	}
	if gate.ends != 1 {
		t.Fatalf("the gate was taken %d times and released %d — a leaked turn deafens the identity", gate.takes, gate.ends)
	}
}
