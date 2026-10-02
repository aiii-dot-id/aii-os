package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

type recallStore interface {
	ListBeliefs() ([]store.Belief, error)
	ListIntentions() ([]store.Intention, error)
	SearchLedgerMirror(q string, beforeSeq uint64, limit int) ([]store.LedgerEventRow, int, error)
	ReasoningPage(terms []string, beforeSeq uint64, limit int) ([]store.Reasoning, error)
	ReasoningOfTurn(turnSeq uint64) (store.Reasoning, bool, error)
	StandingFor(id string) (string, error)
	StandingDetail(ctx context.Context, id string, cur *store.StandingCursor) (*store.StandingReport, error)
}

var recallGroups = []struct{ store, heading, source string }{
	{"beliefs", "Beliefs", "beliefs"},
	{"self_model_synthesis", "Self-Model Syntheses", "syntheses"},
	{"intentions", "Intentions", "intentions"},
	{"commitments", "Commitments", "commitments"},
	{"relationships", "Relationships", "relationships"},
	{"experiences", "Experiences", "experiences"},
	{"conversations", "Conversation", "conversation"},
	{"inbound", "Inbound", "inbound"},
}

func recallSourceName(storeName string) string {
	for _, g := range recallGroups {
		if g.store == storeName {
			return g.source
		}
	}
	return storeName
}

func (e *Engine) verbRecall(ctx context.Context, args map[string]interface{}) (string, error) {
	query, _ := args["query"].(string)
	if query == "" {
		query, _ = args["_positional"].(string)
	}
	query = strings.TrimSpace(query)

	var cursor uint64
	if v, ok := args["after_seq"].(float64); ok && v > 0 {
		cursor = uint64(v)
	} else if v, ok := args["after_seq"].(int); ok && v > 0 {
		cursor = uint64(v)
	}

	source, _ := args["source"].(string)
	edge, _ := args["after_edge"].(string)
	if edge != "" && source != "standing" {
		return "", fmt.Errorf("after_edge is only valid with source=standing")
	}
	if source == "heard" {
		if e.heardHistory == nil {
			return "", fmt.Errorf("heard history is unavailable on this host")
		}
		if exact, _ := args["exact"].(bool); exact {
			return "", fmt.Errorf("heard history matches all query words; use exact=false or omit exact")
		}

		if decay, _ := args["decay"].(string); decay != "" && decay != "none" {
			return "", fmt.Errorf("heard history is bounded context, not ranked memory; use decay=none or omit decay")
		}
		limit := 20
		if n, ok := args["limit"].(float64); ok && n > 0 && n <= 50 {
			limit = int(n)
		} else if n, ok := args["limit"].(int); ok && n > 0 && n <= 50 {
			limit = n
		}
		return e.heardHistory(query, cursor, limit)
	}
	if source == "reasoning" {
		exact, _ := args["exact"].(bool)
		if cursor > 0 && !exact && turnRef.MatchString(query) {
			return "", fmt.Errorf("query=%q reads one turn whole; after_seq pages word searches and listings — drop one of them", query)
		}
		return e.recallReasoning(query, exact, cursor)
	}
	switch source {
	case "", "experiences", "syntheses", "conversation", "ledger":
	case "standing":
		id, _ := args["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			return "", fmt.Errorf("source=standing requires id=<belief id> and query=\"\"")
		}

		cur := &store.StandingCursor{}
		if raw, present := args["after_seq"]; present {
			switch v := raw.(type) {
			case float64:
				if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v != math.Trunc(v) || v > 1<<53-1 {
					return "", fmt.Errorf("standing after_seq must be a nonnegative exact integer")
				}
				cur.Seq = uint64(v)
			case int:
				if v < 0 {
					return "", fmt.Errorf("standing after_seq must be nonnegative")
				}
				cur.Seq = uint64(v)
			case uint64:
				cur.Seq = v
			default:
				return "", fmt.Errorf("standing after_seq must be an integer")
			}
		}
		if raw, present := args["after_edge"]; present {
			var ok bool
			cur.EdgeID, ok = raw.(string)
			if !ok {
				return "", fmt.Errorf("standing after_edge must be a string")
			}
		}
		if (cur.Seq == 0) != (cur.EdgeID == "") {
			return "", fmt.Errorf("standing continuation requires both after_seq and after_edge; omit both for the first page")
		}
		return e.recallBeliefInspect(ctx, id, cur)
	case "alarms", "projects", "skills", "curiosity", "continuity":

		if cursor > 0 {
			return "", fmt.Errorf("after_seq pages the record's stores; %s is standing state and reads whole — drop after_seq", source)
		}
		return e.recallStanding(ctx, source, query)
	default:
		return "", fmt.Errorf("recall source %q is not a source — the record's stores are experiences, syntheses, conversation and ledger, and your reasoning is reasoning; the standing sources are alarms, projects, skills, curiosity and continuity; omit it to recall across the record", source)
	}
	if cursor > 0 && source == "" {
		return "", fmt.Errorf("after_seq=%d needs a source: sequence numbers are per-source, and this one means a different position in each. Pass source=experiences|syntheses|conversation|ledger|reasoning with the seq that source reported", cursor)
	}
	exact, _ := args["exact"].(bool)
	limit := memory.DefaultLimit
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	} else if v, ok := args["limit"].(int); ok && v > 0 {
		limit = v
	}
	if limit > memory.MaxLimit {
		limit = memory.MaxLimit
	}
	decay, _ := args["decay"].(string)

	if source == "" && query != "" {
		return e.recallRanked(ctx, query, exact, limit, decay)
	}
	return e.recallEnumerate(ctx, source, query, exact, cursor)
}

func (e *Engine) recallRanked(ctx context.Context, query string, exact bool, limit int, decay string) (string, error) {
	res, err := e.instruments.Recall(ctx, memory.Query{Text: query, Exact: exact, Limit: limit, Decay: decay, Reinforce: true})
	if err != nil {
		return "", err
	}

	var failedNames, failureDetails []string
	for _, s := range res.Sources {
		if s.Status == memory.StatusSourceUnavailable || s.Status == memory.StatusQueryFailed {
			failedNames = append(failedNames, recallSourceName(s.Store))
			failureDetails = append(failureDetails, recallSourceName(s.Store)+": "+s.Detail)
		}
	}
	if len(res.Sources) > 0 && len(failedNames) == len(res.Sources) {
		return "", fmt.Errorf("recall unavailable from %s: %s", strings.Join(failedNames, ", "), failureDetails[0])
	}
	unavailable := ""
	if len(failureDetails) > 0 {
		unavailable = "\nUnavailable sources: " + strings.Join(failureDetails, "; ")
	}

	items := map[string][]string{}
	for i, h := range res.Hits {
		standing := ""
		if h.Store == "beliefs" {
			standing = standingOrUnavailable(e.store, h.ID)
		}
		items[h.Store] = append(items[h.Store], recallRankedLine(h, i+1, standing))
	}
	var out []string
	mode := "every word, in any order, plus fuzzy matches"
	if exact {
		mode = "the exact phrase, and nothing looser"
	}
	out = append(out, fmt.Sprintf("Recall %q — %s; ranked by match, decayed by policy %s:", query, mode, res.Policy))
	any := false
	for _, g := range recallGroups {
		lines := items[g.store]
		if len(lines) == 0 {
			continue
		}
		any = true
		out = append(out, g.heading+":")
		out = append(out, lines...)
	}

	ringLines := e.ringStateMatches(strings.ToLower(query))
	if len(ringLines) > 0 {
		any = true
		out = append(out, "Ring State:")
		out = append(out, ringLines...)
	}

	var complete, partial []string
	for _, s := range res.Sources {
		switch s.Status {
		case memory.StatusFound, memory.StatusFoundNothing:
			complete = append(complete, recallSourceName(s.Store))
		case memory.StatusPartial:
			partial = append(partial, fmt.Sprintf("%s (%d matched, %d shown)", recallSourceName(s.Store), s.Matched, s.Shown))
		}
	}
	coverage := recallRankedCoverage(complete, partial)

	meaningLine := ""
	switch res.Meaning.Status {
	case memory.StatusFound:
		meaningLine = "\nMeaning layer: consulted (basis " + res.Meaning.Basis + "); a hit marked meaning or both carries its similarity."
	case memory.StatusFoundNothing:
		meaningLine = "\nMeaning layer: consulted (basis " + res.Meaning.Basis + "), nothing above the floor."
	case memory.StatusSourceUnavailable:
		meaningLine = "\nMeaning layer: unavailable — " + res.Meaning.Detail
	}
	warnings := ""
	if len(res.Warnings) > 0 {
		warnings = "\nWarnings: " + strings.Join(res.Warnings, "; ")
	}

	if !any {

		return fmt.Sprintf("No exact-word or fuzzy match for %q in the sources searched. Retry with one distinctive word (fuzzy tolerates a typo; exact=true forces the verbatim phrase); use separate recall calls for separate concepts. Nothing fabricated.", query) +
			coverage + meaningLine + unavailable + warnings, nil
	}
	if unavailable != "" {
		out = append(out, strings.TrimPrefix(unavailable, "\n"))
	}
	out = append(out, "Strength reflects each memory's age, its durability class and how often it was consciously recalled; this recall reinforced what it returned. Sources with no matches are omitted."+
		coverage+meaningLine+warnings)
	return strings.Join(out, "\n"), nil
}

func conversationRecallText(h memory.Hit, text string) string {
	if h.Attribution == "tool" || h.Attribution == "operator_act" {
		return untrusted.Wrap("operator-authorized tool/report", text)
	}
	return text
}

func recallRankedLine(h memory.Hit, rank int, standing string) string {
	tail := fmt.Sprintf("rank %d, %s, strength %.2f", rank, h.Match, h.Strength)
	if h.Similarity > 0 {
		tail += fmt.Sprintf(", similarity %.2f", h.Similarity)
	}
	if h.Graph > 1 {
		tail += fmt.Sprintf(", graph ×%.2f", h.Graph)
	}
	switch h.Store {
	case "experiences":
		line := fmt.Sprintf("  [seq %d, %s, %s | %s]", h.Seq, h.ID, h.Attribution, tail)
		if len(h.LaterContradictions) > 0 {
			line += " later contradicted by [" + strings.Join(h.LaterContradictions, "] [") + "]"
			if h.ContradictionsMore {
				line += fmt.Sprintf(" (more; recall source=ledger query=%q)", h.ID)
			}
		}
		return line + " " + h.Snippet
	case "beliefs":
		return fmt.Sprintf("  [%s, %s, ring %d | %s] %s", h.ID, standing, h.Ring, tail, h.Snippet)
	case "self_model_synthesis":
		return fmt.Sprintf("  [seq %d, %s | %s] %s", h.Seq, h.ID, tail, h.Snippet)
	case "conversations":
		return fmt.Sprintf("  [seq %d, turn %d, %s, %s | %s] %s", h.Seq, h.Seq, h.Attribution, h.Time.Format(time.RFC3339), tail, conversationRecallText(h, h.Snippet))
	default:
		return fmt.Sprintf("  [%s, %s | %s] %s", h.ID, h.Attribution, tail, h.Snippet)
	}
}

func recallRankedCoverage(complete, partial []string) string {
	var parts []string
	if len(complete) > 0 {
		parts = append(parts, "searched to completion: "+strings.Join(complete, ", "))
	}
	if len(partial) > 0 {
		parts = append(parts, "PAGED and possibly incomplete: "+strings.Join(partial, ", ")+
			" — continue with source=<one of those names> to enumerate it newest-first (every word must appear), then after_seq=<the lowest seq that page showed>")
	}
	if len(parts) == 0 {
		return ""
	}
	return "\nCoverage — " + strings.Join(parts, "; ") + "."
}

func (e *Engine) ringStateMatches(q string) []string {
	var lines []string
	matchRing := func(label, content string) {
		if content == "" {
			return
		}
		hay := strings.ToLower(label + " " + content)
		if q != "" && !strings.Contains(hay, q) {
			return
		}
		lines = append(lines, fmt.Sprintf("  [%s] %s", label, recallExcerpt(content, 240)))
	}
	if rc := e.rings.GetContent(ring.Ring2); rc != "" {
		matchRing("ring2/self-model", rc)
	}
	for _, sec := range e.rings.Sections(ring.Ring3) {
		matchRing("ring3/"+sec.Name, sec.Content)
	}
	if rc := e.rings.GetContent(ring.Ring3); rc != "" {
		matchRing("ring3", rc)
	}
	for _, sec := range e.rings.Sections(ring.Ring4) {
		matchRing("ring4/"+sec.Name, sec.Content)
	}
	if brief := e.rings.GetBrief(); brief != "" {
		matchRing("brief", brief)
	}
	return lines
}

func (e *Engine) recallEnumerate(ctx context.Context, source, query string, exact bool, cursor uint64) (string, error) {
	wants := func(name string) bool { return source == "" || source == name }
	pageFloor := cursor
	if pageFloor == 0 {
		pageFloor = 1 << 62
	}

	type group struct {
		name  string
		items []string
	}
	var groups []group
	recorded := 0
	available := 0
	var failedSources, failureDetails []string

	var pagedSources, exhausted []string
	var firstFailure error
	fail := func(source string, err error) {
		failedSources = append(failedSources, source)
		failureDetails = append(failureDetails, fmt.Sprintf("%s: %v", source, err))
		if firstFailure == nil {
			firstFailure = err
		}
	}
	page := func(name, storeName string, size int) ([]memory.Hit, bool) {
		hits, total, err := e.instruments.Enumerate(ctx, storeName, query, exact, cursor, size)
		if err != nil {
			fail(name, err)
			return nil, false
		}
		available++
		recorded += total
		if len(hits) == size {
			pagedSources = append(pagedSources, name)
		} else {
			exhausted = append(exhausted, name)
		}
		return hits, true
	}

	if source == "" {
		beliefs, err := e.store.ListBeliefs()
		if err != nil {
			fail("beliefs", err)
		} else {
			available++
			g := group{name: "Beliefs"}
			for _, b := range beliefs {
				recorded++

				g.items = append(g.items, fmt.Sprintf("  [%s, %s, ring %d, evidence %d] %s",
					b.ID, standingOrUnavailable(e.store, b.ID), b.Ring, b.EvidenceCount, b.Statement))
			}
			groups = append(groups, g)
		}
	}

	if wants("syntheses") {
		if hits, ok := page("syntheses", "self_model_synthesis", recallSelfModelPage); ok {
			g := group{name: "Self-Model Syntheses"}
			for _, h := range hits {
				g.items = append(g.items, fmt.Sprintf("  [seq %d, %s] %s", h.Seq, h.ID, h.Text))
			}
			groups = append(groups, g)
		}
	}

	if source == "" {
		intentions, err := e.store.ListIntentions()
		if err != nil {
			fail("intentions", err)
		} else {
			available++
			g := group{name: "Intentions"}
			for _, i := range intentions {
				recorded++
				g.items = append(g.items, fmt.Sprintf("  [%s, %s] %s", i.ID, i.State, i.Statement))
			}
			groups = append(groups, g)
		}
	}

	if wants("experiences") {
		if hits, ok := page("experiences", "experiences", recallExperiencePage); ok {
			g := group{name: "Experiences"}
			for _, h := range hits {

				g.items = append(g.items, fmt.Sprintf("  [seq %d, %s, %s] %s", h.Seq, h.ID, h.Attribution, h.Text))
			}
			groups = append(groups, g)
		}
	}

	if wants("conversation") {
		if hits, ok := page("conversation", "conversations", recallConversationPage); ok {
			g := group{name: "Conversation"}
			for _, h := range hits {
				g.items = append(g.items, fmt.Sprintf("  [seq %d, turn %d, %s, %s] %s", h.Seq, h.Seq, h.Attribution, h.Time.Format(time.RFC3339Nano), conversationRecallText(h, recallExcerpt(h.Text, 200))))
			}
			groups = append(groups, g)
		}
	}

	if wants("ledger") {
		events, total, err := e.store.SearchLedgerMirror(strings.ToLower(query), pageFloor, recallLedgerPage)
		if err != nil {
			fail("ledger events", err)
		} else {
			available++
			g := group{name: "Ledger Events"}
			recorded += total
			if len(events) == recallLedgerPage {
				pagedSources = append(pagedSources, "ledger")
			} else {
				exhausted = append(exhausted, "ledger")
			}
			for _, ev := range events {
				ringLabel := fmt.Sprintf("ring %d", ev.Ring)
				if ev.Ring < 0 {
					ringLabel = "meta"
				}
				g.items = append(g.items, fmt.Sprintf("  [seq %d, %s, %s, %s] %s", ev.Seq, ev.Type, ringLabel, ev.Timestamp, ev.Payload))
				g.items = append(g.items, ledgerRecallCitation(ev))
			}
			if len(events) > 0 {
				g.items = append(g.items, "  Citations identify records, not independent support for their claims.")
			}
			groups = append(groups, g)
		}
	}

	if source == "" {
		if lines := e.ringStateMatches(""); len(lines) > 0 {
			groups = append(groups, group{name: "Ring State", items: lines})
		}
	}

	if available == 0 && recorded == 0 && firstFailure != nil {
		return "", fmt.Errorf("recall unavailable from %s: %w", strings.Join(failedSources, ", "), firstFailure)
	}
	unavailable := ""
	if len(failureDetails) > 0 {
		unavailable = "\nUnavailable sources: " + strings.Join(failureDetails, "; ")
	}
	if recorded == 0 {
		return "Nothing to recall yet in available sources." + unavailable, nil
	}

	var out []string
	where := "all sources"
	if source != "" {
		where = "source " + source
	}
	switch {
	case query != "" && exact:
		out = append(out, fmt.Sprintf("Recall %q — the exact phrase, %s, newest first:", query, where))
	case query != "":
		out = append(out, fmt.Sprintf("Recall %q — every word must appear, %s, newest first:", query, where))
	default:
		out = append(out, fmt.Sprintf("Recall — %s, newest first (no query):", where))
	}
	any := false
	for _, g := range groups {
		if len(g.items) == 0 {
			continue
		}
		any = true
		out = append(out, g.name+":")
		out = append(out, g.items...)
	}
	if !any {
		return fmt.Sprintf("No word match for %q in %s. Retry with one distinctive word; omit source to recall across every store with fuzzy matching. Nothing fabricated.", query, where) +
			recallCoverage(exhausted, pagedSources) + unavailable, nil
	}
	if unavailable != "" {
		out = append(out, strings.TrimPrefix(unavailable, "\n"))
	}
	out = append(out, "Sources with no matches are omitted. Ring state (ring2/ring3/ring4/brief) is searchable by name or content."+
		recallCoverage(exhausted, pagedSources))
	return strings.Join(out, "\n"), nil
}

func ledgerRecallCitation(row store.LedgerEventRow) string {
	err := row.CitationError
	var raw []byte
	if err == nil {
		raw, err = json.Marshal(struct {
			Cites []ledger.Citation `json:"cites"`
		}{Cites: []ledger.Citation{row.Citation}})
	}
	if err == nil {
		_, err = ledger.ParseCitations(raw)
	}
	if err != nil {
		return fmt.Sprintf("    citation unavailable: %v — verify the original ledger and rebuild its projection", err)
	}
	return "    cite with " + string(raw)
}

func recallCoverage(exhausted, paged []string) string {
	var parts []string
	if len(exhausted) > 0 {
		parts = append(parts, "searched to completion: "+strings.Join(exhausted, ", "))
	}
	if len(paged) > 0 {
		parts = append(parts, "PAGED and possibly incomplete: "+strings.Join(paged, ", ")+
			" — continue with source=<one of those names> and after_seq=<the lowest seq that source showed>")
	}
	if len(parts) == 0 {
		return ""
	}
	return "\nCoverage — " + strings.Join(parts, "; ") + "."
}

func recallExcerpt(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-3]) + "..."
}

const (
	recallSelfModelPage    = 10
	recallExperiencePage   = 20
	recallConversationPage = 10
	recallLedgerPage       = 10
)

const reasoningHeading = "Reasoning — your own raw deliberation behind your replies; thinking, not something you said or did"

var turnRef = regexp.MustCompile(`(?i)^turn\s*[:#]?\s*(\d+)(?:\s+part\s*[:#]?\s*(\d+))?$`)

const (
	recallReasoningPage  = 5
	recallReasoningPart  = 12000
	recallReasoningTaste = 300
)

func (e *Engine) recallReasoning(query string, exact bool, cursor uint64) (string, error) {
	if m := turnRef.FindStringSubmatch(query); m != nil && !exact {
		return e.recallReasoningOfTurn(m[1], m[2])
	}
	var terms []string
	switch {
	case query == "":
	case exact:
		terms = []string{query}
	default:
		terms = strings.Fields(query)
	}
	rows, err := e.store.ReasoningPage(terms, cursor, recallReasoningPage)
	if err != nil {
		return "", fmt.Errorf("recall reasoning: %w", err)
	}
	if len(rows) == 0 {
		where := ""
		if cursor > 0 {
			where = fmt.Sprintf(" before turn %d", cursor)
		}
		if query == "" {
			return fmt.Sprintf("%s:\nNone recorded%s. A reply keeps its reasoning only when your model returns it raw (reasoning_content).", reasoningHeading, where), nil
		}
		how := "every word must appear: try fewer words"
		if exact {
			how = "the exact phrase must appear: try a shorter phrase, or exact=false for the words in any order"
		}
		return fmt.Sprintf("%s:\nNo reply's reasoning%s holds %q. Reasoning is searched only when named, and %s, or read one reply's whole with query=\"turn:N\" (a conversation hit names its turn).",
			reasoningHeading, where, query, how), nil
	}
	var b strings.Builder
	b.WriteString(reasoningHeading + ", newest first:")
	for _, r := range rows {
		excerpt, route := reasoningHit(r, terms)
		fmt.Fprintf(&b, "\n  [turn %d, %s%s] %s — %s", r.TurnSeq, r.CreatedAt, modelsSuffix(r.Models), excerpt, route)
	}
	if len(rows) == recallReasoningPage {
		fmt.Fprintf(&b, "\nOlder: after_seq=%d.", rows[len(rows)-1].TurnSeq)
	}
	return b.String(), nil
}

func (e *Engine) recallReasoningOfTurn(n, partArg string) (string, error) {
	seq, err := strconv.ParseUint(n, 10, 64)
	if err != nil || seq > math.MaxInt64 {
		return fmt.Sprintf("%s:\nTurn %s is not a recorded turn.", reasoningHeading, n), nil
	}
	r, ok, err := e.store.ReasoningOfTurn(seq)
	if err != nil {
		return "", fmt.Errorf("reasoning of turn %d: %w", seq, err)
	}
	if !ok {
		return fmt.Sprintf("%s:\nTurn %d has no reasoning recorded. A reply keeps its reasoning only when your model returns it raw (reasoning_content).", reasoningHeading, seq), nil
	}
	runes := []rune(r.Content)
	parts := (len(runes) + recallReasoningPart - 1) / recallReasoningPart
	part := 1
	if partArg != "" {
		if p, err := strconv.Atoi(partArg); err == nil && p >= 1 && p <= parts {
			part = p
		} else {
			return fmt.Sprintf("%s:\nTurn %d's reasoning has %d part(s); part %s is not one of them.", reasoningHeading, seq, parts, partArg), nil
		}
	}
	lo, hi := (part-1)*recallReasoningPart, part*recallReasoningPart
	if hi > len(runes) {
		hi = len(runes)
	}
	head := fmt.Sprintf("%s — turn %d, %s%s", reasoningHeading, seq, r.CreatedAt, modelsSuffix(r.Models))
	if parts > 1 {
		head += fmt.Sprintf(", part %d of %d", part, parts)
	}
	out := head + ":\n\n" + string(runes[lo:hi])

	out += fmt.Sprintf("\n\n[end of turn %d's reasoning, part %d of %d — your own deliberation, not something you said or did", seq, part, parts)
	if part < parts {
		out += fmt.Sprintf("; continues: query=\"turn:%d part:%d\"]", seq, part+1)
	} else {
		out += "]"
	}
	return out, nil
}

func reasoningHit(r store.Reasoning, terms []string) (string, string) {
	runes := []rune(r.Content)
	at := -1
	if len(terms) > 0 {
		at = runeIndex(foldRunes(runes), foldRunes([]rune(terms[0])))
	}
	parts := (len(runes) + recallReasoningPart - 1) / recallReasoningPart
	route := fmt.Sprintf("query=\"turn:%d\" reads it whole", r.TurnSeq)
	if at < 0 {
		return recallExcerpt(r.Content, recallReasoningTaste), route
	}
	if part := at/recallReasoningPart + 1; parts > 1 {
		route = fmt.Sprintf("the match is in query=\"turn:%d part:%d\" (of %d)", r.TurnSeq, part, parts)
	}
	lo := at - recallReasoningTaste/3
	if lo < 0 {
		lo = 0
	}
	hi := lo + recallReasoningTaste
	if hi > len(runes) {
		hi = len(runes)
	}
	excerpt := string(runes[lo:hi])
	if lo > 0 {
		excerpt = "..." + excerpt
	}
	if hi < len(runes) {
		excerpt += "..."
	}
	return excerpt, route
}

func foldRunes(r []rune) []rune {
	out := make([]rune, len(r))
	for i, c := range r {
		out[i] = unicode.ToLower(c)
	}
	return out
}

func runeIndex(s, sub []rune) int {
	if len(sub) == 0 {
		return -1
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := range sub {
			if s[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func modelsSuffix(models string) string {
	if models == "" {
		return ""
	}
	return ", " + models
}

func (e *Engine) recallBeliefInspect(ctx context.Context, id string, cur *store.StandingCursor) (string, error) {
	r, err := e.store.StandingDetail(ctx, id, cur)
	if err != nil {
		return fmt.Sprintf("Belief %s — eligibility unavailable: the evidence graph could not be read (%v). This is a read failure, not a standing — no promotion, no ring, and no eligibility is inferred from it.", id, err), nil
	}

	if r.Belief.ID == "" {
		return fmt.Sprintf("No belief has id %q.", id), nil
	}

	var out []string
	head := fmt.Sprintf("Belief %s [ring %d, %s, eligible %v]", r.Belief.ID, r.Belief.Ring, r.Standing, r.Eligible)
	if r.Retired {
		head += " — retired (archived or superseded; it no longer stands in working truth)"
	}
	out = append(out, head+" — "+r.Belief.Statement)
	out = append(out, fmt.Sprintf("  Read at ledger head %d, from one snapshot: the evidence and that head are true of the same projection point.", r.ReadHeadSeq))

	if r.QualifyingTotal > 0 {
		out = append(out, fmt.Sprintf("  Qualifying evidence: %d edge(s) from %d distinct source(s) — showing %d:", r.QualifyingTotal, r.DistinctSources, len(r.Qualifying)))
		for _, q := range r.Qualifying {
			out = append(out, fmt.Sprintf("    [%s, seq %d, %s, from %s] provenance %s", q.Edge.ID, q.Edge.CreatedSeq, q.SourceKind, q.Edge.FromID, q.Provenance))
		}
	} else {
		out = append(out, "  Qualifying evidence: none — no active incoming SUPPORTS/REINFORCED_BY/DERIVED_FROM edge with a source that resolves.")
	}

	if r.ExcludedTotal > 0 {
		out = append(out, fmt.Sprintf("  Excluded: %d edge(s), each certifying nothing — showing %d:", r.ExcludedTotal, len(r.Excluded)))
		for _, x := range r.Excluded {
			out = append(out, fmt.Sprintf("    [%s, from %s, seq %d] %s", x.Edge.ID, x.Edge.FromID, x.Edge.CreatedSeq, x.Reason))
		}
	}

	if r.Contradicted {
		out = append(out, "  Contradiction: a live CONTRADICTS edge points at this belief — standing is suspect until it resolves.")
	} else {
		out = append(out, "  Contradiction: none.")
	}

	switch {
	case len(r.AuthorshipClasses) > 0:
		out = append(out, fmt.Sprintf("  Authorship classes: %d (%s).", len(r.AuthorshipClasses), strings.Join(r.AuthorshipClasses, ", ")))
	case r.DistinctSources > 0:
		out = append(out, fmt.Sprintf("  Authorship classes: not evaluated — below 3 distinct sources (%d so far); the gate evaluates classes only once sources reach 3.", r.DistinctSources))
	default:
		out = append(out, "  Authorship classes: none to evaluate — no qualifying sources.")
	}

	switch {
	case r.Contradicted:
		out = append(out, "  Missing for Ring 2: the live contradiction must resolve first.")
	case r.DistinctSources < 3:
		out = append(out, fmt.Sprintf("  Missing for Ring 2: %d more distinct source(s) (has %d; the gate asks 3), then a second authorship class — an independent voice.", 3-r.DistinctSources, r.DistinctSources))
	case len(r.AuthorshipClasses) < 2:
		out = append(out, "  Missing for Ring 2: a second authorship class — an independent voice (operator or external) beside the resident's own substrate.")
	default:
		out = append(out, "  Ring 2 requirements met; promotion is the resident's conscious act, not this read's.")
	}

	if r.NextDetailSeq > 0 || r.NextDetailEdgeID != "" {
		out = append(out, fmt.Sprintf("  More evidence is withheld: continue with source=standing, id=%q, query=\"\", after_seq=%d, after_edge=%q. Each call reads a fresh snapshot; compare ledger heads if evidence changes.", id, r.NextDetailSeq, r.NextDetailEdgeID))
	}
	return strings.Join(out, "\n"), nil
}

func standingOrUnavailable(src interface {
	StandingFor(id string) (string, error)
}, id string) string {
	standing, err := src.StandingFor(id)
	if err != nil {
		return "unavailable"
	}
	return standing
}
