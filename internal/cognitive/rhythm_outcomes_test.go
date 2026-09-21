package cognitive

import (
	"context"
	"errors"
	"testing"
	"time"
)

// .
type fakeIntake struct {
	pending bool
	err     error
	probes  int
	runs    int
}

func (f *fakeIntake) OutcomesPending() bool { f.probes++; return f.pending }
func (f *fakeIntake) ObserveOutcomes(ctx context.Context) error {
	f.runs++
	if f.err == nil {
		f.pending = false
	}
	return f.err
}

func rhythmWithIntake(raw int, in *fakeIntake) (*Rhythm, *fakeOwner, *fakeOwner) {
	dream := &fakeOwner{name: "dream"}
	consolidate := &fakeOwner{name: "consolidate"}
	r := NewRhythm(&fakeRaw{n: raw}, freeGate(), dream, consolidate,
		&fakeOwner{name: "self_model"}, &fakeOwner{name: "identity_review"})
	r.SetOutcomes(in)
	return r, dream, consolidate
}

// .
// .
// .
func TestAnOutcomeIsMaterialWithNoRawExperienceAtAll(t *testing.T) {
	in := &fakeIntake{pending: true}
	r, dream, consolidate := rhythmWithIntake(0, in)
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if in.runs != 1 {
		t.Fatalf("an outcome stood unobserved and the intake ran %d times, want 1", in.runs)
	}
	if dream.runs != 0 || consolidate.runs != 0 {
		t.Fatalf("no raw experience exists, yet dream=%d consolidate=%d", dream.runs, consolidate.runs)
	}
}

// .
// .
func TestTheIntakeTakesTheDeltaAndTheRawPathTheNext(t *testing.T) {
	in := &fakeIntake{pending: true}
	r, dream, consolidate := rhythmWithIntake(3, in)
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if in.runs != 1 || dream.runs != 0 || consolidate.runs != 0 {
		t.Fatalf("first delta: intake=%d dream=%d consolidate=%d, want the intake alone", in.runs, dream.runs, consolidate.runs)
	}
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if in.runs != 1 || consolidate.runs != 1 {
		t.Fatalf("second delta: intake=%d consolidate=%d, want the raw path's turn", in.runs, consolidate.runs)
	}
}

// .
// .
// .
// .
func TestAFailedIntakeWaitsItsSpacingAndTheRawPathGoesOn(t *testing.T) {
	in := &fakeIntake{pending: true, err: errors.New("provider down")}
	r, dream, consolidate := rhythmWithIntake(3, in)
	for i := 0; i < 4; i++ {
		r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	}
	if in.runs != 1 {
		t.Fatalf("a failing intake ran %d times in four ticks, want 1 — it must wait its spacing", in.runs)
	}
	if in.probes != 1 {
		t.Fatalf("the record was probed %d times inside the spacing, want 1", in.probes)
	}
	if got := dream.runs + consolidate.runs; got != 3 {
		t.Fatalf("the raw path ran %d of the three ticks the intake did not take", got)
	}
	// .
	r.lastOutcomeIntake = time.Now().Add(-consolidateSpacing)
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if in.runs != 2 {
		t.Fatalf("after its spacing the intake ran %d times, want 2", in.runs)
	}
}

// .
// .
func TestTheIntakeWaitsForTheTurnGate(t *testing.T) {
	in := &fakeIntake{pending: true}
	gate := heldGate()
	r := NewRhythm(&fakeRaw{}, gate, &fakeOwner{}, &fakeOwner{}, &fakeOwner{}, &fakeOwner{})
	r.SetOutcomes(in)
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if in.probes != 0 || in.runs != 0 {
		t.Fatalf("the identity was in a turn, yet the intake was probed %d and run %d times", in.probes, in.runs)
	}
	gate.held = false
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if in.runs != 1 {
		t.Fatalf("the gate freed and the intake ran %d times, want 1 — a deferred tick spent the spacing", in.runs)
	}
}

// .
func TestWithNoOutcomePendingTheDispatchIsUnchanged(t *testing.T) {
	in := &fakeIntake{}
	r, _, consolidate := rhythmWithIntake(3, in)
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if in.runs != 0 || consolidate.runs != 1 {
		t.Fatalf("intake=%d consolidate=%d, want the raw path untouched", in.runs, consolidate.runs)
	}
}
