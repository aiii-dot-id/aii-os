package store

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestAMirrorThatCarriedSignaturesIsRefilledWithoutThem(t *testing.T) {
	dir := t.TempDir()
	ledgerPath, dbPath := filepath.Join(dir, "ledger.jsonl"), filepath.Join(dir, "aii.db")
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(ledger.EventRing0Genesis, 0, map[string]any{
		"fingerprint": kp.Fingerprint(), "public_key": kp.PublicKeyB64(),
	}, kp); err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 6; i++ {
		if _, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]any{
			"id": "exp_" + strconv.Itoa(i), "content": "kept as it was said", "category": "observation", "provenance": "self",
		}, kp); err != nil {
			t.Fatal(err)
		}
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	head := carryHead(t, ledgerPath, kp)
	stream := func(yield func(*ledger.Event) error) error { return ledger.Stream(ledgerPath, yield) }
	open := func() *Store {
		t.Helper()
		st, err := OpenAndReplay(context.Background(), dbPath, head, stream)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		return st
	}
	st := open()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	raw := openRawForTest(t, dbPath)
	var hadColumn int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('ledger') WHERE name = 'sig'`).Scan(&hadColumn); err != nil {
		t.Fatal(err)
	}
	if hadColumn == 0 {
		if _, err := raw.Exec(`ALTER TABLE ledger ADD COLUMN sig TEXT NOT NULL DEFAULT ''`); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`UPDATE ledger SET sig = '` + strings.Repeat("s", 6172) + `'`,
		`INSERT INTO work_sessions (id, description, status, created_seq, updated_seq) VALUES ('ws1', 'kept', 'delivered', 3, 4)`,
		`INSERT INTO outbox (id, to_role, to_identity, content, created_ms, created_seq) VALUES ('m1', 'operator', '', 'kept', 0, 5)`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%s: %v", q[:40], err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	st = open()
	defer st.Close()
	if has, err := st.tableHasColumn("ledger", "sig"); err != nil || has {
		t.Fatalf("the mirror still has its signature column: %v %v", has, err)
	}
	var rows, maxSeq int64
	if err := st.DB().QueryRow(`SELECT COUNT(*), COALESCE(MAX(seq), 0) FROM ledger`).Scan(&rows, &maxSeq); err != nil {
		t.Fatal(err)
	}
	if rows != int64(head.Seq) || maxSeq != int64(head.Seq) {
		t.Fatalf("the mirror holds %d rows through %d; the record holds %d", rows, maxSeq, head.Seq)
	}
	var sessions, messages int
	if err := st.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM work_sessions WHERE id = 'ws1' AND created_seq = 3 AND updated_seq = 4),
		(SELECT COUNT(*) FROM outbox WHERE id = 'm1' AND created_seq = 5)`).Scan(&sessions, &messages); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 || messages != 1 {
		t.Fatalf("the rows citing the mirror were not kept: work session %d, message %d", sessions, messages)
	}
}
