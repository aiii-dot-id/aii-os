package cognitive

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/cognitive/landing"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

type OutcomeSource interface {
	NextOutcomes(window int) (rows.OutcomeBatch, error)
	PublishOutcomeCursor(from, through uint64) error
	OwnFingerprint() (string, error)
}

const DefaultOutcomeWindow = 512

const outcomeClaimQualifier = "self-reported, unverified"

type outcomeReader interface {
	NextOutcomes(window int) (rows.OutcomeBatch, error)
	OwnFingerprint() (string, error)
}

func (c *ConsolidateFacility) SetOutcomes(src OutcomeSource) {
	if src == nil {
		c.outcomes, c.outcomeCursor = nil, nil
		return
	}
	c.outcomes, c.outcomeCursor = src, landing.OutcomeCursors(src)
}

func (c *ConsolidateFacility) OutcomesWired() bool { return c.outcomes != nil }

func (c *ConsolidateFacility) OutcomesPending() bool {
	if c.outcomes == nil {
		return false
	}
	batch, err := c.outcomes.NextOutcomes(c.cfg().OutcomeWindow)
	if err != nil {
		logsink.Warn("consolidate.error", "outcome probe failed: %v — nothing considered, the cursor stands", err)
		return false
	}
	if len(batch.Outcomes) > 0 {
		return true
	}
	c.passOver(batch)
	return false
}

func (c *ConsolidateFacility) passOver(batch rows.OutcomeBatch) {
	if _, err := c.intake.PassOver(c.outcomeCursor(batch)); err != nil {
		logsink.Warn("consolidate.error", "outcome cursor not published: %v — the same records are considered again", err)
	}
}

func (c *ConsolidateFacility) ObserveOutcomes(ctx context.Context) error {
	if c.outcomes == nil {
		return fmt.Errorf("outcome intake: no source wired")
	}
	if c.ledger == nil {
		return fmt.Errorf("outcome intake: no ledger door — an observation that cannot be recorded is not made")
	}
	cfg := c.cfg()
	batch, err := c.outcomes.NextOutcomes(cfg.OutcomeWindow)
	if err != nil {
		return fmt.Errorf("outcome intake: %w", err)
	}
	if len(batch.Outcomes) == 0 {
		c.passOver(batch)
		return nil
	}
	self, err := c.outcomes.OwnFingerprint()
	if err != nil {
		return fmt.Errorf("outcome intake: %w", err)
	}
	if self == "" {

		return fmt.Errorf("outcome intake: this record's genesis carries no fingerprint — an outcome cannot be cited")
	}

	stood := len(batch.Outcomes)
	if n := int(c.outcomeShow.Load()); n > 0 && n < len(batch.Outcomes) {
		batch = keepOutcomes(batch, n)
	}

	base := outcomePrompt(cfg.ObservationMaxChars)
	check := simpleCheck(c.llm)
	callCtx, systemPrompt, err := fittedPreamble(ctx, c.authority, base, outcomesMessage(keepOutcomes(batch, 1), cfg.ExcerptChars), nil, check)
	if err != nil {
		return fmt.Errorf("outcome intake: authority context: %w", err)
	}
	if check != nil {
		ask := &admission{check: check, ctx: callCtx, system: systemPrompt}
		n, _ := largestAdmitted(len(batch.Outcomes), func(k int) bool {
			return k == 0 || ask.admits(outcomesMessage(keepOutcomes(batch, k), cfg.ExcerptChars))
		})
		if ask.err != nil {
			return fmt.Errorf("outcome intake: %w — nothing sent, the outcomes wait", ask.err)
		}
		if n == 0 {
			c.teller.say(Condition{Facility: "outcomes", Kind: ConditionNoRoom,
				Said: "no outcome fits beside what every request carries (the rings and the intake's prompt) in the model's input budget — nothing sent, nothing published; " + c.teller.room() + " makes room"})
			return fmt.Errorf("outcome intake: no outcome fits the model's input budget beside the required context — nothing sent")
		}
		if n < len(batch.Outcomes) {
			logsink.Info("consolidate.budget", "outcome intake: %d of %d outcome(s) fit the model's input budget; the rest wait for the next intake", n, len(batch.Outcomes))
			batch = keepOutcomes(batch, n)
		}
	}
	cites := make([]ledger.Citation, 0, len(batch.Outcomes))
	for _, o := range batch.Outcomes {
		cites = append(cites, ledger.Citation{Identity: self, Seq: o.Seq, EntryHash: o.EntryHash})
	}
	output, modelID, err := c.llm.ChatSimple(wire.WithTapSource(callCtx, "outcome"), systemPrompt, outcomesMessage(batch, cfg.ExcerptChars))
	if err != nil {
		if refusedForWhatWasAsked(err) {
			return c.outcomesRefused(batch, self, err)
		}
		return fmt.Errorf("outcome intake: LLM call failed: %w — the outcomes wait", err)
	}

	note := strings.TrimSpace(output)
	pass := landing.Pass{Cursor: c.outcomeCursor(batch), ModelID: modelID}
	if note == nothingSurfaced {

		if _, err := c.intake.Land(pass); err != nil {
			return fmt.Errorf("outcome intake: %w", err)
		}
		c.outcomesLanded(batch, stood)
		logsink.Info("consolidate.end", "outcome intake: %d outcome(s) read, nothing observed — the cursor moves to %d", len(batch.Outcomes), batch.Through)
		return nil
	}

	if n := utf8.RuneCountInString(note); n > cfg.ObservationMaxChars {
		return c.outcomesRefused(batch, self, fmt.Errorf("the observation was %d characters, over the %d-character bound (prompt.surfacing_max_chars)", n, cfg.ObservationMaxChars))
	}

	pass.Product = map[string]any{
		"id":         rows.OutcomeObservationPrefix + outputHash(fmt.Sprintf("%d\n%s", batch.Through, note)),
		"content":    note,
		"category":   "observation",
		"provenance": "self",
		"cites":      cites,
	}

	landed, err := c.intake.Land(pass)
	if err != nil && landed.Product == nil {
		return fmt.Errorf("outcome intake: observation refused at the door: %w — nothing published", err)
	}
	if err != nil {

		logsink.Warn("consolidate.error", "outcome intake: %v", err)
	}
	c.outcomesLanded(batch, stood)
	evt := landed.Product
	logsink.Info("consolidate.end", "outcome intake: %d outcome(s) observed in record %d — raw, for the next pass to weigh", len(batch.Outcomes), evt.Seq)
	return nil
}

func (c *ConsolidateFacility) outcomesLanded(batch rows.OutcomeBatch, stood int) {
	c.outcomeShow.Store(0)
	if len(batch.Outcomes) == stood {
		c.teller.say(Condition{Facility: "outcomes", Kind: ConditionWhole, Said: fmt.Sprintf("the last intake was shown every outcome that stood: %d", stood)})
	}
}

func outcomesMessage(batch rows.OutcomeBatch, excerptChars int) string {
	lines := make([]string, 0, len(batch.Outcomes))
	for _, o := range batch.Outcomes {
		lines = append(lines, renderOutcome(o, excerptChars))
	}
	return "Outcomes in your record, oldest first:\n" + strings.Join(lines, "\n")
}

func keepOutcomes(batch rows.OutcomeBatch, n int) rows.OutcomeBatch {
	if n >= len(batch.Outcomes) {
		return batch
	}
	out := rows.OutcomeBatch{From: batch.From, Through: batch.Through, Outcomes: batch.Outcomes[:n]}
	if n > 0 {
		out.Through = batch.Outcomes[n-1].Seq
	}
	return out
}

func (c *ConsolidateFacility) outcomesRefused(batch rows.OutcomeBatch, self string, cause error) error {
	why := cause.Error()
	if len(batch.Outcomes) > 1 {
		next := len(batch.Outcomes) / 2
		c.outcomeShow.Store(int64(next))
		c.teller.say(Condition{Facility: "outcomes", Kind: ConditionHalved,
			Said: fmt.Sprintf("an intake of %d outcomes was refused for what it asked (%s) — nothing minted, nothing published; the next shows %d", len(batch.Outcomes), why, next)})
		return fmt.Errorf("outcome intake: refused for what it asked — the next intake shows %d of these outcomes: %w", next, cause)
	}
	o := batch.Outcomes[0]
	content := fmt.Sprintf("outcome intake: no observation could be made of the outcome in record %d (%s %s) — the request carrying it alone was refused (%s) — so it is passed over. The record holds it whole, and recall reaches it.", o.Seq, o.Kind, o.State, why)
	pass := landing.Pass{Cursor: c.outcomeCursor(batch), Product: map[string]any{
		"id":         tellingPrefix + outputHash(fmt.Sprintf("outcomes|set_aside|%d", o.Seq)),
		"content":    content,
		"category":   "observation",
		"provenance": "system",
		"raw":        true,
		"cites":      []ledger.Citation{{Identity: self, Seq: o.Seq, EntryHash: o.EntryHash}},
	}}
	landed, err := c.intake.Land(pass)
	if landed.Product == nil {
		return fmt.Errorf("outcome intake: the outcome in record %d was refused alone (%v), and passing over it did not land: %w", o.Seq, cause, err)
	}
	if err != nil {

		logsink.Warn("consolidate.error", "outcome intake: %v", err)
	}
	c.outcomeShow.Store(0)
	c.teller.say(Condition{Facility: "outcomes", Kind: ConditionSetAside,
		Said: fmt.Sprintf("the outcome in record %d was refused alone (%s) — passed over, and the identity told", o.Seq, why)})
	return nil
}

func renderOutcome(o rows.Outcome, excerptChars int) string {
	was := excerpt(o.Was, excerptChars)
	if was == "" {
		was = "(the record holds no text for " + o.ID + ")"
	}
	line := fmt.Sprintf("- [record %d] %s %s (%s): %q", o.Seq, o.Kind, o.State, outcomeClaimQualifier, was)
	if said := excerpt(o.Said, excerptChars); said != "" {
		line += fmt.Sprintf(" — what you recorded of it: %q", said)
	}
	return line
}
