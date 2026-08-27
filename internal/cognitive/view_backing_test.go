package cognitive

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The consolidation envelope carries TWO outputs and only one is truth:
// operations become belief.* events, ring3_view is prose the tool schema
// asks for freely. Nothing bound the second to the first, so a pass
// could mint NOTHING, write a whole working truth, and consume every
// input for it — leaving that synthesis only in ring_snapshots, which
// replay deliberately does not rebuild.

func consolidateWith(t *testing.T, envelope string) (*mockStore, *mockRingWriter, *mockLedger) {
	t.Helper()
	st := &mockStore{
		unprocessedCnt: 3,
		experiences: []store.Experience{
			{ID: "e1", Content: "pattern 1", Raw: 1},
			{ID: "e2", Content: "pattern 2", Raw: 1},
			{ID: "e3", Content: "pattern 3", Raw: 1},
		},
		beliefs:   []store.Belief{{ID: "b_old", Statement: "something already known", Ring: 3, EvidenceCount: 2}},
		standings: map[string]string{"b_old": "confirmed"},
	}
	rw := &mockRingWriter{}
	lg := &mockLedger{st: st}
	c := NewConsolidate(st, &mockLLM{override: envelope}, lg, rw, ConsolidateConfig{Threshold: 3})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return st, rw, lg
}

// The reported case: a view, no operations. The prose must not become
// the identity's working truth, because nothing in the chain backs it.
func TestAViewWithNothingMintedIsNotKept(t *testing.T) {
	const invented = "You believe the ledger and the outbox have diverged, and that this matters."
	_, rw, _ := consolidateWith(t, `{"operations": [], "ring3_view": "`+invented+`"}`)

	got := rw.section(ring.Ring3, "working_truth")
	if strings.Contains(got, invented) {
		t.Fatal("a pass that minted nothing wrote its own synthesis into Ring 3 — " +
			"the only copy would live in a snapshot replay does not rebuild, while the ledger says the inputs were consumed")
	}
	// It is not left blank either: the fallback renders what IS backed.
	if !strings.Contains(got, "something already known") {
		t.Fatalf("Ring 3 was not rendered from the beliefs that do exist: %q", got)
	}
}

// A pass that DID mint keeps the model's words — they describe beliefs
// that just landed in the chain, so the snapshot is a render of truth.
func TestAViewIsKeptWhenTheBeliefsBehindItLanded(t *testing.T) {
	const view = "Working truth: three experiences share a pattern."
	_, rw, lg := consolidateWith(t,
		`{"operations": [{"op": "upsert", "id": "n1", "statement": "Three experiences share a pattern", "confidence": 0.6}], "ring3_view": "`+view+`"}`)

	if got := rw.section(ring.Ring3, "working_truth"); got != view {
		t.Fatalf("a backed view did not land in Ring 3: %q", got)
	}
	if len(lg.runPayloads()) != 1 || len(lg.runPayloads()[0].Outputs) != 1 {
		t.Fatalf("expected one minted output behind the view: %+v", lg.runPayloads())
	}
}

// Consumption is unchanged: a pass that looked and found nothing worth
// minting is an honest no-change pass, and re-reading the same material
// forever is the loop the degenerate-envelope gate exists to prevent.
func TestANoChangePassStillConsumesItsInputs(t *testing.T) {
	st, _, _ := consolidateWith(t, `{"operations": [], "ring3_view": "nothing new here"}`)
	if st.unprocessedCnt != 0 {
		t.Fatalf("%d experiences remain — an honest no-change pass must not re-read them forever", st.unprocessedCnt)
	}
}
