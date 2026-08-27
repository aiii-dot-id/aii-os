package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// The materializer table test + drift alarm (P5).
//
// ALARM: the table below must cover EXACTLY ledger.AllEventTypes() —
// every event type the ledger can mint must have a materializer test
// proving its effect lands, and the table must hold no stale entries.
// Adding an event type without its table row fails here (the gate test
// in internal/ledger checks vocabulary↔docs; THIS checks
// vocabulary↔tested behavior — the third leg the gate alone lacked).
//
// Found stale on first write: the entity-types gate claimed commitments
// had no projection table while schema + materializer had one — prose
// drift caught by building this.

func mustPayload(t *testing.T, m map[string]interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func seqEvent(t *testing.T, s *Store, seq uint64, typ ledger.EventType, payload map[string]interface{}) *ledger.Event {
	t.Helper()
	evt := &ledger.Event{
		Seq: seq, PrevHash: "sha256:0", Timestamp: "2026-08-16T00:00:00Z",
		Type: typ, Author: "test", Ring: 3,
		Payload: mustPayload(t, payload), ContentHash: hashOfSeq(seq),
	}
	if err := s.Materialize(evt); err != nil {
		t.Fatalf("materialize %s: %v", typ, err)
	}
	return evt
}

func hashOfSeq(seq uint64) string {
	// deterministic distinct hash per seq — content correctness is the
	// ledger package's business; the materializer only carries it
	h := [64]byte{}
	for i := range h {
		h[i] = byte('a' + (int(seq)+i)%26)
	}
	return "sha256:" + string(h[:])
}

func getBeliefRow(t *testing.T, s *Store, id string) (archived int, supersededBy sql.NullString) {
	t.Helper()
	err := s.db.QueryRow(`SELECT archived, superseded_by FROM beliefs WHERE id = ?`, id).Scan(&archived, &supersededBy)
	if err != nil {
		t.Fatalf("belief row %s: %v", id, err)
	}
	return
}

func getEdgeRowArchived(t *testing.T, s *Store, id string) int {
	t.Helper()
	var archived int
	if err := s.db.QueryRow(`SELECT archived FROM edges WHERE id = ?`, id).Scan(&archived); err != nil {
		t.Fatalf("edge row %s: %v", id, err)
	}
	return archived
}

type matCase struct {
	setup   func(t *testing.T, s *Store)
	payload map[string]interface{}
	ring    int
	assert  func(t *testing.T, s *Store)
}

func TestMaterializerTable(t *testing.T) {
	newStore := func(t *testing.T) *Store {
		return testStore(t)
	}

	table := map[ledger.EventType]matCase{

		ledger.EventRing0Genesis: {
			ring:    0,
			payload: map[string]interface{}{"name": "TableIdentity"},
			assert: func(t *testing.T, s *Store) {
				var ticks int64
				if err := s.db.QueryRow(`SELECT lifetime_ticks FROM identity_lifetime WHERE singleton_id = 'current'`).Scan(&ticks); err != nil {
					t.Fatal("ring0.genesis must materialize identity_lifetime:", err)
				}
				if ticks != 0 {
					t.Fatalf("birth ticks = %d, want 0", ticks)
				}
			},
		},

		ledger.EventRelationshipUpsert: {
			ring: 1,
			payload: map[string]interface{}{
				"id": "rel_table", "counterpart_name": "Peer", "counterpart_role": "peer",
				"relationship_type": "peer", "charter_text": "charter text",
			},
			assert: func(t *testing.T, s *Store) {
				var name string
				if err := s.db.QueryRow(`SELECT counterpart_name FROM relationships WHERE id = 'rel_table'`).Scan(&name); err != nil {
					t.Fatal("relationship.upsert must materialize:", err)
				}
				if name != "Peer" {
					t.Fatalf("counterpart = %q", name)
				}
			},
		},

		ledger.EventBeliefUpsert: {
			ring:    3,
			payload: map[string]interface{}{"id": "b_up", "statement": "table belief", "ring": 3, "confidence": 0.5},
			assert: func(t *testing.T, s *Store) {
				b, err := s.GetBelief("b_up")
				if err != nil || b == nil {
					t.Fatal("belief.upsert must materialize the belief:", err)
				}
				if b.Ring != 3 {
					t.Fatalf("belief born ring=%d, want 3", b.Ring)
				}
				// Standing is DERIVED (no stored status): a belief with no
				// edges stands 'new' (2026-08-17).
				if st := s.StandingFor("b_up"); st != "new" {
					t.Fatalf("edgeless belief standing = %q, want new", st)
				}
			},
		},

		ledger.EventBeliefPromote: {
			setup: func(t *testing.T, s *Store) {
				seqEvent(t, s, 90, ledger.EventBeliefUpsert, map[string]interface{}{"id": "b_pr", "statement": "to promote", "ring": 3, "confidence": 0.5})
			},
			ring:    2,
			payload: map[string]interface{}{"id": "b_pr", "ring": 2},
			assert: func(t *testing.T, s *Store) {
				b, _ := s.GetBelief("b_pr")
				if b.Ring != 2 {
					t.Fatalf("belief.promote must land the ring, got %d", b.Ring)
				}
			},
		},

		ledger.EventExperienceCreate: {
			ring:    3,
			payload: map[string]interface{}{"id": "x_1", "content": "an experience", "category": "observation", "private": false},
			assert: func(t *testing.T, s *Store) {
				exps, _ := s.ListExperiences(10)
				if len(exps) != 1 || exps[0].ID != "x_1" || exps[0].Raw != 1 {
					t.Fatalf("experience.create must materialize a raw experience: %+v", exps)
				}
			},
		},

		ledger.EventEdgeCreate: {
			setup: func(t *testing.T, s *Store) {
				seqEvent(t, s, 91, ledger.EventBeliefUpsert, map[string]interface{}{"id": "b_edge", "statement": "edge target", "ring": 3, "confidence": 0.5})
				seqEvent(t, s, 92, ledger.EventExperienceCreate, map[string]interface{}{"id": "x_src", "content": "source", "category": "observation"})
			},
			ring:    3,
			payload: map[string]interface{}{"id": "e_1", "from_id": "x_src", "to_id": "b_edge", "edge_type": "SUPPORTS"},
			assert: func(t *testing.T, s *Store) {
				b, _ := s.GetBelief("b_edge")
				if b.EvidenceCount != 1 {
					t.Fatalf("edge.create must increment evidence_count, got %d", b.EvidenceCount)
				}
				edges, _ := s.ListEdgesForBelief("b_edge")
				if len(edges) != 1 || edges[0].EdgeType != "SUPPORTS" {
					t.Fatalf("edge row must land: %+v", edges)
				}
			},
		},

		ledger.EventSelfModelSynthesize: {
			setup: func(t *testing.T, s *Store) {
				seqEvent(t, s, 83, ledger.EventBeliefUpsert, map[string]interface{}{"id": "b_syn", "statement": "belief", "ring": 3, "confidence": 0.5})
				seqEvent(t, s, 84, ledger.EventExperienceCreate, map[string]interface{}{"id": "x_syn", "content": "experience", "category": "observation"})
				seqEvent(t, s, 85, ledger.EventIntentionCreate, map[string]interface{}{"id": "i_syn", "statement": "intention"})
				seqEvent(t, s, 86, ledger.EventRelationshipUpsert, map[string]interface{}{
					"id": "rel_syn", "counterpart_name": "Peer", "counterpart_role": "peer",
					"relationship_type": "peer", "charter_text": "charter",
				})
				refs := []map[string]interface{}{
					{"class": "beliefs", "id": "b_syn"},
					{"class": "experiences", "id": "x_syn"},
					{"class": "intentions", "id": "i_syn"},
					{"class": "relationships", "id": "rel_syn"},
				}
				seqEvent(t, s, 87, ledger.EventSelfModelSynthesize, map[string]interface{}{
					"id": "syn_old", "synthesis_text": "old synthesis", "continuity_thread": "continuity", "source_entity_refs": refs,
				})
			},
			ring: 3,
			payload: map[string]interface{}{
				"id": "syn_new", "synthesis_text": "new synthesis", "continuity_thread": "same",
				"changes_since_last": "changed", "previous_synthesis_id": "syn_old",
				"source_entity_refs": []map[string]interface{}{
					{"class": "beliefs", "id": "b_syn"},
					{"class": "experiences", "id": "x_syn"},
					{"class": "intentions", "id": "i_syn"},
					{"class": "relationships", "id": "rel_syn"},
				},
			},
			assert: func(t *testing.T, s *Store) {
				rs, _ := s.ListSelfModelSyntheses(10, 0)
				if len(rs) != 2 {
					t.Fatalf("want 2 syntheses, got %d", len(rs))
				}
				var oldSuper sql.NullString
				if err := s.db.QueryRow(`SELECT superseded_by FROM self_model_synthesis WHERE id = 'syn_old'`).Scan(&oldSuper); err != nil || !oldSuper.Valid || oldSuper.String != "syn_new" {
					t.Fatalf("new synthesis must supersede the prior current one, got %v", oldSuper)
				}
			},
		},

		ledger.EventIntentionCreate: {
			ring:    3,
			payload: map[string]interface{}{"id": "i_1", "statement": "do the thing", "why": "because"},
			assert: func(t *testing.T, s *Store) {
				ints, _ := s.ListIntentions()
				if len(ints) != 1 || ints[0].State != "active" {
					t.Fatalf("intention must be born active: %+v", ints)
				}
			},
		},

		ledger.EventIntentionStateChange: {
			setup: func(t *testing.T, s *Store) {
				seqEvent(t, s, 94, ledger.EventIntentionCreate, map[string]interface{}{"id": "i_2", "statement": "finish it"})
			},
			ring:    3,
			payload: map[string]interface{}{"id": "i_2", "state": "completed", "outcome": "done"},
			assert: func(t *testing.T, s *Store) {
				var state, outcome string
				if err := s.db.QueryRow(`SELECT state, outcome FROM intentions WHERE id = 'i_2'`).Scan(&state, &outcome); err != nil {
					t.Fatal(err)
				}
				if state != "completed" || outcome != "done" {
					t.Fatalf("intention.state_change must land: %s/%s", state, outcome)
				}
			},
		},

		ledger.EventCommitmentPromised: {
			setup: func(t *testing.T, s *Store) {
				seqEvent(t, s, 89, ledger.EventRelationshipUpsert, map[string]interface{}{
					"id": "rel_table", "counterpart_name": "Peer", "counterpart_role": "peer",
					"relationship_type": "peer", "charter_text": "c",
				})
			},
			ring:    3,
			payload: map[string]interface{}{"id": "c_1", "description": "deliver by Friday", "counterpart_id": "rel_table"},
			assert: func(t *testing.T, s *Store) {
				cs, err := s.ListCommitments(true)
				if err != nil || len(cs) != 1 {
					t.Fatalf("commitment must be born owed: %v %+v", err, cs)
				}
				if cs[0].State != "promised" || cs[0].CounterpartID != "rel_table" {
					t.Fatalf("commitment fields: %+v", cs[0])
				}
			},
		},

		ledger.EventCommitmentStateChange: {
			setup: func(t *testing.T, s *Store) {
				seqEvent(t, s, 88, ledger.EventRelationshipUpsert, map[string]interface{}{
					"id": "rel_table", "counterpart_name": "Peer", "counterpart_role": "peer",
					"relationship_type": "peer", "charter_text": "c",
				})
				seqEvent(t, s, 95, ledger.EventCommitmentPromised, map[string]interface{}{"id": "c_2", "description": "repair", "counterpart_id": "rel_table"})
			},
			ring:    3,
			payload: map[string]interface{}{"id": "c_2", "state": "repaired", "repair_state": "made good"},
			assert: func(t *testing.T, s *Store) {
				var state, repair string
				if err := s.db.QueryRow(`SELECT state, repair_state FROM commitments WHERE id = 'c_2'`).Scan(&state, &repair); err != nil {
					t.Fatal(err)
				}
				if state != "repaired" || repair != "made good" {
					t.Fatalf("commitment.state_change must land: %s/%s", state, repair)
				}
			},
		},

		ledger.EventWorkingStyleUpsert: {
			ring:    3,
			payload: map[string]interface{}{"id": "ws_1", "content": "works in the morning", "confidence": 0.8},
			assert: func(t *testing.T, s *Store) {
				b, err := s.GetBelief("ws_1")
				if err != nil || b == nil {
					t.Fatal("working_style.upsert must materialize as a belief:", err)
				}
				if b.Statement != "works in the morning" {
					t.Fatalf("statement must map from content: %q", b.Statement)
				}
				var nodeType sql.NullString
				_ = s.db.QueryRow(`SELECT node_type FROM beliefs WHERE id = 'ws_1'`).Scan(&nodeType)
				if !nodeType.Valid || nodeType.String != "working_style" {
					t.Fatalf("node_type must be working_style, got %v", nodeType)
				}
			},
		},

		ledger.EventBeliefArchive: {
			setup: func(t *testing.T, s *Store) {
				seqEvent(t, s, 96, ledger.EventBeliefUpsert, map[string]interface{}{"id": "b_arch", "statement": "to archive", "ring": 3, "confidence": 0.1})
			},
			ring:    3,
			payload: map[string]interface{}{"id": "b_arch", "reason": "obsolete"},
			assert: func(t *testing.T, s *Store) {
				archived, _ := getBeliefRow(t, s, "b_arch")
				if archived != 1 {
					t.Fatal("belief.archive must set archived=1 (soft delete)")
				}
			},
		},

		ledger.EventBeliefSupersede: {
			setup: func(t *testing.T, s *Store) {
				seqEvent(t, s, 97, ledger.EventBeliefUpsert, map[string]interface{}{"id": "b_old", "statement": "old", "ring": 3, "confidence": 0.1})
				seqEvent(t, s, 98, ledger.EventBeliefUpsert, map[string]interface{}{"id": "b_new", "statement": "new", "ring": 3, "confidence": 0.9})
			},
			ring:    3,
			payload: map[string]interface{}{"old_id": "b_old", "new_id": "b_new", "reason": "sharper"},
			assert: func(t *testing.T, s *Store) {
				_, sup := getBeliefRow(t, s, "b_old")
				if !sup.Valid || sup.String != "b_new" {
					t.Fatalf("supersede must point old → new, got %v", sup)
				}
				edges, _ := s.ListEdgesForBelief("b_old")
				found := false
				for _, e := range edges {
					if e.EdgeType == "SUPERSEDES" && e.FromID == "b_new" {
						found = true
					}
				}
				if !found {
					t.Fatal("supersede must mint the SUPERSEDES edge in the same event")
				}
			},
		},

		ledger.EventTrustEpochAccepted: {
			ring: 0,
			payload: map[string]interface{}{
				"root": "platform_release", "trust_epoch": 3,
				"payload_sha256": "sha256:" + strings.Repeat("c", 64),
			},
			assert: func(t *testing.T, s *Store) {
				epoch, sha, ok, err := s.TrustEpochHighWater("platform_release")
				if err != nil || !ok {
					t.Fatalf("trust.epoch_accepted must land in trust_epochs: ok=%v err=%v", ok, err)
				}
				if epoch != 3 || sha != "sha256:"+strings.Repeat("c", 64) {
					t.Fatalf("trust epoch projection wrong: epoch=%d sha=%s", epoch, sha)
				}
				if _, _, ok, _ := s.TrustEpochHighWater("plugin_reviewer"); ok {
					t.Fatal("high-water mark must be per root — an unrelated root read back a value")
				}
			},
		},

		ledger.EventSystemWitnessed: {
			ring: 0,
			payload: map[string]interface{}{
				"receipt": map[string]interface{}{
					"witness_version": "1.0", "identity_id": "did:aiii:identity:sha256:abc",
					"ledger_ordinal": 42, "ledger_hash": "sha256:" + strings.Repeat("a", 64),
					"range_start_ordinal": 40, "range_hash": "sha256:" + strings.Repeat("b", 64),
					"witnessed_at": "2026-08-16T00:00:00Z", "witness_key_id": "aiii_witness_test",
				},
			},
			assert: func(t *testing.T, s *Store) {
				var anchored int64
				var receipt string
				if err := s.db.QueryRow(`SELECT anchored_seq, receipt_json FROM witness_receipts ORDER BY id DESC LIMIT 1`).Scan(&anchored, &receipt); err != nil {
					t.Fatal("system.witnessed must land in witness_receipts:", err)
				}
				if anchored != 42 || !strings.Contains(receipt, "ledger_ordinal\":42") {
					t.Fatalf("receipt projection wrong: seq=%d json=%s", anchored, receipt)
				}
			},
		},

		ledger.EventConsolidationRun: {
			setup: func(t *testing.T, s *Store) {
				seqEvent(t, s, 102, ledger.EventExperienceCreate, map[string]interface{}{"id": "x_cr1", "content": "raw one"})
				seqEvent(t, s, 103, ledger.EventExperienceCreate, map[string]interface{}{"id": "x_cr2", "content": "raw two"})
				seqEvent(t, s, 104, ledger.EventBeliefUpsert, map[string]interface{}{"id": "b_cr", "statement": "distilled", "ring": 3, "confidence": 0.6})
			},
			ring:    3,
			payload: map[string]interface{}{"inputs": []string{"x_cr1", "x_cr2"}, "outputs": []uint64{104}},
			assert: func(t *testing.T, s *Store) {
				n, err := s.UnprocessedExperienceCount()
				if err != nil {
					t.Fatal(err)
				}
				if n != 0 {
					t.Fatalf("consolidation.run must consume its cited inputs (raw=0), %d remain raw", n)
				}
			},
		},

		ledger.EventDreamRun: {
			setup: func(t *testing.T, s *Store) {
				seqEvent(t, s, 105, ledger.EventExperienceCreate, map[string]interface{}{"id": "x_dr", "content": "raw material"})
				seqEvent(t, s, 106, ledger.EventExperienceCreate, map[string]interface{}{"id": "x_note", "content": "surfacing", "category": "reflection", "provenance": "dream", "raw": false})
			},
			ring:    3,
			payload: map[string]interface{}{"inputs": []string{"x_dr"}, "outputs": []uint64{106}},
			assert: func(t *testing.T, s *Store) {
				n, err := s.UnprocessedExperienceCount()
				if err != nil {
					t.Fatal(err)
				}
				if n != 0 {
					t.Fatalf("dream.run must consume its cited inputs (raw=0), %d remain raw", n)
				}
			},
		},

		ledger.EventEdgeArchive: {
			setup: func(t *testing.T, s *Store) {
				seqEvent(t, s, 99, ledger.EventBeliefUpsert, map[string]interface{}{"id": "b_ea", "statement": "edge holder", "ring": 3, "confidence": 0.1})
				seqEvent(t, s, 100, ledger.EventExperienceCreate, map[string]interface{}{"id": "x_ea", "content": "src"})
				seqEvent(t, s, 101, ledger.EventEdgeCreate, map[string]interface{}{"id": "e_arch", "from_id": "x_ea", "to_id": "b_ea", "edge_type": "SUPPORTS"})
			},
			ring:    3,
			payload: map[string]interface{}{"id": "e_arch"},
			assert: func(t *testing.T, s *Store) {
				if got := getEdgeRowArchived(t, s, "e_arch"); got != 1 {
					t.Fatal("edge.archive must set archived=1")
				}
			},
		},
	}

	// THE DRIFT ALARM: table coverage == the ledger's closed vocabulary.
	registry := map[ledger.EventType]bool{}
	for _, et := range ledger.AllEventTypes() {
		registry[et] = true
		if _, ok := table[et]; !ok {
			t.Errorf("DRIFT: event type %s has no materializer table test — add one before it can ship", et)
		}
	}
	for et := range table {
		if !registry[et] {
			t.Errorf("STALE: table tests event type %s which the ledger no longer defines", et)
		}
	}

	// Run every case in a fresh store.
	for _, et := range ledger.AllEventTypes() {
		et := et
		t.Run(string(et), func(t *testing.T) {
			s := newStore(t)
			tc := table[et]
			if tc.setup != nil {
				tc.setup(t, s)
			}
			seqEvent(t, s, 200, et, tc.payload)
			tc.assert(t, s)
		})
	}
}

// H1-adjacent (2026-08-17 external review): operator succession requires
// an operator-role successor. Before the role gate, ANY relationship
// carrying `supersedes` retired any row — a peer-role mint could quietly
// replace the founding operator relationship Ring 1 stands on. A refused
// succession must also leave no partial write.
func TestSupersedeRequiresOperatorRoleSuccessor(t *testing.T) {
	s := testStore(t)

	if err := s.AddConversationTurn("operator", "yes, rel-founding-abc12345"); err != nil {
		t.Fatal(err)
	}
	var foundingTurn int64
	if err := s.db.QueryRow(`SELECT MAX(turn_seq) FROM conversations`).Scan(&foundingTurn); err != nil {
		t.Fatal(err)
	}
	// Existing operator relationship with real R52 evidence.
	seqEvent(t, s, 1, ledger.EventRelationshipUpsert, map[string]interface{}{
		"id": "rel-founding-abc12345", "counterpart_name": "Op",
		"counterpart_role": "operator", "relationship_type": "founding_operator",
		"operator_approval_excerpt": "yes, rel-founding-abc12345",
		"operator_approval_turn":    foundingTurn,
		"approval_basis":            "conversation_turn",
	})

	// A peer-role successor tries to retire it: refused.
	evt := &ledger.Event{
		Seq: 2, PrevHash: "sha256:0", Timestamp: "2026-08-16T00:00:01Z",
		Type: ledger.EventRelationshipUpsert, Author: "test", Ring: 1,
		Payload: mustPayload(t, map[string]interface{}{
			"id": "rel_peer_takeover1", "counterpart_name": "Somebody",
			"counterpart_role": "peer", "relationship_type": "peer",
			"supersedes": "rel-founding-abc12345",
		}), ContentHash: hashOfSeq(2),
	}
	if err := s.Materialize(evt); err == nil {
		t.Fatal("peer-role successor must not supersede the operator relationship")
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM relationships WHERE id = 'rel_peer_takeover1'`).Scan(&n)
	if n != 0 {
		t.Fatal("refused succession must leave no successor row")
	}
	var sup sql.NullString
	s.db.QueryRow(`SELECT superseded_by FROM relationships WHERE id = 'rel-founding-abc12345'`).Scan(&sup)
	if sup.Valid {
		t.Fatal("founding relationship must remain unretired after a refused succession")
	}

	// An operator-role successor with real approval evidence succeeds.
	if err := s.AddConversationTurn("operator", "yes, rel_operator_next1 approved"); err != nil {
		t.Fatal(err)
	}
	var turnSeq int64
	s.db.QueryRow(`SELECT MAX(turn_seq) FROM conversations`).Scan(&turnSeq)
	seqEvent(t, s, 3, ledger.EventRelationshipUpsert, map[string]interface{}{
		"id": "rel_operator_next1", "counterpart_name": "Op2",
		"counterpart_role": "operator", "relationship_type": "operator",
		"operator_approval_excerpt": "yes, rel_operator_next1 approved",
		"operator_approval_turn":    turnSeq,
		"approval_basis":            "conversation_turn",
		"supersedes":                "rel-founding-abc12345",
	})
	s.db.QueryRow(`SELECT superseded_by FROM relationships WHERE id = 'rel-founding-abc12345'`).Scan(&sup)
	if !sup.Valid || sup.String != "rel_operator_next1" {
		t.Fatalf("operator-role succession must retire the founding row, got %v", sup)
	}
}

// The ring-authority gate (canon §11, 2026-08-18): ValidateEvent DENIES
// a claimed ring outside the type's canonical table — rings are
// owner-derived and gate-validated, never trusted from the caller.
func TestValidateEventRingAuthority(t *testing.T) {
	s := testStore(t)

	payload := mustPayload(t, map[string]interface{}{"id": "exp_ring", "content": "x"})
	if err := s.ValidateEvent(ledger.EventExperienceCreate, 3, payload); err != nil {
		t.Fatalf("canonical ring must pass: %v", err)
	}
	if err := s.ValidateEvent(ledger.EventExperienceCreate, 1, payload); err == nil {
		t.Fatal("experience.create at ring 1 must DENY — not its authority")
	}
	if err := s.ValidateEvent(ledger.EventBeliefPromote, 3, mustPayload(t, map[string]interface{}{"id": "b"})); err == nil {
		t.Fatal("belief.promote at ring 3 must DENY — Ring 2 is its only authority")
	}
	if err := s.ValidateEvent(ledger.EventType("made.up"), 3, payload); err == nil {
		t.Fatal("unknown event type must DENY — no legal ring")
	}
}

// Empty defining text is not an entity (2026-08-23).
//
// Found live in a resident ledger: three belief.upsert events carried
// their text under "content" while materializeBeliefUpsert reads
// "statement". json.Unmarshal does not error on an absent field — it
// zero-values it — so three beliefs materialized with statement = ""
// and nothing reported a problem for two days. The organ now writes
// both keys (identity/commit.go), which closes THAT rename and not the
// class: any future payload-key drift on a defining field is the same
// silent loss, and the drift alarm above proves an effect LANDS, never
// that it carries anything.
//
// The rule these cases pin: an entity whose defining text is empty is
// not an entity. Refused at the door in the R39 shape the rest of this
// file already uses — typed, naming the requirement.
func TestMaterializerRefusesEmptyDefiningText(t *testing.T) {
	s := testStore(t)

	deny := []struct {
		typ     ledger.EventType
		payload map[string]interface{}
		names   string // the field the refusal must name
	}{
		{ledger.EventBeliefUpsert, map[string]interface{}{"id": "b_empty", "ring": 3}, "statement"},
		{ledger.EventIntentionCreate, map[string]interface{}{"id": "i_empty", "why": "because"}, "statement"},
		{ledger.EventExperienceCreate, map[string]interface{}{"id": "exp_empty"}, "content"},
		{ledger.EventWorkingStyleUpsert, map[string]interface{}{"id": "ws_empty"}, "content"},
	}
	for _, tc := range deny {
		t.Run("deny/"+string(tc.typ), func(t *testing.T) {
			err := s.ValidateEvent(tc.typ, 3, mustPayload(t, tc.payload))
			if err == nil {
				t.Fatalf("%s with empty defining text must DENY — a row that says nothing is not an entity", tc.typ)
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Fatalf("%s refusal must name %q (R39: typed refusals name the requirement); got: %v", tc.typ, tc.names, err)
			}
		})
	}

	// Positive control: a guard that refuses everything is not a guard.
	allow := []struct {
		typ     ledger.EventType
		payload map[string]interface{}
	}{
		{ledger.EventBeliefUpsert, map[string]interface{}{"id": "b_ok", "ring": 3, "statement": "I hold this"}},
		{ledger.EventIntentionCreate, map[string]interface{}{"id": "i_ok", "statement": "I will do this", "why": "because"}},
		{ledger.EventExperienceCreate, map[string]interface{}{"id": "exp_ok", "content": "I noticed this"}},
		{ledger.EventWorkingStyleUpsert, map[string]interface{}{"id": "ws_ok", "content": "I work like this"}},
	}
	for _, tc := range allow {
		t.Run("allow/"+string(tc.typ), func(t *testing.T) {
			if err := s.ValidateEvent(tc.typ, 3, mustPayload(t, tc.payload)); err != nil {
				t.Fatalf("%s carrying its text must PASS: %v", tc.typ, err)
			}
		})
	}
}

// A belief's text arrives under EITHER key: identity/commit.go copies
// content into statement before minting, so a canonical payload carries
// both. Reading only "statement" materialized three of a live
// resident's beliefs as empty rows — the text was in the chain the
// whole time, under "content", and json.Unmarshal zero-valued the field
// it was looking for without erroring.
//
// Refusal is unchanged when the text is genuinely absent.
func TestBeliefTextReadFromEitherKey(t *testing.T) {
	s := testStore(t)

	cases := []struct {
		name    string
		payload map[string]interface{}
		want    string
	}{
		{"statement", map[string]interface{}{"id": "b_stmt", "ring": 3, "statement": "I hold this"}, "I hold this"},
		{"content", map[string]interface{}{"id": "b_cont", "ring": 3, "content": "I hold that"}, "I hold that"},
		{"both agree", map[string]interface{}{"id": "b_both", "ring": 3, "statement": "canonical", "content": "canonical"}, "canonical"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seqEvent(t, s, uint64(100+i), ledger.EventBeliefUpsert, tc.payload)
			var got string
			id := tc.payload["id"].(string)
			if err := s.db.QueryRow(`SELECT statement FROM beliefs WHERE id = ?`, id).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("belief %s materialized statement %q, want %q — the text is in the payload either way", id, got, tc.want)
			}
		})
	}

	// Neither key: still refused, at the live door.
	err := s.ValidateEvent(ledger.EventBeliefUpsert, 3,
		mustPayload(t, map[string]interface{}{"id": "b_none", "ring": 3}))
	if err == nil {
		t.Fatal("a belief with text under neither key must still be refused")
	}
	if !strings.Contains(err.Error(), "statement or content") {
		t.Fatalf("the refusal must name both keys it looked for; got: %v", err)
	}
}
