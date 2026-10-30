package store

import (
	"fmt"
	"testing"
)

func TestEdgesTouchingReadsEveryLiveEdgeAtEitherEndOfTheIDs(t *testing.T) {
	s := testStore(t)
	db := s.DB()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("%v\n%s", err, query)
		}
	}
	exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (1, '', '2026-10-20T00:00:00Z', 'test', 3, '{}', '')`)
	for _, e := range [][3]string{
		{"from_e1", "e1", "b_a"},
		{"into_e2", "b_b", "e2"},
		{"between", "e1", "e2"},
		{"elsewhere", "e9", "b_c"},
	} {
		exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES (?, ?, ?, 'SUPPORTS', 1)`, e[0], e[1], e[2])
	}
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq, archived) VALUES ('archived', 'e1', 'b_d', 'SUPPORTS', 1, 1)`)

	edges, err := s.EdgesTouching([]string{"e1", "e2", "e3"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range edges {
		got = append(got, e.ID)
	}
	if want := "[between from_e1 into_e2]"; fmt.Sprint(got) != want {
		t.Fatalf("edges touching e1, e2 and e3: %v; want %s (each live edge once, none archived or elsewhere)", got, want)
	}
	if edges, err := s.EdgesTouching(nil); err != nil || len(edges) != 0 {
		t.Fatalf("no ids: %v, %v; want none and no error", edges, err)
	}
}
