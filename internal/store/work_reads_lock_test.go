package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// growingReadRows is a work session ws_g with n identical calls of ls on
// the record, each with its result, made the way a turn makes them.
func growingReadRows(t *testing.T, s *Store, n int) {
	t.Helper()
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-op"), "operator")
	if err := s.StartWorkSession(ctx, "ws_g", "work"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		turn := fmt.Sprintf("turn-g-%d", i)
		if err := s.RecordToolStart(turn, 1, "main", "m", "", "ls", `{"path":"a"}`); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordToolDoneMeasured(turn, 1, "ls", `{"path":"a"}`, "x", interaction.Succeeded, false, ToolMeasurement{}); err != nil {
			t.Fatal(err)
		}
	}
}

// growingReads reads ws_g by each of the three reads: its calls by count,
// the calls in its work record, and whether it reads as stuck.
func growingReads(s *Store) (counted, recorded int, stuck string, err error) {
	if counted, err = s.SessionToolCalls("ws_g"); err != nil {
		return
	}
	parts, err := s.WorkRecordOf([]string{"ws_g"})
	if err != nil {
		return
	}
	recorded = len(parts["ws_g"].Calls)
	stuck, _, err = s.SessionStuck("ws_g", DefaultStuckThresholds)
	return
}

// THE THREE READS THAT GREW TAKE NO LOCK. Each reads the conversation table by JSON paths no index
// holds: on the build host, at 1,000,000 rows, SessionStuck 3.9 s,
// WorkRecordOf 1.26 s and SessionToolCalls 379 ms, each growing with the
// table. Under Store.mu.RLock every write that arrived meanwhile waited for
// it. Here a writer holds Store.mu throughout, and all three must still
// answer, and answer with what the record holds. Holding the read lock,
// they wait for the writer instead.
func TestTheThreeGrowingReadsAnswerWhileAWriterHoldsTheStore(t *testing.T) {
	s := testStore(t)
	growingReadRows(t, s, 4)
	s.mu.Lock()
	held := true
	defer func() {
		if held {
			s.mu.Unlock()
		}
	}()
	done := make(chan error, 3)
	go func() {
		n, err := s.SessionToolCalls("ws_g")
		if err == nil && n != 4 {
			err = fmt.Errorf("SessionToolCalls counted %d calls, want 4", n)
		}
		done <- err
	}()
	go func() {
		parts, err := s.WorkRecordOf([]string{"ws_g"})
		if err == nil && len(parts["ws_g"].Calls) != 4 {
			err = fmt.Errorf("WorkRecordOf read %d calls, want 4", len(parts["ws_g"].Calls))
		}
		done <- err
	}()
	go func() {
		stuck, _, err := s.SessionStuck("ws_g", DefaultStuckThresholds)
		if err == nil && stuck == "" {
			err = fmt.Errorf("SessionStuck did not read four identical calls as stuck")
		}
		done <- err
	}()
	for range 3 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("a growing read waited for the store's writer: it takes Store.mu, so every write waits out its whole scan")
		}
	}
	s.mu.Unlock()
	held = false
}

// WHAT THE THREE SEE WITHOUT THE LOCK: the committed record, never a write
// half made. Three identical calls are on the record; a write transaction
// is left open with a fourth, call and result. The reads answer as if it
// were not there (three calls, not stuck), and see it once it commits
// (four calls, stuck).
func TestTheThreeGrowingReadsSeeOnlyWhatIsCommitted(t *testing.T) {
	s := testStore(t)
	growingReadRows(t, s, 3)
	counted, recorded, stuck, err := growingReads(s)
	if err != nil || counted != 3 || recorded != 3 || stuck != "" {
		t.Fatalf("before the write: %d counted, %d recorded, stuck %q (%v); want 3, 3, not stuck", counted, recorded, stuck, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// The fourth call is the third's two rows again under new ids, in one
	// statement, so the result's related_id names the copied call.
	if _, err := tx.Exec(`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at, project_id, kind, turn_id,
		related_id, source_kind, source_id, recorded_at, occurred_at, outcome, details)
		SELECT id || '-4', session_id, role, content, turn_seq + 1000, created_at, project_id, kind, 'turn-g-4',
		CASE WHEN related_id IS NULL THEN NULL ELSE related_id || '-4' END, source_kind, source_id || '-4', recorded_at, occurred_at, outcome, details
		FROM conversations WHERE turn_id = 'turn-g-3' AND kind IN ('tool_call','tool_result')`); err != nil {
		t.Fatal(err)
	}
	counted, recorded, stuck, err = growingReads(s)
	if err != nil || counted != 3 || recorded != 3 || stuck != "" {
		t.Fatalf("a read saw a write not yet committed: %d counted, %d recorded, stuck %q (%v); want 3, 3, not stuck", counted, recorded, stuck, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	counted, recorded, stuck, err = growingReads(s)
	if err != nil || counted != 4 || recorded != 4 || stuck == "" {
		t.Fatalf("the committed call is not seen: %d counted, %d recorded, stuck %q (%v); want 4, 4, stuck", counted, recorded, stuck, err)
	}
}
