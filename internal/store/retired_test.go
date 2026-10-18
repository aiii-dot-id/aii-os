package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// retired_test.go — what the projection says about the beliefs the
// identity retired: which, when, and what stands where each stood. Every
// event goes through the materializer with a timestamp the test chose, so a
// date in the answer is the record's date and no other.

// retiredLedger materializes events with times the test names.
type retiredLedger struct {
	t   *testing.T
	s   *Store
	seq uint64
	at  map[uint64]string
}

func newRetiredLedger(t *testing.T) *retiredLedger {
	return &retiredLedger{t: t, s: testStore(t), at: map[uint64]string{}}
}

// mat materializes one event at ts and returns its seq.
func (l *retiredLedger) mat(ts string, et ledger.EventType, payload map[string]any) uint64 {
	l.t.Helper()
	l.seq++
	b, _ := json.Marshal(payload)
	if err := l.s.Materialize(&ledger.Event{Seq: l.seq, Type: et, Payload: b, Timestamp: ts}); err != nil {
		l.t.Fatalf("materialize %s: %v", et, err)
	}
	l.at[l.seq] = ts
	return l.seq
}

func (l *retiredLedger) belief(id, statement string, ring int) {
	l.t.Helper()
	l.mat("2026-09-20T08:00:00Z", ledger.EventBeliefUpsert, map[string]any{"id": id, "statement": statement, "ring": ring, "confidence": 0.6})
}

func (l *retiredLedger) supersede(ts, oldID, newID string) uint64 {
	l.t.Helper()
	return l.mat(ts, ledger.EventBeliefSupersede, map[string]any{"old_id": oldID, "new_id": newID, "reason": "test"})
}

func (l *retiredLedger) archive(ts, id string) uint64 {
	l.t.Helper()
	return l.mat(ts, ledger.EventBeliefArchive, map[string]any{"id": id, "reason": "test"})
}

func retiredIDs(rs []RetiredBelief) []string {
	ids := make([]string, len(rs))
	for i, r := range rs {
		ids[i] = r.ID
	}
	return ids
}

func sameIDs(got []RetiredBelief, want ...string) bool {
	return strings.Join(retiredIDs(got), ",") == strings.Join(want, ",")
}

// THE RETIRED BELIEFS, NEWEST RETIREMENT FIRST, each with the record's own
// time and the belief at the end of its supersession chain — and only the
// beliefs CONSOLIDATE owns: Ring 3, and retired.
func TestRetiredBeliefsSayWhenAndWhatStandsInTheirPlace(t *testing.T) {
	l := newRetiredLedger(t)
	l.belief("b_dawn", "The ferry leaves at dawn", 3)
	l.belief("b_six", "The ferry leaves at six", 3)
	l.belief("b_noon", "The ferry leaves at noon", 3)
	l.belief("b_oil", "The lantern oil is kept aft", 3)
	l.belief("b_keep", "The harbor bell rings at nine", 3)
	l.belief("b_core", "I test before I ship", 3)
	l.mat("2026-09-20T08:01:00Z", ledger.EventBeliefPromote, map[string]any{"id": "b_core", "ring": 2})
	l.belief("b_charter", "We work in the open", 1)

	dawn := l.supersede("2026-09-26T10:00:00Z", "b_dawn", "b_six")
	six := l.supersede("2026-09-27T11:30:45.123456789Z", "b_six", "b_noon")
	oil := l.archive("2026-09-28T09:05:00Z", "b_oil")
	l.archive("2026-09-28T09:06:00Z", "b_core")    // Ring 2: not working truth
	l.archive("2026-09-28T09:07:00Z", "b_charter") // Ring 1: not working truth
	// The retired belief is written again AFTER it was retired: evidence
	// arrives for it. Its last_seq moves; the time it was retired does not.
	l.mat("2026-09-29T07:00:00Z", ledger.EventEdgeCreate, map[string]any{"id": "edge_late", "from_id": "exp_late", "to_id": "b_dawn", "edge_type": "SUPPORTS"})

	got, more, err := l.s.RetiredBeliefs(0, 10)
	if err != nil || more != 0 {
		t.Fatalf("retired beliefs: more=%d err=%v", more, err)
	}
	if !sameIDs(got, "b_oil", "b_six", "b_dawn") {
		t.Fatalf("retired beliefs, newest retirement first, Ring 3 only: %v", retiredIDs(got))
	}
	byID := map[string]RetiredBelief{}
	for _, r := range got {
		byID[r.ID] = r
	}
	for id, want := range map[string]string{"b_dawn": l.at[dawn], "b_six": l.at[six], "b_oil": l.at[oil]} {
		if byID[id].RetiredAt != want {
			t.Errorf("%s retired at %q, the record says %q", id, byID[id].RetiredAt, want)
		}
	}
	if byID["b_dawn"].Statement != "The ferry leaves at dawn" {
		t.Errorf("a retired belief keeps its words: %q", byID["b_dawn"].Statement)
	}
	// A chain a→b→c is replaced by c, for a and for b alike.
	for _, id := range []string{"b_dawn", "b_six"} {
		if r := byID[id].Replacement; r.ID != "b_noon" || r.Kind != "belief" || r.Retired || r.Text != "The ferry leaves at noon" {
			t.Errorf("%s is replaced by the end of its chain, b_noon: %+v", id, r)
		}
	}
	if r := byID["b_oil"].Replacement; r.ID != "" {
		t.Errorf("an archived belief was replaced by nothing: %+v", r)
	}

	// Ring 3 is partitioned: every belief the identity holds is in exactly
	// one of ListBeliefs and RetiredBeliefs.
	held, err := l.s.ListBeliefs()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, b := range held {
		if b.Ring == 3 {
			seen[b.ID] = "held"
		}
	}
	for _, r := range got {
		if seen[r.ID] != "" {
			t.Errorf("%s is both held and retired", r.ID)
		}
		seen[r.ID] = "retired"
	}
	for _, id := range []string{"b_dawn", "b_six", "b_noon", "b_oil", "b_keep"} {
		if seen[id] == "" {
			t.Errorf("Ring 3 belief %s is neither held nor retired", id)
		}
	}
	if seen["b_core"] != "" || seen["b_charter"] != "" {
		t.Errorf("a belief promoted out of Ring 3 or born above it was listed: %v", seen)
	}
}

// THE SPAN AND THE COUNT: only retirements after `after`, and when more
// are left than the limit shows, the newest are shown and the rest counted.
func TestRetiredBeliefsAreWindowedAndCounted(t *testing.T) {
	l := newRetiredLedger(t)
	for _, id := range []string{"b_a", "b_b", "b_c", "b_d"} {
		l.belief(id, "claim "+id, 3)
	}
	a := l.archive("2026-09-26T10:00:00Z", "b_a")
	b := l.archive("2026-09-27T10:00:00Z", "b_b")
	l.archive("2026-09-28T10:00:00Z", "b_c")

	got, more, err := l.s.RetiredBeliefs(a, 10) // after a: a's own retirement is not after it
	if err != nil || more != 0 || !sameIDs(got, "b_c", "b_b") {
		t.Fatalf("after seq %d: %v more=%d err=%v", a, retiredIDs(got), more, err)
	}
	got, more, err = l.s.RetiredBeliefs(b, 10)
	if err != nil || more != 0 || !sameIDs(got, "b_c") {
		t.Fatalf("after seq %d: %v more=%d err=%v", b, retiredIDs(got), more, err)
	}
	got, more, err = l.s.RetiredBeliefs(0, 2)
	if err != nil || !sameIDs(got, "b_c", "b_b") || more != 1 {
		t.Fatalf("a limit of 2 over 3: shown %v, more=%d (want the two newest and 1 more) err=%v", retiredIDs(got), more, err)
	}
	if got, more, err = l.s.RetiredBeliefs(1<<40, 5); err != nil || len(got) != 0 || more != 0 {
		t.Fatalf("nothing retired after the end of the record: %v more=%d err=%v", got, more, err)
	}
	if _, _, err := l.s.RetiredBeliefs(0, 0); err == nil {
		t.Fatal("a limit that shows nothing was accepted")
	}
}

func TestRetiredBeliefsSurviveOddChains(t *testing.T) {
	l := newRetiredLedger(t)
	for _, id := range []string{"b_x", "b_y", "b_p", "b_q", "b_s"} {
		l.belief(id, "claim "+id, 3)
	}
	l.supersede("2026-09-26T10:00:00Z", "b_x", "b_y")
	l.archive("2026-09-26T11:00:00Z", "b_y")
	l.supersede("2026-09-27T10:00:00Z", "b_p", "b_q")
	l.supersede("2026-09-27T11:00:00Z", "b_q", "b_p")
	l.supersede("2026-09-28T10:00:00Z", "b_s", "b_s")

	done := make(chan struct{})
	var got []RetiredBelief
	var err error
	go func() {
		defer close(done)
		got, _, err = l.s.RetiredBeliefs(0, 10)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("A LOOP IN THE SUPERSESSION CHAIN WAS FOLLOWED FOREVER")
	}
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]RetiredBelief{}
	for _, r := range got {
		byID[r.ID] = r
	}
	if len(byID) != 5 {
		t.Fatalf("every retired belief answers: %v", retiredIDs(got))
	}
	if r := byID["b_x"].Replacement; r.ID != "b_y" || !r.Retired {
		t.Errorf("b_x was replaced by b_y, which was then archived: %+v", r)
	}
	if r := byID["b_y"].Replacement; r.ID != "" {
		t.Errorf("b_y was archived and replaced by nothing: %+v", r)
	}
	if r := byID["b_p"].Replacement; r.ID != "b_q" || !r.Retired {
		t.Errorf("b_p: the chain comes back to b_p, so it ends at b_q, a belief since retired: %+v", r)
	}
	if r := byID["b_s"].Replacement; r.ID != "" {
		t.Errorf("a belief that superseded itself was replaced by nothing: %+v", r)
	}
}

// AN UNREADABLE PROJECTION IS AN ERROR, NOT AN EMPTY LIST: a pass told
// "nothing was retired" because a read failed would be told a falsehood.
func TestRetiredBeliefsRefuseAnUnreadableStore(t *testing.T) {
	l := newRetiredLedger(t)
	l.belief("b_a", "claim", 3)
	l.archive("2026-09-26T10:00:00Z", "b_a")
	if _, err := l.s.db.Exec(`PRAGMA foreign_keys = OFF; ALTER TABLE beliefs RENAME TO beliefs_unreadable`); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if got, more, err := l.s.RetiredBeliefs(0, 5); err == nil {
		t.Fatalf("an unreadable store answered: %v more=%d", got, more)
	}
}
