package cognitive

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/cognitive/landing"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

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
// .

// .
type OutcomeSource interface {
	NextOutcomes(window int) (store.OutcomeBatch, error)
	PublishOutcomeCursor(from, through uint64) error
	OwnFingerprint() (string, error)
}

// .
// .
const defaultOutcomeWindow = 512

// .
// .
// .
type outcomeReader interface {
	NextOutcomes(window int) (store.OutcomeBatch, error)
	OwnFingerprint() (string, error)
}

// .
// .
func (c *ConsolidateFacility) SetOutcomes(src OutcomeSource) {
	if src == nil {
		c.outcomes, c.outcomeCursor = nil, nil
		return
	}
	c.outcomes, c.outcomeCursor = src, landing.OutcomeCursors(src)
}

// .
// .
func (c *ConsolidateFacility) OutcomesWired() bool { return c.outcomes != nil }

// .
// .
// .
// .
// .
func (c *ConsolidateFacility) OutcomesPending() bool {
	if c.outcomes == nil {
		return false
	}
	batch, err := c.outcomes.NextOutcomes(c.config.OutcomeWindow)
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

// .
// .
func (c *ConsolidateFacility) passOver(batch store.OutcomeBatch) {
	if _, err := c.intake.PassOver(c.outcomeCursor(batch)); err != nil {
		logsink.Warn("consolidate.error", "outcome cursor not published: %v — the same records are considered again", err)
	}
}

// .
// .
func (c *ConsolidateFacility) ObserveOutcomes(ctx context.Context) error {
	if c.outcomes == nil {
		return fmt.Errorf("outcome intake: no source wired")
	}
	if c.ledger == nil {
		return fmt.Errorf("outcome intake: no ledger door — an observation that cannot be recorded is not made")
	}
	batch, err := c.outcomes.NextOutcomes(c.config.OutcomeWindow)
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
		// .
		// .
		// .
		return fmt.Errorf("outcome intake: this record's genesis carries no fingerprint — an outcome cannot be cited")
	}

	var lines []string
	cites := make([]ledger.Citation, 0, len(batch.Outcomes))
	for _, o := range batch.Outcomes {
		lines = append(lines, renderOutcome(o))
		cites = append(cites, ledger.Citation{Identity: self, Seq: o.Seq, EntryHash: o.EntryHash})
	}
	userMsg := "Outcomes in your record, oldest first:\n" + strings.Join(lines, "\n")

	callCtx, systemPrompt, err := withPreamble(ctx, c.authority, outcomeSystemPrompt)
	if err != nil {
		return fmt.Errorf("outcome intake: authority context: %w", err)
	}
	output, modelID, err := c.llm.ChatSimple(callCtx, systemPrompt, userMsg)
	if err != nil {
		return fmt.Errorf("outcome intake: LLM call failed: %w — the outcomes wait", err)
	}
	// .
	// .
	// .
	note := strings.TrimSpace(output)
	pass := landing.Pass{Cursor: c.outcomeCursor(batch), ModelID: modelID}
	if note == nothingSurfaced {
		// .
		// .
		if _, err := c.intake.Land(pass); err != nil {
			return fmt.Errorf("outcome intake: %w", err)
		}
		logsink.Info("consolidate.end", "outcome intake: %d outcome(s) read, nothing observed — the cursor moves to %d", len(batch.Outcomes), batch.Through)
		return nil
	}
	// .
	// .
	// .
	if n := utf8.RuneCountInString(note); n > c.config.ObservationMaxChars {
		return fmt.Errorf("outcome intake: observation REFUSED — %d characters over the %d-character bound (prompt.surfacing_max_chars); nothing minted, the outcomes wait",
			n, c.config.ObservationMaxChars)
	}

	pass.Product = map[string]interface{}{
		"id":         store.OutcomeObservationPrefix + outputHash(fmt.Sprintf("%d\n%s", batch.Through, note)),
		"content":    note,
		"category":   "observation",
		"provenance": "self",
		"cites":      cites,
	}
	// .
	// .
	// .
	// .
	landed, err := c.intake.Land(pass)
	if err != nil && landed.Product == nil {
		return fmt.Errorf("outcome intake: observation refused at the door: %w — nothing published", err)
	}
	if err != nil {
		// .
		// .
		logsink.Warn("consolidate.error", "outcome intake: %v", err)
	}
	evt := landed.Product
	logsink.Info("consolidate.end", "outcome intake: %d outcome(s) observed in record %d — raw, for the next pass to weigh", len(batch.Outcomes), evt.Seq)
	return nil
}

// .
// .
// .
// .
// .
// .
func renderOutcome(o store.Outcome) string {
	was := excerpt(o.Was)
	if was == "" {
		was = "(the record holds no text for " + o.ID + ")"
	}
	line := fmt.Sprintf("- [record %d] %s %s: %q", o.Seq, o.Kind, o.State, was)
	if said := excerpt(o.Said); said != "" {
		line += fmt.Sprintf(" — what you recorded of it: %q", said)
	}
	return line
}
