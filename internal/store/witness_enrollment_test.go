package store

import (
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// WitnessEnrollment reads the record, not a projection the runtime keeps:
// the genesis time, and the identity every system.witnessed record names —
// a re-wrap footnote as much as a live receipt — in the order first named,
// with the first such record's time. This is what a database rebuilt from
// the record alone still knows about the identity's witness enrollment.
func TestWitnessEnrollmentIsReadFromTheRecord(t *testing.T) {
	st, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	put := func(seq int, ts string, typ ledger.EventType, payload string) {
		t.Helper()
		if _, err := st.db.Exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '0', ?, ?, NULL, ?, '')`,
			seq, ts, string(typ), payload); err != nil {
			t.Fatal(err)
		}
	}
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	if ids, born, first, err := st.WitnessEnrollment(); err != nil || ids != nil || !born.IsZero() || !first.IsZero() {
		t.Fatalf("an empty record names nothing: %v %v %v %v", ids, born, first, err)
	}

	put(1, "2026-08-20T13:32:14.157003771Z", ledger.EventRing0Genesis, `{"name":"x"}`)
	put(2, "2026-08-21T09:00:00Z", ledger.EventExperienceCreate, `{"id":"e"}`)
	if ids, born, first, err := st.WitnessEnrollment(); err != nil || ids != nil || !born.Equal(at("2026-08-20T13:32:14.157003771Z")) || !first.IsZero() {
		t.Fatalf("a record born and never witnessed: ids=%v born=%v first=%v err=%v", ids, born, first, err)
	}

	// A re-wrap footnote first — the receipt it carried attests the
	// record's earlier shape, and still names the identity — then a receipt.
	put(107, "2026-08-23T16:38:26.754234091Z", ledger.EventSystemWitnessed, `{"receipt_before_rewrap":{"identity_id":"did:aiii:identity:sha256:aaaa","ledger_ordinal":106}}`)
	// Right after a re-wrap every witness record may be a footnote: the
	// identity is still named, or a lost database would mint a stranger.
	if ids, _, first, err := st.WitnessEnrollment(); err != nil || len(ids) != 1 || ids[0] != "did:aiii:identity:sha256:aaaa" || !first.Equal(at("2026-08-23T16:38:26.754234091Z")) {
		t.Fatalf("a record whose only witness record is a footnote still names its identity: %v %v %v", ids, first, err)
	}
	put(1034, "2026-09-03T15:34:00Z", ledger.EventSystemWitnessed, `{"receipt":{"identity_id":"did:aiii:identity:sha256:aaaa","ledger_ordinal":1033}}`)
	ids, born, first, err := st.WitnessEnrollment()
	if err != nil || len(ids) != 1 || ids[0] != "did:aiii:identity:sha256:aaaa" {
		t.Fatalf("the footnote and the receipt name one identity: %v %v", ids, err)
	}
	if !born.Equal(at("2026-08-20T13:32:14.157003771Z")) || !first.Equal(at("2026-08-23T16:38:26.754234091Z")) {
		t.Fatalf("born %v and first witnessed %v: the first witness record is the footnote's", born, first)
	}

	// A second identity named later is reported, in order: the record is forked.
	put(1200, "2026-09-10T00:00:00Z", ledger.EventSystemWitnessed, `{"receipt":{"identity_id":"did:aiii:identity:sha256:bbbb","ledger_ordinal":1199}}`)
	if ids, _, _, err := st.WitnessEnrollment(); err != nil || len(ids) != 2 || ids[0] != "did:aiii:identity:sha256:aaaa" || ids[1] != "did:aiii:identity:sha256:bbbb" {
		t.Fatalf("two identities named, first named first: %v %v", ids, err)
	}
}
