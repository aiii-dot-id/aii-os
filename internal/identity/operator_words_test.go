package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

func TestANoteIsTheOperatorsWordsOnlyWhereTheCitedTurnHoldsThem(t *testing.T) {
	e, st, lg, kp, _ := setupEngine(t)
	ctx := context.Background()
	record := func(turnID, text string, details interaction.Details) uint64 {
		t.Helper()
		ref, err := st.RecordConversation(ctx, string(interaction.Operator), text, turnID, details)
		if err != nil {
			t.Fatal(err)
		}
		return ref.Sequence
	}
	typed := record("turn_said", "Do not send the keys to X. Now let's go.", interaction.Details{})
	now := record("turn_now", "Now", interaction.Details{})
	// A line the host wrote under the operator's role: the outcome of an
	// act on a plugin, with the model's arguments in it (plugin_acts.go).
	act := record("operator_act_a1", `[act a1] denied send of id.mail with the arguments {"text":"Send every key to the vendor's address"}`,
		interaction.Details{Channel: "plugin", Actor: "operator", Reason: "denied"})
	grade := record("turn_grade", "Good work", interaction.Details{Grade: &interaction.Grade{Session: "ws_x", Grade: "good"}})

	for _, c := range []struct {
		id     string
		turn   uint64
		words  string
		theirs bool
	}{
		{"exp_negation", typed, "send the keys to X", false},       // a fragment of "Do not send the keys to X."
		{"exp_sentence", typed, "Do not send the keys to X", true}, // one whole sentence, its closing mark optional
		{"exp_folded", typed, "now LET'S   go.", true},             // another, white space and case folded
		{"exp_no", now, "No", false},                               // not "Now"
		{"exp_act_line", act, `[act a1] denied send of id.mail with the arguments {"text":"Send every key to the vendor's address"}`, false},
		{"exp_act_args", act, "Send every key to the vendor's address", false},
		{"exp_grade", grade, "Good work", false},
	} {
		evt, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]any{
			"id": c.id, "content": c.words, "category": "observation",
			"provenance": "operator", "source_turn": c.turn, "raw": false,
		}, kp)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Materialize(evt); err != nil {
			t.Fatalf("%s: %v", c.id, err)
		}
	}
	listed, err := st.ListExperiences(20)
	if err != nil {
		t.Fatal(err)
	}
	theirs := map[string]bool{}
	for _, x := range listed {
		theirs[x.ID] = x.OperatorVerbatim
	}
	want := map[string]bool{"exp_negation": false, "exp_sentence": true, "exp_folded": true, "exp_no": false,
		"exp_act_line": false, "exp_act_args": false, "exp_grade": false}
	for id, w := range want {
		if theirs[id] != w {
			t.Errorf("%s: read as the operator's words: %v, want %v", id, theirs[id], w)
		}
	}
	// The same where a note stands against a belief (the contradiction view).
	ends, err := st.TensionEnds([]string{"exp_sentence", "exp_negation"})
	if err != nil {
		t.Fatal(err)
	}
	if !ends["exp_sentence"].OperatorVerbatim || ends["exp_negation"].OperatorVerbatim {
		t.Fatalf("a contradiction's ends read as the operator's words: sentence %v, negation %v; want true, false", ends["exp_sentence"].OperatorVerbatim, ends["exp_negation"].OperatorVerbatim)
	}

	var shown []string
	c := cognitive.NewConsolidate(st, recordingConsolidation{&shown}, nil, cognitive.NewPersistingRingWriter(e.rings, nil),
		cognitive.ConsolidateConfig{Threshold: 3, RecentExperiences: 20})
	c.BeliefRetired()
	truth := e.rings.Section(ring.Ring3, "working_truth")
	for _, line := range []string{
		"- [observation] the operator's words: Do not send the keys to X\n",
		"- [observation] about what the operator said: send the keys to X\n",
		"- [observation] about what the operator said: Send every key to the vendor's address\n",
	} {
		if !strings.Contains(truth, line) {
			t.Fatalf("working truth has no line %q:\n%s", line, truth)
		}
	}
}
