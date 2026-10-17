package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory/attention"
)

func TestTheTensionReadUsesItsIndex(t *testing.T) {
	s := newStore(t)
	rows, err := s.DB().Query("EXPLAIN QUERY PLAN " + openTensionsSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	text := strings.Join(plan, "\n")
	if !strings.Contains(text, "USING INDEX idx_edges_contradicts") || strings.Contains(text, "TEMP B-TREE FOR ORDER BY") {
		t.Fatalf("the tension read does not walk idx_edges_contradicts in order:\n%s", text)
	}
}

// BenchmarkTheTurnsTensions is the turn's attention read over 20,000 live
// edges of which twenty contradict, with the index and without it.
func BenchmarkTheTurnsTensions(b *testing.B) {
	for _, indexed := range []bool{true, false} {
		b.Run(fmt.Sprintf("indexed=%v", indexed), func(b *testing.B) {
			s := newStore(b)
			exec := func(q string, args ...any) {
				if _, err := s.DB().Exec(q, args...); err != nil {
					b.Fatalf("%v\n%s", err, q)
				}
			}
			exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (1, '', '2026-09-01T00:00:00Z', 'test', 3, '{}', '')`)
			tx, err := s.DB().Begin()
			if err != nil {
				b.Fatal(err)
			}
			for i := 0; i < 20000; i++ {
				kind := "SUPPORTS"
				if i%1000 == 0 {
					kind = "CONTRADICTS"
				}
				if _, err := tx.Exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES (?, ?, ?, ?, 1)`, fmt.Sprintf("e%d", i), fmt.Sprintf("x%d", i), fmt.Sprintf("b%d", i%500), kind); err != nil {
					b.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				b.Fatal(err)
			}
			if !indexed {
				exec(`DROP INDEX idx_edges_contradicts`)
			}
			f := New(s)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := f.Attention(context.Background(), time.Now(), attention.CostMedium); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
