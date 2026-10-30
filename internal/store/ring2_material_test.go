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

// Ring 2 prints at most a bounded line of each source and names, where it
// cuts one, the read that shows it whole. Which read that is depends on
// what the source is, so the material says which projection held its text,
// in the order the text is taken; and the bound in force travels with the
// material, so the composer and the facilities' preamble print one Ring 2.
func TestRing2MaterialNamesWhatEachSourceIsAndCarriesTheBound(t *testing.T) {
	s := testStore(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	for seq := 1; seq <= 12; seq++ {
		exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', '2026-10-13T00:00:00Z', 'test.row', 3, '{}', 'h')`, seq)
	}
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, first_seq, last_seq)
		VALUES ('b_two', 'the belief Ring 2 holds', 2, 0.8, 1, 1), ('b_src', 'a belief it rests on', 3, 0.8, 2, 2)`)
	exec(`INSERT INTO experiences (id, content, provenance, created_seq, created_at)
		VALUES ('x_src', 'a note it rests on', 'external', 3, '2026-10-13T00:00:00Z')`)
	exec(`INSERT INTO intentions (id, statement, created_seq) VALUES ('i_src', 'an intention it rests on', 4)`)
	exec(`INSERT INTO relationships (id, counterpart_name, charter_text, created_seq) VALUES ('rel_src0', 'Quinn', 'a charter it rests on', 5)`)
	exec(`INSERT INTO self_model_synthesis (id, synthesis_text, source_entity_refs, continuity_thread, created_seq, created_at)
		VALUES ('syn_src', 'a portrait it rests on', '[]', 'thread', 6, '2026-10-13T00:00:00Z')`)
	for i, from := range []string{"b_src", "x_src", "i_src", "rel_src0", "syn_src", "gone_src"} {
		exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES (?, ?, 'b_two', 'SUPPORTS', ?)`, "edge_"+from, from, 7+i)
	}

	id, err := s.PromptIdentity()
	if err != nil {
		t.Fatalf("PromptIdentity: %v", err)
	}
	if id.Ring2EvidenceChars != 0 {
		t.Fatalf("with no bound set the material carries %d; 0 leaves the renderer its default", id.Ring2EvidenceChars)
	}
	if len(id.Ring2) != 1 || len(id.Ring2[0].Evidence) != 6 {
		t.Fatalf("rig: one Ring 2 belief on six sources, got %+v", id.Ring2)
	}
	want := []struct{ id, kind, content, provenance string }{
		{"b_src", "belief", "a belief it rests on", "self"},
		{"x_src", "experience", "a note it rests on", "external"},
		{"i_src", "intention", "an intention it rests on", "self"},
		{"rel_src0", "relationship", "your relationship with Quinn", "self"},
		{"syn_src", "self_model", "a portrait it rests on", "self"},
		{"gone_src", "", "gone_src", "self"}, // no projection holds it: its id stands for its text, and it has no kind
	}
	for i, w := range want {
		got := id.Ring2[0].Evidence[i]
		if got.ID != w.id || got.Kind != w.kind || got.Content != w.content || got.Provenance != w.provenance {
			t.Errorf("source %d = %+v; want id %q kind %q content %q provenance %q", i, got, w.id, w.kind, w.content, w.provenance)
		}
	}

	s.SetRing2EvidenceChars(120)
	if id, err = s.PromptIdentity(); err != nil || id.Ring2EvidenceChars != 120 {
		t.Fatalf("a bound of 120 travels with the material: got %d, %v", id.Ring2EvidenceChars, err)
	}
	s.SetRing2EvidenceChars(-5)
	if id, err = s.PromptIdentity(); err != nil || id.Ring2EvidenceChars != 0 {
		t.Fatalf("a bound below zero is none: got %d, %v", id.Ring2EvidenceChars, err)
	}
}

// The budget a new charter is held to travels with the charter to the
// prompt, so that what Ring 1 says of it is the number admission asks: the
// default where none is set, and the operator's once it is.
func TestThePromptsIdentityCarriesTheChartersBudget(t *testing.T) {
	s := testStore(t)
	id, err := s.PromptIdentity()
	if err != nil || id.CharterMaxTokens != CharterDefaultMaxTokens {
		t.Fatalf("with no budget set the prompt is given %d (%v); want the default, %d", id.CharterMaxTokens, err, CharterDefaultMaxTokens)
	}
	s.SetCharterMaxTokens(900)
	if id, err = s.PromptIdentity(); err != nil || id.CharterMaxTokens != 900 {
		t.Fatalf("a budget of 900 reaches the prompt as %d (%v)", id.CharterMaxTokens, err)
	}
}
