package identity

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

const recallWorkSource = ` work: the calls of your current work session or of id=ws_…; query="tex_…" or query="check_…" reads one call's kept result.`

const recallWorkID = ` source=work: a work session's id (ws_…)`

const workRecallClause = ` A session's calls read back with recall source=work.`

const (
	workRecordHeading = "Work record — the tool calls of %s (%s: %s), newest first, %d a page. Each call's printed id (tex_… or check_…), as query, reads its result as kept."
	workResultSource  = "tool result"
	workNoResult      = "no result recorded yet"
	workReturnedNone  = "(it returned nothing)"
	workShownOf       = " [the first %d of %d characters]"
	workCallLineForm  = "  [seq %d, %s, %s, %s] %s(%s)"
	workFactReason    = "reason %s"
	workFactExit      = "exit %d"
	workFactSeveral   = "begun while several sessions were active, counted to the newest"

	workFactStep      = "step: %s"
	workKeptIndent    = "    | "
	workReadAlone     = `recall source=work%s query="" after_seq=%d limit=1 shows it alone, and its printed call id reads its result whole`
	workNoCalls       = "No tool call of %s is recorded."
	workNoOlder       = "No older call of %s before after_seq=%d."
	workNoMatch       = "No call of %s holds %q in the call or its result: every word must appear; try fewer words."
	workOlder         = "Older: after_seq=%d."
	workTexUnknown    = "Work record:\n%s is not a tool call the record holds: the call id (tex_… or check_…) is read from a line of recall source=work."
	workTexHead       = "Work record — %s: %s, %s, %s%s"
	workTexInSession  = "in %s"
	workTexNoSession  = "in no work session"
	workTexByOperator = "an act of your operator's, in no work session"
	workTexCut        = " The full output was %d characters; the record keeps its first %d"
	workTexCutLast    = " and its last line"
	workTexNoPart     = "Work record:\n%s's result has %d part(s); part %s is not one of them."
	workTexEnd        = "[end of %s's result as kept, part %d of %d"
	workTexContinues  = "; continues: query=\"%s part:%d\"]"
	workNoSessionWhy  = "source=work reads the calls of the work session you act in, and you act in none: name one with id=ws_…"
	workUnknownWhy    = "%s names none of your work sessions: work status lists them with their whole ids, and a short id must begin one of them"
	workAmbiguousWhy  = "%s begins %d of your work sessions: use the whole id, as work status lists it"
	workTexCursorWhy  = "query=%q reads one result whole; after_seq pages a session's calls — drop one of them"
)

type WorkRecordRefusal struct {
	Code string
	Why  string
}

func (e *WorkRecordRefusal) Error() string { return e.Why }

const (
	recallWorkPage  = 10
	recallWorkTaste = 300

	recallWorkHead = 1024
)

var texRef = regexp.MustCompile(`^((?i:tex_[0-9a-f]{64})|check_[a-zA-Z0-9_-]+)(?:\s+(?i:part)\s*[:#]?\s*(\d+))?$`)

func (e *Engine) recallWork(ctx context.Context, id, query string, exact bool, cursor uint64, limit int, limitNote string) (string, error) {
	if m := texRef.FindStringSubmatch(query); m != nil && !exact {
		if cursor > 0 {
			return "", &WorkRecordRefusal{Code: "work.tex_with_cursor", Why: fmt.Sprintf(workTexCursorWhy, query)}
		}
		id := m[1]
		if strings.HasPrefix(strings.ToLower(id), "tex_") {
			id = strings.ToLower(id)
		}
		return e.recallWorkResult(ctx, id, m[2])
	}
	session, err := e.workRecordSession(ctx, id)
	if err != nil {
		return "", err
	}
	var terms []string
	switch {
	case query == "":
	case exact:
		terms = []string{query}
	default:
		terms = strings.Fields(query)
	}
	n := limit
	if n == 0 {
		n = recallWorkPage
	}
	calls, err := e.store.WorkCalls(session.ID, terms, cursor, n+1, recallWorkHead, interaction.TurnID(ctx))
	if err != nil {
		return "", fmt.Errorf("recall work: %w", err)
	}
	more := len(calls) > n
	calls = calls[:min(n, len(calls))]
	if limitNote != "" {
		limitNote = "\n" + limitNote
	}
	heading := fmt.Sprintf(workRecordHeading, session.ID, session.Status, recallExcerpt(session.Description, 120), n)
	if len(calls) == 0 {
		switch {
		case cursor > 0:
			return heading + "\n" + fmt.Sprintf(workNoOlder, session.ID, cursor) + limitNote, nil
		case query != "":
			return heading + "\n" + fmt.Sprintf(workNoMatch, session.ID, query) + limitNote, nil
		}
		return heading + "\n" + fmt.Sprintf(workNoCalls, session.ID) + limitNote, nil
	}
	p := &recallPart{source: "work", more: more, scope: " id=" + session.ID}
	for _, c := range calls {
		p.items = append(p.items, workCallLine(c))
		p.seqs = append(p.seqs, c.Seq)
	}
	bound := e.recallBound(ctx)
	return fitParts([]*recallPart{p}, bound, func(fits bool) string {
		var b strings.Builder
		b.WriteString(heading)
		for _, item := range p.items {
			b.WriteString("\n" + item)
		}
		if p.more {
			b.WriteString("\n" + fmt.Sprintf(workOlder, p.seqs[len(p.seqs)-1]))
		}
		b.WriteString(recallCutLine([]*recallPart{p}, bound, fits))
		return b.String() + limitNote
	}), nil
}

func (e *Engine) workRecordSession(ctx context.Context, id string) (*store.WorkSession, error) {
	if id == "" {
		if sub, ok := ctx.Value(SubagentWorkSession{}).(string); ok && sub != "" {
			id = sub
		} else if resumed := interaction.ResumeSession(ctx); resumed != "" {

			id = resumed
		} else {
			ws, err := e.store.ActiveWorkSession()
			if err != nil {
				return nil, fmt.Errorf("recall work: load the active work session: %w", err)
			}
			if ws == nil {
				return nil, &WorkRecordRefusal{Code: "work.no_session", Why: workNoSessionWhy}
			}
			return ws, nil
		}
	}
	ws, err := e.store.WorkSessionByID(id)
	if err != nil {
		return nil, fmt.Errorf("recall work: load work session %s: %w", id, err)
	}
	if ws == nil {

		if r, ok := e.store.(interface {
			RecentWorkSessions(limit int) ([]store.WorkSession, error)
		}); ok {
			recent, err := r.RecentWorkSessions(workSessionPrefixScan)
			if err != nil {
				return nil, fmt.Errorf("recall work: list work sessions: %w", err)
			}
			var hits []store.WorkSession
			for _, s := range recent {
				if strings.HasPrefix(s.ID, id) {
					hits = append(hits, s)
				}
			}
			switch len(hits) {
			case 1:
				return &hits[0], nil
			case 0:
			default:
				return nil, &WorkRecordRefusal{Code: "work.session_ambiguous", Why: fmt.Sprintf(workAmbiguousWhy, id, len(hits))}
			}
		}
		return nil, &WorkRecordRefusal{Code: "work.session_unknown", Why: fmt.Sprintf(workUnknownWhy, id)}
	}
	return ws, nil
}

const workSessionPrefixScan = 200

func workCallLine(c store.WorkCall) string {
	line := fmt.Sprintf(workCallLineForm, c.Seq, c.Tex, c.Tool, workFacts(c), c.Tool, c.Args())
	if !c.Ended {
		return line
	}
	head, _, cut := c.KeptResult()
	if head == "" {
		return line + "\n    " + workReturnedNone
	}
	shown, n := workFramed(hostFrames(c), head, cut || c.ResultClipped, recallWorkTaste)
	line += "\n" + shown
	whole, known := store.RecordChars(c.ResultRecord)
	if !known {
		whole = utf8.RuneCountInString(head)
	}
	if n < whole {
		line += fmt.Sprintf(workShownOf, n, whole)
	}
	return line
}

func workFacts(c store.WorkCall) string {
	if !c.Ended {
		return workNoResult + workSeveral(c) + workStep(c)
	}
	facts := []string{c.Outcome}
	if c.ReasonCode != "" {
		facts = append(facts, fmt.Sprintf(workFactReason, c.ReasonCode))
	}
	if c.ExitStatus != nil {
		facts = append(facts, fmt.Sprintf(workFactExit, *c.ExitStatus))
	}
	if c.DurationMS != nil {
		facts = append(facts, (time.Duration(*c.DurationMS) * time.Millisecond).String())
	}
	return strings.Join(facts, ", ") + workSeveral(c) + workStep(c)
}

func workStep(c store.WorkCall) string {
	if c.PlanStep == nil {
		return ""
	}
	return ", " + fmt.Sprintf(workFactStep, *c.PlanStep)
}

func workSeveral(c store.WorkCall) string {
	if c.SessionReason == store.SeveralActive {
		return ", " + workFactSeveral
	}
	return ""
}

const PluginToolPrefix = "pl_"

const WebFetchTool = "web_fetch"

func hostFrames(c store.WorkCall) bool {
	if c.Actor == "operator" || c.HeardRead() {
		return false
	}
	return lookupVerb(c.Tool) != nil || c.Tool == WebFetchTool || strings.HasPrefix(c.Tool, PluginToolPrefix)
}

func workFramed(host bool, text string, cut bool, most int) (string, int) {
	if !host {
		shown, n := text, utf8.RuneCountInString(text)
		if most > 0 && n > most {
			shown, n = string([]rune(text)[:most]), most
		}
		return "    " + untrusted.Wrap(workResultSource, shown), n
	}
	if cut && openAtEnd(text) {
		text += "\n" + untrusted.Close
	}
	n := utf8.RuneCountInString(text)
	if most > 0 {
		text, n = untrusted.Head(text, most)
	}
	return setIn(text), n
}

func setIn(text string) string {
	return workKeptIndent + strings.ReplaceAll(text, "\n", "\n"+workKeptIndent)
}

func openLine(line string) bool {
	line = strings.TrimPrefix(line, "Error: ")
	return line == untrusted.Open || (strings.HasPrefix(line, untrusted.Open+" source: ") && !untrusted.Contains(line[len(untrusted.Open):]))
}

func openAtEnd(text string) bool {
	return strings.LastIndex(text, untrusted.Open) > strings.LastIndex(text, untrusted.Close)
}

func (e *Engine) recallWorkResult(ctx context.Context, tex, partArg string) (string, error) {
	c, ok, err := e.store.WorkCallByTex(tex)
	if err != nil {
		return "", fmt.Errorf("recall work %s: %w", tex, err)
	}
	if !ok {
		return fmt.Sprintf(workTexUnknown, tex), nil
	}
	where := fmt.Sprintf(workTexInSession, c.WorkSession)
	switch {
	case c.Actor == "operator":
		where = workTexByOperator
	case c.WorkSession == "":
		where = workTexNoSession
	}
	head := fmt.Sprintf(workTexHead, tex, c.Tool+"("+c.Args()+")", workFacts(c), where, ", "+c.CalledAt)
	if !c.Ended {
		return head + ".", nil
	}
	kept, last, cut := c.KeptResult()
	if cut {
		whole, _ := store.RecordChars(c.ResultRecord)
		head += "." + fmt.Sprintf(workTexCut, whole, utf8.RuneCountInString(kept))
		if last != "" {
			head += workTexCutLast
		}
	}
	text := kept
	if last != "" {
		text += "\n…\n" + last
	}
	if text == "" {
		return head + ". " + workReturnedNone, nil
	}
	parts := workParts(hostFrames(c), text, cut && last == "", partRunes(e.recallBound(ctx), recallPartLabels))
	part := 1
	if partArg != "" {
		p, err := strconv.Atoi(partArg)
		if err != nil || p < 1 || p > len(parts) {
			return fmt.Sprintf(workTexNoPart, tex, len(parts), partArg), nil
		}
		part = p
	}
	out := head + ":\n\n" + parts[part-1] + "\n\n" + fmt.Sprintf(workTexEnd, tex, part, len(parts))
	if part < len(parts) {
		return out + fmt.Sprintf(workTexContinues, tex, part+1), nil
	}
	return out + "]", nil
}

func workParts(host bool, text string, cut bool, size int) []string {
	if !host {
		var parts []string
		runes := []rune(text)
		for i := 0; i < len(runes) || i == 0; i += size {
			parts = append(parts, untrusted.Wrap(workResultSource, string(runes[i:min(i+size, len(runes))])))
		}
		return parts
	}
	var parts []string
	var b strings.Builder
	opened, open := "", ""
	n := 0
	indent := utf8.RuneCountInString(workKeptIndent)
	flush := func() {
		body := strings.TrimSuffix(b.String(), "\n")
		if opened != "" {
			body = opened + "\n" + body
		}
		if open != "" {
			body += "\n" + untrusted.Close
		}
		parts = append(parts, body)
		b.Reset()
		n, opened = 0, open
		if open != "" {
			n = utf8.RuneCountInString(open) + utf8.RuneCountInString(untrusted.Close) + 2 + 2*indent
		}
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		n += indent
		for runes := []rune(line); len(runes) > 0; {
			take := min(len(runes), max(size-n, 1))
			if b.Len() > 0 && take < len(runes) && n+len(runes) > size {
				flush()
				n += indent
				continue
			}
			piece := string(runes[:take])
			runes = runes[take:]
			b.WriteString(piece)
			n += take
			bare := strings.TrimSuffix(piece, "\n")
			switch {
			case openLine(bare):
				open = strings.TrimPrefix(bare, "Error: ")
				n += utf8.RuneCountInString(untrusted.Close) + 1 + indent
			case bare == untrusted.Close:
				open = ""
			}
			if n >= size {
				flush()
			}
		}
	}
	if b.Len() > 0 || len(parts) == 0 {
		flush()
	}
	if cut && open != "" && !strings.HasSuffix(parts[len(parts)-1], untrusted.Close) {
		parts[len(parts)-1] += "\n" + untrusted.Close
	}
	for i := range parts {
		parts[i] = setIn(parts[i])
	}
	return parts
}
