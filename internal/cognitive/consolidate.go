package cognitive

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"log"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// ConsolidateConfig holds CONSOLIDATE facility parameters.
type ConsolidateConfig struct {
	Threshold int // duplicate or unconsolidated count threshold
	MaxOps    int // runaway guard: operations minted per run (default 32)
}

// ConsolidateFacility implements CONSOLIDATE — convergent thinking that
// merges duplicates, supersedes outdated beliefs, and turns episodic
// experience into semantic knowledge.
//
// CONSOLIDATE also runs the R16 belief maturation ladder:
//
//	new → confirmed (distinct-source SUPPORTS edges, ≥3 post-dedup)
//	    → trusted (lived time without CONTRADICTS edge)
//	    → suspect (CONTRADICTS edge found)
//
// OUTPUT (external review 2026-08-20, H6/#4): the distillations are the
// product, and they mint as signed belief.upsert / belief.supersede
// events through the ONE preflighted door — working truth lives in the
// ledger, replay rebuilds it. Each metabolism run closes with a
// consolidation.run marker minted LAST ({inputs, outputs}); its
// materializer marks the input experiences consumed, so consumed state
// is f(ledger) too. The second-person Ring 3 view still renders (the
// prompt composer reads it as "what I'm working with"), but it is a
// display cache of the beliefs now — snapshot loss loses no truth.
type ConsolidateFacility struct {
	store      ConsolidateStore
	llm        LLMCaller
	ledger     ConsolidateLedger
	ringWriter RingWriter
	config     ConsolidateConfig
	authority  AuthoritySource
}

// ConsolidateStore is the store interface CONSOLIDATE needs.
type ConsolidateStore interface {
	ExperienceStore                                    // the raw queue (metabolizer)
	ListExperiences(n int) ([]store.Experience, error) // recent salient view (Ring 3 rendering)
	BeliefStore
	EdgeStore
	LifetimeStore
	IntentionStore
	EntityChecker
	ProvenanceResolver
	StandingSource
}

// StandingSource derives a belief's epistemic standing (store-owned,
// read-time f(edges, ticks) — 2026-08-17 ruling).
type StandingSource interface {
	StandingFor(id string) string
	StampConfirmed(id string, ticks int64) error
}

// ProvenanceResolver maps evidence ids to their authorship class.
type ProvenanceResolver interface {
	ProvenanceByIDs(ids []string) (map[string]string, error)
}

// EntityChecker resolves whether an entity id exists (ghost-edge guard).
type EntityChecker interface {
	EntityExists(id string) (bool, error)
}

// IntentionStore provides intention queries.
type IntentionStore interface {
	ListIntentions() ([]store.Intention, error)
}

// EdgeStore provides provenance graph queries.
type EdgeStore interface {
	ListEdgesForBelief(beliefID string) ([]store.Edge, error)
}

// LifetimeStore provides life-clock access.
type LifetimeStore interface {
	LifetimeTicks() (int64, error)
}

// ConsolidateLedger allows CONSOLIDATE to append belief promotions and edges.
type ConsolidateLedger interface {
	Append(eventType ledger.EventType, ring int, payload interface{}, modelID string) (*ledger.Event, error)
}

// NewConsolidate creates a CONSOLIDATE facility.
func NewConsolidate(store ConsolidateStore, llm LLMCaller, lg ConsolidateLedger, ringWriter RingWriter, cfg ConsolidateConfig) *ConsolidateFacility {
	if cfg.Threshold == 0 {
		cfg.Threshold = 3
	}
	if cfg.MaxOps == 0 {
		cfg.MaxOps = 32
	}
	return &ConsolidateFacility{
		store:      store,
		llm:        llm,
		ledger:     lg,
		ringWriter: ringWriter,
		config:     cfg,
	}
}

// Name returns the facility name.
func (c *ConsolidateFacility) Name() string { return "consolidate" }

// Predicate checks CONSOLIDATE's material threshold. R29's independent
// budget-remaining gate is not owned by this source predicate.
func (c *ConsolidateFacility) Predicate(ctx context.Context) bool {
	count, err := c.store.UnprocessedExperienceCount()
	if err != nil {
		return false
	}
	return count >= c.config.Threshold
}

// Execute runs CONSOLIDATE: merge duplicates, supersede outdated, semanticize,
// walk the R16 evidence ladder, and write working truth to Ring 3.
//
// The LLM's operations ARE the product — they mint as ledger events; the
// view is their rendering. If the LLM call fails or its envelope does not
// parse, nothing mints, the ring view stays, and the experiences are NOT
// consumed: nothing was produced, so nothing was consumed (they retry
// next pass). Marking input consumed while discarding output is paying
// for thoughts and throwing them away (honesty review 2026-08-16,
// finding A2). The converse also holds now: an honest "no change" pass
// (empty operations, current view) IS a product, and consumes — otherwise
// the same material would be re-chewed every pass forever.
func (c *ConsolidateFacility) Execute(ctx context.Context) error {
	if !c.Predicate(ctx) {
		// No new material — still refresh Ring 3 from current state
		c.writeRing3(ctx)
		return nil
	}

	// 1. ONE call: the raw experiences plus current state, semanticized.
	// Raw queue, oldest first — guaranteed progress (no starvation).
	experiences, err := c.store.ListRawExperiences(30)
	if err != nil {
		return fmt.Errorf("consolidate: list raw experiences: %w", err)
	}

	var expTexts []string
	var expIDs []string
	for _, e := range experiences {
		expTexts = append(expTexts, evidenceText(e))
		expIDs = append(expIDs, e.ID)
	}

	if len(expTexts) >= c.config.Threshold {
		userMsg := c.buildEvidenceBlock(expTexts)
		systemPrompt, err := withPreamble(c.authority, consolidateSystemPrompt)
		if err != nil {
			return fmt.Errorf("CONSOLIDATE: authority context: %w", err)
		}
		// Offer the envelope as a TOOL and take whichever channel comes
		// back. A substrate with real tool calls returns arguments that
		// need no fence stripped and were schema-checked on the way; one
		// without returns the same JSON as prose, exactly as before. The
		// capability is never declared or probed — it is observed, per
		// call, by which channel answered.
		output, modelID, viaTool, err := c.llm.ChatStructured(ctx, systemPrompt, userMsg, consolidationTool())
		if viaTool {
			log.Printf("CONSOLIDATE: envelope arrived as a native tool call")
		}
		if err != nil {
			log.Printf("CONSOLIDATE: LLM call failed: %v — experiences remain unprocessed (nothing landed, nothing consumed)", err)
			c.writeRing3Deterministic()
			c.writeRing4TopFromStore()
			return nil
		}

		env, perr := parseConsolidationEnvelope(output)
		if perr != nil {
			// Unparseable output = this pass produced nothing recordable.
			// Mint nothing, update no view, consume nothing — loud, and the
			// material retries next pass (the A2 law, structured edition).
			log.Printf("CONSOLIDATE: envelope rejected: %v — nothing minted, nothing consumed (retry next pass)", perr)
			return nil
		}

		// 2. The operations mint through the ONE door (preflight inside).
		// A refused operation is dropped loudly and the pass continues —
		// the facility never crashes on a malformed act, and a refusal
		// happens BEFORE append (never append-then-fail).
		outputs := c.mintOperations(env.Operations, modelID)

		// 3. The run marker, minted LAST (commit-marker ordering: a crash
		// before it = clean re-run; belief.upsert idempotence absorbs the
		// duplicated products). Its materializer consumes the inputs —
		// consumed state is f(ledger), replay restores it (H6).
		if c.ledger == nil {
			log.Printf("CONSOLIDATE: no ledger door — %d operation(s) and consumption skipped", len(env.Operations))
		} else if _, err := c.ledger.Append(ledger.EventConsolidationRun, 3,
			store.FacilityRunPayload{Inputs: expIDs, Outputs: outputs}, modelID); err != nil {
			log.Printf("CONSOLIDATE: run marker refused: %v — nothing consumed, pass will re-run", err)
		} else {
			log.Printf("CONSOLIDATE: consumed %d experiences into %d ledger event(s)", len(expIDs), len(outputs))
		}

		// 4. The view renders LAST — a display cache of the beliefs that
		// now live in the ledger, not the truth itself.
		//
		// AND ONLY WHEN SOMETHING MINTED. The envelope has two outputs
		// and only one of them is truth: operations become belief.*
		// events, ring3_view is prose the tool schema asks for freely
		// ("the identity's working truth, second person"). Nothing bound
		// the second to the first, so a pass could mint NOTHING, write a
		// whole working truth, and consume every input for it — the only
		// copy of that synthesis living in ring_snapshots, which replay
		// deliberately does not rebuild.
		//
		// The parser already refuses an envelope that says nothing at
		// all ("a degenerate {} must not consume 30 experiences"). This
		// is the same law one step further in: prose with no minted
		// belief behind it is not a product either.
		//
		// A pass that minted nothing falls back to the deterministic
		// render — the same one the LLM-failure path uses, built from
		// beliefs already in the store. Ring 3 is then ALWAYS either a
		// model's words about beliefs that just landed, or a render of
		// beliefs that were already there. Both are backed, which is
		// what made "losing the snapshot loses nothing semantic" true.
		switch {
		case len(outputs) > 0 && env.Ring3View != "" && c.ringWriter != nil:
			c.ringWriter.SetRingSection(ring.Ring3, "working_truth", env.Ring3View)
			log.Printf("CONSOLIDATE: wrote %d chars to Ring 3 (working_truth)", len(env.Ring3View))
		case len(outputs) == 0:
			if env.Ring3View != "" {
				log.Printf("CONSOLIDATE: the pass minted nothing — its %d-char view is not backed by any belief and was not kept; rendering Ring 3 from the store instead",
					len(env.Ring3View))
			}
			c.writeRing3Deterministic()
		}
	}

	// 5. Stamp confirmed-at anchors for newly-crossed beliefs (the
	// derived standing's bookkeeping; see stampConfirmedCrossings)
	if beliefs, err := c.store.ListBeliefs(); err == nil {
		c.stampConfirmedCrossings(beliefs)
	}

	// 6. Ring 4 priorities
	c.writeRing4TopFromStore()

	return nil
}

// beliefOperation is one act in the consolidation envelope. The model
// speaks statements and lineage; ids resolve against real beliefs and
// rings are never model-selected (R3).
type beliefOperation struct {
	Op         string   `json:"op"`
	ID         string   `json:"id,omitempty"`
	Statement  string   `json:"statement,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
	OldID      string   `json:"old_id,omitempty"`
	NewID      string   `json:"new_id,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

// consolidationEnvelope is the structured-output contract of the
// metabolism pass. Emission-agnostic by design: the local model may have
// weak tool_calls, so the contract is plain JSON the loop parses.
type consolidationEnvelope struct {
	Operations []beliefOperation `json:"operations"`
	Ring3View  string            `json:"ring3_view"`
}

// parseConsolidationEnvelope extracts the envelope from LLM output,
// forgiving the common local-model wrappers (markdown fences, prose
// around the object) but strict about the object itself: no parse, no
// product. An envelope that says nothing (no operations AND no view) is
// rejected too — a degenerate "{}" must not consume 30 experiences.
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

// mintOperations validates each operation and mints the valid ones as
// signed ledger events through the door; it returns the minted seqs (the
// run marker's outputs). Per-operation failures drop THAT operation with
// a loud log and continue — one hallucinated id must not wedge the
// pass into re-reading the same material forever.
func (c *ConsolidateFacility) mintOperations(ops []beliefOperation, modelID string) []uint64 {
	if len(ops) == 0 {
		return nil
	}
	if c.ledger == nil {
		return nil
	}
	if len(ops) > c.config.MaxOps {
		log.Printf("CONSOLIDATE: %d operations clamped to %d (runaway guard)", len(ops), c.config.MaxOps)
		ops = ops[:c.config.MaxOps]
	}

	// Snapshot of real beliefs: merge/supersede targets resolve against
	// these; ring 3 only — working truth is the unconscious's authority,
	// promoted identity (Ring 2) and charter are not its to rewrite.
	known := map[string]int{}
	if beliefs, err := c.store.ListBeliefs(); err == nil {
		for _, b := range beliefs {
			known[b.ID] = b.Ring
		}
	}

	// The model may name a NEW belief with a local id ("n1") and then
	// supersede toward it. The engine owns real ids — deterministic from
	// the statement, so a crash-and-re-run of the same distillation
	// upserts the same id instead of forking (idempotence) — and the
	// alias map carries the model's name to the engine's.
	alias := map[string]string{}
	minted := map[string]bool{}
	var outputs []uint64

	for i, op := range ops {
		switch op.Op {
		case "upsert":
			stmt := strings.TrimSpace(op.Statement)
			if stmt == "" {
				log.Printf("CONSOLIDATE: op %d (upsert) dropped — empty statement", i)
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
			id := strings.TrimSpace(op.ID)
			if r, exists := known[id]; id != "" && exists {
				if r != 3 {
					log.Printf("CONSOLIDATE: op %d (upsert) dropped — belief %q is ring %d, not working truth", i, id, r)
					continue
				}
			} else {
				engineID := "belief_" + outputHash(stmt)
				if id != "" {
					alias[id] = engineID
				}
				id = engineID
			}
			evt, err := c.ledger.Append(ledger.EventBeliefUpsert, 3, map[string]interface{}{
				"id": id, "statement": stmt, "ring": 3, "confidence": conf,
			}, modelID)
			if err != nil {
				log.Printf("CONSOLIDATE: op %d (upsert %q) refused before append: %v — dropped", i, id, err)
				continue
			}
			outputs = append(outputs, evt.Seq)
			minted[id] = true
		case "supersede":
			oldID := strings.TrimSpace(op.OldID)
			newID := strings.TrimSpace(op.NewID)
			if resolved, ok := alias[newID]; ok {
				newID = resolved
			}
			if r, ok := known[oldID]; !ok {
				log.Printf("CONSOLIDATE: op %d (supersede) dropped — old_id %q names no belief", i, oldID)
				continue
			} else if r != 3 {
				log.Printf("CONSOLIDATE: op %d (supersede) dropped — belief %q is ring %d, not working truth", i, oldID, r)
				continue
			}
			if _, ok := known[newID]; !ok && !minted[newID] {
				log.Printf("CONSOLIDATE: op %d (supersede) dropped — new_id %q names no belief (existing or upserted this pass)", i, newID)
				continue
			}
			if oldID == newID {
				log.Printf("CONSOLIDATE: op %d (supersede) dropped — a belief cannot supersede itself (%q)", i, oldID)
				continue
			}
			evt, err := c.ledger.Append(ledger.EventBeliefSupersede, 3, map[string]interface{}{
				"old_id": oldID, "new_id": newID, "reason": strings.TrimSpace(op.Reason),
			}, modelID)
			if err != nil {
				log.Printf("CONSOLIDATE: op %d (supersede %q→%q) refused before append: %v — dropped", i, oldID, newID, err)
				continue
			}
			outputs = append(outputs, evt.Seq)
		default:
			log.Printf("CONSOLIDATE: op %d dropped — unknown op %q (sanctioned: upsert, supersede)", i, op.Op)
		}
	}
	return outputs
}

// buildEvidenceBlock composes the LLM's input: raw experiences to
// consolidate plus current working state for context.
func (c *ConsolidateFacility) buildEvidenceBlock(expTexts []string) string {
	var parts []string
	parts = append(parts, "Experiences to consolidate:")
	parts = append(parts, joinLines(expTexts))

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
				parts = append(parts, fmt.Sprintf("  [%s, %s] %s", b.ID, c.store.StandingFor(b.ID), b.Statement))
			}
		}
	}

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

	return strings.Join(parts, "\n")
}

// writeRing4TopFromStore renders Ring 4 priorities from current intentions.
func (c *ConsolidateFacility) writeRing4TopFromStore() {
	if intentions, err := c.store.ListIntentions(); err == nil {
		c.writeRing4Top(intentions)
	}
}

// writeRing3 writes the current working truth into Ring 3.
// This is what the prompt composer reads for "What You're Working With."
// Uses LLM judgment to compose the ring content from current beliefs,
// experiences, and intentions.
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

	// Build evidence for the LLM
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
				parts = append(parts, fmt.Sprintf("  [%s, ring %d] %s", c.store.StandingFor(b.ID), b.Ring, b.Statement))
			}
		}
	}

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
	// Render-only prompt: no raw material on the table, so no operations
	// are commanded — prompt and plumbing agree that this pass mints
	// nothing (the metabolism pass is the only mint path).
	systemPrompt, err := withPreamble(c.authority, consolidateViewSystemPrompt)
	if err != nil {
		log.Printf("CONSOLIDATE: authority context unavailable: %v", err)
		c.writeRing3Deterministic()
		return
	}
	output, _, err := c.llm.ChatSimple(ctx, systemPrompt, userMsg)
	if err != nil {
		log.Printf("CONSOLIDATE: LLM call for Ring 3 failed: %v", err)
		// Fall back to deterministic rendering
		c.writeRing3Deterministic()
		return
	}

	// Defensive: a model that answers the render pass in envelope form
	// anyway gets its view extracted; any operations it smuggled are
	// dropped LOUDLY (they were not commanded and do not mint here).
	if env, perr := parseConsolidationEnvelope(output); perr == nil {
		if len(env.Operations) > 0 {
			log.Printf("CONSOLIDATE: render-only pass returned %d operation(s) — dropped, nothing mints outside the metabolism pass", len(env.Operations))
		}
		output = env.Ring3View
	}

	if output != "" {
		// CONSOLIDATE authors the Ring 3 BOTTOM half ("What You're
		// Working With") — DREAM owns the top; neither clobbers the other.
		c.ringWriter.SetRingSection(ring.Ring3, "working_truth", output)
		log.Printf("CONSOLIDATE: wrote %d chars to Ring 3 (working_truth)", len(output))
	}
	c.writeRing4Top(intentions)
}

// writeRing3Deterministic is the fallback when LLM is unavailable.
func (c *ConsolidateFacility) writeRing3Deterministic() {
	// writeRing4Top already guards this and this function did not, so a
	// facility built without a ring writer panicked here rather than
	// doing nothing. Latent until now — the LLM-failure path reaches
	// this same function — and surfaced by giving the no-mint pass a
	// deterministic fallback.
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
				sb.WriteString(fmt.Sprintf("- [%s] %s\n", standingLabel(c.store.StandingFor(b.ID)), b.Statement))
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
	if err == nil && len(intentions) > 0 {
		sb.WriteString("You're pursuing:\n")
		for _, i := range intentions {
			sb.WriteString(fmt.Sprintf("- %s\n", i.Statement))
		}
	}

	if sb.Len() > 0 {
		c.ringWriter.SetRingSection(ring.Ring3, "working_truth", sb.String())
	}
	c.writeRing4Top(intentions)
}

// writeRing4Top authors the TOP of Ring 4 — active priorities. CONSOLIDATE
// owns this half; the runtime work-session state is the bottom half
// (rendered by the composer from the live session).
func (c *ConsolidateFacility) writeRing4Top(intentions []store.Intention) {
	if c.ringWriter == nil {
		return
	}
	var lines []string
	for _, i := range intentions {
		if i.State == "active" {
			lines = append(lines, "- "+i.Statement)
		}
	}
	if len(lines) == 0 {
		return
	}
	c.ringWriter.SetRingSection(ring.Ring4, "priorities",
		"Active priorities:\n"+strings.Join(lines, "\n"))
}

// authorshipClassOf folds a provenance into its authorship class:
// self/dream/work/system = "resident" (the substrate's own voices);
// operator/external are independent classes.
func authorshipClassOf(provenance string) string {
	switch provenance {
	case "operator", "external":
		return provenance
	default:
		return "resident"
	}
}

// countAuthorshipClasses counts distinct equivalence classes among the
// evidence ids. A nil resolver counts every source as resident (fail-closed
// toward NOT confirming on independent classes).
func countAuthorshipClasses(store ProvenanceResolver, fromIDs []string) int {
	if store == nil {
		return 1
	}
	prov, err := store.ProvenanceByIDs(fromIDs)
	if err != nil {
		return 1
	}
	classes := map[string]bool{}
	for _, id := range fromIDs {
		classes[authorshipClassOf(prov[id])] = true // absent id → "" → resident
	}
	return len(classes)
}

// stampConfirmedCrossings records the confirmed-at anchor (bookkeeping,
// store-only) for beliefs whose DERIVED standing has crossed 'confirmed'
// — the anchor the derived 'trusted' standing's elapsed-time rule needs.
// The standing itself is never stored (2026-08-17 ruling: derive, don't
// lifecycle); this writes no ledger events.
func (c *ConsolidateFacility) stampConfirmedCrossings(beliefs []store.Belief) {
	if c.store == nil {
		return
	}
	ticks, _ := c.store.LifetimeTicks()
	for _, b := range beliefs {
		if b.ConfirmedAtTicks == 0 && c.store.StandingFor(b.ID) == "confirmed" {
			if err := c.store.StampConfirmed(b.ID, ticks); err != nil {
				log.Printf("CONSOLIDATE: stamp confirmed-at failed for %s: %v", b.ID, err)
			}
		}
	}
}

// SetAuthority wires the authority-preamble source (nil-safe; tests omit it).
func (c *ConsolidateFacility) SetAuthority(src AuthoritySource) { c.authority = src }

// OnAlarm handles TIME alarm dispatch.
func (c *ConsolidateFacility) OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult {
	if err := c.Execute(ctx); err != nil {
		log.Printf("CONSOLIDATE: execute error: %v", err)
		return AlarmResult{Accepted: false}
	}
	return AlarmResult{Accepted: true}
}

// standingLabel renders a derived standing for the resident-facing view.
func standingLabel(standing string) string {
	if standing == "suspect" {
		return "CONTESTED"
	}
	return standing
}

// consolidationTool mirrors consolidationEnvelope so a tool-capable
// model emits exactly what parseConsolidationEnvelope already reads.
// One shape, two channels: the parser is unchanged and stays the
// fallback for substrates with weak tool calls.
func consolidationTool() llm.ToolDefinition {
	t := llm.ToolDefinition{Type: "function"}
	t.Function.Name = "emit_consolidation"
	t.Function.Description = "Emit the consolidation envelope: belief operations and the Ring 3 working-truth view."
	t.Function.Parameters = map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"operations": map[string]interface{}{
				"type":        "array",
				"description": "upsert and supersede operations over beliefs",
				"items":       map[string]interface{}{"type": "object"},
			},
			"ring3_view": map[string]interface{}{
				"type":        "string",
				"description": "the identity's working truth, second person",
			},
		},
	}
	return t
}
