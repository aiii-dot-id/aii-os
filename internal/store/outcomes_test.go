package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// outcomeRig is a store beside a real ledger, born with a genesis that
// carries the identity's fingerprint — without it nothing is "self" and
// the already-observed check has no one to ask about.
type outcomeRig struct {
	t  *testing.T
	st *Store
	lg *ledger.Ledger
	kp *crypto.KeyPair
}

func newOutcomeRig(t *testing.T) *outcomeRig {
	t.Helper()
	dir := t.TempDir()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := New(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lg.Close(); st.Close() })
	r := &outcomeRig{t: t, st: st, lg: lg, kp: kp}
	r.write(ledger.EventRing0Genesis, map[string]string{"name": "Outcomes", "fingerprint": kp.Fingerprint()})
	// A promise is TO someone who exists.
	r.write(ledger.EventRelationshipUpsert, map[string]string{
		"id": "mira", "counterpart_name": "Mira", "counterpart_role": "peer", "relationship_type": "peer",
	})
	return r
}

func (r *outcomeRig) write(typ ledger.EventType, payload any) *ledger.Event {
	r.t.Helper()
	evt, err := r.lg.Append(typ, legalRing(r.t, typ), payload, r.kp)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.st.Materialize(evt); err != nil {
		r.t.Fatal(err)
	}
	return evt
}

func (r *outcomeRig) intention(id, statement string) {
	r.write(ledger.EventIntentionCreate, map[string]string{"id": id, "statement": statement})
}

func (r *outcomeRig) intentionState(id, state, outcome string) *ledger.Event {
	return r.write(ledger.EventIntentionStateChange, map[string]string{"id": id, "state": state, "outcome": outcome})
}

func (r *outcomeRig) commitment(id, description string) {
	r.write(ledger.EventCommitmentPromised, map[string]string{"id": id, "description": description, "counterpart_id": "mira"})
}

func (r *outcomeRig) commitmentState(id, state string, rest map[string]string) *ledger.Event {
	p := map[string]string{"id": id, "state": state}
	for k, v := range rest {
		p[k] = v
	}
	return r.write(ledger.EventCommitmentStateChange, p)
}

// observe writes one of the identity's own observations citing the events.
func (r *outcomeRig) observe(text string, cited ...*ledger.Event) *ledger.Event {
	var cites []map[string]any
	for _, e := range cited {
		cites = append(cites, map[string]any{"identity": r.kp.Fingerprint(), "seq": e.Seq, "entry_hash": e.EntryHash()})
	}
	return r.write(ledger.EventExperienceCreate, map[string]any{
		"id": fmt.Sprintf("obs_%d", len(text)), "content": text, "source": "self", "cites": cites,
	})
}

// The derived rows keep only the latest state. What happened is in the
// record: an abandonment and the repair that followed are two outcomes.
func TestOutcomesAreReadFromTheRecordOldestFirst(t *testing.T) {
	r := newOutcomeRig(t)
	r.intention("i1", "learn the recovery drill by heart")
	r.commitment("c1", "send Mira the escrow receipt")
	r.intentionState("i1", "active", "") // a transition that is not an outcome
	done := r.intentionState("i1", "completed", "ran it three times without the page")
	r.commitmentState("c1", "in_progress", nil)
	dropped := r.commitmentState("c1", "abandoned", map[string]string{"repair_state": "owed", "note": "the host was down"})
	mended := r.commitmentState("c1", "repaired", map[string]string{"result": "sent a day late"})

	b, err := r.st.NextOutcomes(100)
	if err != nil {
		t.Fatal(err)
	}
	if b.From != 0 || b.Through != mended.Seq {
		t.Fatalf("batch spans %d..%d, want 0..%d", b.From, b.Through, mended.Seq)
	}
	want := []struct {
		evt                    *ledger.Event
		kind, state, said, was string
	}{
		{done, "intention", "completed", "ran it three times without the page", "learn the recovery drill by heart"},
		{dropped, "commitment", "abandoned", "repair: owed — the host was down", "send Mira the escrow receipt"},
		{mended, "commitment", "repaired", "sent a day late", "send Mira the escrow receipt"},
	}
	if len(b.Outcomes) != len(want) {
		t.Fatalf("got %d outcomes, want %d: %+v", len(b.Outcomes), len(want), b.Outcomes)
	}
	for i, w := range want {
		o := b.Outcomes[i]
		if o.Seq != w.evt.Seq || o.EntryHash != w.evt.EntryHash() {
			t.Errorf("outcome %d is record %d %s, want %d %s — the tuple must be the record's own", i, o.Seq, o.EntryHash, w.evt.Seq, w.evt.EntryHash())
		}
		if o.Kind != w.kind || o.State != w.state || o.Said != w.said || o.Was != w.was {
			t.Errorf("outcome %d = %+v, want %s %s said %q was %q", i, o, w.kind, w.state, w.said, w.was)
		}
	}
}

// The probe is a primary-key range: a window holding no outcome still
// moves the cursor to its end, so the read stays bounded whatever the
// length of the history.
func TestAWindowWithNoOutcomeStillMovesTheCursor(t *testing.T) {
	r := newOutcomeRig(t)
	r.intention("i1", "a")
	r.intention("i2", "b")
	last := r.intentionState("i1", "active", "")

	b, err := r.st.NextOutcomes(2) // the birth and the relationship: two records, no outcome
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Outcomes) != 0 || b.Through != 2 {
		t.Fatalf("a window of two records gave %d outcomes through %d, want none through 2", len(b.Outcomes), b.Through)
	}
	if err := r.st.PublishOutcomeCursor(b.From, b.Through); err != nil {
		t.Fatal(err)
	}
	b, err = r.st.NextOutcomes(100)
	if err != nil {
		t.Fatal(err)
	}
	if b.From != 2 || b.Through != last.Seq || len(b.Outcomes) != 0 {
		t.Fatalf("the next window spans %d..%d with %d outcomes, want 2..%d with none", b.From, b.Through, len(b.Outcomes), last.Seq)
	}
}

// More outcomes than one intake takes: the cursor stops at the last one
// taken, and the rest wait for the next intake — none is skipped.
func TestAnIntakeTakesABoundedBatchAndTheRestWait(t *testing.T) {
	r := newOutcomeRig(t)
	var ends []*ledger.Event
	for i := 0; i < MaxOutcomesPerIntake+3; i++ {
		id := fmt.Sprintf("i%d", i)
		r.intention(id, "intention "+id)
		ends = append(ends, r.intentionState(id, "completed", "done "+id))
	}
	b, err := r.st.NextOutcomes(1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Outcomes) != MaxOutcomesPerIntake {
		t.Fatalf("one intake took %d outcomes, want %d", len(b.Outcomes), MaxOutcomesPerIntake)
	}
	if lastTaken := ends[MaxOutcomesPerIntake-1].Seq; b.Through != lastTaken {
		t.Fatalf("the cursor would move to %d, want the last outcome taken, %d", b.Through, lastTaken)
	}
	if err := r.st.PublishOutcomeCursor(b.From, b.Through); err != nil {
		t.Fatal(err)
	}
	b, err = r.st.NextOutcomes(1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Outcomes) != 3 || b.Outcomes[0].Seq != ends[MaxOutcomesPerIntake].Seq {
		t.Fatalf("the next intake got %d outcomes starting at %v, want the 3 that waited", len(b.Outcomes), b.Outcomes)
	}
}

// The product lands before the cursor. A crash between the two, or a
// cursor restored from an older snapshot, must not mint a second
// observation of one outcome: the record itself says it was observed.
func TestAnOutcomeAlreadyObservedIsNotOfferedAgain(t *testing.T) {
	r := newOutcomeRig(t)
	r.intention("i1", "one")
	r.intention("i2", "two")
	first := r.intentionState("i1", "completed", "done one")
	second := r.intentionState("i2", "abandoned", "dropped two")
	r.observe("I finished one.", first) // the product landed; the cursor was never published

	b, err := r.st.NextOutcomes(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Outcomes) != 1 || b.Outcomes[0].Seq != second.Seq {
		t.Fatalf("offered %+v, want only record %d — record %d is already observed", b.Outcomes, second.Seq, first.Seq)
	}
}

// Another identity's record with the same seq is not this identity's
// outcome: only a citation of SELF at that seq counts as observed.
func TestACitationOfAnotherIdentityAtTheSameSeqDoesNotCount(t *testing.T) {
	r := newOutcomeRig(t)
	r.intention("i1", "one")
	end := r.intentionState("i1", "completed", "done")
	r.write(ledger.EventExperienceCreate, map[string]any{
		"id": "obs_foreign", "content": "Mira finished hers.", "source": "self",
		"cites": []map[string]any{{
			"identity":   "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd",
			"seq":        end.Seq,
			"entry_hash": "abababababababababababababababababababababababababababababababab",
		}},
	})
	b, err := r.st.NextOutcomes(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Outcomes) != 1 || b.Outcomes[0].Seq != end.Seq {
		t.Fatalf("offered %+v, want record %d — a citation of another identity observes nothing of ours", b.Outcomes, end.Seq)
	}
}

// The cursor moves only from the value a pass read, and never back.
func TestTheCursorMovesOnlyFromTheValueAPassRead(t *testing.T) {
	r := newOutcomeRig(t)
	for i := 0; i < 8; i++ { // a record at least as long as the cursors below
		r.intention(fmt.Sprintf("i%d", i), "an intention")
	}
	if err := r.st.PublishOutcomeCursor(0, 5); err != nil {
		t.Fatal(err)
	}
	// A second pass that also started from 0 lost the race.
	if err := r.st.PublishOutcomeCursor(0, 3); !errors.Is(err, ErrCursorMoved) {
		t.Fatalf("a pass that started from a stale cursor got %v, want ErrCursorMoved", err)
	}
	if got, _ := r.st.OutcomeCursor(); got != 5 {
		t.Fatalf("the cursor reads %d after a lost race, want 5", got)
	}
	if err := r.st.PublishOutcomeCursor(5, 4); err == nil || errors.Is(err, ErrCursorMoved) {
		t.Fatalf("moving the cursor back got %v, want a refusal of its own", err)
	}
	if err := r.st.PublishOutcomeCursor(5, 9); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.st.OutcomeCursor(); got != 9 {
		t.Fatalf("the cursor reads %d, want 9", got)
	}
}

// A cursor this reader cannot read repeats work; it never skips any.
func TestAnUnreadableCursorStartsOver(t *testing.T) {
	r := newOutcomeRig(t)
	r.intention("i1", "one")
	end := r.intentionState("i1", "completed", "done")
	r.st.mu.Lock()
	err := setRuntimeMeta(r.st.w(), OutcomeCursorKey, "not a number")
	r.st.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.st.NextOutcomes(100)
	if err != nil {
		t.Fatal(err)
	}
	if b.From != 0 || len(b.Outcomes) != 1 || b.Outcomes[0].Seq != end.Seq {
		t.Fatalf("from an unreadable cursor the batch is %+v, want the whole record considered again", b)
	}
	if err := r.st.PublishOutcomeCursor(b.From, b.Through); err != nil {
		t.Fatalf("publishing over an unreadable cursor: %v", err)
	}
}

// A read that fails is an error, never an empty backlog.
func TestAFailedReadIsNotAnEmptyBacklog(t *testing.T) {
	r := newOutcomeRig(t)
	// The store owns the read and its bound: a window of nothing would be
	// a probe that never moves, and a negative one is no LIMIT at all.
	for _, window := range []int{0, -1} {
		if _, err := r.st.NextOutcomes(window); err == nil {
			t.Fatalf("a window of %d was accepted", window)
		}
	}
	r.st.Close()
	if _, err := r.st.NextOutcomes(100); err == nil {
		t.Fatal("a closed database read as an empty backlog")
	}
}

// runtime_meta is not rebuilt with the mirror. A cursor ahead of the
// record it counts was not published over this record; believed, it would
// skip every outcome up to itself, in silence.
func TestACursorAheadOfTheRecordIsNotBelieved(t *testing.T) {
	r := newOutcomeRig(t)
	r.intention("i1", "one")
	end := r.intentionState("i1", "completed", "done")
	r.st.mu.Lock()
	err := setRuntimeMeta(r.st.w(), OutcomeCursorKey, "100000")
	r.st.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.st.NextOutcomes(100)
	if err != nil {
		t.Fatal(err)
	}
	if b.From != 0 || len(b.Outcomes) != 1 || b.Outcomes[0].Seq != end.Seq {
		t.Fatalf("with a cursor ahead of the record the batch is %+v, want the record considered from its start", b)
	}
	if err := r.st.PublishOutcomeCursor(b.From, b.Through); err != nil {
		t.Fatalf("publishing over a disbelieved cursor: %v", err)
	}
	if got, _ := r.st.OutcomeCursor(); got != end.Seq {
		t.Fatalf("the cursor reads %d, want %d", got, end.Seq)
	}
}

// REPLAY NEVER JUDGES cites: a record another writer admitted may carry
// the member in any shape. One such record after an outcome must not turn
// the already-observed read into an SQL error — that is a failed read, a
// failed read publishes nothing, and every intake behind it would stop
// for good.
func TestACitesMemberOfAnyShapeNeverStopsTheIntake(t *testing.T) {
	shapes := map[string]any{
		"a string":             "record 5 of mine",
		"an array of strings":  []any{"record 5 of mine"},
		"an array of numbers":  []any{7},
		"an object":            map[string]any{"identity": "x", "seq": 5},
		"an array of arrays":   []any{[]any{1, 2}},
		"an object of objects": map[string]any{"a": map[string]any{"identity": "x", "seq": 5}},
		"null":                 nil,
	}
	for name, cites := range shapes {
		r := newOutcomeRig(t)
		r.intention("i1", "one")
		end := r.intentionState("i1", "completed", "done")
		// Materialize is replay's path: the grammar is never asked.
		r.write(ledger.EventExperienceCreate, map[string]any{"id": "foreign", "content": "x", "provenance": "self", "cites": cites})
		b, err := r.st.NextOutcomes(100)
		if err != nil {
			t.Errorf("cites is %s: the read FAILED: %v", name, err)
			continue
		}
		if len(b.Outcomes) != 1 || b.Outcomes[0].Seq != end.Seq {
			t.Errorf("cites is %s: offered %+v, want the one outcome — a shape that is not a citation observes nothing", name, b.Outcomes)
		}
	}
	// An object of objects that happens to name this identity and this seq
	// is still not a citation: the member must be the grammar's array.
	r := newOutcomeRig(t)
	r.intention("i1", "one")
	end := r.intentionState("i1", "completed", "done")
	r.write(ledger.EventExperienceCreate, map[string]any{"id": "foreign", "content": "x", "provenance": "self",
		"cites": map[string]any{"a": map[string]any{"identity": r.kp.Fingerprint(), "seq": end.Seq, "entry_hash": end.EntryHash()}}})
	if b, err := r.st.NextOutcomes(100); err != nil || len(b.Outcomes) != 1 {
		t.Fatalf("an object of objects naming the outcome: %+v %v, want it still offered", b.Outcomes, err)
	}
}

// DREAM'S VIEW OF THE RAW QUEUE leaves out what is reserved for
// CONSOLIDATE, and the exclusion is in the QUERY: filtered after a limit,
// a queue whose oldest entries were all reserved would show DREAM nothing
// while unreserved material waited behind them.
func TestTheReservedAreLeftOutInTheQueryNotAfterIt(t *testing.T) {
	r := newOutcomeRig(t)
	for i := 0; i < 25; i++ {
		r.write(ledger.EventExperienceCreate, map[string]any{"id": fmt.Sprintf("%s%02d", OutcomeObservationPrefix, i), "content": "an outcome observation", "provenance": "self"})
	}
	r.write(ledger.EventExperienceCreate, map[string]any{"id": "exp_note_a", "content": "a note", "provenance": "self"})
	r.write(ledger.EventExperienceCreate, map[string]any{"id": "exp_note_b", "content": "another", "provenance": "self"})

	got, err := r.st.ListRawExperiencesExcept(20, OutcomeObservationPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "exp_note_a" || got[1].ID != "exp_note_b" {
		t.Fatalf("DREAM's view of the queue is %d experiences, want the two notes behind twenty-five reserved observations", len(got))
	}
	all, _ := r.st.ListRawExperiences(100)
	if len(all) != 27 {
		t.Fatalf("CONSOLIDATE's view of the queue is %d, want all 27", len(all))
	}
}
