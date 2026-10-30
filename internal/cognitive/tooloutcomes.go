package cognitive

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/cognitive/landing"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

type ToolOutcomeSource interface {
	ToolOutcomesSince(now time.Time, maxSessions int, firstLook time.Duration) (rows.ToolOutcomeBatch, error)
	PublishToolOutcomeCursor(batch rows.ToolOutcomeBatch) error
}

type toolReader interface {
	ToolOutcomesSince(now time.Time, maxSessions int, firstLook time.Duration) (rows.ToolOutcomeBatch, error)
}

const MaxToolSessionsPerIntake = 8

const (
	DefaultToolOutcomeMin    = 3
	DefaultToolOutcomeWindow = 48 * time.Hour
)

const (
	toolOutcomePromptText = `These are your own tool calls over a window, as the host recorded them: for each tool and reason, how many succeeded, failed or were refused, one example of your own arguments, and for a refusal the gate's own words. Where you observed a pair before, the count then and the count since. Write ONE observation in your own voice about what these facts show about how you work, or the exact words ` + nothingSurfaced + `. This is an observation, not a resolution: do not promise, plan or instruct. Do not repeat the arguments; name the tool and what happened. The facts are the host's record, not your claims.`
	toolOutcomeBoundText  = "\n\nAt most %d characters: a longer observation is refused whole."

	toolSessionsHead = "Work sessions delivered in this window, oldest first:"

	toolSessionLine   = "- %s %q: you called it %s; %s."
	toolNoOutcomeWord = "nothing (the result names no outcome)"
	toolGraded        = "graded %s by your operator"
	toolNotGraded     = "not graded"
	toolNoCalls       = "  (no tool call recorded)"
	toolLooseHead     = "Calls outside any work session in this window:"

	toolUseLine  = "  %s: %d succeeded"
	toolPairPart = "; %s %d (%s)"
	toolNoReason = "no reason code"
	toolArgsPart = ", e.g. your arguments: %s"
	toolGatePart = "; the gate said: %q"

	toolRecoveredPart = "; %d recovered, e.g. your call: %s"
	toolSinceHead     = "Pairs your last tool observation was shown (its window %s to %s):"
	toolSinceLine     = "  %s %s (%s): %d in the window you last observed, %d in this one; %d of its calls succeeded in this one."
	toolWindowLine    = "These calls were recorded from %s to %s (UTC)."

	toolPassedOver = "tool outcome intake: no observation could be made of your tool calls from %s to %s — the request carrying them was refused (%s) — so they are passed over. Your conversation record holds every call, and recall source=work reads a session's."
)

func toolOutcomePrompt(maxChars int) string {
	return toolOutcomePromptText + fmt.Sprintf(toolOutcomeBoundText, maxChars)
}

func (c *ConsolidateFacility) SetToolOutcomes(src ToolOutcomeSource) {
	if src == nil {
		c.tools, c.toolCursor = nil, nil
		return
	}
	c.tools, c.toolCursor = src, landing.ToolOutcomeCursors(src)
}

func (c *ConsolidateFacility) ToolOutcomesWired() bool { return c.tools != nil }

func (c *ConsolidateFacility) SetClock(now func() time.Time) { c.clock = now }

func (c *ConsolidateFacility) toolNow() time.Time {
	if c.clock != nil {
		return c.clock()
	}
	return time.Now()
}

type toolVerdict int

const (
	toolWait toolVerdict = iota
	toolDue
	toolNothing
)

func judgeTools(b rows.ToolOutcomeBatch, now time.Time, least int, window time.Duration) toolVerdict {
	type key struct{ tool, outcome, reason string }
	counts := map[key]int{}
	add := func(uses []rows.ToolUse) {
		for _, u := range uses {
			for _, p := range u.Pairs {
				counts[key{u.Tool, p.Outcome, p.Reason}] += p.Count
			}
		}
	}
	for _, s := range b.Sessions {
		add(s.Tools)
	}
	add(b.Loose)
	covered := map[key]bool{}
	clean, cleanish := false, false
	for _, ch := range b.Since {
		covered[key{ch.Tool, ch.Outcome, ch.Reason}] = true
		if ch.Now == 0 && ch.NowSucceeded > 0 {
			cleanish = true
			clean = clean || ch.NowSucceeded >= least
		}
	}
	if len(counts) == 0 && !cleanish {
		return toolNothing
	}
	for k, n := range counts {
		if !covered[k] && n >= least {
			return toolDue
		}
	}
	if clean || (b.Oldest > 0 && now.Sub(time.UnixMilli(b.Oldest)) >= window) {
		return toolDue
	}
	return toolWait
}

func (c *ConsolidateFacility) ToolOutcomesPending() (bool, time.Time) {
	if c.tools == nil {
		return false, time.Time{}
	}
	cfg := c.cfg()
	now := c.toolNow()
	batch, err := c.tools.ToolOutcomesSince(now, MaxToolSessionsPerIntake, cfg.ToolOutcomeWindow)
	if err != nil {
		logsink.Warn("consolidate.error", "tool outcome probe failed: %v — nothing considered, the cursor stands", err)
		return false, time.Time{}
	}
	switch judgeTools(batch, now, cfg.ToolOutcomeMin, cfg.ToolOutcomeWindow) {
	case toolDue:
		return true, time.UnixMilli(batch.Oldest)
	case toolNothing:
		c.toolPassOver(batch)
	}
	return false, time.Time{}
}

func (c *ConsolidateFacility) toolPassOver(batch rows.ToolOutcomeBatch) {
	if _, err := c.intake.PassOver(c.toolCursor(batch, 0)); err != nil {
		logsink.Warn("consolidate.error", "tool outcome cursor not published: %v — the same calls are considered again", err)
	}
}

func (c *ConsolidateFacility) ObserveToolOutcomes(ctx context.Context) error {
	if c.tools == nil {
		return fmt.Errorf("tool outcome intake: no source wired")
	}
	if c.ledger == nil {
		return fmt.Errorf("tool outcome intake: no ledger door — an observation that cannot be recorded is not made")
	}
	cfg := c.cfg()
	now := c.toolNow()
	most := MaxToolSessionsPerIntake
	if n := int(c.toolShow.Load()); n > 0 && n < most {
		most = n
	}
	read := map[int]rows.ToolOutcomeBatch{}
	readCap := func(k int) (rows.ToolOutcomeBatch, error) {
		if b, ok := read[k]; ok {
			return b, nil
		}
		b, err := c.tools.ToolOutcomesSince(now, k, cfg.ToolOutcomeWindow)
		if err == nil {
			read[k] = b
		}
		return b, err
	}
	batch, err := readCap(most)
	if err != nil {
		return fmt.Errorf("tool outcome intake: %w", err)
	}
	if judgeTools(batch, now, cfg.ToolOutcomeMin, cfg.ToolOutcomeWindow) == toolNothing {
		c.toolPassOver(batch)
		return nil
	}
	stood := len(batch.Sessions)

	smallest, err := readCap(1)
	if err != nil {
		return fmt.Errorf("tool outcome intake: %w", err)
	}
	base := toolOutcomePrompt(cfg.ObservationMaxChars)
	check := simpleCheck(c.llm)
	callCtx, systemPrompt, err := fittedPreamble(ctx, c.authority, base, toolOutcomesMessage(smallest, cfg.ExcerptChars), nil, check)
	if err != nil {
		return fmt.Errorf("tool outcome intake: authority context: %w", err)
	}
	if check != nil && len(batch.Sessions) > 1 {
		ask := &admission{check: check, ctx: callCtx, system: systemPrompt}
		i, ok := largestAdmitted(len(batch.Sessions)-1, func(i int) bool {
			b, err := readCap(i + 1)
			if err != nil {
				ask.err = err
				return false
			}
			return ask.admits(toolOutcomesMessage(b, cfg.ExcerptChars))
		})
		if ask.err != nil {
			return fmt.Errorf("tool outcome intake: %w — nothing sent, the calls wait", ask.err)
		}
		if !ok {
			c.teller.say(Condition{Facility: "tools", Kind: ConditionNoRoom,
				Said: "no work session's tool calls fit beside what every request carries (the rings and the intake's prompt) in the model's input budget — nothing sent, nothing published; " + c.teller.room() + " makes room"})
			return fmt.Errorf("tool outcome intake: no window fits the model's input budget beside the required context — nothing sent")
		}
		if i+1 < len(batch.Sessions) {
			logsink.Info("consolidate.budget", "tool outcome intake: %d of %d session(s) fit the model's input budget; the rest wait for the next intake", i+1, len(batch.Sessions))
			batch = read[i+1]
		}
	} else if check != nil {
		ask := &admission{check: check, ctx: callCtx, system: systemPrompt}
		if !ask.admits(toolOutcomesMessage(batch, cfg.ExcerptChars)) {
			if ask.err != nil {
				return fmt.Errorf("tool outcome intake: %w — nothing sent, the calls wait", ask.err)
			}
			c.teller.say(Condition{Facility: "tools", Kind: ConditionNoRoom,
				Said: "the window's tool calls do not fit beside what every request carries in the model's input budget — nothing sent, nothing published; " + c.teller.room() + " makes room"})
			return fmt.Errorf("tool outcome intake: the window does not fit the model's input budget beside the required context — nothing sent")
		}
	}
	output, modelID, err := c.llm.ChatSimple(wire.WithTapSource(callCtx, "tool_outcome"), systemPrompt, toolOutcomesMessage(batch, cfg.ExcerptChars))
	if err != nil {
		if refusedForWhatWasAsked(err) {
			return c.toolsRefused(batch, err)
		}
		return fmt.Errorf("tool outcome intake: LLM call failed: %w — the calls wait", err)
	}
	note := strings.TrimSpace(output)
	pass := landing.Pass{Cursor: c.toolCursor(batch, batch.Shown()), ModelID: modelID}
	if note == nothingSurfaced {
		if _, err := c.intake.Land(pass); err != nil {
			return fmt.Errorf("tool outcome intake: %w", err)
		}
		c.toolsLanded(batch, stood)
		logsink.Info("consolidate.end", "tool outcome intake: %d session(s) read, nothing observed — the cursor moves", len(batch.Sessions))
		return nil
	}
	if n := utf8.RuneCountInString(note); n > cfg.ObservationMaxChars {
		return c.toolsRefused(batch, fmt.Errorf("the observation was %d characters, over the %d-character bound (prompt.surfacing_max_chars)", n, cfg.ObservationMaxChars))
	}

	pass.Product = map[string]any{
		"id":         batch.ObservationID(outputHash(note)[:16]),
		"content":    note,
		"category":   "observation",
		"provenance": "self",
	}
	landed, err := c.intake.Land(pass)
	if err != nil && landed.Product == nil {
		return fmt.Errorf("tool outcome intake: observation refused at the door: %w — nothing published", err)
	}
	if err != nil {

		logsink.Warn("consolidate.error", "tool outcome intake: %v", err)
	}
	c.toolsLanded(batch, stood)
	logsink.Info("consolidate.end", "tool outcome intake: %d session(s) observed in record %d — raw, for the next pass to weigh", len(batch.Sessions), landed.Product.Seq)
	return nil
}

func (c *ConsolidateFacility) toolsLanded(batch rows.ToolOutcomeBatch, stood int) {
	c.toolShow.Store(0)
	if len(batch.Sessions) == stood {
		c.teller.say(Condition{Facility: "tools", Kind: ConditionWhole, Said: fmt.Sprintf("the last tool outcome intake was shown every session that stood: %d", stood)})
	}
}

func (c *ConsolidateFacility) toolsRefused(batch rows.ToolOutcomeBatch, cause error) error {
	why := cause.Error()
	if len(batch.Sessions) > 1 {
		next := len(batch.Sessions) / 2
		c.toolShow.Store(int64(next))
		c.teller.say(Condition{Facility: "tools", Kind: ConditionHalved,
			Said: fmt.Sprintf("a tool outcome intake of %d sessions was refused for what it asked (%s) — nothing minted, nothing published; the next shows %d", len(batch.Sessions), why, next)})
		return fmt.Errorf("tool outcome intake: refused for what it asked — the next intake shows %d session(s): %w", next, cause)
	}
	from, through := windowStamp(batch.From), windowStamp(batch.Through)
	id := tellingPrefix + outputHash(fmt.Sprintf("tools|set_aside|%d|%d", batch.From, batch.Through))
	if batch.FromAfter != "" || batch.ThroughAfter != "" {
		id = tellingPrefix + outputHash(batch.ObservationID("aside"))
	}
	pass := landing.Pass{Cursor: c.toolCursor(batch, batch.Shown())}

	told := false
	if c.store != nil {
		var err error
		if told, err = c.store.EntityExists(id); err != nil {
			return fmt.Errorf("tool outcome intake: the window was refused alone (%v), and whether it was told could not be read: %w", cause, err)
		}
	}
	if !told {
		content := fmt.Sprintf(toolPassedOver, from, through, why)
		if (batch.FromAfter != "" || batch.ThroughAfter != "") && len(batch.Sessions) == 1 {
			content += fmt.Sprintf(" This pass covered session %s.", batch.Sessions[0].ID)
		}
		pass.Product = map[string]any{
			"id":         id,
			"content":    content,
			"category":   "observation",
			"provenance": "system",
			"raw":        true,
		}
	}
	landed, err := c.intake.Land(pass)
	if !told && landed.Product == nil {
		return fmt.Errorf("tool outcome intake: the window from %s to %s was refused alone (%v), and passing over it did not land: %w", from, through, cause, err)
	}
	if err != nil {
		logsink.Warn("consolidate.error", "tool outcome intake: %v", err)
	}
	c.toolShow.Store(0)
	c.teller.say(Condition{Facility: "tools", Kind: ConditionSetAside,
		Said: fmt.Sprintf("the tool calls from %s to %s were refused alone (%s) — passed over, and the identity told", from, through, why)})
	return nil
}

func windowStamp(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04") }

func toolOutcomesMessage(b rows.ToolOutcomeBatch, excerptChars int) string {
	var parts []string
	if len(b.Sessions) > 0 {
		lines := []string{toolSessionsHead}
		for _, s := range b.Sessions {
			outcome := s.Outcome
			if outcome == "" {
				outcome = toolNoOutcomeWord
			}
			grade := toolNotGraded
			if s.Grade != "" {
				grade = fmt.Sprintf(toolGraded, s.Grade)
			}
			lines = append(lines, fmt.Sprintf(toolSessionLine, s.ID, excerpt(s.Description, excerptChars), outcome, grade))
			if len(s.Tools) == 0 {
				lines = append(lines, toolNoCalls)
			}
			lines = append(lines, renderToolUses(s.Tools, excerptChars)...)
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	if len(b.Loose) > 0 {
		parts = append(parts, strings.Join(append([]string{toolLooseHead}, renderToolUses(b.Loose, excerptChars)...), "\n"))
	}
	if len(b.Since) > 0 && b.Last != nil {
		head := fmt.Sprintf(toolSinceHead, windowStamp(b.Last.From), windowStamp(b.Last.Through))
		if b.Last.FromAfter != "" || b.Last.ThroughAfter != "" {
			head = "Pairs your last tool observation was shown in its selected sessions and outside-session calls:"
		}
		lines := []string{head}
		for _, ch := range b.Since {
			lines = append(lines, fmt.Sprintf(toolSinceLine, ch.Tool, ch.Outcome, reasonWords(ch.Reason), ch.Then, ch.Now, ch.NowSucceeded))
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	window := fmt.Sprintf(toolWindowLine, windowStamp(b.From), windowStamp(b.Through))
	if b.FromAfter != "" || b.ThroughAfter != "" {
		window = fmt.Sprintf("The listed sessions' calls are shown together. Calls outside any session were recorded from %s to %s (UTC); sessions sharing a delivery timestamp continue in a later intake.", windowStamp(b.From), windowStamp(b.Through))
	}
	parts = append(parts, window)
	return strings.Join(parts, "\n\n")
}

func renderToolUses(uses []rows.ToolUse, excerptChars int) []string {
	lines := make([]string, 0, len(uses))
	for _, u := range uses {
		line := fmt.Sprintf(toolUseLine, u.Tool, u.Succeeded)
		for _, p := range u.Pairs {
			line += fmt.Sprintf(toolPairPart, p.Outcome, p.Count, reasonWords(p.Reason))
			if a := excerpt(p.Args, excerptChars); a != "" {
				line += fmt.Sprintf(toolArgsPart, a)
			}
			if g := excerpt(p.Gate, excerptChars); g != "" && p.Outcome == "refused" {
				line += fmt.Sprintf(toolGatePart, g)
			}
			if p.Recovered > 0 {
				line += fmt.Sprintf(toolRecoveredPart, p.Recovered, excerpt(p.RecoveredArgs, excerptChars))
			}
		}
		lines = append(lines, line)
	}
	return lines
}

func reasonWords(code string) string {
	if code == "" {
		return toolNoReason
	}
	return code
}
