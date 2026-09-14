// .
// .
// .
package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
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
// .
const planningBriefText = "### Before you act\n" +
	"Reads first, then the plan, once, before the first call that changes anything: " +
	"`work update plan=` with what you examined and what it IS, traced to paths and rows, not what " +
	"it should be; the falsifier in falsifier=, the result that would show the step wrong; the " +
	"simplest route that is sufficient; `steps=` as the count the traced plan implies, read against " +
	"the rhythm line above; `independent=` for what can run apart. Declaring buys the budget; an " +
	"undeclared turn gets the floor. Then act: call the tool rather than describe it. At a checkpoint " +
	"say which: done, with its scope; continue, with the exact resume point; stop, with why. The " +
	"practice is METHOD.md at your root."

func (a *App) buildWorkState() (string, error) {
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
	// .
	// .
	// .
	if a.store != nil {
		if turns, calls, roPct, spawns, harvests, batchCenti, err := a.store.RhythmStats(48 * time.Hour); err != nil {
			// .
			// .
			// .
			log.Printf("Warning: rhythm stats unreadable this turn: %v", err)
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
				log.Printf("Warning: plan calibration unreadable this turn: %v", cerr)
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
				log.Printf("Warning: tool event stats unreadable this turn: %v", terr)
			} else if done > 0 && (failedN > 0 || truncN > 0) {
				line += fmt.Sprintf("\nOf the last %d tool calls, %d failed and %d were truncated.", done, failedN, truncN)
			}
			parts = append(parts, line)
		}
	}
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
		if card := store.RenderResumeCard(store.ResumeFields{
			Focus:            ws.Focus,
			Established:      ws.State,
			NextAction:       ws.NextMove,
			ExpectedEvidence: ws.ExpectedEvidence,
			Falsifier:        ws.Falsifier,
			DecisionNeeded:   ws.DecisionNeeded,
			ResumePoint:      ws.Plan,
			Evidence:         ws.Evidence,
		}); card != "" {
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
		log.Printf("Warning: standing state unreadable this turn: %v", serr)
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
			log.Printf("Warning: recent plan unreadable this turn: %v", rperr)
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
	live, lerr := a.store.LiveSubagentSessions()
	if lerr != nil {
		// .
		// .
		// .
		// .
		log.Printf("Warning: live sub-agent rows unreadable this turn: %v", lerr)
		parts = append(parts, "### Sub-agents running now\n(unavailable this turn — the store could not be read. Do NOT conclude that nothing is running.)")
	} else if len(live) > 0 {
		// .
		// .
		// .
		states, serr := a.store.SubagentQueueStates(identity.SubagentWorkKind)
		if serr != nil {
			log.Printf("Warning: sub-agent queue states unreadable this turn: %v", serr)
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
			log.Printf("attention: unreadable this turn, the prompt goes without it: %v", err)
		} else if medium := memory.OfCost(items, memory.CostMedium); len(medium) > 0 {
			parts = append(parts, "### Attention — one thing the record is holding\n"+medium[0].Text+"\nIt closes nothing by itself; resolve it or hold it knowingly.")
		}
	}
	return strings.Join(parts, "\n\n"), nil
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
			log.Printf("HARVEST: mark %s failed: %v", id, err)
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
	log.Printf("HARVEST_SWEEP swept=%d of=%d", swept, len(a.composedUnharvested))
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
	return llm.EstimateInputTokens(
		[]llm.Message{{Role: "system", Content: conversation.HistoryOmissionNote(omitted)}, current},
		a.buildToolDefinitions(),
	)
}

// .
// .
func (a *App) buildHistory() ([]llm.Message, int, error) {
	recentTurns := a.configSnapshot().Prompt.RecentTurns
	if recentTurns <= 0 {
		recentTurns = 20
	}
	turns, err := a.store.RecentTurns(recentTurns)
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
	total, err := a.store.ConversationTurnCount()
	if err != nil {
		return nil, 0, fmt.Errorf("count conversation history: %w", err)
	}
	omitted := total - len(turns)
	if omitted < 0 {
		omitted = 0
	}
	return conv, omitted, nil
}
