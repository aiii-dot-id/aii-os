package identity

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/ring"
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

	// .
	// .
	var cursor uint64
	if v, ok := args["after_seq"].(float64); ok && v > 0 {
		cursor = uint64(v)
	} else if v, ok := args["after_seq"].(int); ok && v > 0 {
		cursor = uint64(v)
	}
	// .
	// .
	source, _ := args["source"].(string)
	switch source {
	case "", "experiences", "syntheses", "conversation", "ledger":
	case "alarms", "projects", "skills", "curiosity":
		// .
		// .
		// .
		if cursor > 0 {
			return "", fmt.Errorf("after_seq pages the record's stores; %s is standing state and reads whole — drop after_seq", source)
		}
		return e.recallStanding(ctx, source, query)
	default:
		return "", fmt.Errorf("recall source %q is not a source — the record's stores are experiences, syntheses, conversation and ledger; the standing sources are alarms, projects, skills and curiosity; omit it to recall across the record", source)
	}
	if cursor > 0 && source == "" {
		return "", fmt.Errorf("after_seq=%d needs a source: sequence numbers are per-source, and this one means a different position in each. Pass source=experiences|syntheses|conversation|ledger with the seq that source reported", cursor)
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

// .
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

	// .
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

	// .
	// .
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
	// .
	// .
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
		// .
		// .
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

// .
// .
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
		return fmt.Sprintf("  [seq %d, %s, %s | %s] %s", h.Seq, h.ID, h.Attribution, tail, h.Snippet)
	case "beliefs":
		return fmt.Sprintf("  [%s, %s, ring %d | %s] %s", h.ID, standing, h.Ring, tail, h.Snippet)
	case "self_model_synthesis":
		return fmt.Sprintf("  [seq %d, %s | %s] %s", h.Seq, h.ID, tail, h.Snippet)
	case "conversations":
		return fmt.Sprintf("  [seq %d, turn %d, %s, %s | %s] %s", h.Seq, h.Seq, h.Attribution, h.Time.Format(time.RFC3339), tail, h.Snippet)
	default:
		return fmt.Sprintf("  [%s, %s | %s] %s", h.ID, h.Attribution, tail, h.Snippet)
	}
}

// .
// .
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

// .
// .
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

// .
// .
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
	// .
	// .
	// .
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
				// .
				// .
				// .
				// .
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
				// .
				// .
				g.items = append(g.items, fmt.Sprintf("  [seq %d, %s, %s] %s", h.Seq, h.ID, h.Attribution, h.Text))
			}
			groups = append(groups, g)
		}
	}

	// .
	// .
	if wants("conversation") {
		if hits, ok := page("conversation", "conversations", recallConversationPage); ok {
			g := group{name: "Conversation"}
			for _, h := range hits {
				g.items = append(g.items, fmt.Sprintf("  [seq %d, turn %d, %s, %s] %s", h.Seq, h.Seq, h.Attribution, h.Time.Format(time.RFC3339Nano), recallExcerpt(h.Text, 200)))
			}
			groups = append(groups, g)
		}
	}

	// .
	// .
	// .
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

// .
// .
// .
// .
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

// .
// .
const (
	recallSelfModelPage    = 10
	recallExperiencePage   = 20
	recallConversationPage = 10
	recallLedgerPage       = 10
)

// .
// .
func standingOrUnavailable(src interface {
	StandingFor(id string) (string, error)
}, id string) string {
	standing, err := src.StandingFor(id)
	if err != nil {
		return "unavailable"
	}
	return standing
}
