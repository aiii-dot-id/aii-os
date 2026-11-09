package identity

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// planReplacedAtFirstWrite is the store with another writer: just before
// the verb's first write, whatever it is, the session's plan is replaced
// (a review's interleaving, 2026-11-01 21:37Z). It keeps the order of the
// verb's writes and what the plan update answered.
type planReplacedAtFirstWrite struct {
	*store.Store
	t                     *testing.T
	session, replacement  string
	writes                []string
	planUpdateRefusedWith error
}

func (p *planReplacedAtFirstWrite) write(name string) {
	if len(p.writes) == 0 {
		if err := p.Store.UpdateWorkPlan(p.t.Context(), p.session, nil, nil, &p.replacement, nil, nil, nil); err != nil {
			p.t.Fatal(err)
		}
	}
	p.writes = append(p.writes, name)
}

func (p *planReplacedAtFirstWrite) SetStandingState(text string) error {
	p.write("standing")
	return p.Store.SetStandingState(text)
}

func (p *planReplacedAtFirstWrite) SetWorkSessionFamily(ctx context.Context, id, family string) error {
	p.write("family")
	return p.Store.SetWorkSessionFamily(ctx, id, family)
}

func (p *planReplacedAtFirstWrite) SetWorkCheck(ctx context.Context, id, command string) error {
	p.write("check")
	return p.Store.SetWorkCheck(ctx, id, command)
}

func (p *planReplacedAtFirstWrite) UpdateWorkState(ctx context.Context, id, state string) error {
	p.write("state")
	return p.Store.UpdateWorkState(ctx, id, state)
}

func (p *planReplacedAtFirstWrite) UpdateWorkPlanStep(ctx context.Context, id string, focus, nextMove, plan, expectedEvidence, falsifier, decisionNeeded, planStep *string) error {
	p.write("plan")
	err := p.Store.UpdateWorkPlanStep(ctx, id, focus, nextMove, plan, expectedEvidence, falsifier, decisionNeeded, planStep)
	p.planUpdateRefusedWith = err
	return err
}

// A STEP REFUSED BECAUSE THE PLAN CHANGED UNDER IT CHANGES NOTHING ELSE. The
// update names a line of the plan as it stands when the update is asked;
// another writer replaces the plan before the update's first write. The
// update's plan and step are written first, so the refusal is the plan
// update's own transaction's, against the plan as committed, and it comes
// before the standing state, family, check or state is written: none of
// them is. Control: the plan update moved back after the other writes.
func TestAStepRefusedAfterThePlanChangedChangesNothingElse(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "work.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	plan := "1. read the stair\n2. measure the stair"
	if err := st.StartWorkSessionWith(t.Context(), "ws_late", "the stair", store.WorkStart{Plan: &plan}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStandingState("between things"); err != nil {
		t.Fatal(err)
	}
	other := &planReplacedAtFirstWrite{Store: st, t: t, session: "ws_late", replacement: "1. cut the treads"}
	e := &Engine{store: other}
	_, err = e.verbWork(t.Context(), map[string]any{"action": "update", "state": "moved on", "standing": "waiting on the saw",
		"family": "stair-work", "check": "true", "plan_step": "1. read the stair"})
	var refused *Rejection
	if !errors.As(err, &refused) || refused.Code != "update.plan_step" || err.Error() != planStepNotALine {
		t.Fatalf("the update answered %v, want the typed refusal %q", err, planStepNotALine)
	}
	if !slices.Equal(other.writes, []string{"plan"}) || !errors.Is(other.planUpdateRefusedWith, store.ErrPlanStepNotALine) {
		t.Fatalf("the verb wrote %v and the plan update answered %v: the refusal must be the plan update's transaction's, before any other write", other.writes, other.planUpdateRefusedWith)
	}
	ws, err := st.WorkSessionByID("ws_late")
	if err != nil || ws == nil {
		t.Fatal(err)
	}
	if ws.State != "" || ws.Family != "" || ws.Plan != "1. cut the treads" {
		t.Errorf("a refused update changed the session: state %q, family %q, plan %q", ws.State, ws.Family, ws.Plan)
	}
	if standing, err := st.StandingState(); err != nil || standing != "between things" {
		t.Errorf("a refused update changed the standing state: %q %v", standing, err)
	}
	if check, err := st.WorkCheckOf("ws_late"); err != nil || check.Command != "" {
		t.Errorf("a refused update declared a check: %q %v", check.Command, err)
	}
}
