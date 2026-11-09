package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// trail_proofs_test.go — the three proofs owed after the host's 16:24Z
// note (2026-10-30). Each one is the exact case the host named, and each
// is paired with the outcome it must NOT produce, because a test that
// cannot tell the two apart guards nothing.

func proofsStore(t *testing.T) *Store {
	t.Helper()
	st, err := NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// ITEM 3, THE CONTROL THE HOST NAMED: a row that EXISTS with ts = ”.
//
// LedgerTimeAt must treat that as a failed read naming the sequence, not
// as an absent row. The distinction is the whole of item 3: an absent row
// is an answer ("the record holds nothing here") and an empty ts is a
// failure ("the record holds something this cannot read"). A caller that
// collapsed them would date a corrupt row as though it were absent.
//
// This is the case that is only reachable by writing the row directly:
// the ledger never appends an empty ts, so a fixture built through
// produced events cannot reach it. That is why it is seeded here.
func TestLedgerTimeAtFailsOnAPresentButEmptyTime(t *testing.T) {
	st := proofsStore(t)
	if _, err := st.DB().Exec(
		`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content)
		 VALUES (7, '', '', 'note', 3, '{}', 'seed')`); err != nil {
		t.Fatalf("seed ledger with an empty ts: %v", err)
	}

	// LedgerTime keeps presence apart from the string: the row is there,
	// and what it states is empty.
	ts, present, err := st.LedgerTime(7)
	if err != nil {
		t.Fatalf("LedgerTime(7): %v, want no error: reading a present row succeeds", err)
	}
	if !present {
		t.Fatal("LedgerTime(7) reported absent for a row the record holds: presence was collapsed into the string")
	}
	if ts != "" {
		t.Fatalf("LedgerTime(7) = %q, want the empty string the row states", ts)
	}

	// And parsed, it is a failure rather than an absence.
	at, ok, err := st.LedgerTimeAt(7)
	if err == nil {
		t.Fatalf("LedgerTimeAt(7) = (%v, %v, nil), want an error: a present but empty ts is a failed read, not an absent row", at, ok)
	}
	if ok {
		t.Error("a present but empty ts was reported as a time that was read")
	}
	if !at.IsZero() {
		t.Errorf("LedgerTimeAt(7) = %v on failure, want the zero time", at)
	}
	// The error must name the row, so a page can say which one.
	if !strings.Contains(err.Error(), "ledger seq 7") {
		t.Errorf("error = %q, want it to name the sequence", err)
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %q, want it to say the ts is empty", err)
	}
}

// ITEM 2: a citation that is PRESENT but not in the grammar is a failed
// read, and a citation of the wrong shape is too. Both are distinguished
// from "this note names no turn", which is an answer.
func TestExperienceDreamedTurnDistinguishesUnreadableFromUncited(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		cited   bool
		wantErr bool
	}{
		{
			name:    "cited",
			payload: `{"dreamed_through":{"turn":"turn_7f3a2b1c","position":44}}`,
			cited:   true,
		},
		{
			name:    "uncited: the note names no turn",
			payload: `{}`,
			cited:   false,
		},
		{
			name:    "uncited: dreamed_through present, turn absent",
			payload: `{"dreamed_through":{"position":44}}`,
			cited:   false,
		},
		{
			// The record cannot produce an empty turn (the writing grammar
			// requires 1..64 characters), so one here is a payload that is
			// not what the record admits. Rendering "" as a turn would put
			// a visible empty citation on the page.
			name:    "unreadable: a present but empty turn",
			payload: `{"dreamed_through":{"turn":"","position":44}}`,
			wantErr: true,
		},
		{
			name:    "unreadable: a turn of the wrong type",
			payload: `{"dreamed_through":{"turn":12345,"position":44}}`,
			wantErr: true,
		},
		{
			// The ledger's schema forbids a payload that is not JSON at all
			// (CHECK json_valid(payload), schema.sql), so that case cannot
			// be seeded and the unmarshal branch is not reachable that way.
			// It IS reachable through valid JSON of the wrong shape: this
			// unmarshals into a struct, and a string where an object is
			// expected is an error rather than an absent citation.
			name:    "unreadable: dreamed_through of the wrong shape",
			payload: `{"dreamed_through":"turn_7f3a2b1c"}`,
			wantErr: true,
		},
	}

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := proofsStore(t)
			seq := int64(i + 1)
			if _, err := st.DB().Exec(
				`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content)
				 VALUES (?, '', '2026-10-25T00:00:00Z', 'experience.create', 3, ?, 'seed')`,
				seq, c.payload); err != nil {
				t.Fatalf("seed ledger: %v", err)
			}
			if _, err := st.DB().Exec(
				`INSERT INTO experiences (id, content, category, provenance, created_seq, created_at)
				 VALUES ('e1', 'I read the transcript', 'reflection', 'dream', ?, '2026-10-25T00:00:00Z')`,
				seq); err != nil {
				t.Fatalf("seed experience: %v", err)
			}

			turn, cited, err := st.ExperienceDreamedTurn("e1")
			if c.wantErr {
				if err == nil {
					t.Fatalf("ExperienceDreamedTurn = (%q, %v, nil), want an error: %s", turn, cited, c.name)
				}
				if cited {
					t.Error("an unreadable citation was reported as a cited turn")
				}
				return
			}
			if err != nil {
				t.Fatalf("ExperienceDreamedTurn: %v, want no error: %s", err, c.name)
			}
			if cited != c.cited {
				t.Fatalf("cited = %v, want %v: %s", cited, c.cited, c.name)
			}
			if !cited && turn != "" {
				t.Errorf("turn = %q on an uncited note, want empty", turn)
			}
		})
	}
}

// ITEM 1, THE CASE NOT YET COVERED: a superseding relation beside a FULL
// origin arm. The starvation fixture proved the newest contradiction
// survives fifty origins; this proves the same for the supersede arms,
// which are read from the opposite direction and would be dropped by a
// bound applied to the union rather than per arm.
func TestTheSupersedingRelationSurvivesAFullOriginArm(t *testing.T) {
	s := testStore(t)

	var ev []ledger.Event
	var refs []map[string]string
	seq := uint64(1)
	for i := 0; i < 50; i++ {
		id := "exp_o_" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		ev = append(ev, *seqEvent(t, s, seq, ledger.EventExperienceCreate, map[string]any{
			"id": id, "content": "observation " + id, "provenance": "self",
		}))
		refs = append(refs, map[string]string{"kind": "experience", "id": id})
		seq++
	}
	// The belief that is superseded, and carries a full origin arm.
	ev = append(ev, *seqEvent(t, s, seq, ledger.EventBeliefUpsert, map[string]any{
		"id": "b_old", "statement": "the earlier account", "ring": 3,
		"confidence": 0.5, "origin_refs": refs,
	}))
	seq++
	// The belief that supersedes it, created later.
	ev = append(ev, *seqEvent(t, s, seq, ledger.EventBeliefUpsert, map[string]any{
		"id": "b_new", "statement": "the later account", "ring": 3, "confidence": 0.7,
	}))
	seq++
	ev = append(ev, *seqEvent(t, s, seq, ledger.EventEdgeCreate, map[string]any{
		"id": "e_supersede", "from_id": "b_new", "to_id": "b_old",
		"edge_type": "SUPERSEDES", "strength": 0.9,
	}))

	fresh := testStore(t)
	if err := fresh.ReplayAll(EventSlice(ev)); err != nil {
		t.Fatalf("ReplayAll: %v", err)
	}

	arms, err := fresh.TrailArmsForBelief("b_old", 50)
	if err != nil {
		t.Fatalf("TrailArmsForBelief: %v", err)
	}
	if len(arms.Origins) != 50 {
		t.Fatalf("origins = %d, want 50: the arm was not full, so this proves nothing", len(arms.Origins))
	}
	// b_old is the belief being superseded, so the relation arrives on
	// its superseded_by arm: the edge runs b_new -> b_old.
	if len(arms.SupersededBy) != 1 {
		t.Fatalf("superseded_by = %d links, want 1: a superseding relation was dropped beside a full origin arm", len(arms.SupersededBy))
	}
	if arms.SupersededBy[0].ID != "e_supersede" {
		t.Fatalf("superseded_by[0] = %q, want e_supersede", arms.SupersededBy[0].ID)
	}
	if arms.SupersededBy[0].Strength != 0.9 {
		t.Errorf("strength = %v, want 0.9: the edge's own strength, not a default", arms.SupersededBy[0].Strength)
	}
}
