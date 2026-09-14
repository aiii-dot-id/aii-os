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

// .
// .
type SelfModelFacility struct {
	store     SelfModelStore
	llm       SelfModelLLM
	committer SelfModelCommitter
	authority AuthoritySource
	// .
	// .
	// .
	door LedgerWriter
}

// .
func (s *SelfModelFacility) SetDoor(d LedgerWriter) { s.door = d }

// .
// .
type BeliefStore interface {
	ListBeliefs() ([]store.Belief, error)
}

// .
// .
// .
// .
const (
	selfModelRecentRows = 8
	selfModelSpokenRows = 8
)

// .
type SelfModelStore interface {
	BeliefStore
	ListExperiences(n int) ([]store.Experience, error)
	// .
	// .
	ListExperiencesByProvenance(provenances []string, n int) ([]store.Experience, error)
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

// .
func (s *SelfModelFacility) Name() string { return "self_model" }

// .
func (s *SelfModelFacility) Predicate(ctx context.Context) bool {
	return true
}

func (s *SelfModelFacility) Execute(ctx context.Context) error {
	beliefs, err := s.store.ListBeliefs()
	if err != nil {
		return fmt.Errorf("self_model: list beliefs: %w", err)
	}
	experiences, err := s.store.ListExperiences(selfModelRecentRows)
	if err != nil {
		return fmt.Errorf("self_model: list experiences: %w", err)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	spoken, err := s.store.ListExperiencesByProvenance([]string{"operator", "external"}, selfModelSpokenRows)
	if err != nil {
		return fmt.Errorf("self_model: list experiences by provenance: %w", err)
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
	// .
	// .
	// .
	// .
	var people []string
	for _, b := range beliefs {
		class := "beliefs"
		if b.NodeType == "value" && b.Ring <= 2 {
			class = "values"
		} else if b.NodeType == "working_style" {
			class = "working_style"
		}
		parts = append(parts, fmt.Sprintf("- [%s id=%s, standing=%s] %s", class, b.ID, standingOrUnavailable(s.store, b.ID), b.Statement))
		classes[class] = true
	}
	seen := map[string]bool{}
	for _, e := range append(experiences, spoken...) {
		if e.Private != 0 || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		if e.Provenance == "operator" || e.Provenance == "external" {
			people = append(people, e.ID)
		}
		class := "experiences"
		if e.Category == "reflection" {
			class = "notes"
		}
		parts = append(parts, fmt.Sprintf("- [%s id=%s] %s", class, e.ID, evidenceText(e)))
		classes[class] = true
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
		people = append(people, relationship.ID)
	}
	if current != nil {
		parts = append(parts, fmt.Sprintf("- [reflections id=%s] %s", current.ID, current.SynthesisText))
		classes["reflections"] = true
	}
	if len(classes) < 4 {
		log.Printf("SELF_MODEL: only %d source classes available; four required", len(classes))
		return nil
	}

	callCtx, base, err := withPreamble(ctx, s.authority, selfModelSystemPrompt)
	if err != nil {
		return fmt.Errorf("self_model: authority context: %w", err)
	}
	user := "Evidence:\n" + strings.Join(parts, "\n")
	messages := []llm.Message{{Role: "system", Content: base, StableLen: llm.StablePrefix(callCtx)}, {Role: "user", Content: user}}
	opts := llm.ChatOptions{Tools: []llm.ToolDefinition{s.committer.Definition()}}
	resp, err := s.llm.Chat(ctx, messages, opts)
	if err != nil {
		return fmt.Errorf("self_model: LLM call: %w", err)
	}
	aerr := s.applyResponse(llm.WithModelID(ctx, resp.ModelID), resp, people)
	if aerr == nil {
		return nil
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	corrective := llm.Message{Role: "user", Content: "Your previous reply violated the output contract and was refused: " +
		aerr.Error() + "\nReply again now with EXACTLY ONE commit tool call, variant \"self_model.synthesize\" " +
		"(carrying the correct previous_synthesis_id when a portrait exists), and no prose beside the call. " +
		"If there is genuinely no material change, reply with exactly NO_CHANGE."}
	resp2, err := s.llm.Chat(ctx, append(messages, corrective), opts)
	if err != nil {
		return fmt.Errorf("self_model: corrective LLM call: %w", err)
	}
	aerr2 := s.applyResponse(llm.WithModelID(ctx, resp2.ModelID), resp2, people)
	if aerr2 == nil {
		log.Printf("SELF_MODEL: corrective round recovered the pass (first attempt: %v)", aerr)
		return nil
	}
	s.mintFailureExperience(aerr, aerr2, resp2.ModelID)
	return fmt.Errorf("self_model: after corrective retry: %w (first attempt: %v)", aerr2, aerr)
}

// .
// .
// .
// .
func (s *SelfModelFacility) mintFailureExperience(first, second error, modelID string) {
	if s.door == nil {
		log.Printf("SELF_MODEL: no ledger door — failure not recorded as experience: %v", second)
		return
	}
	// .
	// .
	// .
	// .
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

func (s *SelfModelFacility) applyResponse(ctx context.Context, resp *llm.Response, people []string) error {
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
		// .
		// .
		// .
		// .
		// .
		return fmt.Errorf("self_model output contract: commit variant must be self_model.synthesize (received %q)", variant)
	}
	if len(people) > 0 && !citesAny(args["source_entity_refs"], people) {
		// .
		// .
		return fmt.Errorf("self_model output contract: the people on the table are cited nowhere — %s. Name who shaped what, citing them in source_entity_refs, or say in the portrait that a source is unnamed; a category is not a name", strings.Join(people, ", "))
	}
	result, err := s.committer.Commit(ctx, args)
	if err != nil {
		return fmt.Errorf("self_model: commit: %w", err)
	}
	log.Printf("SELF_MODEL: %s", result)
	return nil
}

// .
func (s *SelfModelFacility) SetAuthority(src AuthoritySource) { s.authority = src }

// .
func (s *SelfModelFacility) OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult {
	if err := s.Execute(ctx); err != nil {
		log.Printf("SELF_MODEL: execute error: %v", err)
		return AlarmResult{Accepted: false}
	}
	return AlarmResult{Accepted: true}
}

// .
// .
func citesAny(refs interface{}, ids []string) bool {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	list, _ := refs.([]interface{})
	for _, item := range list {
		if ref, ok := item.(map[string]interface{}); ok {
			if id, _ := ref["id"].(string); want[id] {
				return true
			}
		}
	}
	return false
}
