package cognitive

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// SelfModelFacility implements SELF_MODEL — narrative interpretation of
// who the identity is right now.
type SelfModelFacility struct {
	store     SelfModelStore
	llm       SelfModelLLM
	committer SelfModelCommitter
	authority AuthoritySource
	// door mints the second-failure experience (nil = log only). The
	// identity metabolizes its own failing organ instead of the failure
	// evaporating into a rotated log — same door DREAM mints through.
	door LedgerWriter
}

// SetDoor wires the ledger door for failure experiences (nil-safe).
func (s *SelfModelFacility) SetDoor(d LedgerWriter) { s.door = d }

// BeliefStore is the shared store interface for belief-reading facilities
// (SELF_MODEL, CONSOLIDATE).
type BeliefStore interface {
	ListBeliefs() ([]store.Belief, error)
}

// SelfModelStore is the store interface SELF_MODEL needs.
type SelfModelStore interface {
	BeliefStore
	ListExperiences(n int) ([]store.Experience, error)
	ListIntentions() ([]store.Intention, error)
	CurrentSelfModel() (*store.SelfModelSynthesis, error)
	CurrentOperatorRelationship() (*store.Relationship, error)
	StandingSource
}

type SelfModelLLM interface {
	Chat(ctx context.Context, messages []llm.Message, opts llm.ChatOptions) (*llm.Response, error)
}

type SelfModelCommitter interface {
	Definition() llm.ToolDefinition
	Commit(ctx context.Context, args map[string]interface{}) (string, error)
}

func NewSelfModel(st SelfModelStore, model SelfModelLLM, committer SelfModelCommitter) *SelfModelFacility {
	return &SelfModelFacility{store: st, llm: model, committer: committer}
}

// Name returns the facility name.
func (s *SelfModelFacility) Name() string { return "self_model" }

// Predicate — interval-based on life clock.
func (s *SelfModelFacility) Predicate(ctx context.Context) bool {
	return true
}

func (s *SelfModelFacility) Execute(ctx context.Context) error {
	beliefs, err := s.store.ListBeliefs()
	if err != nil {
		return fmt.Errorf("self_model: list beliefs: %w", err)
	}
	experiences, err := s.store.ListExperiences(8)
	if err != nil {
		return fmt.Errorf("self_model: list experiences: %w", err)
	}
	intentions, err := s.store.ListIntentions()
	if err != nil {
		return fmt.Errorf("self_model: list intentions: %w", err)
	}
	current, err := s.store.CurrentSelfModel()
	if err != nil {
		return fmt.Errorf("self_model: load current portrait: %w", err)
	}
	relationship, err := s.store.CurrentOperatorRelationship()
	if err != nil {
		return fmt.Errorf("self_model: load current operator relationship: %w", err)
	}

	if len(beliefs) == 0 && len(experiences) == 0 && current == nil {
		log.Printf("SELF_MODEL: nothing to synthesize")
		return nil
	}

	var parts []string
	classes := map[string]bool{}
	for _, b := range beliefs {
		class := "beliefs"
		if b.NodeType == "value" && b.Ring <= 2 {
			class = "values"
		} else if b.NodeType == "working_style" {
			class = "working_style"
		}
		parts = append(parts, fmt.Sprintf("- [%s id=%s, standing=%s] %s", class, b.ID, s.store.StandingFor(b.ID), b.Statement))
		classes[class] = true
	}
	for _, e := range experiences {
		if e.Private == 0 {
			class := "experiences"
			if e.Category == "reflection" {
				class = "notes"
			}
			parts = append(parts, fmt.Sprintf("- [%s id=%s] %s", class, e.ID, evidenceText(e)))
			classes[class] = true
		}
	}
	for _, i := range intentions {
		if i.State == "active" {
			parts = append(parts, fmt.Sprintf("- [intentions id=%s] %s", i.ID, i.Statement))
			classes["intentions"] = true
		}
	}
	if relationship != nil {
		parts = append(parts, fmt.Sprintf("- [relationships id=%s] %s", relationship.ID, relationship.CharterText))
		classes["relationships"] = true
	}
	if current != nil {
		parts = append(parts, fmt.Sprintf("- [reflections id=%s] %s", current.ID, current.SynthesisText))
		classes["reflections"] = true
	}
	if len(classes) < 4 {
		log.Printf("SELF_MODEL: only %d source classes available; four required", len(classes))
		return nil
	}

	base, err := withPreamble(s.authority, selfModelSystemPrompt)
	if err != nil {
		return fmt.Errorf("self_model: authority context: %w", err)
	}
	user := "Evidence:\n" + strings.Join(parts, "\n")
	messages := []llm.Message{{Role: "system", Content: base}, {Role: "user", Content: user}}
	opts := llm.ChatOptions{Tools: []llm.ToolDefinition{s.committer.Definition()}}
	resp, err := s.llm.Chat(ctx, messages, opts)
	if err != nil {
		return fmt.Errorf("self_model: LLM call: %w", err)
	}
	aerr := s.applyResponse(llm.WithModelID(ctx, resp.ModelID), resp)
	if aerr == nil {
		return nil
	}

	// ONE CORRECTIVE ROUND, WITH THE ERROR IN IT (evaluate layer,
	// 2026-08-26). Retry-by-cadence re-ran the identical prompt and
	// harvested the identical violation — five times in six days on the
	// live identity. The refusal text is the one new fact the model
	// needs, so it goes in as a fresh user message (no assistant echo:
	// replaying a refused tool call re-enters the dialect pairing rules
	// for nothing). Exactly one retry: a second failure is signal.
	corrective := llm.Message{Role: "user", Content: "Your previous reply violated the output contract and was refused: " +
		aerr.Error() + "\nReply again now with EXACTLY ONE commit tool call, variant \"self_model.synthesize\" " +
		"(carrying the correct previous_synthesis_id when a portrait exists), and no prose beside the call. " +
		"If there is genuinely no material change, reply with exactly NO_CHANGE."}
	resp2, err := s.llm.Chat(ctx, append(messages, corrective), opts)
	if err != nil {
		return fmt.Errorf("self_model: corrective LLM call: %w", err)
	}
	aerr2 := s.applyResponse(llm.WithModelID(ctx, resp2.ModelID), resp2)
	if aerr2 == nil {
		log.Printf("SELF_MODEL: corrective round recovered the pass (first attempt: %v)", aerr)
		return nil
	}
	s.mintFailureExperience(aerr, aerr2, resp2.ModelID)
	return fmt.Errorf("self_model: after corrective retry: %w (first attempt: %v)", aerr2, aerr)
}

// mintFailureExperience turns a twice-failed pass into raw material the
// identity's own metabolism will read — provenance "system", the
// sanctioned class for the resident's substrate speaking. Content-derived
// id, so a repeating failure re-mints nothing new.
func (s *SelfModelFacility) mintFailureExperience(first, second error, modelID string) {
	if s.door == nil {
		log.Printf("SELF_MODEL: no ledger door — failure not recorded as experience: %v", second)
		return
	}
	// BOTH refusals, in order. The dream metabolizes this record, and
	// half the trajectory is half a lesson: live on 2026-08-26 only the
	// final variant error survived into the experience, and whether the
	// first miss was a citation or a variant was unrecoverable.
	content := "self_model pass failed twice. First refusal: " + first.Error() +
		" Refusal after the corrective round: " + second.Error()
	if _, err := s.door.Append(ledger.EventExperienceCreate, 3, map[string]interface{}{
		"id":         "exp_facility_" + outputHash(content),
		"content":    content,
		"category":   "observation",
		"provenance": "system",
		"raw":        true,
	}, modelID); err != nil {
		log.Printf("SELF_MODEL: failure experience refused: %v", err)
	}
}

func (s *SelfModelFacility) applyResponse(ctx context.Context, resp *llm.Response) error {
	if resp == nil || len(resp.Choices) == 0 {
		return fmt.Errorf("self_model output contract: no response choice")
	}
	message := resp.Choices[0].Message
	if len(message.ToolCalls) == 0 {
		if strings.TrimSpace(message.Content) == "NO_CHANGE" {
			log.Printf("SELF_MODEL: no material change")
			return nil
		}
		return fmt.Errorf("self_model output contract: expected one commit call or exact NO_CHANGE")
	}
	if len(message.ToolCalls) != 1 || message.ToolCalls[0].Function.Name != "commit" {
		return fmt.Errorf("self_model output contract: expected exactly one commit call")
	}
	if strings.TrimSpace(message.Content) != "" {
		return fmt.Errorf("self_model output contract: commit call must not carry a free-form answer")
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(message.ToolCalls[0].Function.Arguments), &args); err != nil {
		return fmt.Errorf("self_model output contract: commit arguments are not valid JSON")
	}
	if variant, _ := args["variant"].(string); variant != "self_model.synthesize" {
		// Name what ARRIVED, not only what is required (the class-naming
		// lesson, applied here after the live 2026-08-26 pass: the
		// corrective round re-failed on variant with the requirement
		// already spelled out in its prompt — the missing half of the
		// sentence was what the model had actually sent).
		return fmt.Errorf("self_model output contract: commit variant must be self_model.synthesize (received %q)", variant)
	}
	result, err := s.committer.Commit(ctx, args)
	if err != nil {
		return fmt.Errorf("self_model: commit: %w", err)
	}
	log.Printf("SELF_MODEL: %s", result)
	return nil
}

// SetAuthority wires the authority-preamble source (nil-safe; tests omit it).
func (s *SelfModelFacility) SetAuthority(src AuthoritySource) { s.authority = src }

// OnAlarm handles TIME alarm dispatch.
func (s *SelfModelFacility) OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult {
	if err := s.Execute(ctx); err != nil {
		log.Printf("SELF_MODEL: execute error: %v", err)
		return AlarmResult{Accepted: false}
	}
	return AlarmResult{Accepted: true}
}
