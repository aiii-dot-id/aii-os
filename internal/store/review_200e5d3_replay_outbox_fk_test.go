package store

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
// .
// .
func TestReview200e5d3ReplayOutboxFK(t *testing.T) {
	for _, tc := range []struct {
		name      string
		linked    bool
		delivered bool
	}{
		{name: "null_created_seq_control"},
		{name: "fk_created_seq_pending", linked: true},
		{name: "fk_created_seq_delivered", linked: true, delivered: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "synthetic.db")
			s, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			evt := ledger.Event{
				Seq: 1, Prev: "synthetic-prev", Timestamp: "2026-09-04T00:00:00Z",
				Type: ledger.EventBeliefUpsert, Ring: 3,
				Payload: json.RawMessage(`{"id":"prior-belief","statement":"prior admitted projection","ring":3,"confidence":0.9}`),
				Content: "synthetic-content-hash", Sig: "synthetic-signature",
			}
			if err := s.ReplayAll(EventSlice([]ledger.Event{evt})); err != nil {
				t.Fatalf("initial replay: %v", err)
			}
			var createdSeq *uint64
			if tc.linked {
				createdSeq = &evt.Seq
			}
			if err := s.AddOutboxMessage("retained-message", "operator", "", "retain every field", createdSeq); err != nil {
				t.Fatalf("create outbox through public API: %v", err)
			}
			if tc.delivered {
				if err := s.MarkDelivered("retained-message", "synthetic-delivery"); err != nil {
					t.Fatal(err)
				}
			}
			before := review200e5d3Snapshot(t, s)
			// .
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = New(path)
			if err != nil {
				t.Fatalf("reopen populated current-schema DB: %v", err)
			}
			if got := review200e5d3Snapshot(t, s); !reflect.DeepEqual(got, before) {
				t.Fatalf("production New changed fixture: before=%v after=%v", before, got)
			}
			var fk, linked int
			if err := s.DB().QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
				t.Fatalf("FK enforcement=%d, err=%v", fk, err)
			}
			if err := s.DB().QueryRow(`SELECT count(*) FROM outbox WHERE created_seq = 1`).Scan(&linked); err != nil {
				t.Fatal(err)
			}
			if (linked == 1) != tc.linked {
				t.Fatalf("wrong outbox linkage: linked=%d", linked)
			}
			review200e5d3CheckFK(t, s)
			sourceCalls := 0
			replayErr := s.ReplayAll(func(yield func(*ledger.Event) error) error {
				sourceCalls++
				return yield(&evt)
			})
			if s.txh != nil {
				t.Fatal("ReplayAll left its transaction handle set")
			}
			after := review200e5d3Snapshot(t, s)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("replay changed original rows: before=%v after=%v, err=%v", before, after, replayErr)
			}
			review200e5d3CheckFK(t, s)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = New(path)
			if err != nil {
				t.Fatalf("reopen after replay: %v", err)
			}
			if got := review200e5d3Snapshot(t, s); !reflect.DeepEqual(got, before) {
				t.Fatalf("state not preserved durably: before=%v after=%v", before, got)
			}
			review200e5d3CheckFK(t, s)
			t.Logf("fk=%d linked_rows=%d delivered=%v source_calls=%d replay_error=%v", fk, linked, tc.delivered, sourceCalls, replayErr)
			t.Log("PRESERVED: complete ledger, beliefs, outbox rows/columns (including delivery metadata and created_seq), before/after replay and after production reopen; foreign_key_check clean; txh reset")
			if replayErr != nil {
				if !tc.linked || !strings.Contains(replayErr.Error(), "clear ledger: constraint failed: FOREIGN KEY constraint failed (787)") || !strings.Contains(replayErr.Error(), "rebuild rolled back") || sourceCalls != 0 {
					t.Fatalf("unexpected failure mechanism: %v, source_calls=%d", replayErr, sourceCalls)
				}
				t.Errorf("REGRESSION NOT FIXED: valid retained FK-bound outbox row prevents full replay: %v", replayErr)
			} else if sourceCalls != 1 {
				t.Fatalf("successful replay did not consume source exactly once: %d", sourceCalls)
			}
		})
	}
}

// .
// .
// .
func review200e5d3Snapshot(t *testing.T, s *Store) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, table := range []string{"ledger", "beliefs", "outbox"} {
		rows, err := s.DB().Query("SELECT * FROM " + table + " ORDER BY 1")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		var values [][]any
		for rows.Next() {
			row := make([]any, len(columns))
			ptrs := make([]any, len(columns))
			for i := range row {
				ptrs[i] = &row[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			values = append(values, row)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if len(values) != 1 {
			t.Fatalf("%s fixture has %d rows, want exactly one", table, len(values))
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		out[table] = string(encoded)
	}
	return out
}

func review200e5d3CheckFK(t *testing.T, s *Store) {
	t.Helper()
	rows, err := s.DB().Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("fixture has a foreign-key violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
