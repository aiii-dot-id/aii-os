package store

import (
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
// .
// .
const earlierMirrorDDL = `CREATE TABLE ledger (
    seq          INTEGER PRIMARY KEY,
    prev_hash    TEXT NOT NULL,
    timestamp    TEXT NOT NULL,
    type         TEXT NOT NULL,
    author       TEXT NOT NULL,
    ring         INTEGER CHECK (ring IS NULL OR (ring >= 0 AND ring <= 3)),
    payload      TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    signature    TEXT NOT NULL,
    sig_alg      TEXT NOT NULL DEFAULT 'ML-DSA-87',
    sig_key_id   TEXT NOT NULL,
    model_id     TEXT
)`

func carryFixture(t *testing.T) (dbPath, ledgerPath string, kp *crypto.KeyPair) {
	t.Helper()
	dir := t.TempDir()
	dbPath = filepath.Join(dir, "aii.db")
	ledgerPath = filepath.Join(dir, "ledger.jsonl")
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(ledger.EventRing0Genesis, kp.Fingerprint(), 0, map[string]interface{}{
		"fingerprint": kp.Fingerprint(), "public_key": kp.PublicKeyB64(),
	}, kp); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3, map[string]interface{}{
			"id": "exp_" + string(rune('a'+i)), "content": "seen", "category": "observation", "provenance": "self",
		}, kp); err != nil {
			t.Fatal(err)
		}
	}
	lg.Close()
	st, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplayFromFile(ledgerPath); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	if _, err := st.db.Exec(`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at) VALUES ('c1','s','operator','hello',2,'2026-09-03T00:00:00Z'),('c2','s','resident','hi',3,'2026-09-03T00:00:01Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO runtime_meta (key, value, updated_at) VALUES ('active_project','p','2026-09-03T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	st.Close()
	// .
	// .
	raw := openRawForTest(t, dbPath)
	if _, err := raw.Exec(`DROP TABLE ledger`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(earlierMirrorDDL); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO ledger (seq, prev_hash, timestamp, type, author, ring, payload, content_hash, signature, sig_key_id) VALUES (1,'','t','ring0.genesis','a',0,'{}','h','s','k')`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	return dbPath, ledgerPath, kp
}

func TestCarryAcrossKeepsEveryEphemeralRowAndRebuildsTheDerived(t *testing.T) {
	dbPath, ledgerPath, kp := carryFixture(t)
	// .
	// .
	// .
	st0, err := New(dbPath)
	if err != nil {
		t.Fatalf("fixture: the runtime refused a mirror at an earlier shape — R102 rebuilds it from the record: %v", err)
	}
	if head, err := st0.MaxLedgerSeq(); err != nil || head != 1 {
		t.Fatalf("the acknowledged head must survive the mirror's rebuild: %d %v", head, err)
	}
	st0.Close()
	// .
	if _, err := ledger.Rewrap(ledgerPath, kp, "", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	report, err := CarryAcross(dbPath, ledgerPath)
	if err != nil {
		t.Fatalf("carry: %v", err)
	}
	if report.Ephemeral["conversations"] != 2 || report.Ephemeral["runtime_meta"] != 1 {
		t.Fatalf("ephemeral rows were not carried: %+v", report.Ephemeral)
	}
	if report.Derived["experiences"][1] != 3 || report.Derived["ledger"][1] != 4 {
		t.Fatalf("derived tables were not rebuilt from the record: %+v", report.Derived)
	}
	// .
	// .
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("the carried projection does not open: %v", err)
	}
	defer st.Close()
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("conversations after the carry: %d %v", n, err)
	}
	var head int64
	if err := st.db.QueryRow(`SELECT MAX(seq) FROM ledger`).Scan(&head); err != nil || head != 4 {
		t.Fatalf("mirror head %d %v, want 4", head, err)
	}
	// .
	if err := st.ReplayFromFile(ledgerPath); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("conversations after a boot replay: %d %v", n, err)
	}
}

func TestReplayClearsExactlyTheDerivedTables(t *testing.T) {
	want := DerivedTables()
	if len(want) != 11 || want[0] != "witness_receipts" || want[len(want)-1] != "ledger" {
		t.Fatalf("derived clear order: %v", want)
	}
}
