package cognitive

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"
	"unicode"

	"github.com/aiii-dot-id/aii-os/internal/cognitive/landing"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/memory/trigram"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
type ConsolidateConfig struct {
	Threshold int
	MaxOps    int
	// .
	// .
	// .
	Salience memory.SalienceWeights
	// .
	// .
	Ring3MaxChars int
	// .
	// .
	TensionsMaxChars int
	// .
	// .
	OutcomeWindow int
	// .
	// .
	// .
	// .
	ObservationMaxChars int
}

// .
// .
// .
// .
// .
const defaultRing3MaxChars = 8000

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
// .
// .
// .
// .
// .
// .
// .
// .
type ConsolidateFacility struct {
	decisions  DecisionLog
	store      ConsolidateStore
	llm        LLMCaller
	ledger     ConsolidateLedger
	ringWriter RingWriter
	config     ConsolidateConfig
	authority  AuthoritySource
	tensions   TensionsSource
	// .
	// .
	outcomes      outcomeReader
	outcomeCursor func(store.OutcomeBatch) landing.Cursor
	intake        *landing.Lander
}

// .
type ConsolidateStore interface {
	ExperienceStore
	ListExperiences(n int) ([]store.Experience, error)
	BeliefStore
	EdgeStore
	LifetimeStore
	IntentionStore
	EntityChecker
	ProvenanceResolver
	StandingSource
}

// .
// .
type StandingSource interface {
	StandingFor(id string) (string, error)
}

// .
type ProvenanceResolver interface {
	ProvenanceByIDs(ids []string) (map[string]string, error)
}

// .
type EntityChecker interface {
	EntityExists(id string) (bool, error)
}

// .
type IntentionStore interface {
	ListIntentions() ([]store.Intention, error)
}

// .
type EdgeStore interface {
	ListEdgesForBelief(beliefID string) ([]store.Edge, error)
}

// .
type LifetimeStore interface {
	LifetimeTicks() (int64, error)
}

// .
type ConsolidateLedger interface {
	Append(eventType ledger.EventType, ring int, payload interface{}, modelID string) (*ledger.Event, error)
}

// .
func NewConsolidate(store ConsolidateStore, llm LLMCaller, lg ConsolidateLedger, ringWriter RingWriter, cfg ConsolidateConfig) *ConsolidateFacility {
	if cfg.Threshold == 0 {
		cfg.Threshold = 3
	}
	if cfg.Salience.Version == "" {
		cfg.Salience = memory.DefaultSalience
	}
	if cfg.MaxOps == 0 {
		cfg.MaxOps = 32
	}
	if cfg.Ring3MaxChars <= 0 {
		cfg.Ring3MaxChars = defaultRing3MaxChars
	}
	if cfg.OutcomeWindow <= 0 {
		cfg.OutcomeWindow = defaultOutcomeWindow
	}
	if cfg.ObservationMaxChars <= 0 {
		cfg.ObservationMaxChars = defaultSurfacingMaxChars
	}
	var door landing.Door
	if lg != nil {
		door = lg
	}
	return &ConsolidateFacility{
		store:      store,
		llm:        llm,
		ledger:     lg,
		intake:     landing.New(door),
		ringWriter: ringWriter,
		config:     cfg,
	}
}

// .
func (c *ConsolidateFacility) Name() string { return "consolidate" }

// .
// .
func (c *ConsolidateFacility) Predicate(ctx context.Context) bool {
	count, err := c.store.UnprocessedExperienceCount()
	if err != nil {
		return false
	}
	if count >= c.config.Threshold {
		return true
	}
	if count == 0 {
		return false
	}
	// .
	// .
	// .
	experiences, err := c.store.ListRawExperiences(c.config.Threshold)
	return err == nil && reservedWaiting(experiences)
}

// .
// .
func reservedWaiting(experiences []store.Experience) bool {
	for _, e := range experiences {
		if strings.HasPrefix(e.ID, store.OutcomeObservationPrefix) {
			return true
		}
	}
	return false
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
// .
// .
// .
func (c *ConsolidateFacility) Execute(ctx context.Context) error {
	if !c.Predicate(ctx) {
		// .
		c.writeRing3(ctx)
		return nil
	}

	// .
	// .
	experiences, err := c.store.ListRawExperiences(30)
	if err != nil {
		return fmt.Errorf("consolidate: list raw experiences: %w", err)
	}

	var expTexts []string
	var expIDs []string
	for _, e := range experiences {
		expTexts = append(expTexts, fmt.Sprintf("[%s] %s", e.ID, evidenceText(e)))
		expIDs = append(expIDs, e.ID)
	}

	if len(expTexts) >= c.config.Threshold || reservedWaiting(experiences) {
		userMsg := c.buildEvidenceBlock(expTexts)
		callCtx, systemPrompt, err := withPreamble(ctx, c.authority, consolidateSystemPrompt)
		if err != nil {
			return fmt.Errorf("CONSOLIDATE: authority context: %w", err)
		}
		// .
		// .
		// .
		// .
		// .
		// .
		output, modelID, viaTool, err := c.llm.ChatStructured(callCtx, systemPrompt, userMsg, consolidationTool())
		if viaTool {
			logsink.Debug("consolidate.decision", "envelope arrived as a native tool call")
		}
		if err != nil {
			logsink.Warn("consolidate.error", "LLM call failed: %v — experiences remain unprocessed (nothing landed, nothing consumed)", err)
			c.writeRing3Deterministic()
			return nil
		}

		env, perr := parseConsolidationEnvelope(output)
		if perr != nil {
			// .
			// .
			// .
			logsink.Warn("consolidate.refusal", "envelope rejected: %v — nothing minted, nothing consumed (retry next pass)", perr)
			return nil
		}

		// .
		// .
		// .
		// .
		outputs, unevidenced := c.mintOperations(env.Operations, expIDs, modelID)
		if unevidenced > 0 && len(outputs) == 0 {
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			logsink.Warn("consolidate.refusal", "%d upsert(s) cited no evidence and nothing else minted — nothing consumed; the experiences wait for a pass that says where its beliefs come from", unevidenced)
			return nil
		}

		// .
		// .
		// .
		// .
		// .
		// .
		if c.ledger == nil {
			logsink.Warn("consolidate.refusal", "no ledger door — %d operation(s) and consumption skipped", len(env.Operations))
		} else if _, err := c.ledger.Append(ledger.EventConsolidationRun, 3,
			store.FacilityRunPayload{Inputs: expIDs, Outputs: outputs, Confirmed: c.confirmedCrossings()}, modelID); err != nil {
			logsink.Warn("consolidate.refusal", "run marker refused: %v — nothing consumed, pass will re-run", err)
		} else {
			logsink.Info("consolidate.end", "consumed %d experiences into %d ledger event(s)", len(expIDs), len(outputs))
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
		// .
		// .
		// .
		switch {
		case len(outputs) > 0 && env.Ring3View != "" && c.ringWriter != nil:
			if !c.ring3ViewFits(env.Ring3View) {
				// .
				// .
				// .
				c.writeRing3Deterministic()
				break
			}
			c.ringWriter.SetRingSection(ring.Ring3, "working_truth", env.Ring3View)
			logsink.Info("consolidate.end", "wrote %d chars to Ring 3 (working_truth, from the pass)", len(env.Ring3View))
		case len(outputs) == 0:
			if env.Ring3View != "" {
				logsink.Info("consolidate.decision", "the pass minted nothing — its %d-char view is not backed by any belief and was not kept; rendering Ring 3 from the store instead",
					len(env.Ring3View))
			}
			c.writeRing3Deterministic()
		}

		// .
		// .
		// .
		// .
		// .
		if operator := strings.TrimSpace(env.Operator); operator != "" {
			switch {
			case len(outputs) > 0 && c.ringWriter != nil:
				c.ringWriter.SetRingSection(ring.Ring3, "operator", operator)
				logsink.Info("consolidate.end", "wrote %d chars to Ring 3 (operator)", len(operator))
			case len(outputs) == 0:
				logsink.Info("consolidate.decision", "the pass minted nothing — its %d-char operator model is not backed by any belief and was not kept", len(operator))
			}
		}
	}

	return nil
}

// .
// .
// .
type beliefOperation struct {
	Op         string   `json:"op"`
	ID         string   `json:"id,omitempty"`
	Statement  string   `json:"statement,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
	OldID      string   `json:"old_id,omitempty"`
	NewID      string   `json:"new_id,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	// .
	// .
	// .
	Evidence []string `json:"evidence,omitempty"`
}

// .
// .
// .
type consolidationEnvelope struct {
	Operations []beliefOperation `json:"operations"`
	Ring3View  string            `json:"ring3_view"`
	// .
	// .
	// .
	Operator string `json:"operator,omitempty"`
}

// .
// .
// .
// .
// .
func parseConsolidationEnvelope(output string) (*consolidationEnvelope, error) {
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in output (%d chars)", len(output))
	}
	var env consolidationEnvelope
	if err := json.Unmarshal([]byte(output[start:end+1]), &env); err != nil {
		return nil, fmt.Errorf("envelope does not parse: %w", err)
	}
	if len(env.Operations) == 0 && strings.TrimSpace(env.Ring3View) == "" {
		return nil, fmt.Errorf("empty envelope — neither operations nor a view")
	}
	return &env, nil
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
func (c *ConsolidateFacility) mintOperations(ops []beliefOperation, inputs []string, modelID string) ([]uint64, int) {
	if len(ops) == 0 {
		return nil, 0
	}
	if c.ledger == nil {
		return nil, 0
	}
	onTable := map[string]bool{}
	for _, id := range inputs {
		onTable[id] = true
	}
	unevidenced := 0
	if len(ops) > c.config.MaxOps {
		logsink.Warn("consolidate.budget", "%d operations clamped to %d (runaway guard)", len(ops), c.config.MaxOps)
		ops = ops[:c.config.MaxOps]
	}

	// .
	// .
	// .
	known := map[string]int{}
	statements := map[string]string{}
	if beliefs, err := c.store.ListBeliefs(); err == nil {
		for _, b := range beliefs {
			known[b.ID] = b.Ring
			statements[b.ID] = b.Statement
		}
	}

	// .
	// .
	// .
	// .
	// .
	alias := map[string]string{}
	minted := map[string]bool{}
	var outputs []uint64

	for i, op := range ops {
		switch op.Op {
		case "upsert":
			stmt := strings.TrimSpace(op.Statement)
			if stmt == "" {
				logsink.Debug("consolidate.refusal", "op %d (upsert) dropped — empty statement", i)
				continue
			}
			conf := 0.5
			if op.Confidence != nil {
				conf = *op.Confidence
				if conf < 0 {
					conf = 0
				}
				if conf > 1 {
					conf = 1
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
			// .
			// .
			// .
			id := strings.TrimSpace(op.ID)
			if _, exists := known[id]; id == "" || !exists {
				engineID := "belief_" + outputHash(stmt)
				if id != "" {
					alias[id] = engineID
				}
				id = engineID
			}
			if r, exists := known[id]; exists && r != 3 {
				logsink.Debug("consolidate.refusal", "op %d (upsert) dropped — belief %q is ring %d, not working truth", i, id, r)
				continue
			}
			var evidence []string
			seen := map[string]bool{}
			for _, raw := range op.Evidence {
				ev := strings.TrimSpace(raw)
				if resolved, ok := alias[ev]; ok {
					ev = resolved
				}
				if ev == "" || ev == id || seen[ev] {
					continue
				}
				if _, exists := known[ev]; !exists && !onTable[ev] && !minted[ev] {
					logsink.Debug("consolidate.refusal", "op %d (upsert %q) cites %q, which is neither on the table nor a belief — citation dropped", i, id, ev)
					continue
				}
				seen[ev] = true
				evidence = append(evidence, ev)
			}
			if len(evidence) == 0 {
				logsink.Debug("consolidate.refusal", "op %d (upsert %q) dropped — a belief comes from experiences or other beliefs, and none were cited (evidence: %v)", i, id, op.Evidence)
				unevidenced++
				continue
			}
			// .
			// .
			// .
			// .
			// .
			// .
			decision := c.salienceFor(id, stmt, conf, evidence, inputs, statements)
			if decision.Class == memory.SalienceMemo {
				logsink.Debug("consolidate.refusal", "op %d (upsert %q) kept as a memo by salience (score %.2f; %s)", i, id, decision.Score, strings.Join(decision.Explanations, "; "))
				c.logDecision(decision, stmt, 0)
				continue
			}
			evt, err := c.ledger.Append(ledger.EventBeliefUpsert, 3, map[string]interface{}{
				"id": id, "statement": stmt, "ring": 3, "confidence": conf,
			}, modelID)
			if err != nil {
				logsink.Debug("consolidate.refusal", "op %d (upsert %q) refused before append: %v — dropped", i, id, err)
				continue
			}
			outputs = append(outputs, evt.Seq)
			minted[id] = true
			c.logDecision(decision, stmt, evt.Seq)
			for _, ev := range evidence {
				edge, err := c.ledger.Append(ledger.EventEdgeCreate, 3, map[string]interface{}{
					"id":        "edge_" + outputHash(ev+"|"+id+"|DERIVED_FROM"),
					"from_id":   ev,
					"to_id":     id,
					"edge_type": "DERIVED_FROM",
				}, modelID)
				if err != nil {
					logsink.Debug("consolidate.refusal", "evidence edge %s → %s refused: %v", ev, id, err)
					continue
				}
				outputs = append(outputs, edge.Seq)
			}
		case "supersede":
			oldID := strings.TrimSpace(op.OldID)
			newID := strings.TrimSpace(op.NewID)
			if resolved, ok := alias[newID]; ok {
				newID = resolved
			}
			if r, ok := known[oldID]; !ok {
				logsink.Debug("consolidate.refusal", "op %d (supersede) dropped — old_id %q names no belief", i, oldID)
				continue
			} else if r != 3 {
				logsink.Debug("consolidate.refusal", "op %d (supersede) dropped — belief %q is ring %d, not working truth", i, oldID, r)
				continue
			}
			if _, ok := known[newID]; !ok && !minted[newID] {
				logsink.Debug("consolidate.refusal", "op %d (supersede) dropped — new_id %q names no belief (existing or upserted this pass)", i, newID)
				continue
			}
			if oldID == newID {
				logsink.Debug("consolidate.refusal", "op %d (supersede) dropped — a belief cannot supersede itself (%q)", i, oldID)
				continue
			}
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			if standing, err := c.store.StandingFor(oldID); err != nil {
				logsink.Debug("consolidate.refusal", "op %d (supersede %q) dropped — its standing cannot be derived (%v), so it cannot be shown uncontested", i, oldID, err)
				continue
			} else if standing == "suspect" {
				logsink.Warn("consolidate.refusal", "op %d (supersede %q→%q) dropped — %q is CONTESTED: retiring it would drop an unresolved contradiction from working truth; that is the identity's to settle, not consolidation's", i, oldID, newID, oldID)
				continue
			}
			evt, err := c.ledger.Append(ledger.EventBeliefSupersede, 3, map[string]interface{}{
				"old_id": oldID, "new_id": newID, "reason": strings.TrimSpace(op.Reason),
			}, modelID)
			if err != nil {
				logsink.Debug("consolidate.refusal", "op %d (supersede %q→%q) refused before append: %v — dropped", i, oldID, newID, err)
				continue
			}
			outputs = append(outputs, evt.Seq)
		default:
			logsink.Debug("consolidate.refusal", "op %d dropped — unknown op %q (sanctioned: upsert, supersede)", i, op.Op)
		}
	}
	return outputs, unevidenced
}

// .
// .
func (c *ConsolidateFacility) buildEvidenceBlock(expTexts []string) string {
	var parts []string
	parts = append(parts, "Experiences to consolidate (cite their ids as evidence):")
	parts = append(parts, joinLines(expTexts))

	// .
	// .
	listed := map[string]bool{}
	if beliefs, err := c.store.ListBeliefs(); err == nil && len(beliefs) > 0 {
		var ring3 []store.Belief
		for _, b := range beliefs {
			if b.Ring == 3 {
				ring3 = append(ring3, b)
			}
		}
		if len(ring3) > 0 {
			parts = append(parts, "Current beliefs (for merge/supersede decisions):")
			for _, b := range ring3 {
				parts = append(parts, fmt.Sprintf("  [%s, %s] %s", b.ID, standingOrUnavailable(c.store, b.ID), b.Statement))
				listed[b.ID] = true
			}
		}
	}

	parts = append(parts, c.tensionsBlock(listed)...)

	if intentions, err := c.store.ListIntentions(); err == nil && len(intentions) > 0 {
		var active []store.Intention
		for _, i := range intentions {
			if i.State == "active" {
				active = append(active, i)
			}
		}
		if len(active) > 0 {
			parts = append(parts, "Active intentions:")
			for _, i := range active {
				parts = append(parts, fmt.Sprintf("  %s", i.Statement))
			}
		}
	}

	// .
	// .
	// .
	if c.ringWriter != nil {
		if prior := c.ringWriter.RingSection(ring.Ring3, "operator"); prior != "" {
			parts = append(parts, "Who you work with — your current model (revise it from the experiences above; do not reinvent it):")
			parts = append(parts, prior)
		}
	}

	return strings.Join(parts, "\n")
}

// .
// .
// .
// .
// .
func (c *ConsolidateFacility) tensionsBlock(listed map[string]bool) []string {
	view, err := renderTensions(c.tensions, listed, c.config.TensionsMaxChars)
	if err != nil {
		logsink.Warn("consolidate.error", "%v — the pass runs without the contradiction view", err)
		return nil
	}
	if view == "" {
		return nil
	}
	return []string{
		"Contradictions standing in the record (what each CONTESTED belief stands against — context to render, not a list to settle):",
		view,
	}
}

// .
func (c *ConsolidateFacility) SetTensions(ts TensionsSource) { c.tensions = ts }

// .
// .
// .
// .
func (c *ConsolidateFacility) writeRing3(ctx context.Context) {
	if c.ringWriter == nil {
		return
	}

	beliefs, err := c.store.ListBeliefs()
	if err != nil {
		return
	}

	experiences, _ := c.store.ListExperiences(5)
	intentions, _ := c.store.ListIntentions()

	// .
	var parts []string

	if len(beliefs) > 0 {
		var ring3 []store.Belief
		for _, b := range beliefs {
			if b.Ring == 3 {
				ring3 = append(ring3, b)
			}
		}
		if len(ring3) > 0 {
			parts = append(parts, "Beliefs:")
			for _, b := range ring3 {
				parts = append(parts, fmt.Sprintf("  [%s, ring %d] %s", standingOrUnavailable(c.store, b.ID), b.Ring, b.Statement))
			}
		}
	}

	// .
	// .
	parts = append(parts, c.tensionsBlock(nil)...)

	if len(experiences) > 0 {
		var salient []store.Experience
		for _, e := range experiences {
			if e.Raw == 0 {
				salient = append(salient, e)
			}
		}
		if len(salient) > 0 {
			parts = append(parts, "Recent experiences:")
			for _, e := range salient {
				parts = append(parts, fmt.Sprintf("  [%s] %s", e.Category, e.Content))
			}
		}
	}

	if len(intentions) > 0 {
		var active []store.Intention
		for _, i := range intentions {
			if i.State == "active" {
				active = append(active, i)
			}
		}
		if len(active) > 0 {
			parts = append(parts, "Active intentions:")
			for _, i := range active {
				parts = append(parts, fmt.Sprintf("  %s", i.Statement))
			}
		}
	}

	if len(parts) == 0 {
		return
	}

	userMsg := strings.Join(parts, "\n")
	// .
	// .
	// .
	callCtx, systemPrompt, err := withPreamble(ctx, c.authority, consolidateViewSystemPrompt)
	if err != nil {
		logsink.Warn("consolidate.error", "authority context unavailable: %v", err)
		c.writeRing3Deterministic()
		return
	}
	output, _, err := c.llm.ChatSimple(callCtx, systemPrompt, userMsg)
	if err != nil {
		logsink.Warn("consolidate.error", "LLM call for Ring 3 failed: %v", err)
		// .
		c.writeRing3Deterministic()
		return
	}

	// .
	// .
	// .
	if env, perr := parseConsolidationEnvelope(output); perr == nil {
		if len(env.Operations) > 0 {
			logsink.Warn("consolidate.refusal", "render-only pass returned %d operation(s) — dropped, nothing mints outside the metabolism pass", len(env.Operations))
		}
		output = env.Ring3View
	}

	if output != "" {
		if !c.ring3ViewFits(output) {
			c.writeRing3Deterministic()
			return
		}
		// .
		// .
		c.ringWriter.SetRingSection(ring.Ring3, "working_truth", output)
		logsink.Info("consolidate.end", "wrote %d chars to Ring 3 (working_truth, rendered from the store)", len(output))
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
// .
// .
// .
// .
// .
// .
// .
// .
func (c *ConsolidateFacility) ring3ViewFits(view string) bool {
	if len(view) <= c.config.Ring3MaxChars {
		return true
	}
	logsink.Warn("consolidate.refusal", "Ring 3 view REFUSED — %d chars over the %d-char bound (prompt.ring3_max_chars); "+
		"rendering working truth from the store instead (the beliefs are in the ledger)",
		len(view), c.config.Ring3MaxChars)
	return false
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
// .
// .
func boundRing3Render(render string, maxChars int) string {
	if len(render) <= maxChars {
		return render
	}
	lines := strings.Split(strings.TrimRight(render, "\n"), "\n")
	// .
	// .
	declare := func(dropped int) string {
		return fmt.Sprintf("\n[%d of %d lines not in view; recall (source=ledger) reaches the rest]\n", dropped, len(lines))
	}
	room := maxChars - len(declare(len(lines)))
	kept, used := 0, 0
	for _, line := range lines {
		if used+len(line)+1 > room {
			break
		}
		used += len(line) + 1
		kept++
	}
	return strings.Join(lines[:kept], "\n") + declare(len(lines)-kept)
}

// .
func (c *ConsolidateFacility) writeRing3Deterministic() {
	// .
	// .
	// .
	// .
	// .
	if c.ringWriter == nil {
		return
	}
	var sb strings.Builder

	beliefs, err := c.store.ListBeliefs()
	if err == nil && len(beliefs) > 0 {
		var ring3 []store.Belief
		for _, b := range beliefs {
			if b.Ring == 3 {
				ring3 = append(ring3, b)
			}
		}
		if len(ring3) > 0 {
			sb.WriteString("You believe:\n")
			for _, b := range ring3 {
				sb.WriteString(fmt.Sprintf("- [%s] %s\n", standingLabel(standingOrUnavailable(c.store, b.ID)), b.Statement))
			}
			sb.WriteString("\n")
		}
	}

	experiences, err := c.store.ListExperiences(5)
	if err == nil && len(experiences) > 0 {
		var salient []store.Experience
		for _, e := range experiences {
			if e.Raw == 0 {
				salient = append(salient, e)
			}
		}
		if len(salient) > 0 {
			sb.WriteString("You recently experienced:\n")
			for _, e := range salient {
				category := e.Category
				if category == "" {
					category = "observation"
				}
				sb.WriteString(fmt.Sprintf("- [%s] %s\n", category, e.Content))
			}
			sb.WriteString("\n")
		}
	}

	intentions, err := c.store.ListIntentions()
	if err == nil {
		var active []string
		for _, i := range intentions {
			if i.State == "active" {
				active = append(active, "- "+i.Statement)
			}
		}
		if len(active) > 0 {
			sb.WriteString("You're pursuing:\n" + strings.Join(active, "\n") + "\n")
		}
	}

	if sb.Len() > 0 {
		render := sb.String()
		bounded := boundRing3Render(render, c.config.Ring3MaxChars)
		if bounded != render {
			logsink.Warn("consolidate.budget", "deterministic Ring 3 render bounded — %d chars over the %d-char bound "+
				"(prompt.ring3_max_chars); whole trailing lines dropped, declared in the render",
				len(render), c.config.Ring3MaxChars)
		}
		c.ringWriter.SetRingSection(ring.Ring3, "working_truth", bounded)
	}
}

// .
// .
// .
// .
// .
// .
// .
func (c *ConsolidateFacility) confirmedCrossings() []store.ConfirmedCrossing {
	if c.store == nil {
		return nil
	}
	beliefs, err := c.store.ListBeliefs()
	if err != nil {
		logsink.Warn("consolidate.error", "beliefs cannot be listed — no crossing observed this pass: %v", err)
		return nil
	}
	ticks, _ := c.store.LifetimeTicks()
	if ticks <= 0 {
		return nil
	}
	var out []store.ConfirmedCrossing
	for _, b := range beliefs {
		if b.ConfirmedAtTicks != 0 {
			continue
		}
		standing, err := c.store.StandingFor(b.ID)
		if err != nil {
			logsink.Debug("consolidate.refusal", "standing of %s cannot be proven — not anchoring it: %v", b.ID, err)
			continue
		}
		if standing == "confirmed" {
			out = append(out, store.ConfirmedCrossing{ID: b.ID, Ticks: ticks})
		}
	}
	return out
}

// .
func (c *ConsolidateFacility) SetAuthority(src AuthoritySource) { c.authority = src }

// .
func (c *ConsolidateFacility) OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult {
	if err := c.Execute(ctx); err != nil {
		logsink.Warn("consolidate.error", "execute error: %v", err)
		return AlarmResult{Accepted: false}
	}
	return AlarmResult{Accepted: true}
}

// .
// .
// .
// .
// .
// .
// .
func standingOrUnavailable(src StandingSource, id string) string {
	standing, err := src.StandingFor(id)
	if err != nil {
		return "unavailable"
	}
	return standing
}

func standingLabel(standing string) string {
	if standing == "suspect" {
		return "CONTESTED"
	}
	return standing
}

// .
// .
// .
// .
func consolidationTool() llm.ToolDefinition {
	t := llm.ToolDefinition{Type: "function"}
	t.Function.Name = "emit_consolidation"
	t.Function.Description = "Emit the consolidation envelope: belief operations and the Ring 3 working-truth view."
	t.Function.Parameters = map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"operations": map[string]interface{}{
				"type":        "array",
				"description": "upsert (statement, confidence, and evidence: the ids of the experiences on the table it comes from or of beliefs it derives from — a belief never comes from nowhere) and supersede operations over beliefs",
				"items":       map[string]interface{}{"type": "object"},
			},
			"ring3_view": map[string]interface{}{
				"type":        "string",
				"description": "the identity's working truth, second person",
			},
			"operator": map[string]interface{}{
				"type":        "string",
				"description": "the person the identity works with, second person, as these experiences show them; omit when they show nothing new",
			},
		},
	}
	return t
}

// .
type DecisionLog interface {
	RecordMemoryDecision(store.MemoryDecision) error
}

// .
func (c *ConsolidateFacility) SetDecisionLog(l DecisionLog) { c.decisions = l }

// .
// .
// .
// .
// .
func (c *ConsolidateFacility) salienceFor(id, stmt string, conf float64, evidence, inputs []string, statements map[string]string) memory.SalienceDecision {
	f := memory.SalienceFeatures{
		Impact: conf, PersistenceHint: conf, EmotionalSalience: 0.5,
		CostToStore: memory.CostToStore(stmt, 400), CostToQuery: memory.CostToQuery(stmt),
	}
	best := 0.0
	for otherID, other := range statements {
		if otherID == id {
			continue
		}
		if sim := trigram.Similarity(stmt, other); sim > best {
			best = sim
		}
	}
	f.Novelty = 1 - best
	onTable := map[string]bool{}
	for _, in := range inputs {
		onTable[in] = true
	}
	var cited []string
	for _, ev := range evidence {
		if onTable[ev] {
			cited = append(cited, ev)
		}
	}
	if len(inputs) > 0 {
		f.Recurrence = float64(len(cited)) / float64(len(inputs))
	}
	if len(cited) > 0 {
		if prov, err := c.store.ProvenanceByIDs(cited); err == nil {
			trusted := 0
			for _, p := range prov {
				if p == "operator" || p == "external" {
					trusted++
				}
			}
			f.ProvenanceTrust = float64(trusted) / float64(len(cited))
		}
	}
	if intentions, err := c.store.ListIntentions(); err == nil {
		active, touched := 0, 0
		words := distinctiveWords(stmt)
		for _, in := range intentions {
			if in.State != "active" {
				continue
			}
			active++
			for w := range distinctiveWords(in.Statement) {
				if words[w] {
					touched++
					break
				}
			}
		}
		if active > 0 {
			f.DownstreamUse = float64(touched) / float64(active)
		}
	}
	return memory.Salience(f, c.config.Salience)
}

// .
func distinctiveWords(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) >= 5 {
			out[w] = true
		}
	}
	return out
}

func (c *ConsolidateFacility) logDecision(d memory.SalienceDecision, stmt string, seq uint64) {
	if c.decisions == nil {
		return
	}
	if err := c.decisions.RecordMemoryDecision(store.MemoryDecision{
		Kind: "salience", Facility: "consolidate", Decision: d.Class, Seq: seq, Score: d.Score,
		Record: map[string]interface{}{
			"candidate": stmt, "features": d.Features, "explanations": d.Explanations,
			"policy": d.Policy, "audit_hash": d.AuditHash, "ttl_days": d.TTLDays, "review_after": d.ReviewAfter,
		},
	}); err != nil {
		logsink.Warn("consolidate.error", "salience decision not logged: %v", err)
	}
}
