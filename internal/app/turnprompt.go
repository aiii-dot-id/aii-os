// .
// .
// .
package app

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
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
const planningBriefText = "### Before you act\n" +
	"Reads first, then the plan, once, before the first call that changes anything: " +
	"`work update plan=` with what you examined and what it IS, traced to paths and rows, not what " +
	"it should be; the falsifier in falsifier=, the result that would show the step wrong; the " +
	"simplest route that is sufficient; `steps=` as the count the traced plan implies, read against " +
	"the rhythm line in this turn's substrate block; `independent=` for what can run apart. Declaring buys the budget; an " +
	"undeclared turn gets the floor. Then act: call the tool rather than describe it. At a checkpoint " +
	"say which: done, with its scope; continue, with the exact resume point; stop, with why. The " +
	"practice is METHOD.md at your root."

// .
// .
// .
// .
// .
// .
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

// .
// .
// .
// .
// .
// .
func (a *App) buildWorkState() (string, error) {
	var parts []string
	// .
	// .
	// .
	if p, _ := a.activeOpenProject(); p != nil {
		line := fmt.Sprintf("### Current project: %s", p.Name)
		if p.Description != "" {
			line += " — " + p.Description
		}
		if p.Focus != "" {
			line += "\nWhere you left off here: " + p.Focus
		}
		// .
		// .
		// .
		// .
		// .
		// .
		if p.Contract.Outcome != "" {
			line += "\nPursuing: " + p.Contract.Outcome
		}
		// .
		// .
		// .
		// .
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
	}
	ws, err := a.store.ActiveWorkSession()
	if err != nil {
		return "", fmt.Errorf("load active work: %w", err)
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
	if ws != nil && a.planningBrief {
		parts = append(parts, planningBriefText)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if ws != nil {
		if card := resumeCardFor(ws); card != "" {
			parts = append(parts, card)
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
	standing, serr := a.store.StandingState()
	if serr != nil {
		// .
		// .
		// .
		// .
		// .
		// .
		logsink.Warn("prompt.error", "standing state unreadable this turn: %v", serr)
		parts = append(parts, "### Standing state\n(unavailable this turn — the store could not be read. That is NOT the same as having none; do not re-author on the strength of this.)")
	} else if strings.TrimSpace(standing) != "" {
		parts = append(parts, "### Standing state (yours — re-author when it stops being true)\n"+standing)
	} else if ws == nil || (ws.Focus == "" && ws.NextMove == "" && ws.Plan == "") {
		rp, ok, rperr := a.store.RecentPlan()
		if rperr != nil {
			// .
			// .
			// .
			// .
			// .
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
	// .
	// .
	// .
	// .
	// .
	if line := a.voiceWorkStateLine(); line != "" {
		parts = append(parts, line)
	}
	return strings.Join(parts, "\n\n"), nil
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
func (a *App) buildTurnFacts(events bool) (string, error) {
	var parts []string
	// .
	// .
	// .
	// .
	// .
	// .
	if len(a.bootInterrupted) > 0 {
		parts = append(parts, "### Interrupted at last shutdown\n"+store.InterruptedNote(a.bootInterrupted))
		a.composedInterrupted = true
	}
	// .
	// .
	// .
	// .
	a.composedWithheld = nil
	if a.toolReg != nil {
		if held := a.toolReg.WithheldOffers(); len(held) > 0 {
			parts = append(parts, withheldOffersNote(held))
			for _, h := range held {
				a.composedWithheld = append(a.composedWithheld, h.Name)
			}
		}
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if a.store != nil {
		if turns, calls, roPct, spawns, harvests, batchCenti, err := a.store.RhythmStats(48 * time.Hour); err != nil {
			// .
			// .
			// .
			logsink.Warn("prompt.error", "rhythm stats unreadable this turn: %v", err)
		} else if turns > 0 {
			line := fmt.Sprintf("### Rhythm — last 48h: %d turns, %d calls, %d%% read-only, %d spawns, %d harvests", turns, calls, roPct, spawns, harvests)
			// .
			// .
			// .
			if batchCenti > 0 {
				line += fmt.Sprintf(", %d.%02d calls per model round", batchCenti/100, batchCenti%100)
			}
			// .
			// .
			// .
			// .
			// .
			// .
			if plans, predicted, actual, cerr := a.store.PlanCalibration(48 * time.Hour); cerr != nil {
				// .
				// .
				// .
				logsink.Warn("prompt.error", "plan calibration unreadable this turn: %v", cerr)
			} else if plans > 0 {
				line += fmt.Sprintf("\nOf those, %d turn(s) stated a plan of %d calls in advance and took %d.", plans, predicted, actual)
			}
			// .
			// .
			// .
			// .
			if done, failedN, truncN, terr := a.store.ToolEventStats(48 * time.Hour); terr != nil {
				// .
				// .
				// .
				logsink.Warn("prompt.error", "tool event stats unreadable this turn: %v", terr)
			} else if done > 0 && (failedN > 0 || truncN > 0) {
				line += fmt.Sprintf("\nOf the last %d tool calls, %d failed and %d were truncated.", done, failedN, truncN)
			}
			parts = append(parts, line)
		}
	}
	ws, err := a.store.ActiveWorkSession()
	if err != nil {
		return "", fmt.Errorf("load active work: %w", err)
	}
	// .
	// .
	// .
	// .
	live, lerr := a.store.LiveSubagentSessions()
	if lerr != nil {
		// .
		// .
		// .
		// .
		logsink.Warn("prompt.error", "live sub-agent rows unreadable this turn: %v", lerr)
		parts = append(parts, "### Sub-agents running now\n(unavailable this turn — the store could not be read. Do NOT conclude that nothing is running.)")
	} else if len(live) > 0 {
		// .
		// .
		// .
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
		return "", fmt.Errorf("load delivered work: %w", err)
	}
	if len(subs) > 0 {
		var sb strings.Builder
		sb.WriteString("### Sub-agent outcomes (unharvested, shown once — note what matters)\n")
		for _, w := range subs {
			line := fmt.Sprintf("- [%s] %s → %s", w.ID, store.SubagentGoal(w.Description), w.Result)
			if scope := store.RenderEvidenceScope(w.Evidence); scope != "" {
				line += " " + scope
			}
			sb.WriteString(line + "\n")
		}
		parts = append(parts, sb.String())
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
		ids := make([]string, 0, len(subs))
		for _, w := range subs {
			ids = append(ids, w.ID)
		}
		a.composedUnharvested = ids
	}
	// .
	// .
	// .
	// .
	if cue, cerr := a.store.CuriosityCue(); cerr == nil && cue != nil {
		urgent := ws != nil && (ws.DecisionNeeded != "" || (ws.Evidence != "" && store.EvidenceRecovery(ws.Evidence) != ""))
		// .
		// .
		for _, w := range subs {
			if store.EvidenceRecovery(w.Evidence) != "" {
				urgent = true
			}
		}
		if !urgent {
			if line := store.RenderCuriosityCue(*cue); line != "" {
				parts = append(parts, line)
			}
		}
	}
	// .
	// .
	// .
	// .
	// .
	if !attentionUrgent(ws, subs) && a.store != nil {
		if items, err := memory.New(a.store).Attention(context.Background(), time.Now()); err != nil {
			logsink.Warn("prompt.error", "attention: unreadable this turn, the prompt goes without it: %v", err)
		} else if medium := memory.OfCost(items, memory.CostMedium); len(medium) > 0 {
			parts = append(parts, "### Attention — one thing the record is holding\n"+medium[0].Text+"\nIt closes nothing by itself; resolve it or hold it knowingly.")
		}
	}
	if events {
		if err := a.appendEventsSince(&parts); err != nil {
			return "", err
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

// .
// .
// .
// .
// .
// .
func (a *App) appendEventsSince(parts *[]string) error {
	lastResident, err := a.store.LastTurnAtMs("resident")
	if err != nil {
		return fmt.Errorf("load last resident turn: %w", err)
	}
	if firings, err := a.store.TimerFiringsSince(lastResident); err != nil {
		return fmt.Errorf("load timer firings: %w", err)
	} else if len(firings) > 0 {
		var lines []string
		for _, f := range firings {
			lines = append(lines, "- "+f.Content)
		}
		*parts = append(*parts, "## Your timers fired since your last turn\n"+
			strings.Join(lines, "\n")+
			"\n(These are facts delivered by the time system.)")
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
	if arrivals, err := a.store.InboundSince(lastResident); err != nil {
		return fmt.Errorf("load arrivals: %w", err)
	} else if len(arrivals) > 0 {
		var lines []string
		for _, m := range arrivals {
			// .
			// .
			// .
			lines = append(lines, "- "+a.frameStoredArrival(m))
		}
		*parts = append(*parts, "## Messages that arrived since your last turn\n"+
			strings.Join(lines, "\n")+
			"\n(Answer any that deserve it with send, or leave them.)")
	}
	// .
	// .
	// .
	if outcomes, err := a.store.OutboxOutcomesSince(lastResident); err != nil {
		return fmt.Errorf("load delivery outcomes: %w", err)
	} else if len(outcomes) > 0 {
		*parts = append(*parts, "## What became of the messages you sent\n"+
			deliveryOutcomeLines(outcomes)+
			"\n(Delivered: an adapter took it. Response lost: it may have arrived — do not resend blindly. Parked: no more attempts; your operator can see it.)")
	}
	return nil
}

// .
// .
// .
// .
// .
// .
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
	// .
	// .
	// .
	// .
	// .
	return "### Voice — " + voiceModeSentence(listen, speak) +
		"\nChange it with work action=voice.mode: mode= names a pair, or listen= and/or speak=."
}

// .
// .
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

// .
// .
// .
// .
// .

// .
// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) markComposedHarvests() {
	// .
	// .
	if a.composedInterrupted {
		a.bootInterrupted = nil
		a.composedInterrupted = false
	}
	if len(a.composedWithheld) > 0 && a.toolReg != nil {
		a.toolReg.MarkOfferNoticesTold(a.composedWithheld)
		a.composedWithheld = nil
	}
	if len(a.composedUnharvested) == 0 {
		return
	}
	now := time.Now().UTC().UnixMilli()
	swept := 0
	for _, id := range a.composedUnharvested {
		if err := a.store.MarkHarvested(id, now); err != nil {
			// .
			// .
			// .
			// .
			logsink.Warn("harvest.error", "mark %s failed: %v", id, err)
			continue
		}
		swept++
	}
	// .
	// .
	// .
	// .
	// .
	a.turnMeterMu.Lock()
	a.turnHarvested += swept
	a.turnMeterMu.Unlock()
	logsink.Info("harvest.end", "swept=%d of=%d", swept, len(a.composedUnharvested))
	a.composedUnharvested = nil
}

func (a *App) gatedSystem(p *prompt.Prompt) llm.Message {
	sysText := a.promptGate.SystemForPrompt(p)
	msg := llm.Message{Role: "system", Content: sysText}
	if p.StableLen > 0 && p.StableLen <= len(p.Text) {
		stable := p.Text[:p.StableLen]
		if i := strings.Index(sysText, stable); i >= 0 {
			msg.StableLen = i + len(stable)
		}
	}
	return msg
}

func (a *App) promptReserve(current llm.Message, omitted int) (int, error) {
	if omitted < 0 {
		omitted = 0
	}
	// .
	// .
	// .
	sent := llm.Message{Role: current.Role, Content: conversation.TurnBlock("", omitted, 0, false) + current.Content}
	return llm.EstimateInputTokens([]llm.Message{sent}, a.buildToolDefinitions())
}

// .
// .
func (a *App) buildHistory() ([]llm.Message, int, error) {
	recentTurns := a.configSnapshot().Prompt.RecentTurns
	if recentTurns <= 0 {
		recentTurns = 20
	}
	// .
	// .
	// .
	// .
	turns, total, err := a.store.ConversationWindow(recentTurns)
	if err != nil {
		return nil, 0, fmt.Errorf("load conversation history: %w", err)
	}
	conv := make([]llm.Message, 0, len(turns)+1)
	// .
	// .
	// .
	attributions := a.speakerAnnotations(turns)
	for _, t := range turns {
		if t.Role == "system" {
			continue
		}
		role := "user"
		if t.Role == "resident" {
			role = "assistant"
		}
		content := t.Content
		if payload, ok := attributions[t.TurnSeq]; ok {
			content = attributeContent(content, attributionOf(payload))
		}
		conv = append(conv, llm.Message{Role: role, Content: content})
	}
	// .
	// .
	// .
	if a.engine != nil {
		for _, t := range a.engine.SafeTranscript() {
			if t.Role == "system" {
				continue
			}
			role := "user"
			if t.Role == "resident" {
				role = "assistant"
			}
			conv = append(conv, llm.Message{Role: role, Content: t.Content})
		}
	}
	// .
	// .
	// .
	// .
	omitted := total - len(turns)
	if omitted < 0 {
		omitted = 0
	}
	return conv, omitted, nil
}
