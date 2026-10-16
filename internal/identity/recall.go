package identity

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/memory/fuse"
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
	ReasoningTurns(turns []uint64) (map[uint64]bool, error)
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

func (e *Engine) verbRecall(ctx context.Context, args map[string]any) (string, error) {
	query, _ := args["query"].(string)
	if query == "" {
		query, _ = args["_positional"].(string)
	}
	query = strings.TrimSpace(query)

	cursor, err := recallCursor(args)
	if err != nil {
		return "", err
	}

	source, _ := args["source"].(string)
	limit, limitNote := recallLimit(args)
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

		return e.heardHistory(query, cursor, limit)
	}
	if source == "reasoning" {
		exact, _ := args["exact"].(bool)
		if cursor > 0 && !exact && turnRef.MatchString(query) {
			return "", fmt.Errorf("query=%q reads one turn whole; after_seq pages word searches and listings — drop one of them", query)
		}
		return e.recallReasoning(query, exact, cursor, limit, limitNote)
	}
	switch source {
	case "", "experiences", "syntheses", "conversation", "ledger":
	case "beliefs", "intentions", "commitments", "relationships", "inbound":

	case "standing":
		id, _ := args["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			return "", fmt.Errorf("source=standing requires id=<belief id> and query=\"\"")
		}

		cur := &store.StandingCursor{Seq: cursor}
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

		if cursor > 0 && source != "alarms" {
			return "", fmt.Errorf("after_seq pages the record's stores and the alarms' firings; %s is standing state and reads whole — drop after_seq", source)
		}
		return e.recallStanding(ctx, source, query, int(min(cursor, uint64(math.MaxInt32))))
	default:
		return "", fmt.Errorf("recall source %q is not a source — the record's stores are experiences, syntheses, conversation and ledger (listed newest-first) and beliefs, intentions, commitments, relationships and inbound (read ranked, one alone), and your reasoning is reasoning; the standing sources are alarms, projects, skills, curiosity and continuity; omit it to recall across the record", source)
	}
	if cursor > 0 && source == "" {
		return "", fmt.Errorf("after_seq=%d needs a source: sequence numbers are per-source, and this one means a different position in each. Pass source=experiences|syntheses|conversation|ledger|reasoning with the seq that source reported", cursor)
	}
	exact, _ := args["exact"].(bool)
	decay, _ := args["decay"].(string)

	if store, ok := rankedWithin[source]; ok {
		if cursor > 0 && (source != "beliefs" || query != "") {
			return "", fmt.Errorf("after_seq does not page source=%s: that source is read ranked, every match up to limit (at most %d), and has no sequence to continue from. Drop after_seq; raise limit or use more distinctive words to see further down the ranking (source=beliefs with query=\"\" lists every belief, and that listing pages with after_seq)", source, memory.MaxLimit)
		}
		if query == "" {
			if source == "beliefs" {

				return e.recallEnumerate(ctx, source, query, exact, cursor, 0, "")
			}
			return "", fmt.Errorf("source=%s needs a query: it shows that source's ranked matches for a cue, and without a cue there is nothing to rank (source=beliefs with query=\"\" lists every belief)", source)
		}

		if limit == 0 {
			limit = memory.MaxLimit
		}
		return e.recallRanked(ctx, query, exact, limit, decay, store, limitNote)
	}
	if source == "" && query != "" {
		if limit == 0 {
			limit = memory.DefaultLimit
		}
		return e.recallRanked(ctx, query, exact, limit, decay, "", limitNote)
	}
	if source == "" {

		limit, limitNote = 0, ""
	}
	return e.recallEnumerate(ctx, source, query, exact, cursor, limit, limitNote)
}

func recallLimit(args map[string]any) (limit int, note string) {
	var n float64
	switch v := args["limit"].(type) {
	case float64:
		n = v
	case int:
		n = float64(v)
	}
	switch {
	case n < 1:
		return 0, ""
	case n > memory.MaxLimit:
		return memory.MaxLimit, fmt.Sprintf("limit=%s is over the most one recall returns, and was read as %d.", strconv.FormatFloat(n, 'f', -1, 64), memory.MaxLimit)
	}
	return int(n), ""
}

const maxRecallCursor = 1<<53 - 1

var errRecallCursor = errors.New("after_seq is not a position")

func recallCursor(args map[string]any) (uint64, error) {
	raw, present := args["after_seq"]
	if !present || raw == nil {
		return 0, nil
	}
	n := math.NaN()
	switch v := raw.(type) {
	case float64:
		n = v
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	case uint64:
		if v <= maxRecallCursor {
			return v, nil
		}
	}
	if math.IsNaN(n) || n < 0 || n != math.Trunc(n) || n > maxRecallCursor {

		given, err := json.Marshal(raw)
		if err != nil {
			given = []byte(fmt.Sprint(raw))
		}
		kind := "a number"
		switch raw.(type) {
		case string:
			kind = "a string"
		case bool:
			kind = "a boolean"
		case []any:
			kind = "an array"
		case map[string]any:
			kind = "an object"
		}
		return 0, fmt.Errorf("%w: after_seq=%s (%s) — after_seq is a whole number from 0 (the first page) to %d: the after_seq an answer named, to read on from it", errRecallCursor, given, kind, maxRecallCursor)
	}
	return uint64(n), nil
}

func recallLimitWords() string {
	var pages []string
	for _, p := range []struct {
		source string
		page   int
	}{
		{"experiences", recallExperiencePage}, {"syntheses", recallSelfModelPage}, {"conversation", recallConversationPage},
		{"ledger", recallLedgerPage}, {"reasoning", recallReasoningPage},
	} {
		pages = append(pages, fmt.Sprintf("%s %d", p.source, p.page))
	}
	return fmt.Sprintf("How many to return, at most %[1]d: a larger limit is read as %[1]d, and the answer says so. "+
		"A recall across sources shares it among them (default %[2]d). "+
		"A named source returns up to limit of its own: a ranked source every match up to it (default %[1]d); "+
		"a listing that many a page (default: %[3]s), and the answer names the after_seq of the next page. "+
		"Unused by source=beliefs with query=\"\" (every belief, as many as fit, paged with after_seq), by the standing sources (alarms, projects, skills, curiosity, continuity), "+
		"and by the listing of every source (no source and no query: each source's own page). "+
		"An answer longer than a tool result may be leaves out its last rows, or cuts its one row short, and says so. "+
		"Standing inspection returns at most 10 evidence edges per page.",
		memory.MaxLimit, memory.DefaultLimit, strings.Join(pages, ", ")+", heard: the host's speech.heard_history.page_rows")
}

func RecallHostNote(args map[string]any) string {
	if args["source"] != "heard" {
		return ""
	}
	_, note := recallLimit(args)
	return note
}

func (e *Engine) recallBound() int {
	if n := e.toolResultChars; n > 0 && n < recallAnswerChars {
		return n
	}
	return recallAnswerChars
}

var rankedWithin = map[string]string{
	"beliefs": "beliefs", "intentions": "intentions", "commitments": "commitments",
	"relationships": "relationships", "inbound": "inbound",
}

func recallMark(h memory.Hit) string {
	if h.Match != memory.MatchBoth || len(h.Layers) < 2 {
		return h.Match
	}
	parts := make([]string, len(h.Layers))
	for i, l := range h.Layers {
		parts[i] = l
		if l == memory.MatchMeaning {
			parts[i] += fmt.Sprintf(" %.3f", h.Similarity)
		}
	}
	return h.Match + " (" + strings.Join(parts, " + ") + ")"
}

const RecallOrder = "Hits are ordered by score (fused rank × strength × graph, shown on each hit); the limit is shared across sources, not per source, and a cut answer says so."

const recallMarksLegend = "Marks — exact_words: every word of the cue is in the text. fuzzy: near-spellings of its words are. meaning: the text is near the cue in the model's sense, at or above the floor, whatever its words. both: more than one of these found it, named in brackets, meaning with its similarity; only a mark that names meaning says the meaning layer found it. A similarity close to the floor is a weak match."

func (e *Engine) recallRanked(ctx context.Context, query string, exact bool, limit int, decay string, within string, limitNote string) (string, error) {

	q := memory.Query{Text: query, Exact: exact, Limit: limit, Decay: decay}
	if within != "" {
		q.Stores = []string{within}
	}
	res, err := e.instruments.Recall(ctx, q)
	if err != nil {
		return "", err
	}
	warnings := res.Warnings
	if limitNote != "" {
		warnings = append(warnings[:len(warnings):len(warnings)], limitNote)
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

	lines := make([]string, len(res.Hits))
	thinking := e.thinkingKept(res.Hits)
	for i, h := range res.Hits {
		standing := ""
		if h.Store == "beliefs" {
			standing = standingOrUnavailable(e.store, h.ID)
		}
		lines[i] = recallRankedLine(h, i+1, standing)
		if h.Store == "conversations" && thinking[h.Seq] {
			lines[i] += thinkingRoute(h.Seq)
		}
	}
	mode := "every word, in any order, plus fuzzy matches"
	if exact {
		mode = "the exact phrase, and nothing looser"
	}

	layer := "meaning layer not consulted (the exact phrase was forced)"
	switch res.Meaning.Status {
	case memory.StatusFound, memory.StatusFoundNothing, memory.StatusPartial:
		layer = fmt.Sprintf("meaning layer consulted, floor %.3f", res.Meaning.Floor)
	case memory.StatusSourceUnavailable:
		layer = "meaning layer unavailable (see the last line)"
	}
	scope := ""
	if within != "" {
		scope = " in " + recallSourceName(within) + " only"
	}

	fusedOver := "words"
	if res.Meaning.Status == memory.StatusFound || res.Meaning.Status == memory.StatusFoundNothing || res.Meaning.Status == memory.StatusPartial {
		fusedOver = "words and meaning"
	}
	order := fmt.Sprintf("ordered by score: fused rank (RRF k=%g over each store's %s) × strength (decay policy %s) × graph", fuse.K, fusedOver, res.Policy)
	head := fmt.Sprintf("Recall %q%s — %s; %s; %s:", query, scope, mode, layer, order)

	var ringLines []string
	if within == "" {
		ringLines = e.ringStateMatches(strings.ToLower(query))
	}

	meaningLine := ""
	switch res.Meaning.Status {
	case memory.StatusFound:
		meaningLine = fmt.Sprintf("\nMeaning layer: consulted (basis %s, floor %.3f); a hit marked meaning or both carries its similarity, on this model's own scale.", res.Meaning.Basis, res.Meaning.Floor)
	case memory.StatusFoundNothing:

		meaningLine = fmt.Sprintf("\nMeaning layer: consulted (basis %s, floor %.3f); no memory scored at or above the floor. That is about the scores, not about what the record holds.", res.Meaning.Basis, res.Meaning.Floor)
	case memory.StatusPartial:

		meaningLine = fmt.Sprintf("\nMeaning layer: consulted in part (basis %s, floor %.3f) — %s. A hit marked meaning or both carries its similarity, on this model's own scale.", res.Meaning.Basis, res.Meaning.Floor, res.Meaning.Detail)
	case memory.StatusSourceUnavailable:
		meaningLine = "\nMeaning layer: unavailable — " + res.Meaning.Detail
	}
	warningsOf := func(warnings []string) string {
		if len(warnings) == 0 {
			return ""
		}
		return "\nWarnings: " + strings.Join(warnings, "; ")
	}

	if len(res.Hits) == 0 && len(ringLines) == 0 {

		return fmt.Sprintf("No exact-word or fuzzy match for %q in the sources searched. Retry with one distinctive word (fuzzy tolerates a typo; exact=true forces the verbatim phrase); use separate recall calls for separate concepts. Nothing fabricated.", query) +
			recallRankedCoverage(res.Sources) + meaningLine + unavailable + warningsOf(warnings), nil
	}

	render := func(shown, bound int, warnings []string) string {
		sources := sourcesShown(res.Sources, res.Hits[:shown])
		out := []string{head}
		if cut := recallCut(within, res.Limit, res.Truncated, shown, len(res.Hits), bound, sources); cut != "" {
			out = append(out, cut)
		}
		items := map[string][]string{}
		for i, h := range res.Hits[:shown] {
			items[h.Store] = append(items[h.Store], lines[i])
		}
		for _, g := range recallGroups {
			if len(items[g.store]) > 0 {
				out = append(out, g.heading+":")
				out = append(out, items[g.store]...)
			}
		}
		if len(ringLines) > 0 {
			out = append(out, "Ring State:")
			out = append(out, ringLines...)
		}
		if unavailable != "" {
			out = append(out, strings.TrimPrefix(unavailable, "\n"))
		}
		out = append(out, fmt.Sprintf("Fused is the sum of 1/(%g + its rank) over each pool that ranked a hit; strength reflects each memory's age, its durability class and how often it was consciously recalled; graph is what the record's edges among these hits lend it, 1 where none do. This recall reinforced what it returned. Sources with no matches are omitted.\n", fuse.K)+recallMarksLegend+
			recallRankedCoverage(sources)+meaningLine+warningsOf(warnings))
		return strings.Join(out, "\n")
	}

	bound := e.recallBound()
	shown := len(res.Hits)
	fit := func(warnings []string) string {
		answer := render(shown, bound, warnings)
		for shown > 1 && utf8.RuneCountInString(answer) > bound {
			shown--
			answer = render(shown, bound, warnings)
		}
		return answer
	}
	answer := fit(warnings)

	if err := e.instruments.Reinforce(time.Now(), res.Hits[:shown]); err != nil {
		answer = fit(append(warnings[:len(warnings):len(warnings)], "reinforcement not recorded: "+err.Error()))
	}
	return answer, nil
}

func sourcesShown(sources []memory.Source, hits []memory.Hit) []memory.Source {
	n := map[string]int{}
	for _, h := range hits {
		n[h.Store]++
	}
	out := append([]memory.Source(nil), sources...)
	for i := range out {
		s := &out[i]
		if s.Status != memory.StatusFound && s.Status != memory.StatusPartial {
			continue
		}
		if s.Shown = n[s.Store]; s.Shown < s.Matched {
			s.Status = memory.StatusPartial
		}
	}
	return out
}

func recallCut(within string, limit int, truncated bool, shown, kept, bound int, sources []memory.Source) string {
	var cuts []string
	if truncated && within == "" {
		cuts = append(cuts, fmt.Sprintf("the hits of every source were ranked together by score and the best %d kept in all, not %d per source", limit, limit))
	}
	if shown < kept {
		cuts = append(cuts, fmt.Sprintf("the %d lowest-scored of the %d were left out to keep this answer within %d characters (a narrower cue reaches them)", kept-shown, kept, bound))
	}
	if len(cuts) == 0 {
		return ""
	}
	var counts []string
	for _, s := range sources {
		if s.Matched > 0 {
			counts = append(counts, fmt.Sprintf("%s %d matched, %d shown", recallSourceName(s.Store), s.Matched, s.Shown))
		}
	}
	return "Cut — " + strings.Join(cuts, "; ") + ": " + strings.Join(counts, "; ") + "."
}

func unmetabolizedMark(h memory.Hit) string {
	if h.Unmetabolized {
		return ", kept out of DREAM and CONSOLIDATE"
	}
	return ""
}

func conversationRecallText(h memory.Hit, text string) string {
	if h.Attribution == "tool" || h.Attribution == "operator_act" {
		return untrusted.Wrap("operator-authorized tool/report", text)
	}
	return text
}

func recallRankedLine(h memory.Hit, rank int, standing string) string {
	tail := fmt.Sprintf("rank %d, %s", rank, recallMark(h))
	if h.Match == memory.MatchMeaning {
		tail += fmt.Sprintf(", similarity %.3f", h.Similarity)
	}
	tail += fmt.Sprintf(", score %.5f (fused %.5f × strength %.2f × graph %.2f)", h.Score, h.Fused, h.Strength, h.Graph)
	switch h.Store {
	case "experiences":
		line := fmt.Sprintf("  [seq %d, %s, %s%s | %s]", h.Seq, h.ID, h.Attribution, unmetabolizedMark(h), tail)
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

func recallRankedCoverage(sources []memory.Source) string {
	var complete, partial []string
	for _, s := range sources {
		switch s.Status {
		case memory.StatusFound, memory.StatusFoundNothing:
			complete = append(complete, fmt.Sprintf("%s (%d matched, %d shown)", recallSourceName(s.Store), s.Matched, s.Shown))
		case memory.StatusPartial:
			partial = append(partial, fmt.Sprintf("%s (%d matched, %d shown)", recallSourceName(s.Store), s.Matched, s.Shown))
		}
	}
	var parts []string
	if len(complete) > 0 {
		parts = append(parts, "searched to completion: "+strings.Join(complete, ", "))
	}
	if len(partial) > 0 {
		parts = append(parts, "PAGED and possibly incomplete: "+strings.Join(partial, ", ")+
			" — to read more of one of them: raise limit (at most "+fmt.Sprint(memory.MaxLimit)+"); or ask again with the same query and source=beliefs, intentions, commitments, relationships or inbound, which shows that source's ranked matches alone; or source=experiences, syntheses or conversation, which lists that source newest-first by its words (a match by meaning alone is not in that listing) and pages with the after_seq each page names")
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

func (e *Engine) recallEnumerate(ctx context.Context, source, query string, exact bool, cursor uint64, limit int, limitNote string) (string, error) {
	wants := func(name string) bool { return source == "" || source == name }
	size := func(page int) int {
		if limit > 0 {
			return limit
		}
		return page
	}
	pageFloor := cursor
	if pageFloor == 0 {
		pageFloor = 1 << 62
	}

	var parts []*recallPart
	recorded := 0
	available := 0
	var failedSources, failureDetails []string
	var firstFailure error
	fail := func(source string, err error) {
		failedSources = append(failedSources, source)
		failureDetails = append(failureDetails, fmt.Sprintf("%s: %v", source, err))
		if firstFailure == nil {
			firstFailure = err
		}
	}

	page := func(name, storeName string, n int) ([]memory.Hit, bool, bool) {
		hits, total, err := e.instruments.Enumerate(ctx, storeName, query, exact, cursor, n+1)
		if err != nil {
			fail(name, err)
			return nil, false, false
		}
		available++
		recorded += total
		return hits[:min(n, len(hits))], len(hits) > n, true
	}

	pageOf := map[string]int{}

	if wants("beliefs") {
		beliefs, err := e.store.ListBeliefs()
		if err != nil {
			fail("beliefs", err)
		} else {
			available++
			recorded += len(beliefs)

			slices.SortStableFunc(beliefs, func(a, b store.Belief) int { return cmp.Compare(b.FirstSeq, a.FirstSeq) })
			p := &recallPart{heading: "Beliefs", source: "beliefs", covered: true}
			for _, b := range beliefs {
				if cursor > 0 && uint64(b.FirstSeq) >= cursor {
					continue
				}

				p.items = append(p.items, fmt.Sprintf("  [%s, seq %d, %s, ring %d, evidence %d] %s",
					b.ID, b.FirstSeq, standingOrUnavailable(e.store, b.ID), b.Ring, b.EvidenceCount, b.Statement))
				p.seqs = append(p.seqs, uint64(b.FirstSeq))
			}
			parts = append(parts, p)
		}
	}

	if wants("syntheses") {
		pageOf["syntheses"] = size(recallSelfModelPage)
		if hits, more, ok := page("syntheses", "self_model_synthesis", pageOf["syntheses"]); ok {
			p := &recallPart{heading: "Self-Model Syntheses", source: "syntheses", covered: true, more: more}
			for _, h := range hits {
				p.items = append(p.items, fmt.Sprintf("  [seq %d, %s] %s", h.Seq, h.ID, h.Text))
				p.seqs = append(p.seqs, h.Seq)
			}
			parts = append(parts, p)
		}
	}

	if source == "" {
		intentions, err := e.store.ListIntentions()
		if err != nil {
			fail("intentions", err)
		} else {
			available++
			p := &recallPart{heading: "Intentions", source: "intentions", route: "source=intentions with a query reads them ranked"}
			for _, i := range intentions {
				recorded++
				p.items = append(p.items, fmt.Sprintf("  [%s, %s] %s", i.ID, i.State, i.Statement))
			}
			parts = append(parts, p)
		}
	}

	if wants("experiences") {
		pageOf["experiences"] = size(recallExperiencePage)
		if hits, more, ok := page("experiences", "experiences", pageOf["experiences"]); ok {
			p := &recallPart{heading: "Experiences", source: "experiences", covered: true, more: more}
			for _, h := range hits {

				p.items = append(p.items, fmt.Sprintf("  [seq %d, %s, %s%s] %s", h.Seq, h.ID, h.Attribution, unmetabolizedMark(h), h.Text))
				p.seqs = append(p.seqs, h.Seq)
			}
			parts = append(parts, p)
		}
	}

	if wants("conversation") {
		pageOf["conversation"] = size(recallConversationPage)
		if hits, more, ok := page("conversation", "conversations", pageOf["conversation"]); ok {
			p := &recallPart{heading: "Conversation", source: "conversation", covered: true, more: more}
			thinking := e.thinkingKept(hits)
			for _, h := range hits {
				line := fmt.Sprintf("  [seq %d, turn %d, %s, %s] %s", h.Seq, h.Seq, h.Attribution, h.Time.Format(time.RFC3339Nano), conversationRecallText(h, recallExcerpt(h.Text, 200)))
				if thinking[h.Seq] {
					line += thinkingRoute(h.Seq)
				}
				p.items = append(p.items, line)
				p.seqs = append(p.seqs, h.Seq)
			}
			parts = append(parts, p)
		}
	}

	if wants("ledger") {
		pageOf["ledger"] = size(recallLedgerPage)
		n := pageOf["ledger"]
		events, total, err := e.store.SearchLedgerMirror(strings.ToLower(query), pageFloor, n+1)
		if err != nil {
			fail("ledger events", err)
		} else {
			available++
			recorded += total
			p := &recallPart{heading: "Ledger Events", source: "ledger", covered: true, more: len(events) > n,
				trailer: "  Citations identify records, not independent support for their claims."}
			for _, ev := range events[:min(n, len(events))] {
				ringLabel := fmt.Sprintf("ring %d", ev.Ring)
				if ev.Ring < 0 {
					ringLabel = "meta"
				}
				p.items = append(p.items, fmt.Sprintf("  [seq %d, %s, %s, %s] %s\n%s", ev.Seq, ev.Type, ringLabel, ev.Timestamp, ev.Payload, ledgerRecallCitation(ev)))
				p.seqs = append(p.seqs, ev.Seq)
			}
			parts = append(parts, p)
		}
	}

	if source == "" {
		if lines := e.ringStateMatches(""); len(lines) > 0 {
			parts = append(parts, &recallPart{heading: "Ring State", route: "a query naming ring2, ring3, ring4 or brief reaches it", items: lines})
		}
	}

	if available == 0 && recorded == 0 && firstFailure != nil {
		return "", fmt.Errorf("recall unavailable from %s: %w", strings.Join(failedSources, ", "), firstFailure)
	}
	unavailable := ""
	if len(failureDetails) > 0 {
		unavailable = "\nUnavailable sources: " + strings.Join(failureDetails, "; ")
	}
	if limitNote != "" {
		limitNote = "\n" + limitNote
	}
	bound := e.recallBound()
	if recorded == 0 {

		if source != "" {
			return "Nothing to recall yet in source " + source + "." + recallCoverage(parts) + unavailable + limitNote, nil
		}
		return "Nothing to recall yet in available sources." + unavailable, nil
	}

	where := "all sources"
	newest := "newest first"
	if source != "" {
		where = "source " + source
		if n, paged := pageOf[source]; paged {
			newest = fmt.Sprintf("newest first, %d a page", n)
		}
	}

	layer := "meaning layer not consulted (a named source is listed by its words; omit source to recall by meaning too)"
	if exact {
		layer = "meaning layer not consulted (the exact phrase was forced, and a named source is listed by its words)"
	}
	var head string
	switch {
	case query != "" && exact:
		head = fmt.Sprintf("Recall %q — the exact phrase, %s, %s; %s:", query, where, newest, layer)
	case query != "":
		head = fmt.Sprintf("Recall %q — every word must appear, %s, %s; %s:", query, where, newest, layer)
	default:
		head = fmt.Sprintf("Recall — %s, %s (no query):", where, newest)
	}
	shown := false
	for _, p := range parts {
		shown = shown || len(p.items) > 0
	}
	if !shown {
		if cursor > 0 {

			matching := ""
			if query != "" {
				matching = fmt.Sprintf(" matching %q", query)
			}
			return fmt.Sprintf("No older rows%s in %s before after_seq=%d.", matching, where, cursor) +
				recallCoverage(parts) + unavailable + limitNote, nil
		}
		return fmt.Sprintf("No word match for %q in %s; %s. Retry with one distinctive word; omit source to recall across every store with fuzzy matching. Nothing fabricated.", query, where, layer) +
			recallCoverage(parts) + unavailable + limitNote, nil
	}
	return fitParts(parts, bound, func(fits bool) string {
		out := []string{head}
		for _, p := range parts {
			if len(p.items) == 0 {
				continue
			}
			out = append(out, p.heading+":")
			out = append(out, p.items...)
			if p.trailer != "" {
				out = append(out, p.trailer)
			}
		}
		if unavailable != "" {
			out = append(out, strings.TrimPrefix(unavailable, "\n"))
		}
		out = append(out, "Sources with no matches are omitted. Ring state (ring2/ring3/ring4/brief) is searchable by name or content."+
			recallCoverage(parts)+recallCutLine(parts, bound, fits)+limitNote)
		return strings.Join(out, "\n")
	}), nil
}

type recallPart struct {
	heading string
	source  string
	route   string
	trailer string
	items   []string
	seqs    []uint64
	more    bool
	covered bool
	left    int
	short   bool
}

func (p *recallPart) readAlone() string {
	switch {
	case len(p.seqs) == 0:
		return p.route
	case p.source == "reasoning":
		return fmt.Sprintf(`recall source=reasoning query="turn:%d" reads it whole`, p.seqs[0])
	case p.source == "beliefs":
		return fmt.Sprintf(`recall source=beliefs query="" after_seq=%d shows it first, with the whole of an answer's room`, p.seqs[0]+1)
	}
	return fmt.Sprintf(`recall source=%s query="" after_seq=%d limit=1 shows it alone, with the whole of an answer's room`, p.source, p.seqs[0]+1)
}

func (p *recallPart) label() string {
	if p.source == "" {
		return strings.ToLower(p.heading)
	}
	return p.source
}

const recallCutMark = "[cut short]"

func fitParts(parts []*recallPart, bound int, render func(fits bool) string) string {
	answer := render(true)
	for utf8.RuneCountInString(answer) > bound {
		var widest *recallPart
		width := 0
		for _, p := range parts {
			if len(p.items) < 2 {
				continue
			}
			if n := utf8.RuneCountInString(strings.Join(p.items, "\n")); n > width {
				widest, width = p, n
			}
		}
		if widest != nil {
			widest.items = widest.items[:len(widest.items)-1]
			if len(widest.seqs) > len(widest.items) {
				widest.seqs, widest.more = widest.seqs[:len(widest.items)], true
			}
			widest.left++
			answer = render(true)
			continue
		}
		var longest *recallPart
		for _, p := range parts {
			if len(p.items) == 1 && (longest == nil || utf8.RuneCountInString(p.items[0]) > utf8.RuneCountInString(longest.items[0])) {
				longest = p
			}
		}
		if longest == nil {
			break
		}
		cut, ok := cutShort(longest.items[0], utf8.RuneCountInString(answer)-bound)
		if !ok {
			break
		}
		longest.items[0], longest.short = cut, true
		answer = render(true)
	}
	if utf8.RuneCountInString(answer) > bound {
		return render(false)
	}
	return answer
}

func cutShort(item string, by int) (string, bool) {
	if strings.Contains(item, untrusted.Open) {
		return item, false
	}
	body := []rune(strings.TrimSuffix(item, " "+recallCutMark))
	head := 0
	if i := strings.Index(string(body), "]"); i >= 0 {
		head = utf8.RuneCountInString(string(body)[:i+1])
	}
	keep := max(head, utf8.RuneCountInString(item)-by-utf8.RuneCountInString(" "+recallCutMark))
	out := strings.TrimRight(string(body[:min(keep, len(body))]), " ") + " " + recallCutMark
	if utf8.RuneCountInString(out) >= utf8.RuneCountInString(item) {
		return item, false
	}
	return out, true
}

func recallCutLine(parts []*recallPart, bound int, fits bool) string {
	var left, short []string
	for _, p := range parts {
		if p.left > 0 {
			if p.route != "" {
				left = append(left, fmt.Sprintf("%s (%d; %s)", p.label(), p.left, p.route))
			} else {
				left = append(left, fmt.Sprintf("%s (%d)", p.label(), p.left))
			}
		}
		if p.short {
			short = append(short, p.label()+" ("+p.readAlone()+")")
		}
	}
	var did []string
	if len(left) > 0 {
		did = append(did, "left out the last rows of "+strings.Join(left, ", "))
	}
	if len(short) > 0 {
		did = append(did, "cut short the one row shown of "+strings.Join(short, ", ")+", marked "+recallCutMark)
	}
	switch {
	case len(did) == 0 && fits:
		return ""
	case len(did) == 0:
		return fmt.Sprintf("\nThis answer is still over its bound of %d characters, so its start may be lost.", bound)
	}
	head := fmt.Sprintf("Cut to keep this answer within %d characters", bound)
	if !fits {
		head = fmt.Sprintf("Cut, and still over its bound of %d characters, so the start of this answer may be lost", bound)
	}
	return "\n" + head + ": " + strings.Join(did, "; ") + ". A page's after_seq reads on from its last row shown, cut short or not."
}

func recallCoverage(parts []*recallPart) string {
	var exhausted, paged []string
	for _, p := range parts {
		switch {
		case !p.covered:
		case p.more && len(p.seqs) > 0:
			paged = append(paged, fmt.Sprintf("%s (its next page: after_seq=%d)", p.source, p.seqs[len(p.seqs)-1]))
		default:
			exhausted = append(exhausted, p.source)
		}
	}
	var out []string
	if len(exhausted) > 0 {
		out = append(out, "searched to completion: "+strings.Join(exhausted, ", "))
	}
	if len(paged) > 0 {
		out = append(out, "PAGED, more remain: "+strings.Join(paged, ", ")+
			" — continue with source=<that name> and the after_seq it names")
	}
	if len(out) == 0 {
		return ""
	}
	return "\nCoverage — " + strings.Join(out, "; ") + "."
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

func recallExcerpt(s string, max int) string {
	return untrusted.Cut(s, max, "...")
}

const (
	recallSelfModelPage    = 10
	recallExperiencePage   = 20
	recallConversationPage = 10
	recallLedgerPage       = 10

	recallAnswerChars = 30000
)

const reasoningHeading = "Reasoning — your own deliberation behind your replies; thinking, not something you said or did"

const reasoningLegend = `Marked "` + store.AccountMark + `": your provider returned no raw tokens for that call, and this is its own version of the thinking, kept as it came. It may leave things out.`

const reasoningKept = "A reply's reasoning is kept when your model returns its own tokens (reasoning_content), or when your provider returns its own version of the thinking, such as a summary."

func (e *Engine) thinkingKept(hits []memory.Hit) map[uint64]bool {
	var seqs []uint64
	for _, h := range hits {
		if h.Store == "conversations" && h.Seq != 0 {
			seqs = append(seqs, h.Seq)
		}
	}
	if len(seqs) == 0 {
		return nil
	}
	kept, err := e.store.ReasoningTurns(seqs)
	if err != nil {
		return nil
	}
	return kept
}

func thinkingRoute(seq uint64) string {
	return fmt.Sprintf(" [its thinking is kept: recall source=reasoning query=\"turn:%d\"]", seq)
}

var turnRef = regexp.MustCompile(`(?i)^turn\s*[:#]?\s*(\d+)(?:\s+part\s*[:#]?\s*(\d+))?$`)

const (
	recallReasoningPage  = 5
	recallReasoningPart  = 12000
	recallReasoningTaste = 300
)

func (e *Engine) recallReasoning(query string, exact bool, cursor uint64, limit int, limitNote string) (string, error) {
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
	n := limit
	if n == 0 {
		n = recallReasoningPage
	}

	rows, err := e.store.ReasoningPage(terms, cursor, n+1)
	if err != nil {
		return "", fmt.Errorf("recall reasoning: %w", err)
	}
	more := len(rows) > n
	rows = rows[:min(n, len(rows))]
	if limitNote != "" {
		limitNote = "\n" + limitNote
	}
	if len(rows) == 0 {
		if cursor > 0 {

			matching := ""
			if query != "" {
				matching = fmt.Sprintf(" holding %q", query)
			}
			return fmt.Sprintf("%s:\nNo older rows: no reply's reasoning%s before after_seq=%d.", reasoningHeading, matching, cursor) + limitNote, nil
		}
		if query == "" {
			return fmt.Sprintf("%s:\nNone recorded. %s", reasoningHeading, reasoningKept) + limitNote, nil
		}
		how := "every word must appear: try fewer words"
		if exact {
			how = "the exact phrase must appear: try a shorter phrase, or exact=false for the words in any order"
		}
		return fmt.Sprintf("%s:\nNo reply's reasoning holds %q. Reasoning is searched only when named, and %s, or read one reply's whole with query=\"turn:N\" (a conversation hit names its turn).",
			reasoningHeading, query, how) + limitNote, nil
	}

	p := &recallPart{source: "reasoning", more: more}
	accounts := make([]bool, len(rows))
	for i, r := range rows {
		excerpt, route := reasoningHit(r, terms)
		p.items = append(p.items, fmt.Sprintf("  [turn %d, %s%s] %s — %s", r.TurnSeq, r.CreatedAt, modelsSuffix(r.Models), excerpt, route))
		p.seqs = append(p.seqs, r.TurnSeq)
		accounts[i] = r.AccountMarked()
	}
	bound := e.recallBound()
	return fitParts([]*recallPart{p}, bound, func(fits bool) string {
		var b strings.Builder
		fmt.Fprintf(&b, "%s, newest first, %d a page:", reasoningHeading, n)
		for _, item := range p.items {
			b.WriteString("\n" + item)
		}
		if slices.Contains(accounts[:len(p.items)], true) {
			b.WriteString("\n" + reasoningLegend)
		}
		if p.more {
			fmt.Fprintf(&b, "\nOlder: after_seq=%d.", p.seqs[len(p.seqs)-1])
		}
		b.WriteString(recallCutLine([]*recallPart{p}, bound, fits))
		return b.String() + limitNote
	}), nil
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
		return fmt.Sprintf("%s:\nTurn %d has no reasoning recorded. %s", reasoningHeading, seq, reasoningKept), nil
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
	if r.AccountMarked() {
		head += ". " + reasoningLegend
	} else {
		head += ":"
	}
	out := head + "\n\n" + string(runes[lo:hi])

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

		if r.DistinctSources >= 3 && r.DistinctOrigins < r.DistinctSources {
			out = append(out, fmt.Sprintf("  Those %d sources rest on %d origin(s): a belief counts as what it rests on in turn, a dream's note as the experiences its dream read.", r.DistinctSources, r.DistinctOrigins))
		}

		if r.OriginsUnopened > 0 {
			out = append(out, fmt.Sprintf("  The walk of what those sources rest on opened as many sources as it opens and left %d unopened: the %d origin(s) are what it found, and none is assumed of the rest.", r.OriginsUnopened, r.DistinctOrigins))
		}
		for _, q := range r.Qualifying {
			line := fmt.Sprintf("    [%s, seq %d, %s, from %s] provenance %s", q.Edge.ID, q.Edge.CreatedSeq, q.SourceKind, q.Edge.FromID, q.Provenance)
			if q.Note != "" {
				line += " — " + q.Note
			}
			out = append(out, line)
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
	case r.Contradicted:
		out = append(out, "  Missing for Ring 2: the live contradiction must resolve first.")
	case r.DistinctSources < 3:
		out = append(out, fmt.Sprintf("  Missing for Ring 2: %d more distinct source(s) (has %d; the gate asks 3).", 3-r.DistinctSources, r.DistinctSources))
	case r.DistinctOrigins < 3:
		line := fmt.Sprintf("  Missing for Ring 2: %d more origin(s) (its %d sources rest on %d; the gate asks 3). A source derived from another adds none.", 3-r.DistinctOrigins, r.DistinctSources, r.DistinctOrigins)
		if r.OriginsUnopened > 0 {
			line += fmt.Sprintf(" The %d source(s) the walk did not open are not counted.", r.OriginsUnopened)
		}
		out = append(out, line)
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
