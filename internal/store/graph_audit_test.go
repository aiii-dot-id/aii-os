package store

import (
	"strings"
	"testing"
)

// .
// .
func TestGraphAuditReadsTheRecordAgainstItsRules(t *testing.T) {
	s := testStore(t)
	db := s.DB()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("%v\n%s", err, query)
		}
	}
	for i := 1; i <= 3; i++ {
		exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content, sig) VALUES (?, '', '2026-09-10T00:00:00Z', 'test', 3, '{}', '', '')`, i)
	}
	exec(`INSERT INTO experiences (id, content, category, raw, private, provenance, created_seq, created_at) VALUES ('e1', 'the tide tables', 'observation', 1, 0, 'self', 1, '2026-09-10T00:00:00Z')`)
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_grounded', 'the tide turns twice a day', 3, 0.8, 1, 1, 1)`)
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_bare', 'the moon is made of cheese', 3, 0.5, 0, 2, 2)`)
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq, archived) VALUES ('b_gone', 'an archived belief', 3, 0.5, 0, 3, 3, 1)`)
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed_support', 'e1', 'b_grounded', 'SUPPORTS', 1)`)
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed_tension', 'b_bare', 'b_grounded', 'CONTRADICTS', 2)`)
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed_ghost', 'ghost', 'b_grounded', 'SUPPORTS', 3)`)
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq, archived) VALUES ('ed_ghost_gone', 'ghost2', 'b_bare', 'SUPPORTS', 3, 1)`)

	a, err := s.GraphAudit()
	if err != nil {
		t.Fatal(err)
	}
	if a.Ungrounded != 1 || len(a.UngroundedSample) != 1 || a.UngroundedSample[0] != "b_bare" {
		t.Fatalf("ungrounded = %d %v, want b_bare alone (a tension is not evidence; an archived belief is not current)", a.Ungrounded, a.UngroundedSample)
	}
	if a.Orphans != 1 || len(a.OrphansSample) != 1 || a.OrphansSample[0] != "ed_ghost" {
		t.Fatalf("orphans = %d %v, want ed_ghost alone (an archived edge is not live)", a.Orphans, a.OrphansSample)
	}
	if a.Tensions != 1 {
		t.Fatalf("tensions = %d, want 1", a.Tensions)
	}
	text := RenderGraphAudit(a)
	for _, want := range []string{"Record audit", "ungrounded beliefs: 1", "b_bare", "orphan edges: 1", "ed_ghost", "open tensions: 1", "observational"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendering lacks %q:\n%s", want, text)
		}
	}
}

// .
// .
// .
func TestTheNewIndexesServeTheirReads(t *testing.T) {
	s := testStore(t)
	plan := func(query string) string {
		t.Helper()
		rows, err := s.DB().Query("EXPLAIN QUERY PLAN " + query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			out = append(out, detail)
		}
		return strings.Join(out, "\n")
	}
	if p := plan(`SELECT id FROM edges WHERE (from_id = 'x' OR to_id = 'x') AND archived = 0`); !strings.Contains(p, "idx_edges_to") {
		t.Fatalf("the edge read by target must use idx_edges_to:\n%s", p)
	}
	if p := plan(`SELECT id FROM experiences WHERE private = 0 ORDER BY created_seq DESC LIMIT 7`); !strings.Contains(p, "idx_experiences_created_seq") {
		t.Fatalf("the newest-first page must use idx_experiences_created_seq:\n%s", p)
	}
}
