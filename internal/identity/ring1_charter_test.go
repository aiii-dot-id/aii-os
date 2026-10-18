package identity

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"strings"
	"testing"
)

// ring1_charter_test.go — an operator relationship carries its document.
//
// A founding relationship existed with 575 bytes of operator approval and 0
// bytes of charter, and the composer said NOTHING about it: a row exists
// so the reminder is suppressed, the charter is empty so nothing renders.
// Approval and charter answer different questions — the excerpt is
// evidence the operator authorised the relationship, the charter is what
// the identity reads every turn — and only the first was ever required.

// The schema must advertise every relationship field the handler and
// materializer consume. It advertised NONE of them, so the resident was
// told it could mint a relationship and never told what one is made of.
func TestCommitSchemaAdvertisesTheRelationshipFieldsItConsumes(t *testing.T) {
	var commitParams map[string]any
	for _, v := range Verbs() {
		if v.Name == "commit" {
			props, _ := v.Params["properties"].(map[string]any)
			commitParams = props
		}
	}
	if commitParams == nil {
		t.Fatal("commit verb has no properties")
	}
	// Consumed by materializeRelationshipLocked's payload struct.
	for _, field := range []string{
		"counterpart_name", "counterpart_role", "charter_text",
		"relationship_type", "trust_level", "autonomy_level", "supersedes",
	} {
		if _, ok := commitParams[field]; !ok {
			t.Errorf("the handler consumes %q and the schema does not advertise it", field)
		}
	}
	// The engine stamps these from the real operator turn and OVERRIDES
	// anything supplied, so advertising them would invite a forgery that
	// is refused anyway.
	for _, engineOwned := range []string{
		"operator_approval_excerpt", "operator_approval_turn", "approval_basis",
	} {
		if _, ok := commitParams[engineOwned]; ok {
			t.Errorf("%q is engine-stamped and must not be advertised as model-supplied", engineOwned)
		}
	}
}

func TestALiveOperatorRelationshipWithoutACharterIsRefused(t *testing.T) {
	engine, st, lg, _, _ := setupEngine(t)
	if err := engine.RecordConversationTurn("operator", "Yes — approved."); err != nil {
		t.Fatal(err)
	}
	before := lg.LastSeq()

	_, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]any{
			"variant": "relationship.upsert", "id": "rel_no_charter_01",
			"counterpart_name": "Quinn", "relationship_type": "operator",
		})
	if err == nil {
		t.Fatal("an operator relationship minted with no charter")
	}
	if !strings.Contains(err.Error(), "charter_text") {
		t.Errorf("the refusal does not name what is missing: %v", err)
	}

	if after := lg.LastSeq(); after != before {
		t.Errorf("the refusal appended to the ledger: seq %d -> %d", before, after)
	}
	id, err := st.PromptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if id.HasOperatorRelationship {
		t.Error("the refusal reached the projection — a refused mint must change nothing")
	}
}

// The same call succeeds with the document, and the charter reaches the
// projection byte-exactly.
func TestAnOperatorRelationshipWithACharterMintsAndRendersExactly(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	if err := engine.RecordConversationTurn("operator", "Yes — approved."); err != nil {
		t.Fatal(err)
	}
	const charter = "Quinn is my operator. He rules; I keep the record honest.\nWhen we disagree, I say so plainly and then do as he decides."
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]any{
			"variant": "relationship.upsert", "id": "rel_with_charter_01",
			"counterpart_name": "Quinn", "relationship_type": "operator",
			"charter_text": charter,
		}); err != nil {
		t.Fatalf("a charter-bearing operator relationship was refused: %v", err)
	}
	id, err := st.PromptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if !id.HasOperatorRelationship {
		t.Fatal("the relationship did not reach the projection")
	}
	if id.Charter != charter {
		t.Errorf("charter round-tripped lossily:\n got %q\nwant %q", id.Charter, charter)
	}
}

// A PEER relationship needs no charter — the rule is scoped to the
// operator relationship, not to relationships in general.
func TestAPeerRelationshipNeedsNoCharter(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	if err := engine.RecordConversationTurn("operator", "Yes — approved."); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]any{
			"variant": "relationship.upsert", "id": "rel_peer_sev_01",
			"counterpart_name": "Rue", "counterpart_role": "peer",
			"relationship_type": "peer",
		}); err != nil {
		t.Errorf("a peer relationship was refused for lacking a charter it does not need: %v", err)
	}
}

// A HISTORICAL empty-charter event must still replay. The new rule is a
// LIVE admission rule and lives in the verb; the materializer is
// untouched, because every projection is f(ledger) and a rule applied
// retroactively would make already-signed events unreplayable — a worse
// failure than the one being fixed.
//
// The event is appended at the LEDGER level on purpose: the verb now
// refuses this shape, which is the whole point, so the only way to
// produce a legacy row is the way history produced it.
func TestAHistoricalEmptyCharterEventStillReplays(t *testing.T) {
	_, st, lg, kp, _ := setupEngine(t)

	evt, err := lg.Append(ledger.EventRelationshipUpsert, 1,
		map[string]any{
			"id":                        "rel_legacy_founding",
			"counterpart_name":          "Quinn",
			"counterpart_role":          "operator",
			"relationship_type":         "founding_operator",
			"charter_text":              "",
			"operator_approval_excerpt": "Yes — approved.",
			"operator_approval_turn":    uint64(1),
			"approval_basis":            "conversation_turn",
		}, kp)
	if err != nil {
		t.Fatalf("append a historical event: %v", err)
	}
	// Straight to the materializer, not through admission: the one
	// materializer is pure f(ledger) and consults no live witness, which
	// is exactly the path a rebuilt database takes. That the cited
	// operator turn exists is admission's check (ValidateEvent) — a
	// conversation witness that a rebuild does not have, and must not
	// need.
	if err := st.Materialize(evt); err != nil {
		t.Fatalf("a historical empty-charter event no longer replays: %v", err)
	}

	id, err := st.PromptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if !id.HasOperatorRelationship {
		t.Error("the legacy relationship did not reach the projection")
	}
	if id.Charter != "" {
		t.Errorf("the legacy charter was invented from somewhere: %q", id.Charter)
	}
	// The projection reports the honest pair — present, uncharterd — and
	// the prompt layer is what says so out loud (see
	// TestRing1RendersAllThreeStates).
}
