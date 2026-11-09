package app

// THE BELIEF TRAIL THROUGH THE REAL DOORS (host 1430 item 7, a review 1413
// finding 6). The seven wire and websocket tests in internal/dashboard
// build the trail's shapes by hand, so they cannot see what the store
// actually hands the app. This test seeds a real record and asks the app
// for the trail, because the defects that got past the first commit were
// all of one kind: a value the producer never read, rendered as though it
// had been measured. An edge's strength read as 0.0, a belief the record
// had retired shown as live, a count that never crossed the wire.
//
// Every assertion below is against a fact a human put in the database,
// not against a default the schema supplied.

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// trailApp opens a real record and gives the app a store over it.
func trailApp(t *testing.T) (*App, *store.Store) {
	t.Helper()
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	a := newBareApp(t, &Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	a.store = st
	return a, st
}

// seedLedger writes one ledger row, which every belief and edge needs as
// a foreign key. The trail reads a belief's birth time from this row, so
// its ts is what makes an age — and therefore a decay — real.
func seedLedger(t *testing.T, st *store.Store, seq int64, ts string) {
	t.Helper()
	_, err := st.DB().Exec(
		`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content)
		 VALUES (?, '', ?, 'note', 3, '{}', 'seed')`, seq, ts)
	if err != nil {
		t.Fatalf("seed ledger %d: %v", seq, err)
	}
}

// at returns an RFC 3339 instant a fixed number of days before now.
// Ages are anchored to the clock the producer actually reads, never to
// the calendar: a fixture pinned to an absolute date ages into a
// different judgment as the wall clock moves (host, 2026-10-30, item 6).
func at(daysAgo float64) string {
	return time.Now().UTC().Add(-time.Duration(daysAgo * 24 * float64(time.Hour))).Format(time.RFC3339)
}

func TestBeliefTrailReadsTheRealRecord(t *testing.T) {
	a, st := trailApp(t)
	db := st.DB()

	// A birth the record really holds: ten days old, so CARRD strength has
	// something to decay from. Ages are relative to now, so this fixture
	// reads the same on every day the gate runs.
	seedLedger(t, st, 1, at(10))
	seedLedger(t, st, 2, at(5))
	seedLedger(t, st, 3, at(4))

	// The belief under test: ring 3, two pieces of evidence, live.
	if _, err := db.Exec(
		`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count,
		                      archived, first_seq, last_seq)
		 VALUES ('b_claim', 'the sky is blue', 3, 0.82, 2, 0, 1, 3)`); err != nil {
		t.Fatalf("seed belief: %v", err)
	}
	// A belief the record has retired. It must not come back looking live.
	if _, err := db.Exec(
		`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count,
		                      archived, first_seq, last_seq)
		 VALUES ('b_old', 'the sky is green', 3, 0.40, 0, 1, 1, 1)`); err != nil {
		t.Fatalf("seed archived belief: %v", err)
	}
	// The far end of the support edge, with its own words.
	if _, err := db.Exec(
		`INSERT INTO experiences (id, content, category, provenance, created_seq, created_at)
		 VALUES ('e_one', 'I looked up', 'observation', 'self', 2, ?)`, at(5)); err != nil {
		t.Fatalf("seed experience: %v", err)
	}
	// A support edge whose strength is a real 0.9. The first commit wrote
	// strength 0.0 here because it never read the column, which told the
	// page a well-supported belief was weightless.
	if _, err := db.Exec(
		`INSERT INTO edges (id, from_id, to_id, edge_type, strength, created_seq)
		 VALUES ('ed_sup', 'e_one', 'b_claim', 'SUPPORTS', 0.9, 2)`); err != nil {
		t.Fatalf("seed edge: %v", err)
	}
	// A belief the record holds and has rarely recalled, born long ago.
	// CARRD's standard class has an 82-day half-life, so ten days would
	// leave it near 0.92 and the fading judgment could never be exercised
	// from the true side; this one is six months old, well under the 0.5
	// alert line.
	seedLedger(t, st, 4, at(180))
	if _, err := db.Exec(
		`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count,
		                      archived, first_seq, last_seq)
		 VALUES ('b_faint', 'I once thought the sky was mauve', 3, 0.55, 1, 0, 4, 4)`); err != nil {
		t.Fatalf("seed faint belief: %v", err)
	}

	// Recall: four times. The instant is captured once and used for both
	// the fixture and the assertion: computing at(4) twice from time.Now()
	// and demanding second-exact equality fails a correct producer across
	// a second boundary (a review 1653, host item 2).
	recalledAt := at(4)
	// The store name is "beliefs",
	// which is what the store's own memory_access tests use.
	if _, err := db.Exec(
		`INSERT INTO memory_access (store, id, count, last_at, history)
		 VALUES ('beliefs', 'b_claim', 4, ?, '[]')`, recalledAt); err != nil {
		t.Fatalf("seed memory_access: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO memory_access (store, id, count, last_at, history)
		 VALUES ('beliefs', 'b_faint', 1, ?, '[]')`, at(10)); err != nil {
		t.Fatalf("seed faint memory_access: %v", err)
	}

	trail, err := a.beliefTrailForDashboard("b_claim")
	if err != nil {
		t.Fatalf("beliefTrailForDashboard(b_claim): %v", err)
	}

	if trail.Belief.Statement != "the sky is blue" {
		t.Errorf("statement = %q, want the words the record holds", trail.Belief.Statement)
	}
	if trail.Belief.Confidence != 0.82 {
		t.Errorf("confidence = %v, want 0.82", trail.Belief.Confidence)
	}
	if trail.Belief.Archived {
		t.Error("a live belief was reported archived")
	}

	// Recall crossed the wire as a measurement, not a default.
	if trail.Belief.Recall == nil {
		t.Fatal("recall is null, want a measured strength for a belief with an access row")
	}
	if trail.Belief.RecallCount == nil {
		t.Fatal("recall_count is null, want the count the record holds")
	}
	if *trail.Belief.RecallCount != 4 {
		t.Errorf("recall_count = %d, want 4 (the count the record holds)", *trail.Belief.RecallCount)
	}
	if trail.Belief.RecalledAt == nil {
		t.Error("recalled_at is null, want the time the record holds")
	} else if want := recalledAt; *trail.Belief.RecalledAt != want {
		t.Errorf("recalled_at = %q, want %q", *trail.Belief.RecalledAt, want)
	}
	// Ring 3 with evidence and no archive is exactly what the identity's own
	// attention pass treats as eligible, so the trail may judge fading here.
	// A belief recalled four times in ten days is well recalled: the judgment
	// must be the true one, not merely present. Fading is strength below
	// DecayAlertStrength (0.5), so a strong reading is not fading.
	if trail.Belief.Fading {
		t.Errorf("fading = true at recall %v, want false: a well-recalled belief is not fading",
			*trail.Belief.Recall)
	}

	// The same judgment, from the other side of the threshold. A belief born
	// ten days ago and recalled once must be judged fading — otherwise the
	// test above would pass for a trail that never sets the flag at all.
	faint, err := a.beliefTrailForDashboard("b_faint")
	if err != nil {
		t.Fatalf("beliefTrailForDashboard(b_faint): %v", err)
	}
	if faint.Belief.Recall == nil {
		t.Fatal("faint belief: recall is null, want a measured strength")
	}
	if !faint.Belief.Fading {
		t.Errorf("faint belief: fading = false at recall %v, want true (below %v)",
			*faint.Belief.Recall, 0.5)
	}
	if *faint.Belief.Recall >= *trail.Belief.Recall {
		t.Errorf("faint recall %v is not below the well-recalled %v: the two fixtures do not differ in the way the test assumes",
			*faint.Belief.Recall, *trail.Belief.Recall)
	}

	// The support edge, with the strength a human wrote and the words of the
	// experience it points at.
	if len(trail.Origins) != 1 {
		t.Fatalf("origins = %d, want 1", len(trail.Origins))
	}
	o := trail.Origins[0]
	if o.Strength == nil || *o.Strength != 0.9 {
		t.Errorf("origin strength = %v, want 0.9 (never 0.0, which would be a claim)", o.Strength)
	}
	if o.Kind != "experience" {
		t.Errorf("origin kind = %q, want experience", o.Kind)
	}
	if o.Text != "I looked up" {
		t.Errorf("origin text = %q, want the experience's own words", o.Text)
	}

	// A retired belief is not live, and is not "missing" either.
	old, err := a.beliefTrailForDashboard("b_old")
	if err != nil {
		t.Fatalf("beliefTrailForDashboard(b_old): %v", err)
	}
	if !old.Belief.Archived {
		t.Error("a retired belief was reported live")
	}
	// It is not eligible, so the trail may carry a recall number but must not
	// call it fading: that judgment belongs to ring 3 live beliefs only.
	if old.Belief.Fading {
		t.Error("an archived belief was called fading, a judgment the attention pass never makes")
	}
}

// A failed access read must not arrive as the measured "never recalled".
// The optional-ephemeral policy says the trail still answers, but every
// value it could not read is null rather than a default that reads as a
// finding. a review proved this defect in source at 0b6f67cd; the control
// that broke the producer found no test catching it, so this is that test.
func TestBeliefTrailReportsAnUnreadRecallAsNullNotZero(t *testing.T) {
	a, st := trailApp(t)
	db := st.DB()

	seedLedger(t, st, 1, at(10))
	if _, err := db.Exec(
		`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count,
		                      archived, first_seq, last_seq)
		 VALUES ('b_quiet', 'nothing has recalled this', 3, 0.7, 1, 0, 1, 1)`); err != nil {
		t.Fatalf("seed belief: %v", err)
	}

	// Make the access read fail, rather than finding no row. Dropping the
	// table is the honest way to do it: the query errors, which is the
	// path the producer skips.
	if _, err := db.Exec(`DROP TABLE memory_access`); err != nil {
		t.Fatalf("drop memory_access: %v", err)
	}

	trail, err := a.beliefTrailForDashboard("b_quiet")
	if err != nil {
		t.Fatalf("a failed access read must not fail the trail: %v", err)
	}
	if trail.Belief.Recall != nil {
		t.Errorf("recall = %v, want null when the access read failed", *trail.Belief.Recall)
	}
	if trail.Belief.RecallCount != nil {
		t.Errorf("recall_count = %d, want null when the access read failed: a count that was never read is not the measured zero",
			*trail.Belief.RecallCount)
	}
	if trail.Belief.RecalledAt != nil {
		t.Errorf("recalled_at = %v, want null when the access read failed", *trail.Belief.RecalledAt)
	}
	if trail.Belief.Fading {
		t.Error("fading = true, want false: no strength was measured, so no judgment may be made")
	}
}

// The other side of the same line: a successful read that finds no access
// row IS a measurement, and it is zero. Without this the test above would
// pass for a trail that never sets the count at all.
func TestBeliefTrailReportsAnAbsentAccessRowAsZero(t *testing.T) {
	a, st := trailApp(t)
	db := st.DB()

	seedLedger(t, st, 1, at(10))
	if _, err := db.Exec(
		`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count,
		                      archived, first_seq, last_seq)
		 VALUES ('b_unrecalled', 'never recalled', 3, 0.7, 1, 0, 1, 1)`); err != nil {
		t.Fatalf("seed belief: %v", err)
	}

	trail, err := a.beliefTrailForDashboard("b_unrecalled")
	if err != nil {
		t.Fatalf("beliefTrailForDashboard: %v", err)
	}
	if trail.Belief.RecallCount == nil {
		t.Fatal("recall_count is null, want a present 0: the read succeeded and found no row")
	}
	if *trail.Belief.RecallCount != 0 {
		t.Errorf("recall_count = %d, want 0", *trail.Belief.RecallCount)
	}
	if trail.Belief.RecalledAt != nil {
		t.Error("recalled_at is set, want null: a belief never recalled has no last time")
	}
}

// A belief the record does not hold and a record that is not open are two
// different facts, and the page must be able to tell them apart.
func TestBeliefTrailDistinguishesNotHeldFromNotOpen(t *testing.T) {
	a, _ := trailApp(t)

	if _, err := a.beliefTrailForDashboard("b_nonexistent"); !errors.Is(err, dashboard.ErrBeliefNotFound) {
		t.Errorf("unknown belief: err = %v, want ErrBeliefNotFound", err)
	}

	a.store = nil
	if _, err := a.beliefTrailForDashboard("b_claim"); !errors.Is(err, dashboard.ErrRecordNotOpen) {
		t.Errorf("closed record: err = %v, want ErrRecordNotOpen", err)
	}
}

// The per-arm bound is a bound on the READ, not on the output. A belief
// with more origins than the page draws must report the arm as truncated
// while still returning exactly the bound.
func TestBeliefTrailBoundsEachArmAndSaysSo(t *testing.T) {
	a, st := trailApp(t)
	db := st.DB()

	seedLedger(t, st, 1, at(10))
	seedLedger(t, st, 2, at(5))
	if _, err := db.Exec(
		`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count,
		                      archived, first_seq, last_seq)
		 VALUES ('b_many', 'many supports', 3, 0.7, 3, 0, 1, 2)`); err != nil {
		t.Fatalf("seed belief: %v", err)
	}
	for i := 0; i < trailLinkLimit+5; i++ {
		id := "e_" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if _, err := db.Exec(
			`INSERT INTO experiences (id, content, category, provenance, created_seq, created_at)
			 VALUES (?, 'support', 'observation', 'self', 2, ?)`, id, at(5)); err != nil {
			t.Fatalf("seed experience %d: %v", i, err)
		}
		if _, err := db.Exec(
			`INSERT INTO edges (id, from_id, to_id, edge_type, strength, created_seq)
			 VALUES (?, ?, 'b_many', 'SUPPORTS', 1.0, 2)`, "ed_"+id, id); err != nil {
			t.Fatalf("seed edge %d: %v", i, err)
		}
	}

	trail, err := a.beliefTrailForDashboard("b_many")
	if err != nil {
		t.Fatalf("beliefTrailForDashboard(b_many): %v", err)
	}
	if len(trail.Origins) != trailLinkLimit {
		t.Errorf("origins = %d, want exactly the bound %d", len(trail.Origins), trailLinkLimit)
	}
	if !trail.Truncated.Origins {
		t.Error("truncated.origins = false, want true: the arm was cut")
	}
	if trail.Truncated.Contradicts || trail.Truncated.Supersedes {
		t.Error("an arm that was not cut was reported truncated")
	}
}
