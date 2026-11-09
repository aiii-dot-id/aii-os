package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// mintRelationship records a relationship as the ledger carries it.
func mintRelationship(t *testing.T, st *store.Store, lg *ledger.Ledger, kp *crypto.KeyPair, id, name, role, kind string) {
	t.Helper()
	payload := map[string]any{"id": id, "counterpart_name": name, "counterpart_role": role, "relationship_type": kind}
	if role == "operator" {
		if err := st.AddConversationTurn("operator", "Yes — "+id+" approved."); err != nil {
			t.Fatal(err)
		}
		approval, err := st.GetLatestOperatorTurn()
		if err != nil || approval == nil {
			t.Fatalf("operator approval turn: %+v, %v", approval, err)
		}
		payload["operator_approval_excerpt"] = approval.Content
		payload["operator_approval_turn"] = approval.TurnSeq
		payload["approval_basis"] = "conversation_turn"
	}
	evt, err := lg.Append(ledger.EventRelationshipUpsert, 1, payload, kp)
	if err != nil {
		t.Fatalf("mint relationship %s: %v", id, err)
	}
	if err := st.Materialize(evt); err != nil {
		t.Fatalf("materialize relationship %s: %v", id, err)
	}
}

// A PROMISE IS TO SOMEONE THE RECORD HOLDS. The counterpart is
// refused before anything is written, in the operator's words, with the
// relationships held listed, Ring 1's first; an id the record holds is kept.
func TestAPromiseNamesACounterpartTheRecordHolds(t *testing.T) {
	e, st, lg, kp, _ := setupEngine(t)
	ctx := interaction.WithTurn(context.Background(), "turn-promise")
	promise := func(counterpart string) (string, error) {
		return e.ExecuteAction(ctx, "verb", "commit", map[string]any{"variant": "commitment.promised", "counterpart_id": counterpart, "description": "own the next release"})
	}
	// None held: said so.
	before := lg.LastSeq()
	_, err := promise("avery")
	if err == nil || !strings.Contains(err.Error(), `counterpart_id "avery" names no relationship in your record, and you hold none yet; a promise is to someone you hold a relationship with.`) {
		t.Fatalf("with no relationship held, the refusal is not in the approved words: %v", err)
	}
	if lg.LastSeq() != before {
		t.Fatal("a refused promise was appended to the record")
	}
	// Some held: listed, the operator's first.
	mintRelationship(t, st, lg, kp, "rel_test", "Op", "operator", "founding_operator")
	mintRelationship(t, st, lg, kp, "rel_peer", "Pat", "peer", "peer")
	before = lg.LastSeq()
	_, err = promise("avery")
	want := `counterpart_id "avery" names no relationship in your record. A promise is to someone you hold a relationship with; name them by its id. Yours: rel_test "Op" (your operator), rel_peer "Pat" (peer).`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("the refusal is not in the approved words\nwant: %s\ngot:  %v", want, err)
	}
	if !strings.HasPrefix(err.Error(), "rejected_before_effect (counterpart_id): ") {
		t.Fatalf("the refusal is not one before effect: %v", err)
	}
	if lg.LastSeq() != before {
		t.Fatal("a refused promise was appended to the record")
	}
	commitments, _ := st.ListCommitments(false)
	if len(commitments) != 0 {
		t.Fatalf("a refused promise left a commitment: %+v", commitments)
	}
	// The control: the operator's relationship id is kept.
	if _, err := promise("rel_test"); err != nil {
		t.Fatalf("a promise to a relationship the record holds was refused: %v", err)
	}
	if commitments, _ = st.ListCommitments(false); len(commitments) != 1 || commitments[0].CounterpartID != "rel_test" {
		t.Fatalf("the kept promise is not in the record: %+v", commitments)
	}
}
