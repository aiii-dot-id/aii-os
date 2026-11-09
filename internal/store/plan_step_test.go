package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// A PLAN_STEP IS A LINE OF THE PLAN, COMPARED WHOLE AND TRIMMED, OR NONE.
func TestAPlanStepIsALineOfThePlanOrNone(t *testing.T) {
	plan := "# The stair\r\n1. read the stair\n  2. measure the stair  \n\n3. cut"
	for _, c := range []struct {
		step, line string
		err        error
	}{
		{"2. measure the stair", "2. measure the stair", nil},
		{"  2. measure the stair\t", "2. measure the stair", nil},
		{"# The stair", "# The stair", nil},
		{"none", "", nil},
		{" none ", "", nil},
		{"2. measure", "", ErrPlanStepNotALine},
		{"measure the stair", "", ErrPlanStepNotALine},
		{"", "", ErrPlanStepNotALine},
		{"1. read the stair\n  2. measure the stair", "", ErrPlanStepNotALine},
	} {
		line, err := PlanStepLine(plan, c.step)
		if line != c.line || !errors.Is(err, c.err) || (c.err == nil) != (err == nil) {
			t.Errorf("%q: %q %v, want %q %v", c.step, line, err, c.line, c.err)
		}
	}
	if _, err := PlanStepLine(" \n ", "1. read the stair"); !errors.Is(err, ErrPlanStepNeedsPlan) {
		t.Errorf("a step with no plan: %v", err)
	}
	if line, err := PlanStepLine("", "none"); line != "" || err != nil {
		t.Errorf("none with no plan: %q %v", line, err)
	}
}

// planStepRig is a store with session ws holding plan, and the loop's three
// steps of a work update made one at a time: its start, its plan update
// (committed with its notice), and its result, which a test may hold.
type planStepRig struct {
	t *testing.T
	s *Store
}

func newPlanStepRig(t *testing.T, plan string) planStepRig {
	t.Helper()
	s, err := NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.StartWorkSessionWith(t.Context(), "ws", "the stair", WorkStart{Plan: &plan}); err != nil {
		t.Fatal(err)
	}
	return planStepRig{t, s}
}

func (r planStepRig) start(turn, tool string) {
	r.t.Helper()
	if err := r.s.RecordToolStart(turn, 1, "main", "m", "c", tool, "{}"); err != nil {
		r.t.Fatal(err)
	}
}

func (r planStepRig) update(turn string, plan, step *string) {
	r.t.Helper()
	if err := r.s.UpdateWorkPlanStep(interaction.WithTurn(r.t.Context(), turn), "ws", nil, nil, plan, nil, nil, nil, step); err != nil {
		r.t.Fatal(err)
	}
}

func (r planStepRig) done(turn, tool string) {
	r.t.Helper()
	if err := r.s.RecordToolDoneMeasured(turn, 1, tool, "{}", "Work session updated.", interaction.Succeeded, false, ToolMeasurement{}); err != nil {
		r.t.Fatal(err)
	}
}

// served is the line kept beside turn's call, "" for none.
func (r planStepRig) served(turn string) string {
	r.t.Helper()
	var payload string
	err := r.s.DB().QueryRow(`SELECT a.payload FROM turn_annotations a JOIN conversations c ON c.turn_seq=a.turn_seq
		WHERE c.turn_id=? AND c.kind='tool_call' AND a.kind=?`, turn, AnnotationPlanStepServed).Scan(&payload)
	if err != nil {
		return ""
	}
	var step planStepLine
	if err := json.Unmarshal([]byte(payload), &step); err != nil {
		r.t.Fatal(err)
	}
	return step.Line
}

func ptr(s string) *string { return &s }

// A START READS THE PLAN UPDATE COMMITTED BEFORE IT, WHATEVER ITS RESULT.
// A plan replaced, its call's result held: a call that begins then serves
// no step, not the selection the replacement cleared. A selection made,
// its result held: the call that begins then serves it. Control: the
// selection read from result-row order.
func TestAStartReadsThePlanUpdateCommittedBeforeIt(t *testing.T) {
	r := newPlanStepRig(t, "1. read\n2. measure")
	r.start("t0", "work")
	r.update("t0", nil, ptr("2. measure"))
	r.done("t0", "work")
	r.start("t0b", "probe")
	if got := r.served("t0b"); got != "2. measure" {
		t.Fatalf("a call after a selection serves %q", got)
	}

	r.start("t1", "work")
	r.update("t1", ptr("1. buy\n2. glue"), nil) // its result is held
	r.start("t1b", "probe")
	if got := r.served("t1b"); got != "" {
		t.Errorf("a call that began after the plan was replaced, its result held, serves %q", got)
	}
	r.done("t1", "work")

	r.start("t2", "work")
	r.update("t2", nil, ptr("2. glue")) // its result is held
	r.start("t2b", "probe")
	if got := r.served("t2b"); got != "2. glue" {
		t.Errorf("a call that began after a selection, its result held, serves %q", got)
	}
	r.done("t2", "work")
}

// A DELAYED RESULT DOES NOT BRING A SELECTION BACK. A selection committed,
// then a replacement committed and answered, then the selection's result
// written last: the next call serves no step. Control: the selection read
// from result-row order.
func TestADelayedSelectionResultDoesNotBringItBack(t *testing.T) {
	r := newPlanStepRig(t, "1. read\n2. measure")
	r.start("tA", "work")
	r.update("tA", nil, ptr("2. measure")) // its result is delayed
	r.start("tB", "work")
	r.update("tB", ptr("1. buy\n2. glue"), nil)
	r.done("tB", "work")
	r.done("tA", "work")
	r.start("tC", "probe")
	if got := r.served("tC"); got != "" {
		t.Errorf("a selection whose result came after the replacement came back: %q", got)
	}
}
