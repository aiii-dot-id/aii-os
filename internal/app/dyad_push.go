package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const maxPlaybookBytes = 2000

func playbookRel(family string) string { return path.Join("playbooks", family, "SKILL.md") }

func (a *App) playbook(ctx context.Context, family string) (text string, refused error) {
	if family == "" || a.toolReg == nil {
		return "", nil
	}
	raw, more, err := a.toolReg.ReadOwn(ctx, playbookRel(family), maxPlaybookBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	text = string(raw)
	if more {
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		text += fmt.Sprintf("\n… (cut at %d bytes; read %s for the rest)", maxPlaybookBytes, playbookRel(family))
	}
	return text, nil
}

func (a *App) playbookBlock(ctx context.Context, family string) string {
	text, refused := a.playbook(ctx, family)
	if refused != nil {
		return fmt.Sprintf("Your playbook for %s (%s) could not be read: %v.", family, playbookRel(family), refused)
	}
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return fmt.Sprintf("Your playbook for %s (%s, your own file; fallible, check it against what you find):\n%s", family, playbookRel(family), text)
}

func (a *App) contextText(ctx context.Context, query string) string {
	var parts []string
	for _, s := range a.pluginContext(ctx, query) {
		if strings.HasPrefix(s.Name, "Plugin context — ") && !strings.Contains(s.Content, "is unavailable for this decision") {
			parts = append(parts, s.Content)
		}
	}
	return strings.Join(parts, "\n")
}

func (a *App) workStarted(ctx context.Context, session, family, description string) string {
	var b strings.Builder
	if family != "" && a.store != nil {
		if r, err := a.store.FamilyAttempts(family, 20); err == nil && len(r.Attempts) > 0 {
			b.WriteString("\n\n" + familySummary(r))
		}
		if block := a.playbookBlock(ctx, family); block != "" {
			b.WriteString("\n\n" + block)
		} else {
			fmt.Fprintf(&b, "\n\nNo playbook for %s yet (%s): when the work is served, write one from its calls (work action=measure family=%s).", family, playbookRel(family), family)
		}
	}
	if lessons := a.contextText(ctx, strings.TrimSpace(family+" "+description)); lessons != "" {
		b.WriteString("\n\n" + lessons)
	}
	return b.String()
}

func familySummary(r store.FamilyRecord) string {
	served := 0
	for _, a := range r.Attempts {
		if a.Outcome == "served" {
			served++
		}
	}
	line := fmt.Sprintf("Kind %s: %d delivered attempt(s), %d served.", r.Family, len(r.Attempts), served)
	if r.Shortest != nil {
		line += fmt.Sprintf(" Shortest served: %d calls", r.Shortest.ToolCalls)
		if len(r.Steps) > 0 {
			line += " (" + strings.Join(r.Steps, ", ") + ")"
		}
		line += "."
	}
	return line
}

func (a *App) familyCardLine(ctx context.Context, ws *store.WorkSession) string {
	if ws == nil || ws.Family == "" || a.store == nil {
		return ""
	}
	r, err := a.store.FamilyAttempts(ws.Family, 20)
	if err != nil {
		return ""
	}
	line := "Kind " + ws.Family + ": no delivered attempt yet."
	if len(r.Attempts) > 0 {
		line = familySummary(r)
	}
	if r.Shortest != nil {
		if n, err := a.store.SessionToolCalls(ws.ID); err == nil && n > r.Shortest.ToolCalls {
			line += fmt.Sprintf(" %d calls so far in this session; the shortest served attempt took %d.", n, r.Shortest.ToolCalls)
		}
	}
	if block := a.playbookBlock(ctx, ws.Family); block != "" {
		return line + "\n" + block
	}
	return line + fmt.Sprintf(" No playbook yet (%s).", playbookRel(ws.Family))
}

type failurePushes struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (a *App) failureContext(ctx context.Context, tool string, obs conversation.Observation) string {
	if !obs.Failed && !obs.Refused || obs.Cancelled {
		return ""
	}
	seat := a.callSeat(ctx)
	key := seat.session + "\x00" + tool + "\x00" + obs.ReasonCode
	a.failurePushed.mu.Lock()
	if a.failurePushed.seen == nil {
		a.failurePushed.seen = map[string]bool{}
	}
	shown := a.failurePushed.seen[key]
	a.failurePushed.seen[key] = true
	a.failurePushed.mu.Unlock()
	if shown {
		return ""
	}
	what := obs.Text
	if len(what) > 300 {
		what = what[:300]
		for !utf8.ValidString(what) {
			what = what[:len(what)-1]
		}
	}
	lessons := a.contextText(ctx, tool+" "+obs.ReasonCode+" "+what)
	if lessons == "" {
		return ""
	}
	return "\n\nYour notes about failures like this one:\n" + lessons
}
