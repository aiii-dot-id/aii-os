package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestWhoYouWorkWithReachesTheTurnAndTheFacilitiesUnderTheOperatorsBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"prompt":{"recent_turns":20,"operator_beliefs_max_chars":60}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{cfg: cfg}
	if got := a.operatorBeliefsMaxChars(); got != 60 {
		t.Fatalf("the bound in force is %d; the file says 60", got)
	}

	projection, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = projection.Close() })
	materialize := func(seq uint64, typ ledger.EventType, ringLevel int, payload map[string]any) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := projection.Materialize(&ledger.Event{Seq: seq, Type: typ, Ring: ringLevel, Payload: raw}); err != nil {
			t.Fatalf("materialize %s: %v", typ, err)
		}
	}
	if err := projection.AddConversationTurn("operator", "Yes — approved."); err != nil {
		t.Fatal(err)
	}
	materialize(1, ledger.EventRelationshipUpsert, 1, map[string]any{
		"id": "rel_operator", "counterpart_name": "Quinn", "counterpart_role": "operator",
		"relationship_type": "founding_operator", "charter_text": "THE CHARTER",
		"operator_approval_excerpt": "Yes — approved.", "operator_approval_turn": 1, "approval_basis": "conversation_turn",
	})
	materialize(2, ledger.EventBeliefUpsert, 3, map[string]any{"id": "b_asks", "statement": "Quinn asks what you think first", "ring": 3, "confidence": 0.7})
	materialize(3, ledger.EventBeliefUpsert, 3, map[string]any{"id": "b_reads", "statement": "Quinn reads what is rendered, not the schema", "ring": 3, "confidence": 0.7})
	materialize(4, ledger.EventEdgeCreate, 3, map[string]any{"id": "e1", "from_id": "rel_operator", "to_id": "b_asks", "edge_type": "SHAPED_BY"})
	materialize(5, ledger.EventEdgeCreate, 3, map[string]any{"id": "e2", "from_id": "rel_operator", "to_id": "b_reads", "edge_type": "SHAPED_BY"})

	rings := ring.NewManager()
	_ = rings.SealSafePosture("constitution")
	composer := prompt.New(rings, 100000)
	composer.SetIdentitySource(projection)
	composer.SetOperatorBound(a.operatorBeliefsMaxChars)
	turn, err := composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	facility, err := (ringAuthority{gate: prompt.NewGate(appRingSource{rm: rings}, 100000), st: projection, about: a.operatorBeliefsMaxChars}).AuthorityPreamble()
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"the turn": turn.Text, "the facilities' preamble": facility} {
		charter, who := strings.Index(text, "THE CHARTER"), strings.Index(text, "## Who You Work With")
		if charter < 0 || who < charter {
			t.Fatalf("%s does not carry Who You Work With after the charter:\n%s", name, text)
		}
		// Most recent first: b_reads was minted last.
		if !strings.Contains(text, "- Quinn reads what is rendered, not the schema [b_reads]") || strings.Contains(text, "[b_asks]") {
			t.Fatalf("%s is not rendered under the file's bound of 60:\n%s", name, text[who:])
		}
		if !strings.Contains(text, "1 more belief about Quinn stands in your record past the 60 characters this part shows (prompt.operator_beliefs_max_chars)") {
			t.Fatalf("%s does not say what its bound left out:\n%s", name, text[who:])
		}
	}
}

func TestWhatConsolidationLearnsOfTheOperatorSurvivesReplay(t *testing.T) {
	b := newCognitionBench(t)
	if err := b.st.AddConversationTurn("operator", "Yes, that is our charter."); err != nil {
		t.Fatal(err)
	}
	turn, err := b.st.GetLatestOperatorTurn()
	if err != nil || turn == nil {
		t.Fatalf("operator turn = %+v, %v", turn, err)
	}
	if _, err := b.door.Append(ledger.EventRelationshipUpsert, 1, map[string]any{
		"id": "rel_operator", "counterpart_name": "Quinn", "counterpart_role": "operator",
		"relationship_type": "founding_operator", "charter_text": "Quinn and I build carefully.",
		"operator_approval_excerpt": turn.Content, "operator_approval_turn": turn.TurnSeq, "approval_basis": "conversation_turn",
	}, ""); err != nil {
		t.Fatal(err)
	}
	b.mintExperiences(t, "exp_a", "exp_b", "exp_c")
	envelope := `{"operations": [
		{"op": "upsert", "id": "n1", "statement": "Quinn asks what you think before saying what they think", "confidence": 0.7, "evidence": ["exp_a", "exp_b", "exp_c"], "about_operator": true}],
		"ring3_view": "You believe Quinn asks first."}`
	c := cognitive.NewConsolidate(b.st, envelopeLLM{out: envelope}, b.door, nil, cognitive.ConsolidateConfig{Threshold: 3})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	upserts := b.eventsOfType(t, ledger.EventBeliefUpsert)
	if len(upserts) != 1 {
		t.Fatalf("belief.upsert events = %d, want 1", len(upserts))
	}
	var belief struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(upserts[0].Payload, &belief); err != nil {
		t.Fatal(err)
	}
	var marks int
	for _, e := range b.eventsOfType(t, ledger.EventEdgeCreate) {
		var p struct {
			FromID   string `json:"from_id"`
			ToID     string `json:"to_id"`
			EdgeType string `json:"edge_type"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.FromID == "rel_operator" && p.ToID == belief.ID && p.EdgeType == "SHAPED_BY" {
			marks++
		}
	}
	if marks != 1 {
		t.Fatalf("THE MARK THAT THE BELIEF IS ABOUT THE OPERATOR IS NOT IN THE RECORD (%d marks)", marks)
	}
	if n, err := b.st.UnprocessedExperienceCount(); err != nil || n != 0 {
		t.Fatalf("the pass did not consume its experiences: %d %v", n, err)
	}
	before, err := b.st.AboutOperator()
	if err != nil || len(before.Beliefs) != 1 || before.Beliefs[0].ID != belief.ID {
		t.Fatalf("the belief is not read as about the operator: %+v %v", before, err)
	}
	if err := b.st.ReplayFromFile(b.ledgerP); err != nil {
		t.Fatalf("replay: %v", err)
	}
	after, err := b.st.AboutOperator()
	if err != nil || len(after.Beliefs) != 1 || after.Beliefs[0] != before.Beliefs[0] || after.RelationshipID != "rel_operator" {
		t.Fatalf("REPLAY DID NOT REBUILD WHAT THE IDENTITY BELIEVES ABOUT THE OPERATOR: before %+v, after %+v (%v)", before, after, err)
	}
}
