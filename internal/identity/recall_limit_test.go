package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/jsonschema"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// recall_limit_test.go — a named source returns the limit it is given.
//
// A resident on 0.1.17 asked recall for source=conversation with limit 2,
// and then 6, and was given ten each time, with a footer offering the next
// page. The schema said limit was how many hits to return, at most 50. The
// listings each read a fixed page and never looked at limit. These hold
// the schema's words and the behaviour to each other, through the call as
// a provider sends it: raw JSON, held to the advertised schema, then the
// verb dispatch the host's tool executor uses.

// advertisedRecallCall dispatches one recall as the host's tool executor
// does: the arguments arrive as JSON (numbers are float64), are held to the
// schema recall advertises, and go to ExecuteAction.
func advertisedRecallCall(t *testing.T, e *Engine, args map[string]any) string {
	t.Helper()
	var params any
	for _, v := range Verbs() {
		if v.Name == "recall" {
			params = v.Params
		}
	}
	if params == nil {
		t.Fatal("recall is not advertised")
	}
	var schemaRaw, wire map[string]any
	roundTrip := func(in any, out *map[string]any) {
		data, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatal(err)
		}
	}
	roundTrip(params, &schemaRaw)
	roundTrip(args, &wire)
	schema, err := jsonschema.Compile(schemaRaw)
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(wire); err != nil {
		t.Fatalf("the advertised schema refuses %v: %v", args, err)
	}
	out, err := e.ExecuteAction(context.Background(), "verb", "recall", wire)
	if err != nil {
		t.Fatalf("recall %v: %v", args, err)
	}
	return out
}

// pagedRows reads a listing's rows the way a resident must: off the page,
// newest first. Reasoning lists turns; every other paged source, seqs.
func pagedRows(t *testing.T, source, out string) []uint64 {
	t.Helper()
	re := regexp.MustCompile(`(?m)^  \[seq (\d+),`)
	if source == "reasoning" {
		re = regexp.MustCompile(`(?m)^  \[turn (\d+),`)
	}
	var seqs []uint64
	for _, m := range re.FindAllStringSubmatch(out, -1) {
		n, err := strconv.ParseUint(m[1], 10, 64)
		if err != nil {
			t.Fatalf("unreadable seq %q", m[1])
		}
		seqs = append(seqs, n)
	}
	return seqs
}

// nextPage is the after_seq the answer names for its next page, read off
// the answer's last lines; ok is false when it names none.
func nextPage(out string) (uint64, bool) {
	tail := out
	if i := strings.LastIndex(out, "\nCoverage — "); i >= 0 {
		tail = out[i:]
	} else if i := strings.LastIndex(out, "\nOlder: "); i >= 0 {
		tail = out[i:]
	} else {
		return 0, false
	}
	// The next page's own words; a cut row's route to read it alone
	// names an after_seq too, and is not a next page.
	m := regexp.MustCompile(`(?:its next page: |Older: )after_seq=(\d+)`).FindStringSubmatch(tail)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseUint(m[1], 10, 64)
	return n, err == nil
}

const limitCue = "lanternfish"

// seedPagedSources gives every paged source n rows that hold the cue, and
// returns each source's rows as the store holds them, newest first: the
// truth a walk of pages is held to.
func seedPagedSources(t *testing.T, e *Engine, st *store.Store, n int) map[string][]uint64 {
	t.Helper()
	ctx := context.Background()
	truth := map[string][]uint64{}
	for i := 0; i < n; i++ {
		seq, err := st.AddConversationTurnSeq("operator", fmt.Sprintf("the note is in %s item %d", limitCue, i))
		if err != nil {
			t.Fatal(err)
		}
		truth["conversation"] = append(truth["conversation"], seq)
		if _, err := e.ExecuteAction(ctx, "verb", "note", map[string]any{"content": fmt.Sprintf("read %s entry %d", limitCue, i)}); err != nil {
			t.Fatal(err)
		}
		// A reply's reasoning: the reply's own words leave the cue out, so
		// the conversation's truth is the operator's turns alone.
		truth["reasoning"] = append(truth["reasoning"], recordReply(t, st, fmt.Sprintf("reply %d", i),
			[]store.ReasoningCall{{Model: "test", Text: fmt.Sprintf("weighed %s thought %d", limitCue, i)}}))
	}
	db := st.DB()
	column := func(q string) []uint64 {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []uint64
		for rows.Next() {
			var s uint64
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return out
	}
	// A synthesis per note, dated by that note's own event.
	for i, seq := range column(`SELECT created_seq FROM experiences WHERE content LIKE '%lanternfish%' ORDER BY created_seq`) {
		if _, err := db.Exec(`INSERT INTO self_model_synthesis (id, synthesis_text, source_entity_refs, continuity_thread, created_seq, created_at)
			VALUES (?, ?, '[]', 'thread', ?, '2026-10-14T00:00:00Z')`, fmt.Sprintf("syn_limit_%02d", i), fmt.Sprintf("I keep %s portrait %d", limitCue, i), seq); err != nil {
			t.Fatal(err)
		}
	}
	truth["experiences"] = column(`SELECT created_seq FROM experiences WHERE content LIKE '%lanternfish%' ORDER BY created_seq DESC`)
	truth["syntheses"] = column(`SELECT created_seq FROM self_model_synthesis WHERE synthesis_text LIKE '%lanternfish%' ORDER BY created_seq DESC`)
	events, _, err := st.SearchLedgerMirror(limitCue, 1<<62, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		truth["ledger"] = append(truth["ledger"], ev.Seq)
	}
	slices.Reverse(truth["conversation"])
	slices.Reverse(truth["reasoning"])
	for source, seqs := range truth {
		if len(seqs) < n {
			t.Fatalf("source %s holds %d rows with the cue, want at least %d", source, len(seqs), n)
		}
	}
	return truth
}

// The paged sources, and the page each lists when no limit is given.
var pagedSourceDefaults = map[string]int{
	"conversation": recallConversationPage,
	"experiences":  recallExperiencePage,
	"syntheses":    recallSelfModelPage,
	"ledger":       recallLedgerPage,
	"reasoning":    recallReasoningPage,
}

// THE REPORTED CALL, AND EVERY PAGED SOURCE LIKE IT. A named source
// returns limit rows; following the after_seq each answer names walks the
// source to its end with nothing skipped and nothing repeated, whatever
// the page size; and the last page says the source was read to its end.
func TestANamedSourceReturnsTheLimitItIsGivenAndPagesWithoutSkipOrRepeat(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	truth := seedPagedSources(t, e, st, 60)

	for source := range pagedSourceDefaults {
		for _, limit := range []int{1, 2, 6, 10, 50} {
			var walked []uint64
			cursor := uint64(0)
			for call := 0; ; call++ {
				if call > len(truth[source])+1 {
					t.Fatalf("source=%s limit=%d: the walk does not end", source, limit)
				}
				args := map[string]any{"source": source, "query": limitCue, "limit": limit}
				if cursor > 0 {
					args["after_seq"] = cursor
				}
				out := advertisedRecallCall(t, e, args)
				rows := pagedRows(t, source, out)
				next, more := nextPage(out)
				if more {
					if len(rows) != limit {
						t.Fatalf("source=%s limit=%d: a page with more after it shows %d rows:\n%s", source, limit, len(rows), out)
					}
					if next != rows[len(rows)-1] {
						t.Fatalf("source=%s limit=%d: the next page is named as after_seq=%d, not the last row shown (%d):\n%s", source, limit, next, rows[len(rows)-1], out)
					}
				} else {
					if len(rows) > limit || len(rows) == 0 {
						t.Fatalf("source=%s limit=%d: the last page shows %d rows:\n%s", source, limit, len(rows), out)
					}
					if source != "reasoning" && !strings.Contains(out, "searched to completion: "+source) {
						t.Fatalf("source=%s limit=%d: the last page does not say the source was read to its end:\n%s", source, limit, out)
					}
				}
				walked = append(walked, rows...)
				if !more {
					break
				}
				cursor = next
			}
			if !slices.Equal(walked, truth[source]) {
				t.Fatalf("source=%s limit=%d: the pages walked %d rows %v; the source holds %d %v", source, limit, len(walked), walked, len(truth[source]), truth[source])
			}
		}
	}
}

// Over 50 a limit is read as 50, and the answer says so; without one, each
// listing shows its page, as the schema says.
func TestANamedSourceClampsALimitOverFiftyAndSaysSo(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	seedPagedSources(t, e, st, 60)
	for source, page := range pagedSourceDefaults {
		out := advertisedRecallCall(t, e, map[string]any{"source": source, "query": limitCue, "limit": 80})
		if got := len(pagedRows(t, source, out)); got != 50 {
			t.Fatalf("source=%s limit=80 shows %d rows, want 50:\n%s", source, got, out)
		}
		if !strings.Contains(out, "limit=80") || !strings.Contains(out, "read as 50") {
			t.Fatalf("source=%s: a limit over 50 is read as 50 without saying so:\n%s", source, out)
		}
		out = advertisedRecallCall(t, e, map[string]any{"source": source, "query": limitCue})
		if got := len(pagedRows(t, source, out)); got != page {
			t.Fatalf("source=%s with no limit shows %d rows, want its page of %d:\n%s", source, got, page, out)
		}
		if strings.Contains(out, "read as 50") {
			t.Fatalf("source=%s: a clamp is reported where none was made:\n%s", source, out)
		}
	}
}

// The heard history takes the same limit: given, it is passed; over 50 it
// is read as 50 and the answer says so; omitted, the history's own page.
func TestHeardHistoryTakesTheSameLimit(t *testing.T) {
	e, _, _, _, _ := setupEngine(t)
	var asked []int
	e.heardHistory = func(_ string, _ uint64, limit int) (string, error) {
		asked = append(asked, limit)
		return `{"rows":[]}`, nil
	}
	for _, limit := range []int{1, 2, 6, 10, 50} {
		if out := advertisedRecallCall(t, e, map[string]any{"source": "heard", "query": "", "limit": limit}); out != `{"rows":[]}` {
			t.Fatalf("limit=%d changed the history's answer: %q", limit, out)
		}
	}
	// Over 50: read as 50. The answer stays the history's own, valid JSON;
	// the sentence that says so is the host's (RecallHostNote), which the
	// loop sets outside the untrusted boundary the history is read in.
	args := map[string]any{"source": "heard", "query": "", "limit": 80}
	if out := advertisedRecallCall(t, e, args); out != `{"rows":[]}` {
		t.Fatalf("a heard limit over 50 changed the history's answer: %q", out)
	}
	if note := RecallHostNote(args); !strings.Contains(note, "limit=80") || !strings.Contains(note, "read as 50") {
		t.Fatalf("a heard limit over 50 is not said: %q", note)
	}
	if note := RecallHostNote(map[string]any{"source": "heard", "limit": 50}); note != "" {
		t.Fatalf("a note where nothing was clamped: %q", note)
	}
	advertisedRecallCall(t, e, map[string]any{"source": "heard", "query": ""})
	if want := []int{1, 2, 6, 10, 50, 50, 0}; !slices.Equal(asked, want) {
		t.Fatalf("the history was asked for %v, want %v (0: its own page)", asked, want)
	}
}

// Across sources nothing changes: a recall by query shares its limit among
// the sources, and a listing of every source (no source, no query) shows
// each source's own newest page whatever limit says.
func TestTheAcrossSourcesReadsAreUnchangedByTheNamedSourceLimit(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	seedPagedSources(t, e, st, 30)

	ranked := advertisedRecallCall(t, e, map[string]any{"query": limitCue, "limit": 2})
	if got := len(regexp.MustCompile(`(?m)^  \[.*\| rank \d+,`).FindAllString(ranked, -1)); got != 2 {
		t.Fatalf("a recall across sources with limit 2 shows %d hits:\n%s", got, ranked)
	}
	if !strings.Contains(ranked, "the best 2 kept in all, not 2 per source") {
		t.Fatalf("a recall across sources no longer says its limit is shared:\n%s", ranked)
	}

	all := advertisedRecallCall(t, e, map[string]any{"query": "", "limit": 2})
	for heading, page := range map[string]int{"Conversation:": recallConversationPage, "Experiences:": recallExperiencePage, "Self-Model Syntheses:": recallSelfModelPage} {
		if got := strings.Count(sectionOf(all, heading), "\n  [seq") + 1; got != page {
			t.Fatalf("the listing of every source shows %d under %s, want its page of %d:\n%s", got, heading, page, all)
		}
	}
}

// A page that would not fit the tool result's bound leaves out its oldest
// rows, says so, and names the after_seq that reads them next: nothing is
// skipped and the answer is never cut from its front.
func TestAPageOverItsBoundLeavesOutItsOldestRowsAndContinuesFromThem(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	ctx := context.Background()
	long := strings.Repeat("a long observation of the tide ", 40)
	for i := 0; i < 12; i++ {
		if _, err := e.ExecuteAction(ctx, "verb", "note", map[string]any{"content": fmt.Sprintf("%s %s %d", limitCue, long, i)}); err != nil {
			t.Fatal(err)
		}
	}
	e.toolResultChars = 6000
	out := advertisedRecallCall(t, e, map[string]any{"source": "experiences", "query": limitCue, "limit": 12})
	if n := len([]rune(out)); n > 6000 {
		t.Fatalf("the answer is %d characters, over its bound of 6000", n)
	}
	rows := pagedRows(t, "experiences", out)
	if len(rows) == 0 || len(rows) >= 12 {
		t.Fatalf("an answer over its bound shows %d of 12 rows:\n%s", len(rows), out)
	}
	if !strings.Contains(out, fmt.Sprintf("Cut to keep this answer within 6000 characters: left out the last rows of experiences (%d)", 12-len(rows))) {
		t.Fatalf("the rows left out are not said:\n%s", out)
	}
	next, more := nextPage(out)
	if !more || next != rows[len(rows)-1] {
		t.Fatalf("the next page must start after the last row shown (%d), named %d (%v):\n%s", rows[len(rows)-1], next, more, out)
	}

	// The reasoning listing keeps the same rule.
	for i := 0; i < 12; i++ {
		recordReply(t, st, fmt.Sprintf("reply %d", i), []store.ReasoningCall{{Model: "test", Text: limitCue + " " + long}})
	}
	e.toolResultChars = 2000
	out = advertisedRecallCall(t, e, map[string]any{"source": "reasoning", "query": limitCue, "limit": 12})
	if n := len([]rune(out)); n > 2000 {
		t.Fatalf("the reasoning answer is %d characters, over its bound of 2000", n)
	}
	rows = pagedRows(t, "reasoning", out)
	if len(rows) == 0 || len(rows) >= 12 || !strings.Contains(out, fmt.Sprintf("Cut to keep this answer within 2000 characters: left out the last rows of reasoning (%d)", 12-len(rows))) {
		t.Fatalf("a reasoning page over its bound shows %d of 12 replies, or does not say so:\n%s", len(rows), out)
	}
	if next, more := nextPage(out); !more || next != rows[len(rows)-1] {
		t.Fatalf("the older reasoning page must start after the last reply shown (%d), named %d (%v):\n%s", rows[len(rows)-1], next, more, out)
	}
}
