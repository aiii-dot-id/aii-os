package store

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestDrillReplay(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("AII_LEDGER_DRILL"))
	if n == 0 {
		t.Skip("set AII_LEDGER_DRILL=<records>")
	}
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

	filler := strings.Repeat("the day's observation, kept as it was said ", 14)
	for i := 2; i <= n; i++ {
		kind, ring, payload := ledger.EventExperienceCreate, 3, map[string]any{
			"id": "exp_" + strconv.Itoa(i), "content": filler, "category": "observation", "provenance": "self",
		}
		switch {
		case i%10 == 0:
			kind, payload = ledger.EventBeliefUpsert, map[string]any{"id": "b_" + strconv.Itoa(i), "statement": filler[:200], "ring": 3, "confidence": 0.5}
		case i%10 == 5 && i > 10:
			kind, payload = ledger.EventEdgeCreate, map[string]any{"id": "e_" + strconv.Itoa(i), "from_id": "exp_" + strconv.Itoa(i-1), "to_id": "b_" + strconv.Itoa(i-5), "edge_type": "SUPPORTS"}
		}
		if _, err := lg.Append(kind, ring, payload, kp); err != nil {
			t.Fatal(err)
		}
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	head := carryHead(t, ledgerPath, kp)
	stream := func(yield func(*ledger.Event) error) error { return ledger.Stream(ledgerPath, yield) }
	var wal int64
	open := func() time.Duration {
		start := time.Now()
		st, err := OpenAndReplay(t.Context(), dbPath, head, stream)
		took := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(dbPath + "-wal"); err == nil {
			wal = info.Size()
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		return took
	}
	first := open()

	raw := openRawForTest(t, dbPath)
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	kept := 0
	for i := 1; i <= n; i++ {
		if i%10 == 0 {
			if _, err := tx.Exec(`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at) VALUES (?,'default','operator',?,?,'2026-10-01T00:00:00.000000000Z')`,
				fmt.Sprintf("c%d", i), filler[:120], i); err != nil {
				t.Fatal(err)
			}
			kept++
		}
		if i%100 == 1 {
			if _, err := tx.Exec(`INSERT INTO work_sessions (id, description, status, created_seq, updated_seq) VALUES (?, 'drill', 'delivered', ?, ?)`, fmt.Sprintf("ws%d", i), i, i); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`INSERT INTO outbox (id, to_role, to_identity, content, created_ms, created_seq) VALUES (?, 'operator', '', 'drill', 0, ?)`, fmt.Sprintf("m%d", i), i); err != nil {
				t.Fatal(err)
			}
			kept += 2
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	var steady []time.Duration
	for range 3 {
		steady = append(steady, open())
	}
	slices.Sort(steady)
	t.Logf("DRILL replay records=%d kept_rows=%d first=%s steady=%s per_record=%s wal_bytes=%d",
		n, kept, first.Round(time.Millisecond), steady[1].Round(time.Millisecond), (steady[1] / time.Duration(n)).Round(time.Microsecond/10), wal)
}
