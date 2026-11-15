package store

import (
	"fmt"
	"testing"
	"time"
)

// workSearchRows is a record with one work session's call and notice, and
// one delivered attempt of family lead-022 with a call of its own.
func workSearchRows(t *testing.T, s *Store) {
	t.Helper()
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i, row := range []struct{ kind, details string }{
		{"tool_call", `{"tool":"shell","work_session_id":"ws-1"}`},
		{"notice", `{"work":{"session":"ws-1","event":"step"}}`},
		{"tool_call", `{"tool":"shell","work_session_id":"lead-1"}`},
	} {
		if _, err := s.db.Exec(`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at, kind, details) VALUES (?,?,?,?,?,?,?,?)`,
			fmt.Sprintf("c-%d", i+1), "default", "resident", "x", i+1, base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339Nano), row.kind, row.details); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO work_sessions (id, description, status, family, tool_calls, started_at, delivered_at, result) VALUES ('lead-1','attempt','delivered','lead-022',1,1,2,'served: delivered')`); err != nil {
		t.Fatal(err)
	}
}

// THE TWO WORK SEARCHES TAKE NO LOCK. Each reads the whole
// conversation table (FamilyAttempts once for every attempt it lists): on
// the build host, at 1,000,000 rows, 13.8 s and 888 ms. Under Store.mu.RLock every
// write that arrived meanwhile waited that long, and so did every read after
// that write, since a waiting Lock parks new RLocks; the wait grew with the
// table. Here a writer holds Store.mu throughout, and both searches must
// still answer. Holding the read lock, they wait for the writer instead.
func TestTheTwoWorkSearchesAnswerWhileAWriterHoldsTheStore(t *testing.T) {
	s := testStore(t)
	workSearchRows(t, s)
	s.mu.Lock()
	held := true
	defer func() {
		if held {
			s.mu.Unlock()
		}
	}()
	done := make(chan error, 2)
	go func() {
		_, found, err := s.WorkSessionLastActivity("ws-1")
		if err == nil && !found {
			err = fmt.Errorf("WorkSessionLastActivity found nothing for ws-1")
		}
		done <- err
	}()
	go func() {
		r, err := s.FamilyAttempts("lead-022", 20)
		if err == nil && len(r.Attempts) != 1 {
			err = fmt.Errorf("FamilyAttempts listed %d attempts, want 1", len(r.Attempts))
		}
		done <- err
	}()
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("a work search waited for the store's writer: it takes Store.mu, so every write waits out its whole scan")
		}
	}
	s.mu.Unlock()
	held = false
}

// WHAT A SEARCH SEES WITHOUT THE LOCK: the committed record, never a write
// half made. A write transaction is left open
// with a later notice of ws-1 and a second delivered attempt of lead-022 in
// it; the searches answer as if neither were there, and see both once the
// write commits.
func TestAWorkSearchSeesOnlyWhatIsCommitted(t *testing.T) {
	s := testStore(t)
	workSearchRows(t, s)
	before, _, err := s.WorkSessionLastActivity("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	later := before.Add(time.Hour)
	if _, err := tx.Exec(`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at, kind, details) VALUES ('c-9','default','resident','x',9,?,'notice','{"work":{"session":"ws-1","event":"step"}}')`,
		later.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO work_sessions (id, description, status, family, tool_calls, started_at, delivered_at, result) VALUES ('lead-2','attempt','delivered','lead-022',3,3,4,'served: delivered')`); err != nil {
		t.Fatal(err)
	}
	at, _, err := s.WorkSessionLastActivity("ws-1")
	if err != nil || !at.Equal(before) {
		t.Fatalf("a search saw a write not yet committed: last activity %v, want %v (%v)", at, before, err)
	}
	r, err := s.FamilyAttempts("lead-022", 20)
	if err != nil || len(r.Attempts) != 1 {
		t.Fatalf("a search saw an attempt not yet committed: %d attempts (%v)", len(r.Attempts), err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	at, _, err = s.WorkSessionLastActivity("ws-1")
	if err != nil || !at.Equal(later) {
		t.Fatalf("the committed notice is not seen: last activity %v, want %v (%v)", at, later, err)
	}
	if r, err = s.FamilyAttempts("lead-022", 20); err != nil || len(r.Attempts) != 2 {
		t.Fatalf("the committed attempt is not seen: %d attempts (%v)", len(r.Attempts), err)
	}
}
