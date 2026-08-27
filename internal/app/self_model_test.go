package app

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

type selfModelToolLLM struct{}

func (selfModelToolLLM) Chat(context.Context, []llm.Message, llm.ChatOptions) (*llm.Response, error) {
	var call llm.ToolCall
	call.Type = "function"
	call.Function.Name = "commit"
	call.Function.Arguments = `{"variant":"self_model.synthesize","id":"syn_app","synthesis_text":"I learn by following evidence.","continuity_thread":"I remain honest and curious.","source_entity_refs":[{"class":"beliefs","id":"b_app"},{"class":"experiences","id":"x_app"},{"class":"intentions","id":"i_app"},{"class":"relationships","id":"rel_app"}]}`
	return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{ToolCalls: []llm.ToolCall{call}}}}, ModelID: "self-model-producer"}, nil
}

func TestSelfModelUsesNativeCommitEndToEnd(t *testing.T) {
	b := newCognitionBench(t)
	b.lg.SetModelID("configured-model")
	mint := func(typ ledger.EventType, ringLevel int, payload map[string]interface{}) {
		if _, err := b.door.Append(typ, ringLevel, payload, ""); err != nil {
			t.Fatal(err)
		}
	}
	mint(ledger.EventBeliefUpsert, 3, map[string]interface{}{"id": "b_app", "statement": "Evidence matters", "ring": 3, "confidence": 0.8})
	mint(ledger.EventExperienceCreate, 3, map[string]interface{}{"id": "x_app", "content": "A test exposed a defect"})
	mint(ledger.EventIntentionCreate, 3, map[string]interface{}{"id": "i_app", "statement": "Improve"})
	if err := b.st.AddConversationTurn("operator", "Yes — rel_app approved."); err != nil {
		t.Fatal(err)
	}
	approval, err := b.st.GetLatestOperatorTurn()
	if err != nil || approval == nil {
		t.Fatalf("operator approval turn = %+v, %v", approval, err)
	}
	mint(ledger.EventRelationshipUpsert, 1, map[string]interface{}{
		"id": "rel_app", "counterpart_name": "Operator", "counterpart_role": "operator",
		"relationship_type": "founding_operator", "charter_text": "Work honestly",
		"operator_approval_excerpt": approval.Content, "operator_approval_turn": approval.TurnSeq,
		"approval_basis": "conversation_turn",
	})
	engine := identity.NewEngine(b.st, b.door, ring.NewManager(), nil)
	facility := cognitive.NewSelfModel(b.st, selfModelToolLLM{}, selfModelCommitter{engine: engine})
	if err := facility.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := b.st.CurrentSelfModel()
	if err != nil || current == nil || current.ID != "syn_app" {
		t.Fatalf("current self-model = %+v, %v", current, err)
	}
	events := b.eventsOfType(t, ledger.EventSelfModelSynthesize)
	if len(events) != 1 || events[0].ModelID != "self-model-producer" {
		t.Fatalf("self-model provenance = %+v, want producing model", events)
	}
}
