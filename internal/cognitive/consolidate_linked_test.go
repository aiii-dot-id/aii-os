package cognitive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestAConsolidationThatCutsItsBeliefsKeepsThoseTheBatchIsLinkedToFirst(t *testing.T) {
	st := linkedBatchStore()
	model := &sizedModel{limit: 16_000, reply: upsertOn}
	lg := &mockLedger{st: st}
	c := NewConsolidate(st, checkedModel{model}, rememberingLedger{lg}, &mockRingWriter{}, ConsolidateConfig{Threshold: 3})

	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.sent) != 1 {
		t.Fatalf("%d request(s) sent of %d asked", len(model.sent), len(model.attempts))
	}
	sent := model.sent[0]
	if !strings.Contains(sent, "of 202 current beliefs shown") {
		t.Fatalf("the list was not cut, so this test shows nothing:\n%.600s", sent)
	}
	linked, nearest := strings.Index(sent, "b_linked"), strings.Index(sent, "b_midnight")
	if linked < 0 || nearest < 0 || linked > nearest {
		t.Fatalf("the belief the batch is linked to in the record is cut or follows the nearest (at %d, nearest at %d):\n%.900s", linked, nearest, sent)
	}
	if !strings.Contains(sent, "those linked to what you read here, then those nearest it") {
		t.Fatalf("the request does not say which beliefs it kept first:\n%.900s", sent)
	}
	told := toldIdentity(lg)
	if len(told) != 1 {
		t.Fatalf("the identity is told once that their consolidation saw part of their beliefs: %v", told)
	}
	for _, content := range told {
		if !strings.Contains(content, "each pass weighs those linked to what it read, then those nearest it") {
			t.Fatalf("what the identity is told does not say which beliefs a pass keeps: %q", content)
		}
	}
	if st.edgeReads != 1 {
		t.Fatalf("the batch's edges were read %d times in one pass; want one read", st.edgeReads)
	}
}

// AN UNREADABLE EDGE READ IS SAID, AND THE PASS GOES ON. The read of the
// batch's edges failed in silence, so a pass that kept no linked belief
// first looked like one that had none. It is logged once, and the pass
// keeps the beliefs nearest what it reads, as it did before links counted.
func TestAConsolidationWhoseEdgesCannotBeReadSaysSoOnceAndGoesOn(t *testing.T) {
	logs := captureLog(t)
	st := linkedBatchStore()
	st.edgesErr = errors.New("database is locked")
	model := &sizedModel{limit: 16_000, reply: upsertOn}
	c := NewConsolidate(st, checkedModel{model}, rememberingLedger{&mockLedger{st: st}}, &mockRingWriter{}, ConsolidateConfig{Threshold: 3})

	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.sent) != 1 || !strings.Contains(model.sent[0], "b_midnight") {
		t.Fatalf("the pass did not go on with the nearest beliefs: %d request(s) sent", len(model.sent))
	}
	said := 0
	for _, line := range logs.Lines() {
		if strings.Contains(line, "the edges of the batch cannot be read (database is locked)") {
			said++
		}
	}
	if said != 1 {
		t.Fatalf("the failed read is said %d time(s) in one pass; want once:\n%s", said, logs.String())
	}
}

// linkedBatchStore is a batch of three notes about midnight releases, two
// hundred unrelated beliefs, the nearest one, and one the first note
// supports in the record in other words.
func linkedBatchStore() *mockStore {
	st := &mockStore{unprocessedCnt: 3, experiences: []store.Experience{
		{ID: "e1", Content: "the operator shipped the release at midnight", Raw: 1},
		{ID: "e2", Content: "another midnight release from the operator", Raw: 1},
		{ID: "e3", Content: "a third release shipped at midnight", Raw: 1},
	}}
	for i := 0; i < 200; i++ {
		st.beliefs = append(st.beliefs, store.Belief{ID: fmt.Sprintf("b%03d", i), Ring: 3,
			Statement: fmt.Sprintf("Belief %03d about gardening, tides and the weather in another country entirely. ", i) + strings.Repeat("Unrelated detail. ", 12)})
	}
	st.beliefs = append(st.beliefs,
		store.Belief{ID: "b_linked", Ring: 3, Statement: "Late work keeps a household awake, which is costly."},
		store.Belief{ID: "b_midnight", Ring: 3, Statement: "The operator ships releases at midnight."})
	st.edges = []store.Edge{{ID: "edge_1", FromID: "e1", ToID: "b_linked", EdgeType: "SUPPORTS", CreatedSeq: 1}}
	return st
}
