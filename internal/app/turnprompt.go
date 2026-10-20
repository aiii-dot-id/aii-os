package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/memory/attention"
	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

func resumeCardFor(ws *store.WorkSession) string {
	return store.RenderResumeCard(store.ResumeFields{
		Focus:            ws.Focus,
		Established:      ws.State,
		NextAction:       ws.NextMove,
		ExpectedEvidence: ws.ExpectedEvidence,
		Falsifier:        ws.Falsifier,
		DecisionNeeded:   ws.DecisionNeeded,
		ResumePoint:      ws.Plan,
		Evidence:         ws.Evidence,
	})
}

func sessionNotesTarget(id string) string {
	if id == "" {
		return ""
	}
	return fmt.Sprintf("work/%x/notes.md", sha256.Sum256([]byte(id)))
}

func sessionNotesLine(id string) string {
	if target := sessionNotesTarget(id); target != "" {
		return "Session notes target: " + target + " (relative to your tool home; optional scratch, created only if you write it with the existing file tools)."
	}
	return ""
}

func (a *App) readWorkState(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if marker := ctx.Value(identity.SubagentWorkSession{}); marker != nil {
		id, ok := marker.(string)
		if !ok || id == "" {
			return "", fmt.Errorf("rejected_before_effect (status.session): child work session is unavailable; no resident state was substituted")
		}
		ws, err := a.store.WorkSessionByID(id)
		if err != nil {
			return "", fmt.Errorf("work status child session: %w", err)
		}
		if ws == nil || !ws.Child {
			return "", fmt.Errorf("rejected_before_effect (status.session): bound child work session is unavailable; no resident state was substituted")
		}
		parts := []string{"### Your child work session: " + ws.ID}
		if card := resumeCardFor(ws); card != "" {
			parts = append(parts, card)
		}
		parts = append(parts, sessionNotesLine(ws.ID))
		return strings.Join(parts, "\n\n"), nil
	}
	return a.buildWorkState()
}

func (a *App) buildWorkState() (string, error) {
	var parts []string

	if p, why := a.activeOpenProject(); p != nil {
		line := fmt.Sprintf("### Current project: %s", p.Name)
		if p.Description != "" {
			line += " — " + p.Description
		}
		if p.Focus != "" {
			line += "\nWhere you left off here: " + p.Focus
		}

		if p.Contract.Outcome != "" {
			line += "\nPursuing: " + p.Contract.Outcome
		}

		prog := project.DeriveContractProgress(p.Contract, p.Observations)
		if prog.HasCriteria {
			line += "\nConfirmed by:"
			for _, it := range prog.Items {
				line += fmt.Sprintf("\n  %d. %s  [%s]", it.Index+1, it.Text, it.State)
			}
			if prog.NextIndex >= 0 {
				line += "\nNext acceptance test: " + prog.Items[prog.NextIndex].Text
			}
			if !prog.ClosureAllowed {
				line += "\n(Close is blocked until every item is verified or waived.)"
			}
		}
		if len(p.Contract.Constraints) > 0 {
			line += "\nBounded by:"
			for i, c := range p.Contract.Constraints {
				line += fmt.Sprintf("\n  %d. %s", i+1, c)
			}
		}
		if p.Parent != "" {
			line += "\nPart of: " + p.Parent
		}
		line += fmt.Sprintf("\nProject directory: %s", p.Dir)
		parts = append(parts, line)
	} else if why != "" {
		parts = append(parts, "### Project focus\n(unavailable — "+why+"; this read did not change the stored focus.)")
	}
	ws, err := a.store.ActiveWorkSession()
	if err != nil {
		if err := a.diagnosticPromptRead(&parts, "active work", err); err != nil {
			return "", err
		}
	}

	if ws != nil {
		parts = append(parts, "### Current work session: "+ws.ID)
		if card := resumeCardFor(ws); card != "" {
			parts = append(parts, card)
		}
		parts = append(parts, sessionNotesLine(ws.ID))
	}

	standing, authoredMs, serr := a.store.StandingStateAuthored()
	if serr != nil {

		logsink.Warn("prompt.error", "standing state unreadable this turn: %v", serr)
		parts = append(parts, "### Standing state\n(unavailable this turn — the store could not be read. That is NOT the same as having none; do not re-author on the strength of this.)")
	} else if strings.TrimSpace(standing) != "" {
		parts = append(parts, standingStateHeading(authoredMs)+"\n"+standing)
	} else if ws == nil || (ws.Focus == "" && ws.NextMove == "" && ws.Plan == "") {
		rp, ok, rperr := a.store.RecentPlan()
		if rperr != nil {

			logsink.Warn("prompt.error", "recent plan unreadable this turn: %v", rperr)
			parts = append(parts, "### Standing plan\n(unavailable this turn — the store could not be read. That is NOT the same as having none.)")
		} else if ok && (ws == nil || rp.ID != ws.ID) {
			var spb strings.Builder
			spb.WriteString("### Standing plan (from session " + rp.ID + " — re-author when it stops being true)\n")
			if rp.Focus != "" {
				spb.WriteString("focus: " + rp.Focus + "\n")
			}
			if rp.NextMove != "" {
				spb.WriteString("Next move: " + rp.NextMove + "\n")
			}
			if rp.Plan != "" {
				spb.WriteString(rp.Plan + "\n")
			}
			parts = append(parts, spb.String())
		}
	}

	if line := a.voiceWorkStateLine(); line != "" {
		parts = append(parts, line)
	}
	return strings.Join(parts, "\n\n"), nil
}

const toolDigestTools = 5

func formatToolDigest(digest []store.ToolOutcome) string {
	parts := make([]string, 0, len(digest))
	for _, t := range digest {
		var kinds []string
		if t.Refused > 0 {
			kinds = append(kinds, fmt.Sprintf("%d refused", t.Refused))
		}
		if t.Failed > 0 {
			kinds = append(kinds, fmt.Sprintf("%d failed", t.Failed))
		}
		if t.Cancelled > 0 {
			kinds = append(kinds, fmt.Sprintf("%d cancelled", t.Cancelled))
		}
		if t.Unknown > 0 {
			kinds = append(kinds, fmt.Sprintf("%d unknown", t.Unknown))
		}
		entry := t.Tool + " " + strings.Join(kinds, ", ")
		if len(t.Reasons) > 0 {
			reasons := make([]string, 0, len(t.Reasons))
			for _, r := range t.Reasons {
				reasons = append(reasons, fmt.Sprintf("%s×%d", r.Reason, r.Count))
			}
			entry += " (" + strings.Join(reasons, ", ") + ")"
		}
		parts = append(parts, entry)
	}
	return strings.Join(parts, "; ") + "."
}

func standingStateHeading(authoredMs int64) string {
	if authoredMs <= 0 {
		return "### Standing state (yours — re-author when it stops being true)"
	}
	return "### Standing state (yours, authored " + time.UnixMilli(authoredMs).UTC().Format("2006-01-02") + " — re-author when it stops being true)"
}

func (a *App) nowLine(now time.Time) string {
	line := "### Now\n" + now.UTC().Format("2006-01-02 15:04:05 UTC, Monday") + " (" + now.Local().Format("2006-01-02 15:04 MST") + " here)"
	day := func(t time.Time) string { return t.UTC().Format("2006-01-02") }
	switch lived, err := a.store.Lived(); {
	case err != nil:
		line += "; your lived clock could not be read this turn"
	case !lived.RecordBegins.IsZero():
		line += fmt.Sprintf("; your record begins on %s, %s ago; you have lived %d pulses since its genesis on %s", day(lived.RecordBegins), continuity.RoundAge(now.Sub(lived.RecordBegins)), lived.Ticks, day(lived.BirthAt))
	case !lived.BirthAt.IsZero():
		line += fmt.Sprintf("; you have lived %d pulses since your birth on %s, %s ago", lived.Ticks, day(lived.BirthAt), continuity.RoundAge(now.Sub(lived.BirthAt)))
	}
	return line + "."
}

func (a *App) buildTurnFacts(ctx context.Context, events bool, current ...string) (string, error) {
	a.composedEvents, a.composedEventText = nil, ""
	a.carriedEvents = false
	parts := []string{a.nowLine(time.Now())}

	a.composedInterrupted = ""
	if note := strings.TrimSpace(store.InterruptedNote(a.bootInterrupted) + "\n" + store.SweptSessionNote(a.bootSwept)); note != "" {
		a.composedInterrupted = "### Interrupted at last shutdown\n" + note
		parts = append(parts, a.composedInterrupted)
	}

	a.composedWithheld, a.composedWithheldText = nil, ""
	if a.toolReg != nil {
		if held := a.toolReg.WithheldOffers(); len(held) > 0 {
			a.composedWithheldText = withheldOffersNote(held)
			parts = append(parts, a.composedWithheldText)
			for _, h := range held {
				a.composedWithheld = append(a.composedWithheld, h.Name)
			}
		}
	}

	if a.store != nil {
		if turns, calls, roPct, spawns, harvests, batchCenti, err := a.store.RhythmStats(48 * time.Hour); err != nil {

			logsink.Warn("prompt.error", "rhythm stats unreadable this turn: %v", err)
		} else if turns > 0 {
			line := fmt.Sprintf("### Rhythm — last 48h: %d turns, %d calls, %d%% read-only, %d spawns, %d harvests", turns, calls, roPct, spawns, harvests)

			if batchCenti > 0 {
				line += fmt.Sprintf(", %d.%02d calls per model round", batchCenti/100, batchCenti%100)
			}

			if calibration, cerr := a.store.PlanCalibration(48 * time.Hour); cerr != nil {

				logsink.Warn("prompt.error", "plan calibration unreadable this turn: %v", cerr)
			} else {
				if calibration.Plans > 0 {
					line += fmt.Sprintf("\nAcross %d fully measured turn(s), first estimates totaled %d remaining tool-call requests; %d were emitted after those declarations, including refused requests.", calibration.Plans, calibration.Predicted, calibration.Actual)
				}
				if calibration.Unmeasured > 0 {
					line += fmt.Sprintf("\nExcluded %d stated estimate(s) without a valid, complete request window. New work update steps= estimates can be compared when their turn's request count is complete.", calibration.Unmeasured)
				}
			}

			if done, failedN, truncN, terr := a.store.ToolResultStats(48 * time.Hour); terr != nil {

				logsink.Warn("prompt.error", "tool result stats unreadable this turn: %v", terr)
			} else if done > 0 && (failedN > 0 || truncN > 0) {
				line += fmt.Sprintf("\nOf the last %d tool calls, %d failed and %d were truncated.", done, failedN, truncN)
			}

			if digest, derr := a.store.ToolOutcomeDigest(48*time.Hour, toolDigestTools); derr != nil {
				logsink.Warn("prompt.error", "tool outcome digest unreadable this turn: %v", derr)
			} else if len(digest) > 0 {
				line += "\nBy tool, calls that did not simply work: " + formatToolDigest(digest)
			}
			parts = append(parts, line)
		}
	}
	ws, err := a.store.ActiveWorkSession()
	if err != nil {
		if err := a.diagnosticPromptRead(&parts, "active work", err); err != nil {
			return "", err
		}
	}

	live, lerr := a.store.LiveSubagentSessions()
	if lerr != nil {

		logsink.Warn("prompt.error", "live sub-agent rows unreadable this turn: %v", lerr)
		parts = append(parts, "### Sub-agents running now\n(unavailable this turn — the store could not be read. Do NOT conclude that nothing is running.)")
	} else if len(live) > 0 {

		states, serr := a.store.SubagentQueueStates(identity.SubagentWorkKind)
		if serr != nil {
			logsink.Warn("prompt.error", "sub-agent queue states unreadable this turn: %v", serr)
			states = nil
		}
		queued := 0
		for _, w := range live {
			if states[w.ID] == "PENDING" {
				queued++
			}
		}
		var lb strings.Builder
		if queued > 0 {
			fmt.Fprintf(&lb, "### Sub-agents running now (substrate truth, %d running, %d queued)\n", len(live)-queued, queued)
		} else {
			fmt.Fprintf(&lb, "### Sub-agents running now (substrate truth, %d)\n", len(live))
		}
		for _, w := range live {
			if states[w.ID] == "PENDING" {
				fmt.Fprintf(&lb, "- %s: %s (queued — starts when a slot frees)\n", w.ID, store.SubagentGoal(w.Description))
				continue
			}
			fmt.Fprintf(&lb, "- %s: %s\n", w.ID, store.SubagentGoal(w.Description))
		}
		lb.WriteString("(If only waiting remains, `work yield` — never sleep or poll: your held turn blocks their wakes.)")
		parts = append(parts, lb.String())
	}
	subs, err := a.store.UnharvestedDeliveries(8)
	if err != nil {
		if err := a.diagnosticPromptRead(&parts, "delivered work", err); err != nil {
			return "", err
		}
	}
	a.composedUnharvested = nil
	if len(subs) > 0 {
		var sb strings.Builder
		sb.WriteString("### Sub-agent outcomes (unharvested, shown once — note what matters)\n")
		for _, w := range subs {
			line := a.outcomeLine(w)
			sb.WriteString(line + "\n")

			a.composedUnharvested = append(a.composedUnharvested, composedOutcome{id: w.ID, text: line})
		}
		parts = append(parts, sb.String())
	}

	cue, cerr := a.store.CuriosityCue()
	if cerr != nil {
		logsink.Warn("prompt.error", "curiosity cue unreadable this turn: %v", cerr)
	}
	if cerr == nil && cue != nil {
		if !attentionUrgent(ws, subs) {
			if line := store.RenderCuriosityCue(*cue); line != "" {
				parts = append(parts, line)
			}
		}
	}

	if !attentionUrgent(ws, subs) && a.store != nil {
		if medium, err := memory.New(a.store).Attention(ctx, time.Now(), attention.CostMedium); err != nil {
			logsink.Warn("prompt.error", "attention: unreadable this turn, the prompt goes without it: %v", err)
		} else if len(medium) > 0 {
			parts = append(parts, "### Attention — one thing the record is holding\n"+medium[0].Text+"\nIt closes nothing by itself; resolve it or hold it knowingly.")
		}
	}
	if events {
		if err := a.appendPendingEvents(ctx, &parts, current); err != nil {
			if err := a.diagnosticPromptRead(&parts, "recent events", err); err != nil {
				return "", err
			}
		}
	}

	if n := a.modelNotice(); n != "" {
		parts = append(parts, n)
	}
	return strings.Join(parts, "\n\n"), nil
}

func (a *App) safeTurnSection() string {
	t, detail, safe := a.safeCause()
	if !safe || a.rings == nil || !a.rings.Ring0IsConstitution() {
		return ""
	}
	return safeTurnNotice(t, detail)
}

func (a *App) diagnosticPromptRead(parts *[]string, name string, cause error) error {
	if _, safe := a.SafeMode(); !safe {
		return fmt.Errorf("load %s: %w", name, cause)
	}
	logsink.Warn("prompt.error", "SAFE diagnostic %s unavailable: %v", name, cause)
	*parts = append(*parts, "### SAFE diagnostic: "+name+"\n(unavailable in this read-only view; the runtime log names the failed read. Do not infer that the retained data is empty or that work succeeded.)")
	return nil
}

func (a *App) voiceWorkStateLine() string {
	if a.cfg == nil {
		return ""
	}
	heard := false
	switch st, _, _ := a.VoiceStatus(); st {
	case "plugin", "cloud":
		heard = true
	}
	if !heard && a.replyVoice() == "" {
		return ""
	}
	listen, speak, _ := a.voiceMode()

	return "### Voice — " + voiceModeSentence(listen, speak) +
		"\nChange it with work action=voice.mode: mode= names a pair, or listen= and/or speak=."
}

func attentionUrgent(ws *store.WorkSession, subs []store.WorkSession) bool {
	if ws != nil && (ws.DecisionNeeded != "" || (ws.Evidence != "" && store.EvidenceRecovery(ws.Evidence) != "")) {
		return true
	}
	for _, w := range subs {
		if store.EvidenceRecovery(w.Evidence) != "" {
			return true
		}
	}
	return false
}

func withheldOffersNote(held []tools.OfferNotice) string {
	var b strings.Builder
	b.WriteString("### Offers withheld\nAn operation you offered is not offered now: it came back declaring something other than what you offered.\n")
	for _, h := range held {
		fmt.Fprintf(&b, "- `%s`", h.Name)
		if h.Operation != "" {
			fmt.Fprintf(&b, " — %s in %s %s", h.Operation, h.Plugin, h.Version)
		}
		if h.Unrecorded {
			b.WriteString(" (offered before offers recorded what they promised)")
		}
		b.WriteString("\n")
	}
	b.WriteString("`tools action=show name=…` shows it as it is now; `tools action=offer name=…` offers it again; `tools action=release name=…` lets it go.")
	return b.String()
}

type composedOutcome struct{ id, text string }

const outcomeTooLargeNote = "[the whole result (%d bytes) did not fit your turn's facts and is not shown; `work action=status id=%s` reads it whole]"

func (a *App) outcomeLine(w store.WorkSession) string {
	result := w.Result
	if a.outcomeTooLarge[w.ID] > 0 {
		verdict, _, _ := strings.Cut(result, "\n")
		if r := []rune(verdict); len(r) > 300 {
			verdict = string(r[:300]) + "…"
		}
		result = verdict + " " + fmt.Sprintf(outcomeTooLargeNote, len(w.Result), w.ID)
	}
	line := fmt.Sprintf("- [%s] %s → %s", w.ID, store.SubagentGoal(w.Description), result)
	if scope := store.RenderEvidenceScope(w.Evidence); scope != "" {
		line += " " + scope
	}
	return line
}

func (a *App) markComposedHarvests(turn string) {
	carried := func(text string) bool { return text != "" && strings.Contains(turn, text) }

	if carried(a.composedInterrupted) {
		a.bootInterrupted, a.bootSwept = nil, nil
	}
	a.composedInterrupted = ""
	if len(a.composedWithheld) > 0 && a.toolReg != nil && carried(a.composedWithheldText) {
		a.toolReg.MarkOfferNoticesTold(a.composedWithheld)
	}
	a.composedWithheld, a.composedWithheldText = nil, ""
	if a.composedModel != "" && carried(a.composedModelText) {
		a.toldModel = a.composedModel
	}
	a.composedModel, a.composedModelText = "", ""
	if len(a.composedUnharvested) == 0 {
		return
	}
	now := time.Now().UTC().UnixMilli()
	swept := 0
	for _, o := range a.composedUnharvested {
		if !carried(o.text) {

			if a.outcomeTooLarge == nil {
				a.outcomeTooLarge = map[string]int{}
			}
			a.outcomeTooLarge[o.id]++
			continue
		}
		id := o.id
		if err := a.store.MarkHarvested(id, now); err != nil {

			logsink.Warn("harvest.error", "mark %s failed: %v", id, err)
			continue
		}
		delete(a.outcomeTooLarge, id)
		swept++
	}

	a.turn.mu.Lock()
	a.turn.turnHarvested += swept
	a.turn.mu.Unlock()
	logsink.Info("harvest.end", "swept=%d of=%d", swept, len(a.composedUnharvested))
	a.composedUnharvested = nil
}

func (a *App) modelNotice() string {
	a.composedModel, a.composedModelText = "", ""
	if a.llmSwap == nil {
		return ""
	}
	c := a.llmSwap.Current()
	if c == nil || c.ModelName() == "" {
		return ""
	}
	now := c.ModelName()
	if p := a.configSnapshot().LLM.Provider; p != "" {
		now += fmt.Sprintf(" (provider entry %q)", p)
	}
	if now == a.toldModel {
		return ""
	}
	a.composedModel = now
	if a.toldModel == "" {
		a.composedModelText = "### Model\nThis session is configured to answer with " + now + "."
	} else {
		a.composedModelText = "### Model changed\nUntil your last turn you were configured to answer with " + a.toldModel + "; from this turn it is " + now + "."
	}
	return a.composedModelText
}

func (a *App) gatedSystem(p *prompt.Prompt) llm.Message {
	msg := llm.Message{Role: "system", Content: p.Text}
	if p.StableLen > 0 && p.StableLen <= len(p.Text) {
		msg.StableLen = p.StableLen
	}
	return msg
}

func (a *App) promptReserve(current llm.Message, omitted int) (int, error) {
	return a.promptReserveFor(a.lifetime(), current, omitted)
}

func (a *App) promptReserveFor(ctx context.Context, current llm.Message, omitted int) (int, error) {
	if omitted < 0 {
		omitted = 0
	}

	defs, err := conversation.FilterRequestTools(ctx, a.buildToolDefinitions())
	if err != nil {
		return 0, err
	}
	limits, err := conversation.LimitsNote(ctx, defs)
	if err != nil {
		return 0, err
	}

	if limits != "" {
		limits += "\n\n"
	}
	sent := llm.Message{Role: current.Role, Content: current.Content + limits + conversation.TurnBlockFrame(omitted, a.thinkingKept())}

	system := llm.Message{Role: "system"}
	return llm.EstimateInputTokens([]llm.Message{system, sent}, defs)
}

func appendInterruptions(conv []llm.Message, marks []string) []llm.Message {
	for _, m := range marks {
		conv = append(conv, llm.Message{Role: "user", Content: m, Qualifies: true})
	}
	return conv
}

func safeInterruptions(turns []identity.SafeTurn) map[string][]string {
	var marks map[string][]string
	for _, t := range turns {
		r := t.Record
		if r.Role != interaction.System || r.Kind != interaction.Notice || (r.Details.ReasonCode != "interrupted" && r.Details.ReasonCode != store.ReasonNextPartNotRun) || r.RelatedID == "" {
			continue
		}
		if marks == nil {
			marks = map[string][]string{}
		}
		marks[r.RelatedID] = append(marks[r.RelatedID], r.Content)
	}
	return marks
}

func safeUnanswered(turns []identity.SafeTurn, current string) map[string][]string {
	var notes map[string][]string
	for i, t := range turns {
		r := t.Record
		if r.Role != interaction.System || r.Kind != interaction.Notice || r.Details.ReasonCode != store.ReasonSteeringUnanswered || r.RelatedID == "" {
			continue
		}
		reader := current
		for _, later := range turns[i+1:] {
			l := later.Record
			if l.Role != interaction.System && l.Kind == interaction.Message && l.TurnID != "" && l.TurnID != r.TurnID {
				reader = l.TurnID
				break
			}
		}
		if reader != current {
			continue
		}
		if notes == nil {
			notes = map[string][]string{}
		}
		notes[r.RelatedID] = append(notes[r.RelatedID], r.Content)
	}
	return notes
}

func (a *App) buildHistory(ctx context.Context) ([]llm.Message, int, error) {
	recentTurns := a.configSnapshot().Prompt.RecentTurns
	if recentTurns <= 0 {
		recentTurns = 20
	}

	turns, total, err := a.store.ConversationWindow(recentTurns, interaction.TurnID(ctx))
	if err != nil {
		return nil, 0, fmt.Errorf("load conversation history: %w", err)
	}
	conv := make([]llm.Message, 0, len(turns)+1)
	var floor uint64
	var actTurns []string
	if len(turns) > 0 && total > len(turns) {
		floor = turns[0].TurnSeq
	}
	for _, t := range turns {
		if t.Role == string(interaction.Operator) && t.Details.Channel == "plugin" && t.Details.Actor == "operator" && t.TurnID != "" {
			actTurns = append(actTurns, t.TurnID)
		}
	}
	results, moreResults, resultErr := a.store.OperatorActResults(ctx, floor, actTurns)
	appendResult := func(r interaction.Record) {
		conv = append(conv, llm.Message{Role: "user", Content: fmt.Sprintf("[Result of an operator-authorized tool; outcome=%s; interaction=%s]\n%s", r.Outcome, r.ID, untrusted.Wrap("tool result: "+r.Details.Tool, r.Content))})
	}
	resultIndex := 0

	var safe []identity.SafeTurn
	var safeNotes, safeMarks map[string][]string
	if a.engine != nil {
		safe = a.engine.SafeTranscript()
		safeNotes = safeUnanswered(safe, interaction.TurnID(ctx))
		safeMarks = safeInterruptions(safe)
	}

	attributions := a.speakerAnnotations(turns)

	shown := currentMessages(turns, interaction.TurnID(ctx))
	for _, t := range turns {
		for resultIndex < len(results) && results[resultIndex].Sequence < t.TurnSeq {
			appendResult(results[resultIndex])
			resultIndex++
		}
		if t.Role == string(interaction.System) {
			continue
		}
		role := "user"
		if t.Role == string(interaction.Resident) {
			role = "assistant"
		}
		content := t.Content
		if t.Role == string(interaction.Operator) && t.Details.Channel == "plugin" && t.Details.Actor == "operator" {

			content = fmt.Sprintf("[Host-recorded operator decision: %s; tool=%s]\n%s", t.Details.Reason, t.Details.Tool, untrusted.Wrap("operator-act report (may include historical plugin output)", content))
		}
		if payload, ok := attributions[t.TurnSeq]; ok {
			content = attributeContent(content, attributionOf(payload))
		}

		msg := llm.Message{Role: role}
		if role == "user" && shown[t.ID] {

			if msg.Images, err = a.picturesWith(ctx, &t); err != nil {
				return nil, 0, err
			}
		}
		msg.Content = withAttached(content, t.Attached) + notShownLines(t.NotShown)
		conv = append(conv, msg)
		conv = appendInterruptions(conv, t.Interruptions)
		conv = appendInterruptions(conv, safeMarks[t.ID])

		conv = appendInterruptions(conv, t.Unanswered)
		conv = appendInterruptions(conv, safeNotes[t.ID])
	}
	for ; resultIndex < len(results); resultIndex++ {
		appendResult(results[resultIndex])
	}
	if resultErr != nil {
		logsink.Warn("act.error", "operator-act result history unavailable: %v", resultErr)
		conv = append(conv, llm.Message{Role: "user", Content: "[Operator-act result history could not be read for this prompt. Do not infer an outcome from its absence; inspect the recorded interaction history when available.]"})
	} else if moreResults {
		conv = append(conv, llm.Message{Role: "user", Content: "[Older operator-act results were omitted from this bounded prompt. Their retained excerpts remain in interaction history.]"})
	}

	if a.engine != nil {
		attached := safeAttached(safe)
		for _, t := range safe {
			if t.Role == string(interaction.System) {
				continue
			}
			role := "user"
			if t.Role == string(interaction.Resident) {
				role = "assistant"
			}
			conv = append(conv, llm.Message{Role: role, Content: withAttached(t.Content, attached[t.Record.ID])})
			if t.Role == string(interaction.Resident) {
				conv = appendInterruptions(conv, safeMarks[t.Record.ID])
			}
			conv = appendInterruptions(conv, safeNotes[t.Record.ID])
		}

		_, transient, _ := a.engine.TransientInteractions()
		for _, r := range transient {
			if r.Details.Origin == "unrecorded_output" && r.Role == interaction.Participant && r.Details.Channel == "plugin" && r.Details.Tool != "" {
				conv = append(conv, llm.Message{Role: "user", Content: r.Content})
			}
		}
	}

	omitted := total - len(turns)
	if omitted < 0 {
		omitted = 0
	}
	return conv, omitted, nil
}
