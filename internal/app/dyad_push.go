package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

const defaultPlaybookMaxBytes = 2000

const playbookCut = "\n… (cut at %d bytes; read %s for the rest)"

func playbookRel(family string) string { return path.Join("playbooks", family, "SKILL.md") }

func (a *App) playbook(ctx context.Context, family string) (text string, refused error) {
	if family == "" || a.toolReg == nil {
		return "", nil
	}
	most := a.playbookMaxBytes()
	raw, more, err := a.toolReg.ReadOwn(ctx, playbookRel(family), most)
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
		text += fmt.Sprintf(playbookCut, most, playbookRel(family))
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
	if a.store != nil {
		b.WriteString(a.kindsDone(family))
	}
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
	line := fmt.Sprintf("Kind %s: %d delivered attempt(s): %s.", r.Family, len(r.Attempts), r.Tally().Line())
	if r.Shortest != nil {
		line += fmt.Sprintf(" %s: %d calls", r.ShortestLabel(), r.Shortest.ToolCalls)
		if len(r.Steps) > 0 {
			line += " (" + strings.Join(r.Steps, ", ") + ")"
		}
		line += "."
	}
	return line
}

const (
	kindsNewWords  = "\n\nNew kind %s. Kinds you have done: %s. If this is one of them: work action=update family=<that one>."
	kindsNoneWords = "\n\nNo kind named. Kinds you have done: %s. If this is one of them: work action=update family=<that one>."

	checkExitedWords  = "\nYour check exited %d (%s)."
	checkDiffersWords = "\nYour check exited %d (%s): your result says %s; the record keeps both."
	checkNoneWords    = "\nNo check was declared."
	checkNotRunWords  = "\nYour check did not run: %s."
	checkRefusedWords = "\nYour check was refused before it ran (%s): %s"
	checkNoExitWords  = "\nYour check ended without an exit status (%s)."
	checkUnrecorded   = "\nYour check ran, and its run could not be recorded: %v."

	deliverKindWords     = "\nKind %s: %d attempt(s); this one took %d calls, %s %d."
	deliverKindFirstWord = "\nKind %s: %d attempt(s); this one took %d calls."
	deliverPlaybookWords = "\n%s: %s. If this attempt taught a step, edit it now: one line per step, each naming this session (%s)."
)

const kindsMost = 8

func (a *App) kindsDone(family string) string {
	kinds, err := a.store.Kinds(kindsMost)
	if err != nil || len(kinds) == 0 {
		return ""
	}
	listed := make([]string, len(kinds))
	for i, k := range kinds {
		if k.Family == family {
			return ""
		}
		listed[i] = fmt.Sprintf("%s (%d)", k.Family, k.Attempts)
	}
	if family == "" {
		return fmt.Sprintf(kindsNoneWords, strings.Join(listed, ", "))
	}

	if r, err := a.store.FamilyAttempts(family, 1); err == nil && len(r.Attempts) > 0 {
		return ""
	}
	return fmt.Sprintf(kindsNewWords, family, strings.Join(listed, ", "))
}

func (a *App) workDelivered(ctx context.Context, session string) string {
	if a.store == nil {
		return ""
	}
	out := a.runCheck(ctx, session)
	ws, err := a.store.WorkSessionByID(session)
	if err != nil || ws == nil || ws.Family == "" {
		return out
	}
	return out + a.deliveredKind(ctx, ws)
}

func (a *App) runCheck(ctx context.Context, session string) string {
	c, err := a.store.WorkCheckOf(session)
	switch {
	case err != nil:
		return fmt.Sprintf(checkNotRunWords, "its declaration could not be read: "+err.Error())
	case c.Command == "":
		return checkNoneWords
	case c.Ran:
		return checkLine(c, a.resultClass(session))
	case a.toolReg == nil:
		return fmt.Sprintf(checkNotRunWords, "this host runs no shell")
	case !a.toolReg.ToolEnabled("shell"):
		return fmt.Sprintf(checkNotRunWords, "your operator has the shell off")
	}
	if why, held := a.SafeMode(); held {
		return fmt.Sprintf(checkNotRunWords, "SAFE holds the shell ("+why+")")
	}
	a.keepSandboxOffPlaces()
	started := time.Now()
	res, err := a.toolReg.ExecuteModel(ctx, "shell", map[string]any{"command": c.Command})
	elapsed := time.Since(started).Milliseconds()
	if errors.Is(err, tools.ErrUnknownTool) {
		return fmt.Sprintf(checkNotRunWords, "this host runs no shell")
	}
	if err != nil {
		res = tools.Result{Error: err.Error()}
	}
	outcome := interaction.Succeeded
	switch {
	case res.Refused:
		outcome = interaction.Refused
	case res.Failed():
		outcome = interaction.Failed
	}
	m := store.ToolMeasurement{DurationMS: &elapsed, ExitStatus: res.ExitStatus, ReasonCode: res.ReasonCode}
	if err := a.store.RecordWorkCheckRun(ctx, session, c.Command, res.Text(), outcome, res.Truncated, m); err != nil {
		return fmt.Sprintf(checkUnrecorded, err)
	}
	c.Ran, c.Outcome, c.ExitStatus, c.DurationMS, c.ReasonCode = true, string(outcome), res.ExitStatus, &elapsed, res.ReasonCode
	if outcome == interaction.Refused {
		return fmt.Sprintf(checkRefusedWords, res.ReasonCode, res.Text())
	}
	return checkLine(c, a.resultClass(session))
}

func checkLine(c store.WorkCheck, class string) string {
	took := "no time recorded"
	if c.DurationMS != nil {
		took = fmt.Sprintf("%.1f s", float64(*c.DurationMS)/1000)
	}
	switch {
	case c.Outcome == string(interaction.Refused):
		return fmt.Sprintf(checkRefusedWords, c.ReasonCode, "see its call in recall source=work")
	case c.ExitStatus == nil:
		return fmt.Sprintf(checkNoExitWords, c.ReasonCode)
	case *c.ExitStatus == 0 || class == "":
		return fmt.Sprintf(checkExitedWords, *c.ExitStatus, took)
	}
	return fmt.Sprintf(checkDiffersWords, *c.ExitStatus, took, class)
}

func (a *App) resultClass(session string) string {
	ws, err := a.store.WorkSessionByID(session)
	if err != nil || ws == nil {
		return ""
	}
	word, _, ok := strings.Cut(ws.Result, ":")
	if !ok || word != "served" && word != "partial" && word != "unserved" {
		return ""
	}
	return word
}

func (a *App) deliveredKind(ctx context.Context, ws *store.WorkSession) string {
	r, err := a.store.FamilyAttempts(ws.Family, 50)
	if err != nil {
		return ""
	}
	var line string
	if r.Shortest != nil && r.Shortest.ID != ws.ID {
		line = fmt.Sprintf(deliverKindWords, ws.Family, len(r.Attempts), ws.ToolCalls, strings.ToLower(r.ShortestLabel()[:1])+r.ShortestLabel()[1:], r.Shortest.ToolCalls)
	} else {
		line = fmt.Sprintf(deliverKindFirstWord, ws.Family, len(r.Attempts), ws.ToolCalls)
	}
	return line + fmt.Sprintf(deliverPlaybookWords, playbookRel(ws.Family), a.playbookState(ctx, ws.Family), ws.ID)
}

func (a *App) playbookState(ctx context.Context, family string) string {
	if a.toolReg == nil {
		return "none"
	}
	when, err := a.toolReg.ModifiedOwn(ctx, playbookRel(family))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "none"
	case err != nil:
		return "could not be read (" + err.Error() + ")"
	}
	return "last edited " + when.UTC().Format("2006-01-02")
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
			line += fmt.Sprintf(cardPastShortest, n, strings.ToLower(r.ShortestLabel()), r.Shortest.ToolCalls)
		}
	}
	if block := a.playbookBlock(ctx, ws.Family); block != "" {
		return line + "\n" + block
	}
	return line + fmt.Sprintf(" No playbook yet (%s).", playbookRel(ws.Family))
}

const cardPastShortest = " %d calls so far in this session; the %s attempt took %d."

const cardCheckWords = "Check (the host runs it at deliver): %s"

func (a *App) checkCardLine(ws *store.WorkSession) string {
	if ws == nil || a.store == nil {
		return ""
	}
	c, err := a.store.WorkCheckOf(ws.ID)
	if err != nil || c.Command == "" {
		return ""
	}
	return fmt.Sprintf(cardCheckWords, c.Command)
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
	var out string
	if a.store != nil {
		if r, ok, err := a.store.LastRecovery(tool, obs.ReasonCode); err == nil && ok {
			out = fmt.Sprintf(recoveredWords, tool, endedWords[r.Outcome], reasonOrNone(obs.ReasonCode), tool, r.Args)
		}
	}
	if lessons := a.contextText(ctx, failureQuery(tool, obs)); lessons != "" {
		out += "\n\nYour notes about failures like this one:\n" + lessons
	}
	return out
}

const recoveredWords = "\n\nLast time %s %s (%s), your next call that worked was: %s %s"

var endedWords = map[string]string{"failed": "failed", "refused": "was refused"}

func reasonOrNone(code string) string {
	if code == "" {
		return "no reason code"
	}
	return code
}

func failureQuery(tool string, obs conversation.Observation) string {
	q := strings.TrimSpace(tool + " " + obs.ReasonCode)
	switch {
	case obs.Refused:
		what := obs.Text
		if len(what) > 300 {
			what = what[:300]
			for !utf8.ValidString(what) {
				what = what[:len(what)-1]
			}
		}
		q += " " + what
	case obs.ExitStatus != nil:
		q += fmt.Sprintf(" exit %d", *obs.ExitStatus)
	}
	return q
}
