package store

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestMemoryDecisionsAreLoggedAndBounded(t *testing.T) {
	s := testStore(t)
	if err := s.RecordMemoryDecision(MemoryDecision{Kind: "salience", Facility: "consolidate", Decision: "memo", Score: 0.2, Record: map[string]any{"candidate": "the tide"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMemoryDecision(MemoryDecision{Kind: "rhythm", Facility: "consolidate", Decision: "run", Record: map[string]any{"reason": "capacity"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMemoryDecision(MemoryDecision{Kind: "guess", Facility: "x", Decision: "y"}); err == nil {
		t.Fatal("an unknown kind must be refused")
	}
	got, err := recentDecisions(t, s, "salience", 10)
	if err != nil || len(got) != 1 || got[0].Decision != "memo" || got[0].Record["candidate"] != "the tide" || got[0].DecidedAt.IsZero() {
		t.Fatalf("salience decisions = %+v %v", got, err)
	}
	// The log at its bound and past it, seeded in one transaction: the
	// bound is the constant the runtime uses. The one decision recorded
	// through the writer is what prunes.
	tx, err := s.w().Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < memoryDecisionsKeep+19; i++ {
		if _, err := tx.Exec(`INSERT INTO memory_decisions (decided_at, kind, facility, decision, record) VALUES (?, 'rhythm', 'dream', 'skip', ?)`,
			time.Now().UTC().Format(time.RFC3339Nano), fmt.Sprintf(`{"i":%d}`, i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMemoryDecision(MemoryDecision{Kind: "rhythm", Facility: "dream", Decision: "skip", Record: map[string]any{"i": memoryDecisionsKeep + 19}}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM memory_decisions WHERE kind = 'rhythm'`).Scan(&n); err != nil || n != memoryDecisionsKeep {
		t.Fatalf("rhythm decisions kept = %d (%v), want the bound %d", n, err, memoryDecisionsKeep)
	}
	newest, _ := recentDecisions(t, s, "rhythm", 1)
	if fmt.Sprint(newest[0].Record["i"]) != fmt.Sprint(memoryDecisionsKeep+19) {
		t.Fatalf("the newest must survive the prune: %+v", newest[0].Record)
	}
}

// recentDecisions reads one kind's newest decisions, newest first.
func recentDecisions(t *testing.T, s *Store, kind string, n int) ([]MemoryDecision, error) {
	t.Helper()
	rows, err := s.db.Query(`SELECT decided_at, kind, facility, decision, seq, score, record FROM memory_decisions WHERE kind = ? ORDER BY id DESC LIMIT ?`, kind, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryDecision
	for rows.Next() {
		var d MemoryDecision
		var at, record string
		if err := rows.Scan(&at, &d.Kind, &d.Facility, &d.Decision, &d.Seq, &d.Score, &record); err != nil {
			return nil, err
		}
		d.DecidedAt, _ = time.Parse(time.RFC3339Nano, at)
		_ = json.Unmarshal([]byte(record), &d.Record)
		out = append(out, d)
	}
	return out, rows.Err()
}
