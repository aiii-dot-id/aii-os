package app

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// dashboard_recall_reads_test.go — a recall the dashboard asks is not the
// resident remembering. R101: only a conscious act of
// recall reinforces. The memory view's search runs the resident's own
// recall verb, so the operator reads what the resident would. Whether a
// person typed the query or the Beliefs page restored it on a reload,
// neither is the resident's act, and neither writes access history. The
// resident's own recall still does.

// readsFixture boots a live identity holding three beliefs and three
// conversation turns that match "lighthouse". The turns are ten days old,
// because an aged memory's strength moves when it is reinforced: a recall
// that wrote access history changes the numbers the next one prints. The
// beliefs are as old as the fixture's birth.
func readsFixture(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Reads")
	a := New(safebootConfig(t, dir, "Reads", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("LIVE boot: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if reason, safe := a.SafeMode(); safe {
		t.Fatalf("the fixture booted SAFE (%s)", reason)
	}
	aged := time.Now().UTC().Add(-10 * 24 * time.Hour).Format(time.RFC3339)
	for i := 1; i <= 3; i++ {
		if _, err := a.store.DB().Exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq)
			VALUES (?, ?, 3, 0.9, 0, 1, 1)`, fmt.Sprintf("reads-b%d", i),
			fmt.Sprintf("the lighthouse on reef %d keeps its lamp lit", i)); err != nil {
			t.Fatal(err)
		}
		if _, err := a.store.DB().Exec(`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at)
			VALUES (?, 'reads', 'operator', ?, ?, ?)`, fmt.Sprintf("reads-c%d", i),
			fmt.Sprintf("the lighthouse on reef %d was dark last night", i), 900000+i, aged); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

// readsHits are the six rows the fixture seeds, as the access table keys
// them, and how each shows in a recall answer.
var readsHits = []struct{ store, id, shown string }{
	{"beliefs", "reads-b1", "[reads-b1,"},
	{"beliefs", "reads-b2", "[reads-b2,"},
	{"beliefs", "reads-b3", "[reads-b3,"},
	{"conversations", "reads-c1", "turn 900001,"},
	{"conversations", "reads-c2", "turn 900002,"},
	{"conversations", "reads-c3", "turn 900003,"},
}

// requireReadsHitsShown fails unless the answer shows all six seeded rows,
// so a test about them is about hits from both stores.
func requireReadsHitsShown(t *testing.T, who, out string) {
	t.Helper()
	for _, h := range readsHits {
		if !strings.Contains(out, h.shown) {
			t.Fatalf("%s does not show %s/%s (%q), so this does not test hits from both stores:\n%s", who, h.store, h.id, h.shown, out)
		}
	}
}

type accessRecord struct {
	count         int64
	last, history string
}

func (r accessRecord) String() string {
	return fmt.Sprintf("count %d, newest %s, history %s", r.count, r.last, r.history)
}

// accessRecords is every access record the store holds, by store/id.
func accessRecords(t *testing.T, a *App) map[string]accessRecord {
	t.Helper()
	rows, err := a.store.DB().Query(`SELECT store, id, count, last_at, history FROM memory_access`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]accessRecord{}
	for rows.Next() {
		var st, id string
		var r accessRecord
		if err := rows.Scan(&st, &id, &r.count, &r.last, &r.history); err != nil {
			t.Fatal(err)
		}
		out[st+"/"+id] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// changedRecords lists every access record that is not as it was.
func changedRecords(before, after map[string]accessRecord) []string {
	var out []string
	for k, now := range after {
		was, ok := before[k]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s: no record, now %s", k, now))
		case was != now:
			out = append(out, fmt.Sprintf("%s: %s, now %s", k, was, now))
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			out = append(out, k+": gone")
		}
	}
	sort.Strings(out)
	return out
}

// What an answer says of reinforcement: the resident's sentence, which a
// model reads, and the one the operator reads under a dashboard search.
const (
	saysReinforced        = "This recall reinforced what it returned."
	saysReinforcedNothing = "This search reinforced nothing: only the identity's own recall does."
)

// A RECALL THE DASHBOARD ASKS LEAVES EVERY HIT'S ACCESS RECORD AS IT WAS,
// AND SAYS SO. The answer shows hits from two stores, beliefs and
// conversation turns, and the access table after it is the table before
// it, row for row: count, newest time and history. The answer does not
// claim the reinforcement it did not do; it says it did none.
func TestARecallTheDashboardAsksLeavesEveryHitsAccessRecordAsItWas(t *testing.T) {
	a := readsFixture(t)
	before := accessRecords(t, a)
	out, err := a.recallForDashboard("lighthouse")
	if err != nil {
		t.Fatal(err)
	}
	requireReadsHitsShown(t, "the dashboard's recall", out)
	if changed := changedRecords(before, accessRecords(t, a)); len(changed) > 0 {
		t.Fatalf("a recall the dashboard asked wrote access history, which only the resident's own recall writes:\n  %s", strings.Join(changed, "\n  "))
	}
	if strings.Contains(out, saysReinforced) || !strings.Contains(out, saysReinforcedNothing) {
		t.Fatalf("a recall the dashboard asked reinforced nothing, and its answer must say so (%q), not %q:\n%s", saysReinforcedNothing, saysReinforced, out)
	}
}

// THE RESIDENT'S OWN RECALL STILL REINFORCES WHAT IT SHOWS. The same query
// through the verb, as the resident's loop calls it, adds one access to
// each hit it shows, in both stores, and its answer still says so in the
// words the resident has always read. A fix that stopped reinforcement for
// every caller would pass the test above and fail this one.
func TestTheResidentsOwnRecallStillReinforcesWhatItShows(t *testing.T) {
	a := readsFixture(t)
	before := accessRecords(t, a)
	out, err := a.engine.ExecuteAction(context.Background(), "verb", "recall", map[string]any{"query": "lighthouse"})
	if err != nil {
		t.Fatal(err)
	}
	requireReadsHitsShown(t, "the resident's recall", out)
	if !strings.Contains(out, saysReinforced) || strings.Contains(out, saysReinforcedNothing) {
		t.Errorf("the resident's recall must still say %q:\n%s", saysReinforced, out)
	}
	after := accessRecords(t, a)
	for _, h := range readsHits {
		k := h.store + "/" + h.id
		if got, want := after[k].count, before[k].count+1; got != want {
			t.Errorf("%s: the resident's recall showed it, and its access count is %d, not %d", k, got, want)
		}
	}
}

// printedNumber is a run of digits, with its fractional part if it has one.
var printedNumber = regexp.MustCompile(`[0-9]+(\.[0-9]+)?`)

// differentNumbers says where two answers differ by more than age moves
// them in a moment: in any character that is not part of a number, in a
// whole number, or in a fractional number by more than one unit in its
// last printed place. It returns "" when they agree.
func differentNumbers(first, second string) string {
	a, b := strings.Split(first, "\n"), strings.Split(second, "\n")
	if len(a) != len(b) {
		return fmt.Sprintf("the first answer has %d lines and the second %d", len(a), len(b))
	}
	for i := range a {
		say := fmt.Sprintf("line %d:\n  first:  %s\n  second: %s", i+1, a[i], b[i])
		if printedNumber.ReplaceAllString(a[i], "#") != printedNumber.ReplaceAllString(b[i], "#") {
			return say
		}
		na, nb := printedNumber.FindAllString(a[i], -1), printedNumber.FindAllString(b[i], -1)
		for j := range na {
			if na[j] == nb[j] {
				continue
			}
			da, db := strings.IndexByte(na[j], '.'), strings.IndexByte(nb[j], '.')
			if da < 0 || db < 0 || len(na[j])-da != len(nb[j])-db {
				return fmt.Sprintf("%s then %s on %s", na[j], nb[j], say)
			}
			x, _ := strconv.ParseFloat(na[j], 64)
			y, _ := strconv.ParseFloat(nb[j], 64)
			if unit := math.Pow(10, -float64(len(na[j])-da-1)); math.Abs(x-y) > 1.5*unit {
				return fmt.Sprintf("%s then %s on %s", na[j], nb[j], say)
			}
		}
	}
	return ""
}

// TWO RECALLS THE DASHBOARD ASKS PRINT THE SAME NUMBERS. The Beliefs page
// asks again on every reload with the query it kept. An answer's lines are
// built before its own reinforcement is written (recallRanked), so a
// recall that wrote shows in the next answer. An aged turn recalled once
// ages at about three quarters of its rate, and its strength and score
// move by about a third. Age moves them too, by far less than one unit
// in their last printed place in
// the moment between two asks; so a fractional number may differ by that
// one unit and no more, and every other character is the same.
func TestTwoRecallsTheDashboardAsksPrintTheSameNumbers(t *testing.T) {
	a := readsFixture(t)
	first, err := a.recallForDashboard("lighthouse")
	if err != nil {
		t.Fatal(err)
	}
	requireReadsHitsShown(t, "the dashboard's first recall", first)
	second, err := a.recallForDashboard("lighthouse")
	if err != nil {
		t.Fatal(err)
	}
	if diff := differentNumbers(first, second); diff != "" {
		t.Fatalf("the dashboard asked the same recall twice, and the second answer differs from the first by more than age moves it: %s", diff)
	}
}
