package cognitive

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestAClaimRestatedInARetiredBeliefsWordsIsMintedAsANewBelief(t *testing.T) {
	const dawn = "The ferry leaves at dawn"
	retiredID := "belief_" + outputHash(dawn)
	nextID := "belief_" + outputHash(fmt.Sprintf("%s\x00%d", dawn, 2))

	notes := []store.Experience{
		{ID: "e1", Content: "the ferry left at dawn again", Raw: 1},
		{ID: "e2", Content: "caught the dawn ferry", Raw: 1},
		{ID: "e3", Content: "dawn, and the ferry was on time", Raw: 1},
	}
	st := &mockStore{
		unprocessedCnt: 3,
		experiences:    notes,
		beliefs:        []store.Belief{{ID: "b_noon", Statement: "The ferry leaves at noon", Ring: 3}},
		// Minted by an earlier pass under the statement's hash, since superseded.
		retired: []store.Belief{{ID: retiredID, Statement: dawn, Ring: 3}},
	}
	restated := `{
		"operations": [
			{"op": "upsert", "statement": "The ferry leaves at dawn", "confidence": 0.7, "evidence": ["e1", "e2", "e3"]}
		],
		"ring3_view": "You believe the ferry leaves at dawn."
	}`
	upsertIDs := func(lg *mockLedger) []string {
		var ids []string
		for i, kind := range lg.appended {
			if kind == ledger.EventBeliefUpsert {
				p, _ := lg.payloads[i].(map[string]any)
				id, _ := p["id"].(string)
				ids = append(ids, id)
			}
		}
		return ids
	}

	lg := &mockLedger{st: st}
	c := NewConsolidate(st, &mockLLM{override: restated}, lg, &mockRingWriter{}, ConsolidateConfig{Threshold: 3})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	ids := upsertIDs(lg)
	if len(ids) != 1 {
		t.Fatalf("the restated claim was not minted once: %v (appended %v)", ids, lg.appended)
	}
	if ids[0] == retiredID {
		t.Fatalf("A CLAIM STATED AGAIN LANDED ON ITS RETIRED ROW (%s): nothing enters working truth and the notes are used up", retiredID)
	}
	if ids[0] != nextID {
		t.Fatalf("the new belief's id is %q, want the statement's next id %q: a re-run must find it", ids[0], nextID)
	}

	// The same statement again, with the new belief now standing: that
	// belief is written to, and no third id is made.
	stands := false
	for _, b := range st.beliefs {
		stands = stands || b.ID == nextID
	}
	if !stands { // the mock ledger may already have projected it
		st.beliefs = append(st.beliefs, store.Belief{ID: nextID, Statement: dawn, Ring: 3})
	}
	st.experiences = []store.Experience{
		{ID: "e4", Content: "dawn ferry, once more", Raw: 1},
		{ID: "e5", Content: "and again at dawn", Raw: 1},
		{ID: "e6", Content: "the dawn ferry", Raw: 1},
	}
	st.unprocessedCnt = 3
	again := `{
		"operations": [
			{"op": "upsert", "statement": "The ferry leaves at dawn", "confidence": 0.8, "evidence": ["e4", "e5", "e6"]}
		],
		"ring3_view": "You believe the ferry leaves at dawn."
	}`
	lg2 := &mockLedger{st: st}
	c2 := NewConsolidate(st, &mockLLM{override: again}, lg2, &mockRingWriter{}, ConsolidateConfig{Threshold: 3})
	if err := c2.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ids := upsertIDs(lg2); len(ids) != 1 || ids[0] != nextID {
		t.Fatalf("a second pass over the same statement wrote %v, want the standing belief %q", ids, nextID)
	}
}

// NO COUNT OF RETIREMENTS ENDS THE SEARCH FOR A FREE ID. A claim retired
// sixteen times in the same words was dropped at the seventeenth
// statement, counted refused, and a pass with nothing else left its
// experiences unconsumed: the same pass came back for good. It is minted
// under the first id the record does not hold.
func TestAClaimRetiredManyTimesInTheSameWordsIsStillMinted(t *testing.T) {
	const dawn = "The ferry leaves at dawn"
	idAt := func(n int) string {
		if n == 1 {
			return "belief_" + outputHash(dawn)
		}
		return "belief_" + outputHash(fmt.Sprintf("%s\x00%d", dawn, n))
	}
	var retired []store.Belief
	for n := 1; n <= 16; n++ {
		retired = append(retired, store.Belief{ID: idAt(n), Statement: dawn, Ring: 3})
	}
	st := &mockStore{
		unprocessedCnt: 3,
		experiences: []store.Experience{
			{ID: "e1", Content: "the ferry left at dawn again", Raw: 1},
			{ID: "e2", Content: "caught the dawn ferry", Raw: 1},
			{ID: "e3", Content: "dawn, and the ferry was on time", Raw: 1},
		},
		retired: retired,
	}
	lg := &mockLedger{st: st}
	c := NewConsolidate(st, &mockLLM{override: `{
		"operations": [
			{"op": "upsert", "statement": "The ferry leaves at dawn", "confidence": 0.7, "evidence": ["e1", "e2", "e3"]}
		],
		"ring3_view": "You believe the ferry leaves at dawn."
	}`}, lg, &mockRingWriter{}, ConsolidateConfig{Threshold: 3})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	var minted []string
	for i, kind := range lg.appended {
		if kind == ledger.EventBeliefUpsert {
			p, _ := lg.payloads[i].(map[string]any)
			id, _ := p["id"].(string)
			minted = append(minted, id)
		}
	}
	if len(minted) != 1 || minted[0] != idAt(17) {
		t.Fatalf("A CLAIM RETIRED SIXTEEN TIMES IN THE SAME WORDS WAS NOT MINTED under the next free id %q: minted %v (appended %v)", idAt(17), minted, lg.appended)
	}
	if st.unprocessedCnt != 0 {
		t.Fatalf("the pass did not close: %d experience(s) left for the next pass", st.unprocessedCnt)
	}
}

// A PASS WHOSE UPSERTS THE DOOR REFUSED DID NOT CITE NOTHING. A pass whose
// every upsert was refused, and that minted nothing else, is refused and
// its experiences are read again in a smaller pass; the identity is told
// why. It was told "refused for citing nothing behind them" whatever
// refused them, so an upsert that cited three notes and met the door's
// rule on a retired belief's id was said to cite none.
func TestAPassWhoseUpsertsTheDoorRefusedIsNotSaidToCiteNothing(t *testing.T) {
	for _, tc := range []struct {
		name, ops, want string
	}{
		{"the door", `{"op": "upsert", "id": "n1", "statement": "The operator ships at midnight", "confidence": 0.7, "evidence": ["e1", "e2", "e3"]}`,
			"(1 upsert(s) refused before append, and nothing else minted)"},
		{"no source", `{"op": "upsert", "id": "n1", "statement": "The operator ships at midnight", "confidence": 0.7, "evidence": ["e9"]}`,
			"(1 upsert(s) refused for citing nothing behind them, and nothing else minted)"},
		{"both", `{"op": "upsert", "id": "n1", "statement": "The operator ships at midnight", "confidence": 0.7, "evidence": ["e1", "e2", "e3"]},
			{"op": "upsert", "id": "n2", "statement": "The operator rests on Sundays", "confidence": 0.7, "evidence": []}`,
			"(2 upsert(s) refused, 1 for citing nothing behind them and 1 before append, and nothing else minted)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, lg, _, c := consolidateBench(`{"operations": [` + tc.ops + `], "ring3_view": "You believe the operator ships at midnight."}`)
			lg.refuse = func(et ledger.EventType) error {
				if et == ledger.EventBeliefUpsert {
					return fmt.Errorf("%w before append: a retired belief stays as it was retired", store.ErrRefused)
				}
				return nil
			}
			var said []Condition
			c.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }})
			if err := c.Execute(context.Background()); err != nil {
				t.Fatal(err)
			}
			if st.unprocessedCnt != 3 {
				t.Fatalf("rig: a pass that minted nothing must consume nothing: %d left", st.unprocessedCnt)
			}
			if len(said) != 1 || !strings.Contains(said[0].Said, tc.want) {
				t.Fatalf("the identity is not told what refused the pass's upserts, %s: %+v", tc.want, said)
			}
		})
	}
}
