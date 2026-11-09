package app

// THE FOUR THINGS THE HOST NAMED AFTER READING 0b6f67cd
// (2026-10-30 15:30Z, …-1530-host-021-to-sev-step-8-0b6f67cd-four-left.md).
//
// Item 1: a failed or malformed read of the record fails the trail whole;
// only an absent row is an absent time. Item 3: a link names the turn its
// note was dreamed through, and a note that names none is an answer.

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// seedLedgerPayload writes a ledger row carrying a real payload, so a
// reader that inspects the signed record has something to inspect.
func seedLedgerPayload(t *testing.T, st *store.Store, seq int64, ts, payload string) {
	t.Helper()
	_, err := st.DB().Exec(
		`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content)
		 VALUES (?, '', ?, 'experience.create', 3, ?, 'seed')`, seq, ts, payload)
	if err != nil {
		t.Fatalf("seed ledger %d with payload: %v", seq, err)
	}
}

// A ledger timestamp that is not RFC 3339 is a record this cannot read,
// not a record that states nothing. Before the host's item 1 the trail
// rendered such a belief as having no birth, which is a claim the record
// does not make.
func TestBeliefTrailFailsOnAMalformedLedgerTime(t *testing.T) {
	a, st := trailApp(t)
	db := st.DB()

	// A row that exists and states something unparseable.
	seedLedgerPayload(t, st, 1, "the twenty-fifth of October", `{}`)
	if _, err := db.Exec(
		`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count,
		                      archived, first_seq, last_seq)
		 VALUES ('b_bad', 'a belief with a broken birth', 3, 0.7, 1, 0, 1, 1)`); err != nil {
		t.Fatalf("seed belief: %v", err)
	}

	trail, err := a.beliefTrailForDashboard("b_bad")
	if err == nil {
		t.Fatalf("trail came back with held_since %q and no error; a malformed ledger time is a failed read of the record",
			trail.Belief.HeldSince)
	}
	if !strings.Contains(err.Error(), "ledger seq 1") {
		t.Errorf("error = %q, want it to name the sequence that could not be read", err)
	}
}

// The other side of item 1 lives at the store, in ledger_time_test.go:
// the schema makes a dangling birth reference impossible
// (beliefs.first_seq is NOT NULL REFERENCES ledger(seq), schema.sql:41),
// so a belief cannot name a row that is not there. The absent case is
// exercised where it is reachable, at seq 0, rather than by seeding a
// record the schema forbids.

// A dream note names the turn it was dreamed through, and the page says
// which. The citation is a stable turn id string carried in the signed
// payload, and it is allowed on experience.create and nothing else.
func TestBeliefTrailNamesTheTurnADreamNoteCites(t *testing.T) {
	a, st := trailApp(t)
	db := st.DB()

	seedLedger(t, st, 1, "2026-10-20T00:00:00Z")
	// The cited note: a dream's, naming turn_7f3a.
	seedLedgerPayload(t, st, 2, "2026-10-25T00:00:00Z",
		`{"dreamed_through":{"turn":"turn_7f3a2b1c","position":44}}`)
	// The uncited note: the identity's own, naming no turn at all.
	seedLedgerPayload(t, st, 3, "2026-10-26T00:00:00Z", `{}`)

	if _, err := db.Exec(
		`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count,
		                      archived, first_seq, last_seq)
		 VALUES ('b_claim', 'the sky is blue', 3, 0.8, 2, 0, 1, 3)`); err != nil {
		t.Fatalf("seed belief: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO experiences (id, content, category, provenance, created_seq, created_at)
		 VALUES ('e_dream', 'I read the transcript', 'reflection', 'dream', 2, '2026-10-25T00:00:00Z')`); err != nil {
		t.Fatalf("seed dream experience: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO experiences (id, content, category, provenance, created_seq, created_at)
		 VALUES ('e_mine', 'I looked up', 'observation', 'self', 3, '2026-10-26T00:00:00Z')`); err != nil {
		t.Fatalf("seed own experience: %v", err)
	}
	for _, e := range []string{"e_dream", "e_mine"} {
		if _, err := db.Exec(
			`INSERT INTO edges (id, from_id, to_id, edge_type, strength, created_seq)
			 VALUES (?, ?, 'b_claim', 'SUPPORTS', 1.0, 3)`, "ed_"+e, e); err != nil {
			t.Fatalf("seed edge %s: %v", e, err)
		}
	}

	trail, err := a.beliefTrailForDashboard("b_claim")
	if err != nil {
		t.Fatalf("beliefTrailForDashboard(b_claim): %v", err)
	}
	if len(trail.Origins) != 2 {
		t.Fatalf("origins = %d, want 2", len(trail.Origins))
	}

	turns := map[string]*string{}
	for _, o := range trail.Origins {
		turns[o.ID] = o.Turn
	}

	dream, ok := turns["e_dream"]
	if !ok {
		t.Fatal("the dream note is not among the origins")
	}
	if dream == nil {
		t.Fatal("the dream note's turn is null, want the id the record names")
	}
	if *dream != "turn_7f3a2b1c" {
		t.Errorf("turn = %q, want turn_7f3a2b1c (the id the payload states)", *dream)
	}

	mine, ok := turns["e_mine"]
	if !ok {
		t.Fatal("the identity's own note is not among the origins")
	}
	if mine != nil {
		t.Errorf("turn = %q on a note that names none, want null: an uncited note is an answer, not a failure", *mine)
	}
}
