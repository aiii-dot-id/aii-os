package store

import "testing"

// Ring 2's evidence is a belief's INCOMING edges only, as standing counts
// it (standing.go): an edge from a belief is evidence for the other end,
// never for itself.
func TestRing2MaterialDirectionAware(t *testing.T) {
	s := testStore(t)

	mkLedger := func(seq int, typ, payload string) {
		t.Helper()
		if _, err := s.db.Exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content)
			VALUES (?, '', '2026-09-26T00:00:00Z', ?, 3, ?, 'h')`, seq, typ, payload); err != nil {
			t.Fatalf("insert ledger %d: %v", seq, err)
		}
	}
	mkLedger(1, "experience.create", `{"id":"e1","content":"operator said it holds in production"}`)
	if _, err := s.db.Exec(`INSERT INTO experiences (id, content, created_seq, created_at)
		VALUES ('e1', 'operator said it holds in production', 1, '2026-09-26T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	mkLedger(2, "belief.upsert", `{"id":"b1","statement":"it holds in production","ring":2}`)
	mkLedger(3, "belief.upsert", `{"id":"b2","statement":"a grounded other belief"}`)
	if _, err := s.db.Exec(`INSERT INTO beliefs (id, statement, ring, confidence, first_seq, last_seq)
		VALUES ('b1', 'it holds in production', 2, 0.8, 2, 2), ('b2', 'a grounded other belief', 3, 0.8, 3, 3)`); err != nil {
		t.Fatal(err)
	}
	mkLedger(4, "edge.create", `{"id":"edge-in","from_id":"e1","to_id":"b1","edge_type":"SUPPORTS"}`)
	mkLedger(5, "edge.create", `{"id":"edge-out","from_id":"b1","to_id":"b2","edge_type":"SUPPORTS"}`)
	if _, err := s.db.Exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq)
		VALUES ('edge-in', 'e1', 'b1', 'SUPPORTS', 4), ('edge-out', 'b1', 'b2', 'SUPPORTS', 5)`); err != nil {
		t.Fatal(err)
	}
	mkLedger(6, "belief.promote", `{"id":"b1","ring":2}`)

	id, err := s.PromptIdentity()
	if err != nil {
		t.Fatalf("PromptIdentity: %v", err)
	}
	beliefs := id.Ring2
	if len(beliefs) != 1 {
		t.Fatalf("got %d ring-2 beliefs, want 1", len(beliefs))
	}
	b := beliefs[0]
	if len(b.Evidence) != 1 || b.Evidence[0].ID != "e1" {
		t.Fatalf("evidence = %+v, want only the incoming edge from e1 — the outgoing edge to b2 is evidence for b2, never for b1", b.Evidence)
	}
}
