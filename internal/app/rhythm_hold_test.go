package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// holdModel answers CONSOLIDATE's structured call with a reply cut off at
// the output limit whenever the request carries more than most experiences,
// and otherwise with one belief resting on the experiences on the table.
// It answers DREAM's call with a note, a different one each time. It keeps
// what each was shown.
type holdModel struct {
	most         int
	consolidated [][]string // the experiences on the table of each consolidation request
	dreamt       []string   // the user message of each dream request
}

func (m *holdModel) ChatSimple(_ context.Context, _, user string) (string, string, error) {
	m.dreamt = append(m.dreamt, user)
	return fmt.Sprintf("You may be noticing a rhythm in the work, for the %d. time.", len(m.dreamt)), "hold-model", nil
}

func (m *holdModel) ChatStructured(_ context.Context, _, user string, _ llm.ToolDefinition) (string, string, bool, error) {
	var table []string
	for _, line := range strings.Split(user, "\n") {
		if rest, ok := strings.CutPrefix(line, "- ["); ok {
			id, _, _ := strings.Cut(rest, "]")
			table = append(table, id)
		}
	}
	m.consolidated = append(m.consolidated, table)
	if len(table) > m.most {
		return "", "", false, &llm.IncompleteResponseError{Reason: "length", Got: 8000}
	}
	evidence, err := json.Marshal(table)
	if err != nil {
		return "", "", false, err
	}
	return fmt.Sprintf(`{"operations": [{"op": "upsert", "statement": "The operator ships at midnight", "confidence": 0.7, "evidence": %s}],
		"ring3_view": "You believe the operator ships at midnight."}`, evidence), "hold-model", false, nil
}

// runsOf is what each run marker of one kind in the ledger file consumed,
// in the order the markers landed.
func (b *cognitionBench) runsOf(t *testing.T, marker ledger.EventType) [][]string {
	t.Helper()
	var out [][]string
	for _, e := range b.eventsOfType(t, marker) {
		var p struct{ Inputs []string }
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p.Inputs)
	}
	return out
}

func TestTheRhythmLeavesARefusedBatchToConsolidationUntilAPassCloses(t *testing.T) {
	ctx := context.Background()
	cfg := *defaultConfig()
	cfg.Cognition.ConsolidateBatch = 4
	if err := validCognition(cfg); err != nil {
		t.Fatalf("rig: a batch of four is refused: %v", err)
	}
	b := newCognitionBench(t)
	b.mintExperiences(t, "exp_a", "exp_b", "exp_c", "exp_d", "exp_e")
	model := &holdModel{most: 2}
	consolidate := cognitive.NewConsolidate(b.st, model, b.door, nil, consolidateConfig(cfg))
	dream := cognitive.NewDream(b.st, model, b.door, nil, dreamConfig(cfg))
	dream.SetHeld(consolidate)
	r := cognitive.NewRhythm(b.st, openGate{}, dream, consolidate, nil, nil)
	r.SetSpacing(rhythmSpacing(cfg))
	tick := func() { r.OnAlarm(ctx, "rhythm", "wall", 0, "") }
	waiting := func() int {
		t.Helper()
		n, err := b.st.UnprocessedExperienceCount()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	tick() // CONSOLIDATE's: the four oldest, and the reply is cut off
	if got := fmt.Sprint(model.consolidated); got != "[[exp_a exp_b exp_c exp_d]]" || len(model.dreamt) != 0 || waiting() != 5 {
		t.Fatalf("rig: the first tick is consolidation's, its pass of four is refused and nothing is consumed: asked %s, %d dream call(s), %d waiting", got, len(model.dreamt), waiting())
	}

	tick() // DREAM's, inside consolidation's spacing
	if len(model.dreamt) != 1 || !strings.Contains(model.dreamt[0], "observed exp_e") {
		t.Fatalf("rig: the tick after is DREAM's, and one experience is its own to read: %d dream call(s) %q", len(model.dreamt), model.dreamt)
	}
	for _, id := range []string{"exp_a", "exp_b", "exp_c", "exp_d"} {
		if strings.Contains(model.dreamt[0], "observed "+id) {
			t.Fatalf("DREAM READ WHAT CONSOLIDATION SAID ITS NEXT PASS WOULD: %s is in the dream request:\n%s", id, model.dreamt[0])
		}
	}
	if got := fmt.Sprint(b.runsOf(t, ledger.EventDreamRun)); got != "[[exp_e]]" || waiting() != 4 {
		t.Fatalf("DREAM CONSUMED A BATCH CONSOLIDATION WAS REFUSED: its run markers consumed %s, %d waiting; want [[exp_e]] and the four held still waiting", got, waiting())
	}

	tick() // DREAM's again: nothing of its own waits
	if len(model.dreamt) != 1 || len(model.consolidated) != 1 || waiting() != 4 {
		t.Fatalf("a tick with nothing of DREAM's own asks no model and consumes nothing: %d dream call(s), %d consolidation request(s), %d waiting", len(model.dreamt), len(model.consolidated), waiting())
	}

	// The spacing over, RHYTHM gives CONSOLIDATE its tick as it does at
	// every dispatch: the facility's own alarm.
	if res := consolidate.OnAlarm(ctx, "rhythm:consolidate", "wall", 0, ""); !res.Accepted {
		t.Fatal("consolidation's next pass failed")
	}
	if got := fmt.Sprint(model.consolidated); got != "[[exp_a exp_b exp_c exp_d] [exp_a exp_b]]" {
		t.Fatalf("consolidation's next pass asks for the two it said it would: asked %s", got)
	}
	if got := fmt.Sprint(b.runsOf(t, ledger.EventConsolidationRun)); got != "[[exp_a exp_b]]" || waiting() != 2 {
		t.Fatalf("THE PASS CONSOLIDATION ANNOUNCED DID NOT CLOSE: its run markers consumed %s, %d waiting; want [[exp_a exp_b]] and two waiting", got, waiting())
	}

	tick() // DREAM's: no refusal stands, and two wait under consolidation's threshold
	if got := fmt.Sprint(b.runsOf(t, ledger.EventDreamRun)); got != "[[exp_e] [exp_c exp_d]]" || waiting() != 0 {
		t.Fatalf("with nothing held DREAM reads what is left: its run markers consumed %s, %d waiting; want [[exp_e] [exp_c exp_d]] and none waiting", got, waiting())
	}
}
