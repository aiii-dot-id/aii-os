package identity

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
// .
func TestCommitSchemaAdvertisesTheRelationshipFieldsItConsumes(t *testing.T) {
	var commitParams map[string]interface{}
	for _, v := range Verbs() {
		if v.Name == "commit" {
			props, _ := v.Params["properties"].(map[string]interface{})
			commitParams = props
		}
	}
	if commitParams == nil {
		t.Fatal("commit verb has no properties")
	}
	// .
	for _, field := range []string{
		"counterpart_name", "counterpart_role", "charter_text",
		"relationship_type", "trust_level", "autonomy_level", "supersedes",
	} {
		if _, ok := commitParams[field]; !ok {
			t.Errorf("the handler consumes %q and the schema does not advertise it", field)
		}
	}
	// .
	// .
	// .
	for _, engineOwned := range []string{
		"operator_approval_excerpt", "operator_approval_turn", "approval_basis",
	} {
		if _, ok := commitParams[engineOwned]; ok {
			t.Errorf("%q is engine-stamped and must not be advertised as model-supplied", engineOwned)
		}
	}
}

// .
// .
func TestALiveOperatorRelationshipWithoutACharterIsRefused(t *testing.T) {
	engine, st, lg, _, _ := setupEngine(t)
	if err := engine.RecordConversationTurn("operator", "Yes — approved."); err != nil {
		t.Fatal(err)
	}
	before := lg.LastSeq()

	_, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "relationship.upsert", "id": "rel_no_charter_01",
			"counterpart_name": "Sam", "relationship_type": "operator",
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

// .
// .
func TestAnOperatorRelationshipWithACharterMintsAndRendersExactly(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	if err := engine.RecordConversationTurn("operator", "Yes — approved."); err != nil {
		t.Fatal(err)
	}
	const charter = "Sam is my operator. He rules; I keep the record honest.\nWhen we disagree, I say so plainly and then do as he decides."
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "relationship.upsert", "id": "rel_with_charter_01",
			"counterpart_name": "Sam", "relationship_type": "operator",
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

// .
// .
func TestAPeerRelationshipNeedsNoCharter(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	if err := engine.RecordConversationTurn("operator", "Yes — approved."); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "relationship.upsert", "id": "rel_peer_sev_01",
			"counterpart_name": "Robin", "counterpart_role": "peer",
			"relationship_type": "peer",
		}); err != nil {
		t.Errorf("a peer relationship was refused for lacking a charter it does not need: %v", err)
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestAHistoricalEmptyCharterEventStillReplays(t *testing.T) {
	_, st, lg, kp, _ := setupEngine(t)

	evt, err := lg.Append(ledger.EventRelationshipUpsert, kp.Fingerprint(), 1,
		map[string]interface{}{
			"id":                        "rel_legacy_founding",
			"counterpart_name":          "Sam",
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
	// .
	// .
	// .
	// .
	// .
	if err := st.MaterializeReplay(evt); err != nil {
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
	// .
	// .
	// .
}
