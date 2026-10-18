package cognitive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

type SelfModelConfig struct {
	EvidenceRows int
	SpokenRows   int
}

type SelfModelFacility struct {
	store     SelfModelStore
	llm       SelfModelLLM
	committer SelfModelCommitter
	authority AuthoritySource

	door LedgerWriter

	config atomic.Pointer[SelfModelConfig]

	teller *Teller
}

func (s *SelfModelFacility) SetDoor(d LedgerWriter) { s.door = d }

func (s *SelfModelFacility) SetTeller(t *Teller) { s.teller = t }

func (s *SelfModelFacility) Reconfigure(cfg SelfModelConfig) {
	if cfg.EvidenceRows <= 0 {
		cfg.EvidenceRows = DefaultSelfModelEvidenceRows
	}
	if cfg.SpokenRows <= 0 {
		cfg.SpokenRows = DefaultSelfModelSpokenRows
	}
	s.config.Store(&cfg)
}

func (s *SelfModelFacility) told() *Teller {
	t := &Teller{Door: s.door, Exists: s.store.EntityExists}
	if s.teller != nil {
		t.Report = s.teller.Report
	}
	return t
}

type BeliefStore interface {
	ListBeliefs() ([]rows.Belief, error)
}

const (
	DefaultSelfModelEvidenceRows = 8
	DefaultSelfModelSpokenRows   = 8
)

type SelfModelStore interface {
	BeliefStore
	ListExperiences(n int) ([]rows.Experience, error)

	ListExperiencesByProvenance(provenances []string, n int) ([]rows.Experience, error)
	ListIntentions() ([]rows.Intention, error)
	CurrentSelfModel() (*rows.SelfModelSynthesis, error)
	CurrentOperatorRelationship() (*rows.Relationship, error)

	SelfModelMaxTokens() int
	StandingSource
	EntityChecker
}

type SelfModelLLM interface {
	Chat(ctx context.Context, messages []wire.Message, opts wire.ChatOptions) (*wire.Response, error)
}

type SelfModelCommitter interface {
	Definition() wire.ToolDefinition
	Commit(ctx context.Context, args map[string]any) (string, error)
}

func NewSelfModel(st SelfModelStore, model SelfModelLLM, committer SelfModelCommitter) *SelfModelFacility {
	s := &SelfModelFacility{store: st, llm: model, committer: committer}
	s.Reconfigure(SelfModelConfig{})
	return s
}

func (s *SelfModelFacility) Name() string { return "self_model" }

func (s *SelfModelFacility) Predicate(ctx context.Context) bool {
	return true
}

func (s *SelfModelFacility) Execute(ctx context.Context) error {
	cfg := s.config.Load()
	beliefs, err := s.store.ListBeliefs()
	if err != nil {
		return fmt.Errorf("self_model: list beliefs: %w", err)
	}
	experiences, err := s.store.ListExperiences(cfg.EvidenceRows)
	if err != nil {
		return fmt.Errorf("self_model: list experiences: %w", err)
	}

	spoken, err := s.store.ListExperiencesByProvenance([]string{"operator", "external"}, cfg.SpokenRows)
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
		logsink.Tick("selfmodel.pass", "passes, nothing to synthesize", 0)
		return nil
	}

	ev := portraitEvidence{beliefs: classesFirst(beliefs)}
	for _, b := range ev.beliefs {
		ev.standing = append(ev.standing, standingOrUnavailable(s.store, b.ID))
	}

	var people []string
	seen := map[string]bool{}
	for _, e := range append(experiences, spoken...) {
		if seen[e.ID] {
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
		ev.rows = append(ev.rows, fmt.Sprintf("- [%s id=%s] %s", class, e.ID, evidenceText(e)))
		ev.rowClasses = append(ev.rowClasses, class)
	}
	for _, i := range intentions {
		if i.State == "active" {
			ev.intentions = append(ev.intentions, fmt.Sprintf("- [intentions id=%s] %s", i.ID, i.Statement))
		}
	}
	if relationship != nil {
		ev.tail = append(ev.tail, fmt.Sprintf("- [relationships id=%s] %s", relationship.ID, relationship.CharterText))
		ev.tailClasses = append(ev.tailClasses, "relationships")
		people = append(people, relationship.ID)
	}
	if current != nil {
		ev.tail = append(ev.tail, fmt.Sprintf("- [reflections id=%s] %s", current.ID, current.SynthesisText))
		ev.tailClasses = append(ev.tailClasses, "reflections")
	}

	tools := []wire.ToolDefinition{s.committer.Definition()}
	check := toolCheck(s.llm, tools)
	base := selfModelPrompt(s.store.SelfModelMaxTokens(), ledger.SelfModelMinSourceClasses)
	floor := min(len(ev.beliefs), beliefClassCount(ev.beliefs))
	whole := portraitView{beliefs: len(ev.beliefs), intentions: len(ev.intentions)}
	if have := len(ev.classes(whole)); have < ledger.SelfModelMinSourceClasses {
		logsink.Warn("selfmodel.refusal", "only %d source classes available; %d required", have, ledger.SelfModelMinSourceClasses)
		return nil
	}

	smallest := portraitView{beliefs: floor}
	if len(ev.classes(smallest)) < ledger.SelfModelMinSourceClasses {
		smallest.intentions = min(1, len(ev.intentions))
	}
	callCtx, system, err := fittedPreamble(ctx, s.authority, base, ev.render(smallest), tools, check)
	if err != nil {
		return fmt.Errorf("self_model: authority context: %w", err)
	}

	fitted := func(after string, first error) (view portraitView, sendable bool, err error) {
		view = whole
		if check != nil {
			ask := &admission{check: check, ctx: callCtx, system: system}
			admits := func(v portraitView) bool { return ask.admits(ev.render(v) + after) }
			if !admits(view) {
				k, ok := largestAdmitted(len(ev.beliefs)-floor, func(k int) bool { view.beliefs = floor + k; return admits(view) })
				view.beliefs = floor + k
				if !ok {
					view.intentions, ok = largestAdmitted(len(ev.intentions), func(n int) bool { view.intentions = n; return admits(view) })
				}
				if ask.err != nil {
					return view, false, fmt.Errorf("self_model: %w", ask.err)
				}
				if !ok {
					s.unsent(first, "no_room",
						"its smallest form (one of each kind of evidence, the current portrait and the people) does not fit the model's input budget beside what every request carries",
						"even its smallest form does not fit beside what every request carries in the model's input budget")
					return view, false, nil
				}
			}
		}
		if have := len(ev.classes(view)); have < ledger.SelfModelMinSourceClasses {

			s.unsent(first, "classes",
				fmt.Sprintf("fitted to the model's input budget it shows %d kinds of evidence where a portrait must cite %d (the active intentions were left out for room)", have, ledger.SelfModelMinSourceClasses),
				fmt.Sprintf("what fits beside what every request carries in the model's input budget shows only %d of the %d kinds of evidence a portrait must cite (your active intentions were left out for room)", have, ledger.SelfModelMinSourceClasses))
			return view, false, nil
		}
		return view, true, nil
	}
	view, sendable, err := fitted("", nil)
	if err != nil || !sendable {
		return err
	}

	messages := []wire.Message{{Role: "system", Content: system, StableLen: wire.StablePrefix(callCtx)}, {Role: "user", Content: ev.render(view)}}
	opts := wire.ChatOptions{Tools: tools, Source: "selfmodel"}
	resp, err := s.llm.Chat(ctx, messages, opts)
	if err != nil {
		s.sayUnsent(err, nil)
		return fmt.Errorf("self_model: LLM call: %w", err)
	}
	s.sayFit(ev, view)
	aerr := s.applyResponse(wire.WithModelID(ctx, resp.ModelID), resp, people)
	if aerr == nil || waits(aerr) {
		return aerr
	}

	corrective := correctionLabel + "Your previous reply violated the output contract and was refused: " +
		aerr.Error() + "\nReply again now with EXACTLY ONE commit tool call, variant \"self_model.synthesize\" " +
		"(carrying the correct previous_synthesis_id when a portrait exists), and no prose beside the call. " +
		"If there is genuinely no material change, reply with exactly NO_CHANGE."
	view, sendable, err = fitted(corrective, aerr)
	if err != nil {
		return err
	}
	if !sendable {

		return fmt.Errorf("self_model: %w — and the corrective round was not sent, for want of room", aerr)
	}
	messages[1].Content = ev.render(view) + corrective
	resp2, err := s.llm.Chat(ctx, messages, opts)
	if err != nil {
		s.sayUnsent(err, aerr)
		return fmt.Errorf("self_model: corrective LLM call: %w (first attempt: %v)", err, aerr)
	}
	s.sayFit(ev, view)
	aerr2 := s.applyResponse(wire.WithModelID(ctx, resp2.ModelID), resp2, people)
	if aerr2 == nil {
		logsink.Info("selfmodel.decision", "corrective round recovered the pass (first attempt: %v)", aerr)
		return nil
	}
	if waits(aerr2) {
		return fmt.Errorf("%w (first attempt: %v)", aerr2, aerr)
	}
	s.mintFailureExperience(aerr, aerr2, resp2.ModelID)
	return fmt.Errorf("self_model: after corrective retry: %w (first attempt: %v)", aerr2, aerr)
}

const correctionLabel = "\n\nCorrection (from the runtime; the evidence ended above):\n"

func waits(err error) bool {
	var unfinished *wire.IncompleteResponseError
	return errors.As(err, &unfinished) && !refusedForWhatWasAsked(err)
}

func (s *SelfModelFacility) unsent(first error, key, card, yours string) {
	said := "the portrait's request was not sent: " + card + " — the current portrait stands"
	told := yours + ", so nothing was sent and your current portrait stands"
	if first != nil {
		key += "|" + refusalKind(first)
		said = fmt.Sprintf("the portrait's first reply was refused (%v) and its one corrective round was not sent: with the refusal beside the evidence, %s — the current portrait stands", first, card)
		told = fmt.Sprintf("your first reply was refused (%v), and the one round that would have told you so was not sent: with the refusal beside what your portrait is drawn from, %s. Your current portrait stands", first, yours)
	}
	s.told().tell(Condition{Facility: "self_model", Kind: ConditionNoRoom, Said: said + "; " + s.teller.room() + " makes room"},
		"self_model|"+key,
		"self-model: your self-portrait could not be redrawn — "+told+". Your operator can make room on the Cognition card.")
}

func (s *SelfModelFacility) sayUnsent(err, first error) {
	var limit *wire.ContextLimitError
	if !errors.As(err, &limit) {
		return
	}
	s.unsent(first, fmt.Sprintf("refused|%d", limit.Limit),
		fmt.Sprintf("the client refused it before it was sent (%v)", err),
		fmt.Sprintf("its request was over the model's input budget of %d tokens", limit.Limit))
}

type portraitEvidence struct {
	beliefs     []rows.Belief
	standing    []string
	rows        []string
	rowClasses  []string
	intentions  []string
	tail        []string
	tailClasses []string
}

type portraitView struct{ beliefs, intentions int }

func beliefClass(b rows.Belief) string {
	switch {
	case b.NodeType == "value" && b.Ring <= 2:
		return "values"
	case b.NodeType == "working_style":
		return "working_style"
	}
	return "beliefs"
}

func classesFirst(beliefs []rows.Belief) []rows.Belief {
	var lead, rest []rows.Belief
	seen := map[string]bool{}
	for _, b := range beliefs {
		if c := beliefClass(b); !seen[c] {
			seen[c] = true
			lead = append(lead, b)
			continue
		}
		rest = append(rest, b)
	}
	return append(lead, rest...)
}

func beliefClassCount(beliefs []rows.Belief) int {
	seen := map[string]bool{}
	for _, b := range beliefs {
		seen[beliefClass(b)] = true
	}
	return len(seen)
}

func (e portraitEvidence) render(v portraitView) string {
	var parts []string
	for i, b := range e.beliefs[:v.beliefs] {
		parts = append(parts, fmt.Sprintf("- [%s id=%s, standing=%s] %s", beliefClass(b), b.ID, e.standing[i], b.Statement))
	}
	if v.beliefs < len(e.beliefs) {
		parts = append(parts, fmt.Sprintf("- [%d of %d beliefs shown — one of each kind first, then by ring and confidence; the rest stand in your record, and recall (source=ledger) reaches them]", v.beliefs, len(e.beliefs)))
	}
	parts = append(parts, e.rows...)
	parts = append(parts, e.intentions[:v.intentions]...)
	if v.intentions < len(e.intentions) {
		parts = append(parts, fmt.Sprintf("- [%d of %d active intentions shown; recall (source=ledger) reaches the rest]", v.intentions, len(e.intentions)))
	}
	parts = append(parts, e.tail...)
	return "Evidence:\n" + strings.Join(parts, "\n")
}

func (e portraitEvidence) classes(v portraitView) map[string]bool {
	out := map[string]bool{}
	for _, b := range e.beliefs[:v.beliefs] {
		out[beliefClass(b)] = true
	}
	for _, c := range e.rowClasses {
		out[c] = true
	}
	if v.intentions > 0 {
		out["intentions"] = true
	}
	for _, c := range e.tailClasses {
		out[c] = true
	}
	return out
}

func (s *SelfModelFacility) sayFit(e portraitEvidence, v portraitView) {
	if v.beliefs == len(e.beliefs) && v.intentions == len(e.intentions) {
		s.teller.say(Condition{Facility: "self_model", Kind: ConditionWhole, Said: fmt.Sprintf("the last portrait pass was shown all %d belief(s) and %d intention(s)", len(e.beliefs), len(e.intentions))})
		return
	}
	said := fmt.Sprintf("the last portrait pass fitted the model's input budget: %d of %d beliefs and %d of %d active intentions were shown — a lower cognition.self_model_evidence_rows, or %s, makes room", v.beliefs, len(e.beliefs), v.intentions, len(e.intentions), s.teller.room())
	if v.beliefs == len(e.beliefs) {
		s.teller.say(Condition{Facility: "self_model", Kind: ConditionFitted, Said: said})
		return
	}
	s.told().tell(Condition{Facility: "self_model", Kind: ConditionFitted, Said: said},
		fmt.Sprintf("self_model|beliefs|%d", doubling(len(e.beliefs))),
		fmt.Sprintf("self-model: only %d of your %d beliefs fit beside the rest of what your self-portrait is drawn from, so it is drawn from your values and working style first and then your most confident beliefs; the rest stand in your record, and recall (source=ledger) reaches them.", v.beliefs, len(e.beliefs)))
}

func (s *SelfModelFacility) mintFailureExperience(first, second error, modelID string) {
	if s.door == nil {
		logsink.Warn("selfmodel.refusal", "no ledger door — failure not recorded as experience: %v", second)
		return
	}

	twice := func(first, second string) string {
		return "self_model pass failed twice. First refusal: " + first + " Refusal after the corrective round: " + second
	}
	mintOnce(s.door, s.store.EntityExists, "self_model", "exp_facility_"+outputHash(twice(refusalKind(first), refusalKind(second))), twice(first.Error(), second.Error()), modelID)
}

func refusalKind(err error) string {
	var unfinished *wire.IncompleteResponseError
	if errors.As(err, &unfinished) {
		return "unfinished reply: " + unfinished.Reason
	}
	return err.Error()
}

func (s *SelfModelFacility) applyResponse(ctx context.Context, resp *wire.Response, people []string) error {
	if resp == nil || len(resp.Choices) == 0 {
		return fmt.Errorf("self_model output contract: no response choice")
	}

	if err := wire.Finished(resp.Choices[0]); err != nil {
		if !refusedForWhatWasAsked(err) {
			return fmt.Errorf("self_model: %w — the pass waits for its next cadence", err)
		}
		return fmt.Errorf("self_model output contract: %w", err)
	}
	message := resp.Choices[0].Message
	if len(message.ToolCalls) == 0 {
		if strings.TrimSpace(message.Content) == "NO_CHANGE" {
			logsink.Tick("selfmodel.pass", "passes, no material change", 0)
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
	var args map[string]any
	if err := json.Unmarshal([]byte(message.ToolCalls[0].Function.Arguments), &args); err != nil {
		return fmt.Errorf("self_model output contract: commit arguments are not valid JSON")
	}
	if variant, _ := args["variant"].(string); variant != "self_model.synthesize" {

		return fmt.Errorf("self_model output contract: commit variant must be self_model.synthesize (received %q)", variant)
	}
	if len(people) > 0 && !citesAny(args["source_entity_refs"], people) {

		return fmt.Errorf("self_model output contract: the people on the table are cited nowhere — %s. Name who shaped what, citing them in source_entity_refs, or say in the portrait that a source is unnamed; a category is not a name", strings.Join(people, ", "))
	}
	result, err := s.committer.Commit(ctx, args)
	if err != nil {
		return fmt.Errorf("self_model: commit: %w", err)
	}
	logsink.Info("selfmodel.end", "%s", logsink.Preview(result))
	return nil
}

func (s *SelfModelFacility) SetAuthority(src AuthoritySource) { s.authority = src }

func (s *SelfModelFacility) OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult {
	if err := s.Execute(ctx); err != nil {
		logsink.Warn("selfmodel.error", "execute error: %v", err)
		return AlarmResult{Accepted: false}
	}
	return AlarmResult{Accepted: true}
}

func citesAny(refs any, ids []string) bool {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	list, _ := refs.([]any)
	for _, item := range list {
		if ref, ok := item.(map[string]any); ok {
			if id, _ := ref["id"].(string); want[id] {
				return true
			}
		}
	}
	return false
}
