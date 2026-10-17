package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// wedgeModel admits a request by the client's own rule (llm.AdmitInput,
// the offered tools priced) against a stated window, asked before anything
// is sent, and answers a request it admits: consolidation with one belief
// resting on the experiences on the table, the portrait with a valid
// commit.
type wedgeModel struct {
	ceiling int
	sent    []string // the user message of each request sent
	refused int      // requests refused before they were sent
}

func (m *wedgeModel) admit(system, user string, tools []llm.ToolDefinition) error {
	_, err := llm.AdmitInput([]llm.Message{{Role: "system", Content: system}, {Role: "user", Content: user}}, tools, m.ceiling)
	return err
}

func (m *wedgeModel) send(system, user string, tools []llm.ToolDefinition) error {
	if err := m.admit(system, user, tools); err != nil {
		m.refused++
		return err
	}
	m.sent = append(m.sent, user)
	return nil
}

func (m *wedgeModel) CheckSimple(_ context.Context, system, user string) error {
	return m.admit(system, user, nil)
}

func (m *wedgeModel) CheckWithTools(_ context.Context, system, user string, tools []llm.ToolDefinition) error {
	return m.admit(system, user, tools)
}

func (m *wedgeModel) ChatSimple(_ context.Context, system, user string) (string, string, error) {
	return "You hold what the record shows.", "wedge-model", m.send(system, user, nil)
}

func (m *wedgeModel) ChatStructured(_ context.Context, system, user string, tool llm.ToolDefinition) (string, string, bool, error) {
	return `{"operations": [{"op": "upsert", "statement": "The operator ships at midnight", "confidence": 0.7, "evidence": ["exp_a", "exp_b", "exp_c"]}],
		"ring3_view": "You believe the operator ships at midnight."}`, "wedge-model", false, m.send(system, user, []llm.ToolDefinition{tool})
}

func (m *wedgeModel) Chat(_ context.Context, messages []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
	if err := m.send(messages[0].Content, messages[len(messages)-1].Content, opts.Tools); err != nil {
		return nil, err
	}
	return selfModelToolLLM{}.Chat(context.Background(), messages, opts)
}

// seedBeliefsPastTheWindow writes a hundred and fifty Ring 3 beliefs of
// about nine hundred characters each — a long-lived identity's list (a live
// one's statements averaged 842, config.go) — some forty-five thousand
// tokens: more than the 32,000-token window they are put to.
func seedBeliefsPastTheWindow(t *testing.T, b *cognitionBench) {
	t.Helper()
	for i := 0; i < 150; i++ {
		if _, err := b.st.DB().Exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq)
			VALUES (?, ?, 3, 0.5, 0, 1, 1)`, fmt.Sprintf("b_long_%03d", i),
			fmt.Sprintf("Belief %03d about gardening, tides and the weather elsewhere. ", i)+strings.Repeat("An unrelated detail, at length. ", 26)); err != nil {
			t.Fatal(err)
		}
	}
}

// tellings is what the record holds of the rhythm telling the identity of
// its own condition: raw experiences in the substrate's voice.
func (b *cognitionBench) tellings(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, e := range b.eventsOfType(t, ledger.EventExperienceCreate) {
		var p struct{ ID, Content, Provenance string }
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.Provenance == "system" && strings.HasPrefix(p.ID, "exp_rhythm_") {
			out = append(out, p.Content)
		}
	}
	return out
}

func TestTheBeliefListWedgesStayClosedWithEverySettingAtItsDefault(t *testing.T) {
	ctx := context.Background()
	for _, cadence := range []int{60, 600} {
		cfg := *defaultConfig()
		cfg.Agency.RhythmSeconds = cadence
		if err := validCognition(cfg); err != nil {
			t.Fatalf("rig: the defaults at a %d-second cadence are refused: %v", cadence, err)
		}
		var said []cognitive.Condition
		report := func(c cognitive.Condition) { said = append(said, c) }
		fitted := func(facility string) bool {
			for _, c := range said {
				if c.Facility == facility && c.Kind == cognitive.ConditionFitted {
					return true
				}
			}
			return false
		}

		// RH-1: CONSOLIDATE.
		b := newCognitionBench(t)
		b.mintExperiences(t, "exp_a", "exp_b", "exp_c")
		seedBeliefsPastTheWindow(t, b)
		model := &wedgeModel{ceiling: llm.DefaultContextBudgetTokens}
		consolidate := cognitive.NewConsolidate(b.st, model, b.door, nil, consolidateConfig(cfg))
		consolidate.SetTeller(&cognitive.Teller{Report: report, Door: b.door, Exists: b.st.EntityExists})
		dream := &countingOwner{name: "dream"}
		r := cognitive.NewRhythm(b.st, openGate{}, dream, consolidate, nil, nil)
		r.SetSpacing(rhythmSpacing(cfg))
		r.OnAlarm(ctx, "rhythm", "wall", 0, "")
		if len(model.sent) != 1 || model.refused != 0 {
			t.Fatalf("cadence %d: THE BELIEF LIST STOPPED CONSOLIDATION (RH-1): %d request(s) sent, %d refused before sending", cadence, len(model.sent), model.refused)
		}
		if !strings.Contains(model.sent[0], "of 150 current beliefs shown") || !fitted("consolidate") {
			t.Fatalf("cadence %d: the pass does not say what it left out, in the request and on the card (%v):\n%.400s", cadence, said, model.sent[0])
		}
		waiting, err := b.st.ListRawExperiences(10)
		if err != nil {
			t.Fatal(err)
		}
		if got := b.tellings(t); len(got) != 1 || !strings.Contains(got[0], "of your 150 current beliefs") {
			t.Fatalf("cadence %d: the identity is told once that their consolidation saw part of their beliefs: %v", cadence, got)
		}
		// What waits now is the telling alone: the three that were read are consumed.
		if upserts, runs := len(b.eventsOfType(t, ledger.EventBeliefUpsert)), len(b.eventsOfType(t, ledger.EventConsolidationRun)); upserts != 1 || runs != 1 || len(waiting) != 1 || !strings.HasPrefix(waiting[0].ID, "exp_rhythm_") {
			t.Fatalf("cadence %d: the pass forms its belief and consumes what it read: %d upsert(s), %d run marker(s), waiting %+v", cadence, upserts, runs, waiting)
		}
		b.mintExperiences(t, "exp_d")
		r.OnAlarm(ctx, "rhythm", "wall", 0, "")
		if dream.runs != 1 || len(model.sent) != 1 || r.Spacing() != 30*time.Minute {
			t.Fatalf("cadence %d: with nothing set consolidation's spacing is the thirty minutes the code carried, and the tick inside it is DREAM's: spacing %s, DREAM ran %d time(s), consolidation was sent %d request(s)", cadence, r.Spacing(), dream.runs, len(model.sent))
		}

		// RH-2: SELF_MODEL.
		said = nil
		pb, engine := selfModelEvidence(t)
		seedBeliefsPastTheWindow(t, pb)
		pb.st.SetSelfModelMaxTokens(cfg.Prompt.SelfModelMaxTokens)
		model = &wedgeModel{ceiling: llm.DefaultContextBudgetTokens}
		portrait := cognitive.NewSelfModel(pb.st, model, selfModelCommitter{engine: engine})
		portrait.Reconfigure(selfModelConfig(cfg))
		portrait.SetDoor(pb.door)
		portrait.SetTeller(&cognitive.Teller{Report: report})
		cognitive.NewRhythm(pb.st, openGate{}, nil, nil, portrait, nil).OnAlarm(ctx, cognitive.ReflectSelfModelAlarm, "life", 1, "")
		if len(model.sent) != 1 || model.refused != 0 {
			t.Fatalf("cadence %d: THE BELIEF LIST FROZE THE PORTRAIT (RH-2): %d request(s) sent, %d refused before sending", cadence, len(model.sent), model.refused)
		}
		if current, err := pb.st.CurrentSelfModel(); err != nil || current == nil || current.ID != "syn_app" {
			t.Fatalf("cadence %d: the pass commits a portrait from what fits: %+v (%v)", cadence, current, err)
		}
		if !strings.Contains(model.sent[0], "of 151 beliefs shown") || !fitted("self_model") {
			t.Fatalf("cadence %d: the portrait's request does not say what it left out, in the request and on the card (%v):\n%.400s", cadence, said, model.sent[0])
		}
		if got := pb.tellings(t); len(got) != 1 || !strings.Contains(got[0], "of your 151 beliefs") {
			t.Fatalf("cadence %d: THE PORTRAIT TOLD NOBODY what it was not drawn from: %v", cadence, got)
		}
	}
}
