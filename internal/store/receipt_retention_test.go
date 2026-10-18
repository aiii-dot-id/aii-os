package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedReceipt(t *testing.T, s *Store, id string, at time.Time) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO plugin_receipts (receipt_id, plugin_id, operation, target, success, receipt_json, created_at)
		VALUES (?, 'p', 'kv.put', 'k', 1, '{"audit_persisted":true}', ?)`, id, at.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func receiptIDs(t *testing.T, s *Store) string {
	t.Helper()
	rows, err := s.DB().Query(`SELECT receipt_id FROM plugin_receipts ORDER BY receipt_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(ids, " ")
}

// THE DAILY PASS PRUNES THE RECEIPTS PAST 90 DAYS AND KEEPS THE REST. The
// table gained a row per answered host call and was never pruned. A
// receipt the broker writes today, one a day inside the window and the
// newest one past it are told apart by Housekeep itself, and the report
// says how many went; a second pass has nothing to prune.
func TestHousekeepPrunesPluginReceiptsPastNinetyDays(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	day := 24 * time.Hour
	seedReceipt(t, s, "a-old", now.Add(-400*day))
	seedReceipt(t, s, "b-past", now.Add(-91*day))
	seedReceipt(t, s, "c-inside", now.Add(-89*day))
	if err := s.AppendPluginReceipt("d-today", "p", "kv.put", "k", true, []byte(`{"audit_persisted":true}`)); err != nil {
		t.Fatal(err)
	}

	note, err := s.Housekeep(t.Context())
	if err != nil {
		t.Fatalf("housekeep: %v", err)
	}
	if !strings.Contains(note, "plugin receipts pruned 2") {
		t.Fatalf("the report does not say what the window took: %q", note)
	}
	if got := receiptIDs(t, s); got != "c-inside d-today" {
		t.Fatalf("receipts after the pass = %q, want the two inside the window", got)
	}
	note, err = s.Housekeep(t.Context())
	if err != nil || strings.Contains(note, "plugin receipts") {
		t.Fatalf("a second pass found more to prune: %q %v", note, err)
	}
}

// THE WINDOW IS 90 DAYS TO THE MILLISECOND, COMPARED AS TIME. A receipt
// written exactly 90 days before now is kept and one a millisecond older
// goes. created_at is RFC 3339 with a fraction of varying width, which
// sorts wrongly as text: a receipt half a second inside the window
// ("…12:00:00.5Z") sorts before the cutoff ("…12:00:00Z").
func TestThePluginReceiptWindowIsNinetyDaysToTheMillisecond(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "boundary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-90 * 24 * time.Hour)
	seedReceipt(t, s, "a-before-by-1ms", cutoff.Add(-time.Millisecond))
	seedReceipt(t, s, "b-before-by-half-a-second", cutoff.Add(-500*time.Millisecond))
	seedReceipt(t, s, "c-at-the-cutoff", cutoff)
	seedReceipt(t, s, "d-after-by-1ms", cutoff.Add(time.Millisecond))
	seedReceipt(t, s, "e-after-by-half-a-second", cutoff.Add(500*time.Millisecond))
	conn, err := s.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	n, err := prunePluginReceipts(t.Context(), conn, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := receiptIDs(t, s); n != 2 || got != "c-at-the-cutoff d-after-by-1ms e-after-by-half-a-second" {
		t.Fatalf("pruned %d, kept %q; want the two before the cutoff gone and the rest kept", n, got)
	}
}
