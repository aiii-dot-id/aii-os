package cognitive

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/cognitive/landing"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/memory/salience"
	"github.com/aiii-dot-id/aii-os/internal/memory/trigram"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

type ConsolidateConfig struct {
	Threshold int

	MaxOps int

	Batch int

	Salience salience.SalienceWeights

	Ring3MaxChars int

	TensionsMaxChars int

	ExcerptChars int

	RecentExperiences int

	OutcomeWindow int

	ObservationMaxChars int
}

const DefaultRing3MaxChars = 8000

const (
	DefaultConsolidateBatch  = 30
	DefaultConsolidateMaxOps = 32
)

type ConsolidateFacility struct {
	decisions  DecisionLog
	store      ConsolidateStore
	llm        LLMCaller
	ledger     ConsolidateLedger
	ringWriter RingWriter

	madeWith sync.Once
	weights  salience.SalienceWeights

	config    atomic.Pointer[ConsolidateConfig]
	authority AuthoritySource

	teller      *Teller
	aside       setAside
	outcomeShow atomic.Int64
	tensions    TensionsSource

	outcomes      outcomeReader
	outcomeCursor func(rows.OutcomeBatch) landing.Cursor
	intake        *landing.Lander

	ring3Mu    sync.Mutex
	retiredGen atomic.Uint64

	lastRenderIn  [sha256.Size]byte
	lastRenderOut string

	batch atomic.Int64

	refusedLast atomic.Bool
}

func (c *ConsolidateFacility) nextBatch(cfg *ConsolidateConfig) int {
	full := cfg.Batch
	if n := c.batch.Load(); n > 0 && int(n) < full {
		return int(n)
	}
	return full
}

func (c *ConsolidateFacility) cfg() *ConsolidateConfig { return c.config.Load() }

func (c *ConsolidateFacility) Reconfigure(cfg ConsolidateConfig) {
	if cfg.Threshold == 0 {
		cfg.Threshold = 3
	}
	if cfg.Salience.Version == "" {
		cfg.Salience = salience.DefaultSalience()
	}

	c.madeWith.Do(func() { c.weights = cfg.Salience })
	cfg.Salience = c.weights
	if cfg.MaxOps <= 0 {
		cfg.MaxOps = DefaultConsolidateMaxOps
	}
	if cfg.Batch <= 0 {
		cfg.Batch = DefaultConsolidateBatch
	}
	if cfg.Ring3MaxChars <= 0 {
		cfg.Ring3MaxChars = DefaultRing3MaxChars
	}
	if cfg.OutcomeWindow <= 0 {
		cfg.OutcomeWindow = DefaultOutcomeWindow
	}
	if cfg.ObservationMaxChars <= 0 {
		cfg.ObservationMaxChars = DefaultSurfacingMaxChars
	}
	c.config.Store(&cfg)
}

func (c *ConsolidateFacility) SetTeller(t *Teller) { c.teller = t }

func (c *ConsolidateFacility) Reconsider() {
	c.refusedLast.Store(false)
	c.aside.forget()
	c.outcomeShow.Store(0)
}

type ConsolidateStore interface {
	ExperienceStore

	ListExperiencesForConsolidate(n int) ([]rows.Experience, error)
	BeliefStore
	EdgeStore
	LifetimeStore
	IntentionStore
	EntityChecker
	ProvenanceResolver
	StandingSource
	RetiredSource
}

type StandingSource interface {
	StandingFor(id string) (string, error)

	Fault(err error) bool
}

type ProvenanceResolver interface {
	ProvenanceByIDs(ids []string) (map[string]string, error)
}

type EntityChecker interface {
	EntityExists(id string) (bool, error)
}

type IntentionStore interface {
	ListIntentions() ([]rows.Intention, error)
}

type EdgeStore interface {
	ListEdgesForBelief(beliefID string) ([]rows.Edge, error)
}

type LifetimeStore interface {
	LifetimeTicks() (int64, error)
}

type ConsolidateLedger interface {
	Append(eventType ledger.EventType, ring int, payload any, modelID string) (*ledger.Event, error)
}

func NewConsolidate(store ConsolidateStore, llm LLMCaller, lg ConsolidateLedger, ringWriter RingWriter, cfg ConsolidateConfig) *ConsolidateFacility {
	var door landing.Door
	if lg != nil {
		door = lg
	}
	c := &ConsolidateFacility{
		store:      store,
		llm:        llm,
		ledger:     lg,
		intake:     landing.New(door),
		ringWriter: ringWriter,
	}
	c.Reconfigure(cfg)
	return c
}

func (c *ConsolidateFacility) Name() string { return "consolidate" }

func (c *ConsolidateFacility) Predicate(ctx context.Context) bool { return c.due(c.cfg()) }

func (c *ConsolidateFacility) due(cfg *ConsolidateConfig) bool {
	count, err := c.store.UnprocessedExperienceCount()
	if err != nil {
		return false
	}
	if count >= cfg.Threshold {
		return true
	}
	if count == 0 {
		return false
	}

	experiences, err := c.waiting(cfg.Threshold)
	return err == nil && reservedWaiting(experiences)
}

func reservedWaiting(experiences []rows.Experience) bool {
	for _, e := range experiences {
		if strings.HasPrefix(e.ID, rows.OutcomeObservationPrefix) {
			return true
		}
	}
	return false
}

func (c *ConsolidateFacility) Execute(ctx context.Context) error {

	c.refusedLast.Store(false)
	cfg := c.cfg()
	if !c.due(cfg) {

		c.writeRing3(ctx, cfg)
		return nil
	}

	experiences, err := c.waiting(c.nextBatch(cfg))
	if err != nil {
		return fmt.Errorf("consolidate: list raw experiences: %w", err)
	}
	if len(experiences) == 0 {

		c.writeRing3(ctx, cfg)
		return nil
	}

	if len(experiences) < cfg.Threshold && !reservedWaiting(experiences) && c.batch.Load() == 0 {
		return nil
	}
	beforeEvidence := c.retiredGen.Load()
	material := c.material(cfg, experiences)
	view, callCtx, systemPrompt, err := c.fit(ctx, cfg, material, experiences)
	if none := (noRoom{}); errors.As(err, &none) {
		budget := "the model's input budget"
		if none.limit > 0 {
			budget = fmt.Sprintf("the model's input budget of %d tokens", none.limit)
		}
		c.toldOf().tell(Condition{Facility: "consolidate", Kind: ConditionNoRoom,
			Said: fmt.Sprintf("no experience fits beside what every request carries (the rings and the consolidation prompt) in %s — nothing sent, nothing consumed; %s, or a lower prompt.ring3_max_chars or prompt.tensions_max_chars, makes room", budget, c.teller.room())},
			fmt.Sprintf("consolidate|no_room|%d", none.limit),
			fmt.Sprintf("consolidation: none of your waiting experiences fits beside what every consolidation request carries in %s, so nothing was sent and nothing consumed. Your operator can make room on the Cognition card.", budget))
		c.writeRing3Deterministic(cfg)
		return nil
	}
	if err != nil {
		return fmt.Errorf("CONSOLIDATE: %w", err)
	}
	experiences = view.experiences
	expIDs := make([]string, 0, len(experiences))
	for _, e := range experiences {
		expIDs = append(expIDs, e.ID)
	}
	userMsg := material.render(view)

	output, modelID, viaTool, err := c.llm.ChatStructured(wire.WithTapSource(callCtx, "consolidate"), systemPrompt, userMsg, consolidationTool())
	if viaTool {
		logsink.Debug("consolidate.decision", "envelope arrived as a native tool call")
	}
	if err != nil {
		if refusedForWhatWasAsked(err) {
			c.refused(experiences, err.Error())
			return nil
		}
		logsink.Warn("consolidate.error", "LLM call failed: %v — experiences remain unprocessed (nothing landed, nothing consumed)", err)
		c.writeRing3Deterministic(cfg)
		return nil
	}

	env, perr := parseConsolidationEnvelope(output)
	if perr != nil {

		c.refused(experiences, "envelope rejected: "+perr.Error())
		return nil
	}

	if len(env.Operations) > cfg.MaxOps && len(experiences) > 1 {
		c.refused(experiences, fmt.Sprintf("%d operations, and a pass mints at most %d (cognition.consolidate_max_ops)", len(env.Operations), cfg.MaxOps))
		return nil
	}

	viewStale := c.retiredGen.Load() != beforeEvidence

	outputs, refused, mintErr := c.mintOperations(cfg, env.Operations, expIDs, modelID)
	if mintErr != nil {
		logsink.Warn("consolidate.refusal", "%v — nothing consumed; the material retries next pass", mintErr)
		return nil
	}
	if refused.total() > 0 && len(outputs) == 0 {

		c.refused(experiences, refused.said())
		return nil
	}

	if c.ledger == nil {
		logsink.Warn("consolidate.refusal", "no ledger door — %d operation(s) and consumption skipped", len(env.Operations))
	} else if _, err := c.ledger.Append(ledger.EventConsolidationRun, 3,
		rows.FacilityRunPayload{Inputs: expIDs, Outputs: outputs, Confirmed: c.confirmedCrossings()}, modelID); err != nil {
		logsink.Warn("consolidate.refusal", "run marker refused: %v — nothing consumed, pass will re-run", err)
	} else {
		logsink.Info("consolidate.end", "consumed %d experiences into %d ledger event(s)", len(expIDs), len(outputs))

		next := 2 * c.batch.Load()
		if next >= int64(cfg.Batch) {
			next = 0
		}
		c.batch.Store(next)
		c.sayFit(material, view)
		if n := len(env.Operations); n > cfg.MaxOps {

			c.teller.say(Condition{Facility: "consolidate", Kind: ConditionRefused,
				Said: fmt.Sprintf("the reply to a pass of one experience (%s) held %d operations, and a pass mints at most %d (cognition.consolidate_max_ops) — the %d past that were not minted, and the experience is consumed", expIDs[0], n, cfg.MaxOps, n-cfg.MaxOps)})
		}
	}

	afterMints := c.retiredGen.Load()

	partial := view.beliefs < len(material.beliefs)
	switch {
	case len(outputs) > 0 && env.Ring3View != "" && c.ringWriter != nil && partial:
		logsink.Info("consolidate.decision", "the pass was shown %d of %d current beliefs — its view describes part of the working truth and is not kept; Ring 3 is rendered from the store", view.beliefs, len(material.beliefs))
		c.writeRing3Deterministic(cfg)
	case len(outputs) > 0 && env.Ring3View != "" && c.ringWriter != nil:
		if !c.ring3ViewFits(cfg, env.Ring3View) {

			c.writeRing3Deterministic(cfg)
			break
		}
		if viewStale || !c.writeViewIfCurrent(afterMints, env.Ring3View) {
			logsink.Info("consolidate.decision", "a belief was retired while the pass was writing its %d-char view — the view is not kept; Ring 3 stands as rendered from the store", len(env.Ring3View))
			c.writeRing3Deterministic(cfg)
			break
		}
		logsink.Info("consolidate.end", "wrote %d chars to Ring 3 (working_truth, from the pass)", len(env.Ring3View))
	case len(outputs) == 0:
		if env.Ring3View != "" {
			logsink.Info("consolidate.decision", "the pass minted nothing — its %d-char view is not backed by any belief and was not kept; rendering Ring 3 from the store instead",
				len(env.Ring3View))
		}
		c.writeRing3Deterministic(cfg)
	}

	if operator := strings.TrimSpace(env.Operator); operator != "" {
		switch {
		case !view.extras:
			logsink.Info("consolidate.decision", "the pass was not shown the operator model, for want of room — its %d-char model is not kept; the current one stands", len(operator))
		case len(outputs) > 0 && c.ringWriter != nil:
			c.ringWriter.SetRingSection(ring.Ring3, "operator", operator)
			logsink.Info("consolidate.end", "wrote %d chars to Ring 3 (operator)", len(operator))
		case len(outputs) == 0:
			logsink.Info("consolidate.decision", "the pass minted nothing — its %d-char operator model is not backed by any belief and was not kept", len(operator))
		}
	}
	return nil
}

func (c *ConsolidateFacility) sayFit(m *passMaterial, v evidenceView) {
	var left []string
	if v.cutTo > 0 {
		left = append(left, fmt.Sprintf("experience %s was read in part (%d of %d characters)", v.experiences[0].ID, v.cutTo, v.cutFrom))
	}
	if v.beliefs < len(m.beliefs) {
		left = append(left, fmt.Sprintf("%d of %d current beliefs were shown", v.beliefs, len(m.beliefs)))
	}
	if v.intentions < len(m.intentions) {
		left = append(left, fmt.Sprintf("%d of %d active intentions were shown", v.intentions, len(m.intentions)))
	}
	if !v.extras {
		left = append(left, "the retired beliefs, the contradictions and the operator model were left out")
	}
	if len(left) == 0 {
		c.teller.say(Condition{Facility: "consolidate", Kind: ConditionWhole, Said: fmt.Sprintf("the last pass carried all it was asked: %d experience(s), %d belief(s), %d intention(s)", len(v.experiences), len(m.beliefs), len(m.intentions))})
		return
	}
	said := fmt.Sprintf("the last pass fitted the model's input budget: %s — a lower cognition.consolidate_batch (fewer experiences a pass), or %s, makes room", strings.Join(left, "; "), c.teller.room())
	if v.cutTo > 0 {

		id := v.experiences[0].ID
		c.toldOf().tellOf(id, Condition{Facility: "consolidate", Kind: ConditionFitted, Said: said},
			"consolidate|cut|"+id,
			fmt.Sprintf("consolidation: your experience %s was longer than one consolidation pass can carry; its first %d of %d characters were read, and it is consumed as read in part. The record holds it whole.", id, v.cutTo, v.cutFrom))
		return
	}
	if v.beliefs >= len(m.beliefs) {
		c.teller.say(Condition{Facility: "consolidate", Kind: ConditionFitted, Said: said})
		return
	}
	c.toldOf().tell(Condition{Facility: "consolidate", Kind: ConditionFitted, Said: said},
		fmt.Sprintf("consolidate|beliefs|%d", doubling(len(m.beliefs))),
		fmt.Sprintf("consolidation: only %d of your %d current beliefs fit beside the experiences a pass reads, so each pass weighs those nearest what it read; the rest stand in your record unchanged, and recall (source=ledger) reaches them. Your operator can make room on the Cognition card.", v.beliefs, len(m.beliefs)))
}

type beliefOperation struct {
	Op           string   `json:"op"`
	ID           string   `json:"id,omitempty"`
	Statement    string   `json:"statement,omitempty"`
	Confidence   *float64 `json:"confidence,omitempty"`
	OldID        string   `json:"old_id,omitempty"`
	NewID        string   `json:"new_id,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	DistinctFrom []string `json:"distinct_from,omitempty"`
	Distinction  string   `json:"distinction,omitempty"`

	Evidence []string `json:"evidence,omitempty"`
}

type consolidationEnvelope struct {
	Operations []beliefOperation `json:"operations"`
	Ring3View  string            `json:"ring3_view"`

	Operator string `json:"operator,omitempty"`
}

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

type upsertRefusals struct {
	uncited, atDoor int
}

func (r upsertRefusals) total() int { return r.uncited + r.atDoor }

func (r upsertRefusals) said() string {
	switch {
	case r.atDoor == 0:
		return fmt.Sprintf("%d upsert(s) refused for citing nothing behind them, and nothing else minted", r.uncited)
	case r.uncited == 0:
		return fmt.Sprintf("%d upsert(s) refused before append, and nothing else minted", r.atDoor)
	default:
		return fmt.Sprintf("%d upsert(s) refused, %d for citing nothing behind them and %d before append, and nothing else minted", r.total(), r.uncited, r.atDoor)
	}
}

func (c *ConsolidateFacility) newBeliefID(stmt string, known map[string]int) (string, error) {
	for n := 1; ; n++ {
		id := "belief_" + outputHash(stmt)
		if n > 1 {
			id = "belief_" + outputHash(fmt.Sprintf("%s\x00%d", stmt, n))
		}
		if _, active := known[id]; active {
			return id, nil
		}
		held, err := c.store.EntityExists(id)
		if err != nil {
			return "", fmt.Errorf("whether %q is already a belief could not be read: %w", id, err)
		}
		if !held {
			return id, nil
		}
	}
}

func (c *ConsolidateFacility) mintOperations(cfg *ConsolidateConfig, ops []beliefOperation, inputs []string, modelID string) ([]uint64, upsertRefusals, error) {
	if len(ops) == 0 {
		return nil, upsertRefusals{}, nil
	}
	if c.ledger == nil {
		return nil, upsertRefusals{}, nil
	}
	onTable := map[string]bool{}
	for _, id := range inputs {
		onTable[id] = true
	}
	var refused upsertRefusals
	if maxOps := cfg.MaxOps; len(ops) > maxOps {

		ops = ops[:maxOps]
	}

	known := map[string]int{}
	statements := map[string]string{}
	beliefs, err := c.store.ListBeliefs()
	if err != nil {
		return nil, upsertRefusals{}, fmt.Errorf("belief comparison unavailable: %w", err)
	}
	for _, b := range beliefs {
		known[b.ID] = b.Ring
		statements[b.ID] = b.Statement
	}

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

			id := strings.TrimSpace(op.ID)
			if _, exists := known[id]; id == "" || !exists {
				engineID, err := c.newBeliefID(stmt, known)
				if err != nil {
					return outputs, refused, fmt.Errorf("op %d (upsert): %w", i, err)
				}
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
			var origins []map[string]string
			seen := map[string]bool{}
			invalid := false
			for _, raw := range op.Evidence {
				ev := strings.TrimSpace(raw)
				if resolved, ok := alias[ev]; ok {
					ev = resolved
				}
				if ev == "" || ev == id {
					invalid = true
					continue
				}
				if seen[ev] {
					continue
				}
				if _, exists := known[ev]; !exists && !onTable[ev] && !minted[ev] {
					logsink.Debug("consolidate.refusal", "op %d (upsert %q) cites %q, which is neither on the table nor a belief — citation dropped", i, id, ev)
					invalid = true
					continue
				}
				seen[ev] = true
				evidence = append(evidence, ev)
				kind := "belief"
				if onTable[ev] {
					kind = "experience"
				}
				origins = append(origins, map[string]string{"kind": kind, "id": ev})
			}
			if invalid || len(evidence) == 0 {
				logsink.Debug("consolidate.refusal", "op %d (upsert %q) dropped — missing or invalid experience/belief origin (evidence: %v)", i, id, op.Evidence)
				refused.uncited++
				continue
			}

			decision := c.salienceFor(cfg, id, stmt, conf, evidence, inputs, statements)
			if decision.Class == salience.SalienceMemo {
				logsink.Debug("consolidate.refusal", "op %d (upsert %q) kept as a memo by salience (score %.2f; %s)", i, id, decision.Score, strings.Join(decision.Explanations, "; "))
				c.logDecision(decision, stmt, 0)
				continue
			}
			distinctFrom := make([]string, 0, len(op.DistinctFrom))
			for _, candidate := range op.DistinctFrom {
				if resolved, ok := alias[candidate]; ok {
					candidate = resolved
				}
				distinctFrom = append(distinctFrom, candidate)
			}
			evt, err := c.ledger.Append(ledger.EventBeliefUpsert, 3, map[string]any{
				"id": id, "statement": stmt, "ring": 3, "confidence": conf,
				"origin_refs":   origins,
				"distinct_from": distinctFrom, "distinction": strings.TrimSpace(op.Distinction),
			}, modelID)
			if err != nil {
				if !errors.Is(err, rows.ErrRefused) {
					return outputs, refused, fmt.Errorf("op %d (upsert %q) neither landed nor was refused: %w", i, id, err)
				}
				logsink.Debug("consolidate.refusal", "op %d (upsert %q): %v — dropped", i, id, err)
				refused.atDoor++
				continue
			}
			outputs = append(outputs, evt.Seq)
			minted[id] = true
			known[id] = 3
			statements[id] = stmt
			c.logDecision(decision, stmt, evt.Seq)
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

			if standing, err := c.store.StandingFor(oldID); c.store.Fault(err) {
				return outputs, refused, fmt.Errorf("op %d (supersede %q): its standing could not be read: %w", i, oldID, err)
			} else if err != nil {
				logsink.Debug("consolidate.refusal", "op %d (supersede %q) dropped — its standing cannot be derived (%v), so it cannot be shown uncontested", i, oldID, err)
				continue
			} else if standing == "suspect" {
				logsink.Warn("consolidate.refusal", "op %d (supersede %q→%q) dropped — %q is CONTESTED: retiring it would drop an unresolved contradiction from working truth; that is the identity's to settle, not consolidation's", i, oldID, newID, oldID)
				continue
			}
			evt, err := c.ledger.Append(ledger.EventBeliefSupersede, 3, map[string]any{
				"old_id": oldID, "new_id": newID, "reason": strings.TrimSpace(op.Reason),
			}, modelID)
			if err != nil {
				if !errors.Is(err, rows.ErrRefused) {
					return outputs, refused, fmt.Errorf("op %d (supersede %q→%q) neither landed nor was refused: %w", i, oldID, newID, err)
				}
				logsink.Debug("consolidate.refusal", "op %d (supersede %q→%q): %v — dropped", i, oldID, newID, err)
				continue
			}
			outputs = append(outputs, evt.Seq)
		default:
			logsink.Debug("consolidate.refusal", "op %d dropped — unknown op %q (sanctioned: upsert, supersede)", i, op.Op)
		}
	}
	return outputs, refused, nil
}

func (c *ConsolidateFacility) tensionsBlock(cfg *ConsolidateConfig, listed map[string]bool) []string {
	return tensionsLines(c.tensions, listed, cfg.TensionsMaxChars, cfg.ExcerptChars)
}

func tensionsLines(src TensionsSource, listed map[string]bool, maxChars, excerptChars int) []string {
	view, err := renderTensions(src, listed, maxChars, excerptChars)
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

func (c *ConsolidateFacility) SetTensions(ts TensionsSource) { c.tensions = ts }

func (c *ConsolidateFacility) writeRing3(ctx context.Context, cfg *ConsolidateConfig) {
	if c.ringWriter == nil {
		return
	}

	rendered := c.retiredGen.Load()
	beliefs, err := c.store.ListBeliefs()
	if err != nil {
		return
	}

	experiences, _ := c.store.ListExperiencesForConsolidate(recentExperiences(cfg))
	intentions, _ := c.store.ListIntentions()

	var parts []string

	if len(beliefs) > 0 {
		var ring3 []rows.Belief
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

	parts = append(parts, c.tensionsBlock(cfg, nil)...)

	if len(experiences) > 0 {
		var salient []rows.Experience
		for _, e := range experiences {
			if e.Raw == 0 {
				salient = append(salient, e)
			}
		}
		if len(salient) > 0 {

			parts = append(parts, "Recent experiences:")
			for _, e := range salient {
				parts = append(parts, fmt.Sprintf("  [%s] %s", e.Category, evidenceText(e)))
			}
		}
	}

	if len(intentions) > 0 {
		var active []rows.Intention
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

	check := simpleCheck(c.llm)
	callCtx, systemPrompt, err := fittedPreamble(ctx, c.authority, consolidateViewPrompt(cfg.Ring3MaxChars), userMsg, nil, check)
	if err != nil {
		logsink.Warn("consolidate.error", "authority context unavailable: %v", err)
		c.writeRing3Deterministic(cfg)
		return
	}
	if ask := (&admission{check: check, ctx: callCtx, system: systemPrompt}); !ask.admits(userMsg) {

		if ask.err != nil {
			logsink.Warn("consolidate.error", "the render-only pass was not sent: %v — Ring 3 is rendered from the store instead", ask.err)
		} else {
			logsink.Info("consolidate.budget", "the render-only pass does not fit the model's input budget of %d tokens — Ring 3 is rendered from the store instead, and no call is made", ask.limit)
		}
		c.writeRing3Deterministic(cfg)
		return
	}

	in := sha256.Sum256([]byte(systemPrompt + "\x00" + userMsg))
	if in == c.lastRenderIn && c.lastRenderOut != "" && c.ringWriter.RingSection(ring.Ring3, "working_truth") == c.lastRenderOut {
		logsink.Info("consolidate.decision", "Ring 3 render skipped: its input is unchanged since the last render, which still stands")
		return
	}
	output, _, err := c.llm.ChatSimple(wire.WithTapSource(callCtx, "consolidate"), systemPrompt, userMsg)
	if err != nil {
		logsink.Warn("consolidate.error", "LLM call for Ring 3 failed: %v", err)

		c.writeRing3Deterministic(cfg)
		return
	}

	if env, perr := parseConsolidationEnvelope(output); perr == nil {
		if len(env.Operations) > 0 {
			logsink.Warn("consolidate.refusal", "render-only pass returned %d operation(s) — dropped, nothing mints outside the metabolism pass", len(env.Operations))
		}
		output = env.Ring3View
	}

	if output != "" {
		if !c.ring3ViewFits(cfg, output) {
			c.writeRing3Deterministic(cfg)
			return
		}

		if !c.writeViewIfCurrent(rendered, output) {
			logsink.Info("consolidate.decision", "a belief was retired while Ring 3 was being rendered — the %d-char render is not kept; Ring 3 stands as rendered from the store", len(output))
			return
		}
		c.lastRenderIn, c.lastRenderOut = in, output
		logsink.Info("consolidate.end", "wrote %d chars to Ring 3 (working_truth, rendered from the store)", len(output))
	}
}

func (c *ConsolidateFacility) ring3ViewFits(cfg *ConsolidateConfig, view string) bool {
	bound := cfg.Ring3MaxChars
	if utf8.RuneCountInString(view) <= bound {
		return true
	}
	logsink.Warn("consolidate.refusal", "Ring 3 view REFUSED — %d chars over the %d-char bound (prompt.ring3_max_chars); "+
		"rendering working truth from the store instead (the beliefs are in the ledger)",
		utf8.RuneCountInString(view), bound)
	return false
}

const DefaultRecentExperiences = 5

func recentExperiences(cfg *ConsolidateConfig) int {
	if cfg.RecentExperiences > 0 {
		return cfg.RecentExperiences
	}
	return DefaultRecentExperiences
}

func boundRing3Render(render string, maxChars int) string {
	if utf8.RuneCountInString(render) <= maxChars {
		return render
	}
	lines := strings.Split(strings.TrimRight(render, "\n"), "\n")

	declare := func(dropped int) string {
		return fmt.Sprintf("\n[%d of %d lines not in view; recall (source=ledger) reaches the rest]\n", dropped, len(lines))
	}
	room := maxChars - utf8.RuneCountInString(declare(len(lines)))
	kept, used := 0, 0
	for _, line := range lines {
		n := utf8.RuneCountInString(line) + 1
		if used+n > room {
			break
		}
		used += n
		kept++
	}
	return strings.Join(lines[:kept], "\n") + declare(len(lines)-kept)
}

func (c *ConsolidateFacility) writeRing3Deterministic(cfg *ConsolidateConfig) {
	c.ring3Mu.Lock()
	defer c.ring3Mu.Unlock()
	c.renderRing3Deterministic(cfg, false)
}

func (c *ConsolidateFacility) renderRing3Deterministic(cfg *ConsolidateConfig, clearIfEmpty bool) {

	if c.ringWriter == nil {
		return
	}
	var sb strings.Builder

	beliefs, err := c.store.ListBeliefs()
	if err == nil && len(beliefs) > 0 {
		var ring3 []rows.Belief
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

	experiences, err := c.store.ListExperiencesForConsolidate(recentExperiences(cfg))
	if err == nil && len(experiences) > 0 {
		var salient []rows.Experience
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
				sb.WriteString(fmt.Sprintf("- [%s] %s\n", category, evidenceLine(e, cfg.ExcerptChars)))
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
		bound := cfg.Ring3MaxChars
		bounded := boundRing3Render(render, bound)
		if bounded != render {
			logsink.Warn("consolidate.budget", "deterministic Ring 3 render bounded — %d chars over the %d-char bound "+
				"(prompt.ring3_max_chars); whole trailing lines dropped, declared in the render",
				len(render), bound)
		}
		c.ringWriter.SetRingSection(ring.Ring3, "working_truth", bounded)
	} else if clearIfEmpty {
		c.ringWriter.SetRingSection(ring.Ring3, "working_truth", "")
	}
}

func (c *ConsolidateFacility) writeViewIfCurrent(gen uint64, view string) bool {
	c.ring3Mu.Lock()
	defer c.ring3Mu.Unlock()
	if c.retiredGen.Load() != gen {
		return false
	}
	c.ringWriter.SetRingSection(ring.Ring3, "working_truth", view)
	return true
}

func (c *ConsolidateFacility) BeliefRetired() {
	if c.ringWriter == nil {
		return
	}
	c.retiredGen.Add(1)
	c.ring3Mu.Lock()
	defer c.ring3Mu.Unlock()
	c.renderRing3Deterministic(c.cfg(), true)
	logsink.Info("consolidate.end", "a belief was retired — Ring 3 rewritten from the store at once (no model call; the next pass renders the prose)")
}

func (c *ConsolidateFacility) ReconcileRing3(writtenAt time.Time) {
	retired, _, err := c.store.RetiredBeliefs(0, 1)
	switch {
	case err != nil:
		logsink.Warn("consolidate.error", "the record of retirements cannot be read (%v) — Ring 3 is rewritten from the store rather than trusted", err)
	case len(retired) == 0:
		return
	default:
		if at, perr := time.Parse(time.RFC3339Nano, retired[0].RetiredAt); perr == nil && at.Before(writtenAt) {
			return
		}
		logsink.Info("consolidate.decision", "the restored Ring 3 was written before the record's last retirement (%s)", retired[0].RetiredAt)
	}
	c.BeliefRetired()
}

func (c *ConsolidateFacility) confirmedCrossings() []rows.ConfirmedCrossing {
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
	var out []rows.ConfirmedCrossing
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
			out = append(out, rows.ConfirmedCrossing{ID: b.ID, Ticks: ticks})
		}
	}
	return out
}

func (c *ConsolidateFacility) SetAuthority(src AuthoritySource) { c.authority = src }

func (c *ConsolidateFacility) OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult {
	if err := c.Execute(ctx); err != nil {
		logsink.Warn("consolidate.error", "execute error: %v", err)
		return AlarmResult{Accepted: false}
	}
	return AlarmResult{Accepted: true}
}

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

func consolidationTool() wire.ToolDefinition {
	t := wire.ToolDefinition{Type: "function"}
	t.Function.Name = "emit_consolidation"
	t.Function.Description = "Emit the consolidation envelope: belief operations and the Ring 3 working-truth view."
	t.Function.Parameters = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operations": map[string]any{
				"type":        "array",
				"description": "upsert (statement, confidence, and evidence: the ids of the experiences on the table it comes from or of beliefs it derives from — a belief never comes from nowhere) and supersede operations over beliefs",
				"items":       map[string]any{"type": "object"},
			},
			"ring3_view": map[string]any{
				"type":        "string",
				"description": "the identity's working truth, second person",
			},
			"operator": map[string]any{
				"type":        "string",
				"description": "the person the identity works with, as these experiences show them, written to the identity (\"you\" is the identity) with the operator in the third person by their name, never addressed; omit when they show nothing new",
			},
		},
	}
	return t
}

type DecisionLog interface {
	RecordMemoryDecision(rows.MemoryDecision) error
}

func (c *ConsolidateFacility) SetDecisionLog(l DecisionLog) { c.decisions = l }

func (c *ConsolidateFacility) salienceFor(cfg *ConsolidateConfig, id, stmt string, conf float64, evidence, inputs []string, statements map[string]string) salience.SalienceDecision {
	f := salience.SalienceFeatures{
		Impact: conf, PersistenceHint: conf, EmotionalSalience: 0.5,
		CostToStore: salience.CostToStore(stmt, 400), CostToQuery: salience.CostToQuery(stmt),
	}
	best := 0.0
	like := trigram.Against(stmt)
	for otherID, other := range statements {
		if otherID == id {
			continue
		}
		if sim := like(other); sim > best {
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
	return salience.Salience(f, cfg.Salience)
}

func distinctiveWords(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) >= 5 {
			out[w] = true
		}
	}
	return out
}

func (c *ConsolidateFacility) logDecision(d salience.SalienceDecision, stmt string, seq uint64) {
	if c.decisions == nil {
		return
	}
	if err := c.decisions.RecordMemoryDecision(rows.MemoryDecision{
		Kind: "salience", Facility: "consolidate", Decision: d.Class, Seq: seq, Score: d.Score,
		Record: map[string]any{
			"candidate": stmt, "features": d.Features, "explanations": d.Explanations,
			"policy": d.Policy, "audit_hash": d.AuditHash, "ttl_days": d.TTLDays, "review_after": d.ReviewAfter,
		},
	}); err != nil {
		logsink.Warn("consolidate.error", "salience decision not logged: %v", err)
	}
}
