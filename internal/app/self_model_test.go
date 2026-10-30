package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	b, engine := selfModelEvidence(t)
	facility := cognitive.NewSelfModel(b.st, selfModelToolLLM{}, selfModelCommitter{engine: engine})
	if err := facility.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := b.st.CurrentSelfModel()
	if err != nil || current == nil || current.ID != "syn_app" {
		t.Fatalf("current self-model = %+v, %v", current, err)
	}
	events := b.eventsOfType(t, ledger.EventSelfModelSynthesize)
	if len(events) != 1 {
		t.Fatalf("self-model events = %+v, want one", events)
	}

	var stamped struct {
		ModelID string `json:"model_id"`
	}
	if err := json.Unmarshal(events[0].Payload, &stamped); err != nil || stamped.ModelID != "self-model-producer" {
		t.Fatalf("self-model provenance = %q (%v), want producing model", stamped.ModelID, err)
	}
}

// THE RECORD NAMES THE MODEL THAT ANSWERED, under its signature. The
// self-model's commit is a model's act: a real client asks a provider that
// resolves the configured name to a dated version and says so in its reply,
// and the signed payload carries that name; a provider that names none
// leaves the configured one. The operator's acts beside it keep the
// substrate's configured name, and the record verifies and replays as it
// was written: no stamp already in it is read differently or rewritten.
func TestTheRecordNamesTheModelThatAnswered(t *testing.T) {
	fixed, _ := selfModelToolLLM{}.Chat(context.Background(), nil, llm.ChatOptions{})
	args, _ := json.Marshal(fixed.Choices[0].Message.ToolCalls[0].Function.Arguments) // the same commit, as a JSON string
	for _, tc := range []struct{ name, named, want string }{
		{"a provider that names the model", `"model":"producer-2026-10-01",`, "producer-2026-10-01"},
		{"a provider that names none", ``, "producer"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"r",%s"choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"commit","arguments":%s}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`, tc.named, args)
		}))
		client := llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "producer", NoStream: true, Retries: -1})
		b, engine := selfModelEvidence(t)
		if err := cognitive.NewSelfModel(b.st, client, selfModelCommitter{engine: engine}).Execute(context.Background()); err != nil {
			srv.Close()
			t.Fatalf("%s: %v", tc.name, err)
		}
		srv.Close()
		stamps := map[ledger.EventType]string{}
		if _, err := ledger.VerifyChainVisiting(b.ledgerP, b.kp.PublicKeyBytes(), nil, func(e *ledger.Event) error {
			var p struct {
				ModelID string `json:"model_id"`
			}
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return err
			}
			stamps[e.Type] = p.ModelID
			return nil
		}); err != nil {
			t.Fatalf("%s: the record does not verify: %v", tc.name, err)
		}
		if got := stamps[ledger.EventSelfModelSynthesize]; got != tc.want {
			t.Errorf("%s: the self-model's signed record names %q, want %q", tc.name, got, tc.want)
		}
		if got := stamps[ledger.EventRelationshipUpsert]; got != "configured-model" {
			t.Errorf("%s: the operator's act names %q, want the configured substrate", tc.name, got)
		}
		if err := b.st.ReplayFromFile(b.ledgerP); err != nil {
			t.Fatalf("%s: replay: %v", tc.name, err)
		}
		if current, err := b.st.CurrentSelfModel(); err != nil || current == nil || current.ID != "syn_app" {
			t.Errorf("%s: after replay the self-model = %+v, %v", tc.name, current, err)
		}
	}
}

// selfModelEvidence is a record holding the four source classes a
// SELF_MODEL pass needs, the ids selfModelToolLLM's commit cites.
func selfModelEvidence(t *testing.T) (*cognitionBench, *identity.Engine) {
	t.Helper()
	b := newCognitionBench(t)
	b.lg.SetModelID("configured-model")
	mint := func(typ ledger.EventType, ringLevel int, payload map[string]any) {
		if _, err := b.door.Append(typ, ringLevel, payload, ""); err != nil {
			t.Fatal(err)
		}
	}
	mint(ledger.EventExperienceCreate, 3, map[string]any{"id": "x_app", "content": "A test exposed a defect"})
	mint(ledger.EventBeliefUpsert, 3, map[string]any{"id": "b_app", "statement": "Evidence matters", "ring": 3, "confidence": 0.8,
		"origin_refs": []map[string]string{{"kind": "experience", "id": "x_app"}}})
	mint(ledger.EventIntentionCreate, 3, map[string]any{"id": "i_app", "statement": "Improve"})
	if err := b.st.AddConversationTurn("operator", "Yes — rel_app approved."); err != nil {
		t.Fatal(err)
	}
	approval, err := b.st.GetLatestOperatorTurn()
	if err != nil || approval == nil {
		t.Fatalf("operator approval turn = %+v, %v", approval, err)
	}
	mint(ledger.EventRelationshipUpsert, 1, map[string]any{
		"id": "rel_app", "counterpart_name": "Operator", "counterpart_role": "operator",
		"relationship_type": "founding_operator", "charter_text": "Work honestly",
		"operator_approval_excerpt": approval.Content, "operator_approval_turn": approval.TurnSeq,
		"approval_basis": "conversation_turn",
	})
	return b, identity.NewEngine(b.st, b.door, ring.NewManager(), nil, identity.Ports{})
}
