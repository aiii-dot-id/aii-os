package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
// .
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
	// .
	r.write(ledger.EventRelationshipUpsert, map[string]string{
		"id": "nova", "counterpart_name": "Nova", "counterpart_role": "peer", "relationship_type": "peer",
	})
	return r
}

func (r *outcomeRig) write(typ ledger.EventType, payload interface{}) *ledger.Event {
	r.t.Helper()
	evt, err := r.lg.Append(typ, r.kp.Fingerprint(), legalRing(r.t, typ), payload, r.kp)
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
	r.write(ledger.EventCommitmentPromised, map[string]string{"id": id, "description": description, "counterpart_id": "nova"})
}

func (r *outcomeRig) commitmentState(id, state string, rest map[string]string) *ledger.Event {
	p := map[string]string{"id": id, "state": state}
	for k, v := range rest {
		p[k] = v
	}
	return r.write(ledger.EventCommitmentStateChange, p)
}

// .
func (r *outcomeRig) observe(text string, cited ...*ledger.Event) *ledger.Event {
	var cites []map[string]interface{}
	for _, e := range cited {
		cites = append(cites, map[string]interface{}{"identity": r.kp.Fingerprint(), "seq": e.Seq, "entry_hash": e.EntryHash()})
	}
	return r.write(ledger.EventExperienceCreate, map[string]interface{}{
		"id": fmt.Sprintf("obs_%d", len(text)), "content": text, "source": "self", "cites": cites,
	})
}

// .
// .
func TestOutcomesAreReadFromTheRecordOldestFirst(t *testing.T) {
	r := newOutcomeRig(t)
	r.intention("i1", "learn the recovery drill by heart")
	r.commitment("c1", "send Nova the escrow receipt")
	r.intentionState("i1", "active", "")
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
		{dropped, "commitment", "abandoned", "repair: owed — the host was down", "send Nova the escrow receipt"},
		{mended, "commitment", "repaired", "sent a day late", "send Nova the escrow receipt"},
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

// .
// .
// .
func TestAWindowWithNoOutcomeStillMovesTheCursor(t *testing.T) {
	r := newOutcomeRig(t)
	r.intention("i1", "a")
	r.intention("i2", "b")
	last := r.intentionState("i1", "active", "")

	b, err := r.st.NextOutcomes(2)
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

// .
// .
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

// .
// .
// .
func TestAnOutcomeAlreadyObservedIsNotOfferedAgain(t *testing.T) {
	r := newOutcomeRig(t)
	r.intention("i1", "one")
	r.intention("i2", "two")
	first := r.intentionState("i1", "completed", "done one")
	second := r.intentionState("i2", "abandoned", "dropped two")
	r.observe("I finished one.", first)

	b, err := r.st.NextOutcomes(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Outcomes) != 1 || b.Outcomes[0].Seq != second.Seq {
		t.Fatalf("offered %+v, want only record %d — record %d is already observed", b.Outcomes, second.Seq, first.Seq)
	}
}

// .
// .
func TestACitationOfAnotherIdentityAtTheSameSeqDoesNotCount(t *testing.T) {
	r := newOutcomeRig(t)
	r.intention("i1", "one")
	end := r.intentionState("i1", "completed", "done")
	r.write(ledger.EventExperienceCreate, map[string]interface{}{
		"id": "obs_foreign", "content": "Nova finished hers.", "source": "self",
		"cites": []map[string]interface{}{{
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

// .
func TestTheCursorMovesOnlyFromTheValueAPassRead(t *testing.T) {
	r := newOutcomeRig(t)
	for i := 0; i < 8; i++ {
		r.intention(fmt.Sprintf("i%d", i), "an intention")
	}
	if err := r.st.PublishOutcomeCursor(0, 5); err != nil {
		t.Fatal(err)
	}
	// .
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

// .
func TestAnUnreadableCursorStartsOver(t *testing.T) {
	r := newOutcomeRig(t)
	r.intention("i1", "one")
	end := r.intentionState("i1", "completed", "done")
	r.st.mu.Lock()
	err := r.st.setRuntimeMeta(OutcomeCursorKey, "not a number")
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

// .
func TestAFailedReadIsNotAnEmptyBacklog(t *testing.T) {
	r := newOutcomeRig(t)
	// .
	// .
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

// .
// .
// .
func TestACursorAheadOfTheRecordIsNotBelieved(t *testing.T) {
	r := newOutcomeRig(t)
	r.intention("i1", "one")
	end := r.intentionState("i1", "completed", "done")
	r.st.mu.Lock()
	err := r.st.setRuntimeMeta(OutcomeCursorKey, "100000")
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

// .
// .
// .
// .
// .
func TestACitesMemberOfAnyShapeNeverStopsTheIntake(t *testing.T) {
	shapes := map[string]interface{}{
		"a string":             "record 5 of mine",
		"an array of strings":  []interface{}{"record 5 of mine"},
		"an array of numbers":  []interface{}{7},
		"an object":            map[string]interface{}{"identity": "x", "seq": 5},
		"an array of arrays":   []interface{}{[]interface{}{1, 2}},
		"an object of objects": map[string]interface{}{"a": map[string]interface{}{"identity": "x", "seq": 5}},
		"null":                 nil,
	}
	for name, cites := range shapes {
		r := newOutcomeRig(t)
		r.intention("i1", "one")
		end := r.intentionState("i1", "completed", "done")
		// .
		r.write(ledger.EventExperienceCreate, map[string]interface{}{"id": "foreign", "content": "x", "provenance": "self", "cites": cites})
		b, err := r.st.NextOutcomes(100)
		if err != nil {
			t.Errorf("cites is %s: the read FAILED: %v", name, err)
			continue
		}
		if len(b.Outcomes) != 1 || b.Outcomes[0].Seq != end.Seq {
			t.Errorf("cites is %s: offered %+v, want the one outcome — a shape that is not a citation observes nothing", name, b.Outcomes)
		}
	}
	// .
	// .
	r := newOutcomeRig(t)
	r.intention("i1", "one")
	end := r.intentionState("i1", "completed", "done")
	r.write(ledger.EventExperienceCreate, map[string]interface{}{"id": "foreign", "content": "x", "provenance": "self",
		"cites": map[string]interface{}{"a": map[string]interface{}{"identity": r.kp.Fingerprint(), "seq": end.Seq, "entry_hash": end.EntryHash()}}})
	if b, err := r.st.NextOutcomes(100); err != nil || len(b.Outcomes) != 1 {
		t.Fatalf("an object of objects naming the outcome: %+v %v, want it still offered", b.Outcomes, err)
	}
}

// .
// .
// .
// .
func TestTheReservedAreLeftOutInTheQueryNotAfterIt(t *testing.T) {
	r := newOutcomeRig(t)
	for i := 0; i < 25; i++ {
		r.write(ledger.EventExperienceCreate, map[string]interface{}{"id": fmt.Sprintf("%s%02d", OutcomeObservationPrefix, i), "content": "an outcome observation", "provenance": "self"})
	}
	r.write(ledger.EventExperienceCreate, map[string]interface{}{"id": "exp_note_a", "content": "a note", "provenance": "self"})
	r.write(ledger.EventExperienceCreate, map[string]interface{}{"id": "exp_note_b", "content": "another", "provenance": "self"})

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
