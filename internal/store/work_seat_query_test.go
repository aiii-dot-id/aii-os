package store

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
)

// The predicate queuedChild's two probes replaced: one OR over a substr, which no
// index can serve. Kept here only as the reference the new form must agree
// with, and as the "before" of the benchmark.
const queuedChildByScan = `NOT EXISTS (SELECT 1 FROM work_queue w WHERE w.kind = ? AND (w.dedup_key = work_sessions.id OR
	substr(w.dedup_key, 1, length(work_sessions.id) + 1) = work_sessions.id || '#'))`

// THE SEAT READ NEVER SCANS THE QUEUE. Terminal queue rows are never
// deleted, so a scan grows with every sub-agent leg and alarm firing ever
// run; each probe of the anti-join must be a search of wq_dedup_idx.
func TestTheSeatReadSearchesTheQueueIndex(t *testing.T) {
	s := testStore(t)
	for name, query := range map[string]string{
		"main seat":     `SELECT id FROM work_sessions WHERE status='active' AND NOT ` + queuedChild("") + ` ORDER BY rowid DESC LIMIT 1`,
		"boot sweep":    `SELECT id FROM work_sessions WHERE status='active' AND NOT ` + queuedChild(` AND w.state IN ('PENDING','CLAIMED')`),
		"live children": `SELECT id FROM work_sessions WHERE status='active' AND ` + queuedChild("") + ` ORDER BY rowid DESC LIMIT 10`,
	} {
		rows, err := s.DB().Query("EXPLAIN QUERY PLAN "+query, SubagentWorkKind, SubagentWorkKind)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		searches := 0
		for _, step := range plan {
			if step == "SCAN w" || strings.HasPrefix(step, "SCAN w ") {
				t.Errorf("%s scans the queue:\n%s", name, strings.Join(plan, "\n"))
			}
			if strings.Contains(step, "SEARCH w USING") && strings.Contains(step, "wq_dedup_idx") {
				searches++
			}
		}
		if searches != 2 {
			t.Errorf("%s: %d searches of wq_dedup_idx, want 2:\n%s", name, searches, strings.Join(plan, "\n"))
		}
	}
}

// THE TWO PROBES ANSWER WHAT THE SCAN ANSWERED. Ids share prefixes and
// carry '#' and '$', keys are runs, legs, near-misses and other kinds; on
// every one the session set the new predicate leaves equals the old one's.
func TestTheSplitChildTestAgreesWithTheScan(t *testing.T) {
	s := testStore(t)
	r := rand.New(rand.NewSource(60))
	parts := []string{"s", "s1", "s10", "a#", "a$", "#", "$", "x"}
	pick := func() string {
		var b strings.Builder
		for n := 1 + r.Intn(3); n > 0; n-- {
			b.WriteString(parts[r.Intn(len(parts))])
		}
		return b.String()
	}
	ids := map[string]bool{}
	for len(ids) < 60 {
		ids[pick()] = true
	}
	n := 0
	for id := range ids {
		if _, err := s.DB().Exec(`INSERT INTO work_sessions (id, description, status) VALUES (?, 'w', 'active')`, id); err != nil {
			t.Fatal(err)
		}
		var key string
		switch r.Intn(7) {
		case 0:
			key = id
		case 1:
			key = fmt.Sprintf("%s#%d", id, r.Intn(5))
		case 2:
			key = id + "#"
		case 3:
			key = id + "$"
		case 4:
			key = id + "$1"
		case 5:
			key = id[:len(id)-1]
		default:
			continue
		}
		kind := SubagentWorkKind
		if r.Intn(4) == 0 {
			kind = "alarm.owner"
		}
		n++
		if _, err := s.DB().Exec(`INSERT INTO work_queue (id, kind, dedup_key, state) VALUES (?, ?, ?, 'DONE')`, fmt.Sprintf("q%d", n), kind, key); err != nil {
			t.Fatal(err)
		}
	}
	free := func(where string, args ...any) []string {
		rows, err := s.DB().Query(`SELECT id FROM work_sessions WHERE `+where, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			out = append(out, id)
		}
		slices.Sort(out)
		return out
	}
	old := free(queuedChildByScan, SubagentWorkKind)
	split := free("NOT "+queuedChild(""), SubagentWorkKind, SubagentWorkKind)
	if !slices.Equal(old, split) {
		t.Fatalf("the split predicate leaves %v; the scan left %v", split, old)
	}
	if len(old) == 0 || len(old) == len(ids) {
		t.Fatalf("the fixture decides nothing: %d of %d sessions free", len(old), len(ids))
	}
}

// BenchmarkTheSeatRead times the main-seat anti-join over 10k terminal
// queue rows, the scan and the split form in the same binary.
func BenchmarkTheSeatRead(b *testing.B) {
	s := testStore(b)
	if _, err := s.DB().Exec(`INSERT INTO work_sessions (id, description, status) VALUES ('main', 'w', 'active')`); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		kind, key := SubagentWorkKind, fmt.Sprintf("leg-%d#1", i)
		if i%2 == 0 {
			kind, key = "alarm.owner", fmt.Sprintf("alarm-%d", i)
		}
		if _, err := s.DB().Exec(`INSERT INTO work_queue (id, kind, dedup_key, state) VALUES (?, ?, ?, 'DONE')`, fmt.Sprintf("q%d", i), kind, key); err != nil {
			b.Fatal(err)
		}
	}
	for _, c := range []struct {
		name, where string
		args        []any
	}{
		{"scan", queuedChildByScan, []any{SubagentWorkKind}},
		{"split", "NOT " + queuedChild(""), []any{SubagentWorkKind, SubagentWorkKind}},
	} {
		b.Run(c.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				var id string
				if err := s.DB().QueryRow(`SELECT id FROM work_sessions WHERE status='active' AND `+c.where+` ORDER BY rowid DESC LIMIT 1`, c.args...).Scan(&id); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
