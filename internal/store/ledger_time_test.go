package store

// LEDGER TIME: THREE OUTCOMES, KEPT APART (host, 2026-10-30 15:30Z, item 1).
//
// A caller holding a sequence needs to know which of three things is true:
// the record holds no such event, it holds one and states a time this can
// read, or it holds one and states something this cannot read. The first
// is an answer and the third is a failure, and collapsing them lets a
// corrupt row render as a belief with no birth — a claim the record does
// not make.
//
// The absent case is exercised at seq 0 because the schema makes a
// dangling birth reference impossible: beliefs.first_seq is
// NOT NULL REFERENCES ledger(seq) (schema.sql:41), so a belief cannot name
// a row that is not there. Seq 0 is the reachable absent sequence, and
// LedgerTime answers it without a query.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ledgerTimeStore(t *testing.T) *Store {
	t.Helper()
	st, err := NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// An absent sequence is an absent time, and no error: the record states
// nothing about it.
func TestLedgerTimeAtReportsAnAbsentSequenceAsAbsent(t *testing.T) {
	st := ledgerTimeStore(t)

	at, ok, err := st.LedgerTimeAt(0)
	if err != nil {
		t.Fatalf("LedgerTimeAt(0): %v, want no error: an absent sequence is an answer", err)
	}
	if ok {
		t.Error("LedgerTimeAt(0) reported a time for a sequence the record does not hold")
	}
	if !at.IsZero() {
		t.Errorf("LedgerTimeAt(0) = %v, want the zero time", at)
	}
}

// A row that exists and parses is read, and it is read in UTC.
func TestLedgerTimeAtReadsAWellFormedTime(t *testing.T) {
	st := ledgerTimeStore(t)
	if _, err := st.DB().Exec(
		`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content)
		 VALUES (1, '', '2026-10-20T06:30:00Z', 'note', 3, '{}', 'seed')`); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}

	at, ok, err := st.LedgerTimeAt(1)
	if err != nil {
		t.Fatalf("LedgerTimeAt(1): %v", err)
	}
	if !ok {
		t.Fatal("LedgerTimeAt(1) reported absent for a row the record holds")
	}
	want := time.Date(2026, 10, 20, 6, 30, 0, 0, time.UTC)
	if !at.Equal(want) {
		t.Errorf("LedgerTimeAt(1) = %v, want %v", at, want)
	}
}

// A row that exists and states something unparseable is a FAILED read,
// not an absent one. This is the distinction the host asked for: the
// record states a time, and this cannot read what it states.
func TestLedgerTimeAtFailsOnAMalformedTime(t *testing.T) {
	st := ledgerTimeStore(t)
	if _, err := st.DB().Exec(
		`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content)
		 VALUES (2, '', 'last Tuesday', 'note', 3, '{}', 'seed')`); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}

	at, ok, err := st.LedgerTimeAt(2)
	if err == nil {
		t.Fatalf("LedgerTimeAt(2) = (%v, %v, nil), want an error: a malformed timestamp is a failed read", at, ok)
	}
	if ok {
		t.Error("a malformed time was reported as a time that was read")
	}
	// The error must name what could not be read, so the page can say
	// which row rather than that something went wrong.
	if !strings.Contains(err.Error(), "ledger seq 2") {
		t.Errorf("error = %q, want it to name the sequence", err)
	}
	if !strings.Contains(err.Error(), "last Tuesday") {
		t.Errorf("error = %q, want it to name the text it could not parse", err)
	}
}
