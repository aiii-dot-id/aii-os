package cognitive

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestThePromptStatesTheNumbersItIsHeldTo(t *testing.T) {
	ctx := context.Background()

	dreamStore := &mockStore{unprocessedCnt: 1, experiences: []store.Experience{{ID: "e1", Content: "a thing", Raw: 1}}}
	dreamModel := &mockLLM{override: nothingSurfaced}
	if err := NewDream(dreamStore, dreamModel, &mockLedger{st: dreamStore}, nil, DreamConfig{MaxChars: 600}).Execute(ctx); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Output 33-100 words", "a reply over 600\ncharacters is refused whole"} {
		if !strings.Contains(dreamModel.lastSystem, want) {
			t.Errorf("DREAM's prompt does not state %q under a 600-character bound:\n%s", want, dreamModel.lastSystem)
		}
	}

	st, lg, rw, _ := consolidateBench("")
	c := NewConsolidate(st, &mockLLM{override: `{"operations": [{"op": "upsert", "statement": "Midnight ships", "confidence": 0.6, "evidence": ["e1"]}], "ring3_view": "You believe it."}`}, lg, rw, ConsolidateConfig{Threshold: 3, Ring3MaxChars: 5000, MaxOps: 7})
	if err := c.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	metabolism := c.llm.(*mockLLM).lastSystem
	for _, want := range []string{"at most 5000 characters", "At most 7 operations", manyExperiencesOverTheLimit, oneExperienceOverTheLimit} {
		if !strings.Contains(metabolism, want) {
			t.Errorf("CONSOLIDATE's prompt does not state %q:\n%s", want, metabolism)
		}
	}
	st.unprocessedCnt = 0 // nothing raw: the render-only pass
	c.llm.(*mockLLM).override = "You believe it, rendered."
	if err := c.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if view := c.llm.(*mockLLM).lastSystem; !strings.Contains(view, "at most 5000 characters") {
		t.Errorf("the render-only pass does not state the view's bound:\n%s", view)
	}

	smStore := selfModelStore()
	smStore.selfModelMaxTokens = 1234
	smModel := &mockLLM{}
	if err := NewSelfModel(smStore, smModel, &mockSelfModelCommitter{}).Execute(ctx); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"at most 1234 tokens", "about 3702 characters", "at least four distinct"} {
		if !strings.Contains(smModel.lastSystem, want) {
			t.Errorf("SELF_MODEL's prompt does not state %q:\n%s", want, smModel.lastSystem)
		}
	}

	briefModel := &mockLLM{override: "A short bridge."}
	if err := NewMorningBrief(&mockStore{}, briefModel, &mockBriefWriter{}, MorningBriefConfig{MaxChars: 900}).Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(briefModel.lastSystem, "under 150 words; a brief over 900 characters is not kept") {
		t.Errorf("the brief's prompt does not state its bound:\n%s", briefModel.lastSystem)
	}

	outModel, door := &mockLLM{override: nothingSurfaced}, &captureDoor{}
	oc := NewConsolidate(nil, outModel, door, nil, ConsolidateConfig{ObservationMaxChars: 1500})
	oc.SetOutcomes(&fakeOutcomes{self: strings.Repeat("cd", 32)})
	if err := oc.ObserveOutcomes(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outModel.lastSystem, "at most 1500 characters") {
		t.Errorf("the outcome intake's prompt does not state its bound:\n%s", outModel.lastSystem)
	}
	// And it says what the code does with a longer one (outcomesRefused):
	// fewer outcomes shown, and one alone passed over, told. It said "the
	// outcomes wait", and one of them does not.
	for _, want := range []string{"refused whole and you are then shown fewer outcomes", "an outcome shown alone whose\nobservation is still longer is passed over, and you are told"} {
		if !strings.Contains(outModel.lastSystem, want) {
			t.Errorf("the outcome intake's prompt does not say %q:\n%s", want, outModel.lastSystem)
		}
	}
	if strings.Contains(outModel.lastSystem, "the outcomes wait") {
		t.Errorf("the outcome intake's prompt tells the model the outcomes wait, and one refused alone is passed over:\n%s", outModel.lastSystem)
	}
}

// What consolidation's prompt says becomes of a reply holding more
// operations than a pass may mint: one clause for a pass of several
// experiences, one for a pass of one. Each is held to what the pass does,
// below.
const (
	manyExperiencesOverTheLimit = "With more than one experience on the table, a reply with more\n  mints nothing and consumes nothing, and the next pass is given fewer experiences"
	oneExperienceOverTheLimit   = "with\n  one, the operations past that number are not minted"
)

func TestWhatThePromptSaysOfTooManyOperationsIsWhatThePassDoes(t *testing.T) {
	ctx := context.Background()
	st, lg, _, c := consolidateBench("")
	c.Reconfigure(ConsolidateConfig{Threshold: 1, MaxOps: 2})
	var said []Condition
	c.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }, Door: rememberingLedger{lg}, Exists: st.EntityExists})
	model := c.llm.(*mockLLM)

	// Several experiences on the table.
	model.override = overBudget(5, "e1")
	if err := c.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(model.lastSystem, manyExperiencesOverTheLimit) || strings.Count(model.lastUser, "[e") != 3 {
		t.Fatalf("rig: a pass of three experiences is told what becomes of a reply over the limit:\n%s", model.lastSystem)
	}
	if len(lg.appended) != 0 || st.unprocessedCnt != 3 || c.nextBatch(c.cfg()) != 1 {
		t.Fatalf("THE PROMPT SAYS A REPLY OVER THE LIMIT MINTS NOTHING, CONSUMES NOTHING AND THE NEXT PASS IS GIVEN FEWER: %v appended, %d of 3 still waiting, the next pass reads %d", lg.appended, st.unprocessedCnt, c.nextBatch(c.cfg()))
	}
	if kinds := saidOf(said, "consolidate"); len(kinds) != 1 || kinds[0] != ConditionHalved {
		t.Fatalf("the card is told the pass was refused and the next reads fewer: %v", kinds)
	}

	// One experience on the table: the pass that follows.
	next, _ := st.ListRawExperiences(1)
	model.override = overBudget(5, next[0].ID)
	if err := c.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(model.lastSystem, oneExperienceOverTheLimit) || strings.Count(model.lastUser, "[e") != 1 {
		t.Fatalf("rig: a pass of one experience is told what becomes of a reply over the limit:\n%s", model.lastSystem)
	}
	runs := lg.runPayloads()
	if upsertsIn(lg) != 2 || len(runs) != 1 || len(runs[0].Inputs) != 1 || runs[0].Inputs[0] != next[0].ID || len(runs[0].Outputs) != 2 || st.unprocessedCnt != 2 {
		t.Fatalf("THE PROMPT SAYS THE OPERATIONS PAST THE LIMIT ARE NOT MINTED: %d upsert(s) minted, run(s) %+v, %d still waiting; want the first 2 minted and %s consumed", upsertsIn(lg), runs, st.unprocessedCnt, next[0].ID)
	}
	for i, et := range lg.appended {
		if p, ok := lg.payloads[i].(map[string]any); ok && et == ledger.EventBeliefUpsert && !strings.Contains(fmt.Sprint(p["statement"]), "number 0") && !strings.Contains(fmt.Sprint(p["statement"]), "number 1") {
			t.Fatalf("an operation past the limit was minted: %v", p["statement"])
		}
	}
	last := said[len(said)-1]
	if last.Facility != "consolidate" || last.Kind != ConditionRefused || !strings.Contains(last.Said, "("+next[0].ID+") held 5 operations, and a pass mints at most 2") || !strings.Contains(last.Said, "the 3 past that were not minted, and the experience is consumed") {
		t.Fatalf("THE CUT OF A PASS OVER ONE EXPERIENCE REACHED ONLY THE LOG: the card was last told %+v", last)
	}
	if told := toldIdentity(lg); len(told) != 0 || len(lg.appended) != 3 {
		t.Fatalf("the cut is said on the card and adds nothing to the record: %v told, the door admitted %v", told, lg.appended)
	}
}

func TestTheRhythmsSpacingIsTheOperatorsAndLeavesDreamItsTicks(t *testing.T) {
	dream, consolidate := &fakeOwner{name: "dream"}, &fakeOwner{name: "consolidate"}
	r := NewRhythm(&fakeRaw{n: 3}, nil, dream, consolidate, nil, nil)
	if r.Spacing() != DefaultSpacing {
		t.Fatalf("rig: an unset spacing is the default, %s", DefaultSpacing)
	}
	r.lastConsolidate = time.Now().Add(-30 * time.Minute)
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if consolidate.runs != 1 || dream.runs != 0 {
		t.Fatalf("rig: under the default spacing the tick is consolidation's: consolidate %d, dream %d", consolidate.runs, dream.runs)
	}
	r.SetSpacing(90 * time.Minute)
	r.lastConsolidate = time.Now().Add(-30 * time.Minute)
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if dream.runs != 1 || consolidate.runs != 1 {
		t.Fatalf("UNDER THE OPERATOR'S SPACING THE TICK WAS NOT DREAM'S: consolidate %d, dream %d", consolidate.runs, dream.runs)
	}
}
