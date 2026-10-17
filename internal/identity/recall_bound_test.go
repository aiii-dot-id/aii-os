package identity

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// boundWalk follows the after_seq each answer names to the end of one
// source, at random limits, and holds each answer to its bound: within
// it, or saying it is still over it, and never claiming a fit it did not
// make.
func boundWalk(t *testing.T, e *Engine, r *rand.Rand, source string, base map[string]any) []uint64 {
	t.Helper()
	bound := e.recallBound()
	var walked []uint64
	cursor := uint64(0)
	for call := 0; ; call++ {
		if call > 500 {
			t.Fatalf("source=%s bound %d: the walk does not end", source, bound)
		}
		args := map[string]any{"source": source}
		for k, v := range base {
			args[k] = v
		}
		if n := r.IntN(12); n > 0 {
			args["limit"] = n * 5
		}
		if cursor > 0 {
			args["after_seq"] = cursor
		}
		out := advertisedRecallCall(t, e, args)
		n := utf8.RuneCountInString(out)
		claimsFit := strings.Contains(out, fmt.Sprintf("Cut to keep this answer within %d characters", bound))
		saysOver := strings.Contains(out, fmt.Sprintf("still over its bound of %d characters", bound))
		switch {
		case n > bound && !saysOver:
			t.Fatalf("source=%s bound %d: an answer of %d characters does not say it is over its bound:\n%s", source, bound, n, out)
		case n <= bound && saysOver:
			t.Fatalf("source=%s bound %d: an answer of %d characters says it is over its bound:\n%s", source, bound, n, out)
		case claimsFit && n > bound:
			t.Fatalf("source=%s bound %d: an answer of %d characters claims to be within it:\n%s", source, bound, n, out)
		}
		rows := pagedRows(t, source, out)
		walked = append(walked, rows...)
		next, more := nextPage(out)
		if !more {
			return walked
		}
		if len(rows) == 0 || next != rows[len(rows)-1] {
			t.Fatalf("source=%s bound %d: the next page is named after_seq=%d, not the last row shown:\n%s", source, bound, next, out)
		}
		cursor = next
	}
}

// S1. Rows of every length at bounds from smaller than one row to roomy:
// the walk still reads every row once; an answer over its bound, which is
// only one whose frame alone is, says so; and a row cut short is marked.
func TestEveryAnswerFitsItsBoundOrSaysWhatIsTrue(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	ctx := context.Background()
	r := rand.New(rand.NewPCG(7, 7))
	var experiences, reasoning []uint64
	for i := 0; i < 30; i++ {
		long := strings.Repeat("tide ", 20+r.IntN(400))
		if _, err := e.ExecuteAction(ctx, "verb", "note", map[string]any{"content": fmt.Sprintf("%s %d %s", limitCue, i, long)}); err != nil {
			t.Fatal(err)
		}
		reasoning = append(reasoning, recordReply(t, st, fmt.Sprintf("reply %d", i), []store.ReasoningCall{{Model: "test", Text: limitCue + " " + long}}))
	}
	slices.Reverse(reasoning)
	rows, err := st.DB().Query(`SELECT created_seq FROM experiences WHERE content LIKE '%lanternfish%' ORDER BY created_seq DESC`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s uint64
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		experiences = append(experiences, s)
	}
	rows.Close()
	marked := false
	for _, bound := range []int{300, 700, 1500, 4000, 12000} {
		e.toolResultChars = bound
		for seed := uint64(1); seed <= 3; seed++ {
			r := rand.New(rand.NewPCG(seed, uint64(bound)))
			if got := boundWalk(t, e, r, "experiences", map[string]any{"query": limitCue}); !slices.Equal(got, experiences) {
				t.Fatalf("bound %d: the experiences walked %v; the store holds %v", bound, got, experiences)
			}
			if got := boundWalk(t, e, r, "reasoning", map[string]any{"query": limitCue}); !slices.Equal(got, reasoning) {
				t.Fatalf("bound %d: the reasoning walked %v; the store holds %v", bound, got, reasoning)
			}
		}
		out := advertisedRecallCall(t, e, map[string]any{"source": "experiences", "query": limitCue, "limit": 1})
		if bound >= 1500 && utf8.RuneCountInString(out) > bound {
			t.Fatalf("bound %d: a one-row page whose frame fits is over its bound:\n%s", bound, out)
		}
		if strings.Contains(out, recallCutMark) {
			marked = true
			// A row cut short says how to read it alone, and that call
			// reads that row.
			row := pagedRows(t, "experiences", out)[0]
			if !strings.Contains(out, fmt.Sprintf(`recall source=experiences query="" after_seq=%d limit=1 shows it alone`, row+1)) {
				t.Fatalf("bound %d: a row cut short does not say how to read it alone:\n%s", bound, out)
			}
			alone := advertisedRecallCall(t, e, map[string]any{"source": "experiences", "query": "", "after_seq": row + 1, "limit": 1})
			if got := pagedRows(t, "experiences", alone); len(got) != 1 || got[0] != row {
				t.Fatalf("bound %d: the route a cut row names reads %v, not seq %d:\n%s", bound, got, row, alone)
			}
		}
		rout := advertisedRecallCall(t, e, map[string]any{"source": "reasoning", "query": limitCue, "limit": 1})
		if strings.Contains(rout, recallCutMark) {
			turn := pagedRows(t, "reasoning", rout)[0]
			if !strings.Contains(rout, fmt.Sprintf(`recall source=reasoning query="turn:%d" reads it whole`, turn)) {
				t.Fatalf("bound %d: a reasoning row cut short does not name the read of its whole:\n%s", bound, rout)
			}
		}
	}
	if !marked {
		t.Fatal("no row was ever cut short: the probe did not reach a row over its bound")
	}
}

// An after_seq that is not a seq is refused, in the same sentence, by
// every source, before anything is read.
func TestAnOutOfRangeCursorIsRefusedInOneSentenceForEverySource(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	seedPagedSources(t, e, st, 3)
	e.heardHistory = func(string, uint64, int) (string, error) { return `{"rows":[]}`, nil }
	sources := []string{"", "experiences", "syntheses", "conversation", "ledger", "beliefs", "intentions", "reasoning", "heard", "standing", "alarms", "projects"}
	for _, source := range sources {
		for _, bad := range []any{float64(1 << 63), 1e19, 1e300, float64(-1), 1.5, int(-4), "12"} {
			args := map[string]any{"query": "", "after_seq": bad}
			if source != "" {
				args["source"] = source
			}
			if source == "standing" {
				args["id"], args["after_edge"] = "b_probe", "edge"
			}
			_, err := e.ExecuteAction(context.Background(), "verb", "recall", args)
			if !errors.Is(err, errRecallCursor) {
				t.Fatalf("source=%q after_seq=%v: want the cursor refusal, got %v", source, bad, err)
			}
			if !strings.Contains(err.Error(), "after_seq is a whole number from 0") {
				t.Fatalf("source=%q after_seq=%v: the refusal does not say what after_seq is: %v", source, bad, err)
			}
			// The value as it was given, with its JSON type: a string "12"
			// must not read as the number 12.
			kind := "(a number)"
			if _, isString := bad.(string); isString {
				kind = "(a string)"
			}
			if !strings.Contains(err.Error(), kind) || (kind == "(a string)" && !strings.Contains(err.Error(), `after_seq="12"`)) {
				t.Fatalf("source=%q after_seq=%v: the refusal does not show the value as given: %v", source, bad, err)
			}
		}
		// The largest exact seq is a position, not a refusal.
		args := map[string]any{"query": "", "after_seq": float64(1<<53 - 1)}
		if source != "" {
			args["source"] = source
		}
		if _, err := e.ExecuteAction(context.Background(), "verb", "recall", args); errors.Is(err, errRecallCursor) {
			t.Fatalf("source=%q: the largest exact seq was refused: %v", source, err)
		}
	}
}

// seedLongBeliefs gives the store n current beliefs, each born at its own
// event as a real belief is, newest first by that event.
func seedLongBeliefs(t *testing.T, e *Engine, st *store.Store, n int) []uint64 {
	t.Helper()
	ctx := context.Background()
	var seqs []uint64
	for i := 0; i < n; i++ {
		if _, err := e.ExecuteAction(ctx, "verb", "note", map[string]any{"content": fmt.Sprintf("the ground for belief %d", i)}); err != nil {
			t.Fatal(err)
		}
		var seq uint64
		if err := st.DB().QueryRow(`SELECT MAX(seq) FROM ledger`).Scan(&seq); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().Exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES (?, ?, 3, 0.5, 0, ?, ?)`,
			fmt.Sprintf("b_long_%02d", i), fmt.Sprintf("belief %d holds that %s", i, strings.Repeat("the harbour light turns slowly ", 8)), seq, seq); err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, seq)
	}
	slices.Reverse(seqs)
	return seqs
}

// The belief listing is cut to its bound like every listing, newest first,
// and its after_seq reads every belief once.
func TestTheBeliefListingIsCutToItsBoundAndPagesOn(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	want := seedLongBeliefs(t, e, st, 40)
	e.toolResultChars = 3000
	var walked []uint64
	cursor := uint64(0)
	beliefRow := regexp.MustCompile(`(?m)^  \[b_long_\d+, seq (\d+),`)
	for call := 0; call < 100; call++ {
		args := map[string]any{"source": "beliefs", "query": ""}
		if cursor > 0 {
			args["after_seq"] = cursor
		}
		out := advertisedRecallCall(t, e, args)
		if n := utf8.RuneCountInString(out); n > 3000 {
			t.Fatalf("a belief listing of %d characters, over its bound of 3000:\n%s", n, out)
		}
		var rows []uint64
		for _, m := range beliefRow.FindAllStringSubmatch(out, -1) {
			n, _ := strconv.ParseUint(m[1], 10, 64)
			rows = append(rows, n)
		}
		walked = append(walked, rows...)
		next, more := nextPage(out)
		if !more {
			if !strings.Contains(out, "searched to completion: beliefs") {
				t.Fatalf("the last page of beliefs does not say the listing is whole:\n%s", out)
			}
			break
		}
		if !strings.Contains(out, "Cut to keep this answer within 3000 characters: left out the last rows of beliefs") || next != rows[len(rows)-1] {
			t.Fatalf("a cut belief listing does not say so, or names the wrong next page (%d):\n%s", next, out)
		}
		cursor = next
	}
	if !slices.Equal(walked, want) {
		t.Fatalf("the belief pages walked %v; the store holds %v", walked, want)
	}
}

// The listing of every source is cut to its bound too, and each part it
// cut says how to read the rest.
func TestTheListingOfEverySourceIsCutToItsBound(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	seedPagedSources(t, e, st, 25)
	seedLongBeliefs(t, e, st, 25)
	e.toolResultChars = 5000
	out := advertisedRecallCall(t, e, map[string]any{"query": ""})
	if n := utf8.RuneCountInString(out); n > 5000 {
		t.Fatalf("the listing of every source is %d characters, over its bound of 5000:\n%s", n, out)
	}
	if !strings.Contains(out, "Cut to keep this answer within 5000 characters: left out the last rows of ") {
		t.Fatalf("a cut listing of every source does not say so:\n%s", out)
	}
	if !regexp.MustCompile(`PAGED, more remain: [^\n]*beliefs \(its next page: after_seq=\d+\)`).MatchString(out) {
		t.Fatalf("the cut beliefs do not name their next page:\n%s", out)
	}
}

// A cursor past the oldest row finds no older rows, and says that; the
// words matched, and nothing tells the reader to change them.
func TestACursorPastTheOldestRowSaysThereAreNoOlderRows(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	truth := seedPagedSources(t, e, st, 4)
	for source := range pagedSourceDefaults {
		oldest := truth[source][len(truth[source])-1]
		out := advertisedRecallCall(t, e, map[string]any{"source": source, "query": limitCue, "after_seq": oldest})
		if !strings.Contains(out, "No older rows") || strings.Contains(out, "No word match") || strings.Contains(out, "Retry") || strings.Contains(out, "try fewer words") {
			t.Fatalf("source=%s after the oldest row:\n%s", source, out)
		}
	}
}

// The schema's limit words state each listing's default page, and each is
// the page the listing serves.
func TestTheLimitWordsStateThePagesTheListingsServe(t *testing.T) {
	var desc string
	for _, v := range Verbs() {
		if v.Name == "recall" {
			desc = v.Params["properties"].(map[string]any)["limit"].(map[string]any)["description"].(string)
		}
	}
	for source, page := range pagedSourceDefaults {
		m := regexp.MustCompile(source + ` (\d+)`).FindStringSubmatch(desc)
		if m == nil || m[1] != strconv.Itoa(page) {
			t.Fatalf("the limit words state %v for %s; the listing serves %d: %s", m, source, page, desc)
		}
	}
}
