package store

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
func legalRing(t *testing.T, typ ledger.EventType) int {
	t.Helper()
	rings := ledger.CanonicalRings(typ)
	if len(rings) == 0 {
		t.Fatalf("no canonical rings for %s", typ)
	}
	return rings[0]
}

func mustRaw(t *testing.T, m map[string]interface{}) []byte {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// .
// .
// .
// .
// .
// .
// .
// .
func TestPreflightRefusesUnknownTargets(t *testing.T) {
	s := testStore(t)

	cases := []struct {
		name string
		typ  ledger.EventType
		pl   map[string]interface{}
	}{
		{"belief.promote", ledger.EventBeliefPromote, map[string]interface{}{"id": "ghost", "ring": 3}},
		{"belief.archive", ledger.EventBeliefArchive, map[string]interface{}{"id": "ghost", "reason": "r"}},
		{"belief.supersede", ledger.EventBeliefSupersede, map[string]interface{}{"old_id": "ghost", "new_id": "n1", "reason": "r"}},
		{"edge.archive", ledger.EventEdgeArchive, map[string]interface{}{"id": "ghost"}},
		{"intention.state_change", ledger.EventIntentionStateChange, map[string]interface{}{"id": "ghost", "state": "completed"}},
		{"commitment.state_change", ledger.EventCommitmentStateChange, map[string]interface{}{"id": "ghost", "state": "completed"}},
	}
	for _, c := range cases {
		if err := s.ValidateEvent(c.typ, legalRing(t, c.typ), mustRaw(t, c.pl)); err == nil {
			t.Errorf("%s with an unknown target passed preflight — it would append durably and then fail materialization forever", c.name)
		}
	}
}

// .
// .
// .
func TestPreflightRefusesDuplicateCreates(t *testing.T) {
	s := testStore(t)

	// .
	// .
	seedRel := probeEvent(1, ledger.EventRelationshipUpsert, legalRing(t, ledger.EventRelationshipUpsert), map[string]interface{}{
		"id": "op", "counterpart_name": "Peer", "counterpart_role": "peer",
		"relationship_type": "peer",
	})
	if err := s.Materialize(&seedRel); err != nil {
		t.Fatalf("seed relationship: %v", err)
	}
	seedInt := probeEvent(2, ledger.EventIntentionCreate, legalRing(t, ledger.EventIntentionCreate), map[string]interface{}{
		"id": "i1", "statement": "s", "why": "w",
	})
	if err := s.Materialize(&seedInt); err != nil {
		t.Fatalf("seed intention: %v", err)
	}
	seedCom := probeEvent(3, ledger.EventCommitmentPromised, legalRing(t, ledger.EventCommitmentPromised), map[string]interface{}{
		"id": "c1", "description": "d", "counterpart_id": "op",
	})
	if err := s.Materialize(&seedCom); err != nil {
		t.Fatalf("seed commitment: %v", err)
	}

	if err := s.ValidateEvent(ledger.EventIntentionCreate, legalRing(t, ledger.EventIntentionCreate),
		mustRaw(t, map[string]interface{}{"id": "i1", "statement": "dup", "why": "dup"})); err == nil {
		t.Error("duplicate intention.create passed preflight — the ledger would record a no-op that claims to create")
	}
	if err := s.ValidateEvent(ledger.EventCommitmentPromised, legalRing(t, ledger.EventCommitmentPromised),
		mustRaw(t, map[string]interface{}{"id": "c1", "description": "dup", "counterpart_id": "op"})); err == nil {
		t.Error("duplicate commitment.promised passed preflight — the ledger would record a no-op that claims to create")
	}
}

// .
// .
// .
// .
func TestPreflightRefusesUnparseableBeliefPayload(t *testing.T) {
	s := testStore(t)
	raw := []byte(`{"id":"b-float","statement":"s","ring":3.5,"confidence":0.5}`)
	if err := s.ValidateEvent(ledger.EventBeliefUpsert, legalRing(t, ledger.EventBeliefUpsert), raw); err == nil {
		t.Fatal("belief payload with ring 3.5 passed preflight — valid tool input would permanently poison replay")
	}
}

// .
// .
// .
func TestPreflightLeavesNoResidue(t *testing.T) {
	s := testStore(t)

	pl := map[string]interface{}{"id": "i-clean", "statement": "s", "why": "w"}
	ring := legalRing(t, ledger.EventIntentionCreate)
	if err := s.ValidateEvent(ledger.EventIntentionCreate, ring, mustRaw(t, pl)); err != nil {
		t.Fatalf("valid create must pass preflight: %v", err)
	}

	var rows, mirror int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM intentions`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ledger`).Scan(&mirror); err != nil {
		t.Fatal(err)
	}
	if rows != 0 || mirror != 0 {
		t.Fatalf("preflight left residue: intentions=%d mirror=%d (must be rollback-only)", rows, mirror)
	}

	evt := probeEvent(1, ledger.EventIntentionCreate, ring, pl)
	if err := s.Materialize(&evt); err != nil {
		t.Fatalf("real materialization after preflight must succeed: %v", err)
	}
}

// .
// .
// .
// .
func TestPreflightRefusesDefectiveFacilityRuns(t *testing.T) {
	s := testStore(t)

	seedRaw := probeEvent(1, ledger.EventExperienceCreate, 3, map[string]interface{}{
		"id": "x_raw", "content": "raw material",
	})
	if err := s.Materialize(&seedRaw); err != nil {
		t.Fatal(err)
	}
	seedPriv := probeEvent(2, ledger.EventExperienceCreate, 3, map[string]interface{}{
		"id": "x_priv", "content": "sealed", "private": true,
	})
	if err := s.Materialize(&seedPriv); err != nil {
		t.Fatal(err)
	}

	ring := legalRing(t, ledger.EventConsolidationRun)
	cases := []struct {
		name string
		pl   map[string]interface{}
	}{
		{"no inputs — a run that consumed nothing is a ledgered no-op",
			map[string]interface{}{"inputs": []string{}, "outputs": []uint64{}}},
		{"ghost input — signed marker applies to nothing",
			map[string]interface{}{"inputs": []string{"x_ghost"}, "outputs": []uint64{}}},
		{"private input — Charter #9: sealed experiences are never metabolized",
			map[string]interface{}{"inputs": []string{"x_priv"}, "outputs": []uint64{}}},
		{"fabricated output seq — provenance must name a real prior event",
			map[string]interface{}{"inputs": []string{"x_raw"}, "outputs": []uint64{999}}},
	}
	for _, c := range cases {
		if err := s.ValidateEvent(ledger.EventConsolidationRun, ring, mustRaw(t, c.pl)); err == nil {
			t.Errorf("preflight admitted a defective run marker: %s", c.name)
		}
	}

	// .
	// .
	good := map[string]interface{}{"inputs": []string{"x_raw"}, "outputs": []uint64{1}}
	if err := s.ValidateEvent(ledger.EventConsolidationRun, ring, mustRaw(t, good)); err != nil {
		t.Fatalf("valid run marker must pass preflight: %v", err)
	}
	run := probeEvent(3, ledger.EventConsolidationRun, ring, good)
	if err := s.Materialize(&run); err != nil {
		t.Fatalf("valid run marker must materialize: %v", err)
	}
	if err := s.ValidateEvent(ledger.EventConsolidationRun, ring, mustRaw(t, good)); err == nil {
		t.Error("re-citing a consumed experience passed preflight — double consumption must refuse live")
	}
}

// .
func TestPreflightStillValidatesRingAuthority(t *testing.T) {
	s := testStore(t)
	err := s.ValidateEvent(ledger.EventBeliefUpsert, 0, mustRaw(t, map[string]interface{}{
		"id": "b1", "statement": "s", "ring": 0, "confidence": 0.5,
	}))
	if err == nil || !strings.Contains(err.Error(), "ring") {
		t.Fatalf("ring 0 must not be a legal authority for belief.upsert, got %v", err)
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestExperienceProvenanceVocabularyMatchesSchema(t *testing.T) {
	schemaBytes, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`provenance IN \(([^)]+)\)`).FindStringSubmatch(string(schemaBytes))
	if m == nil {
		t.Fatal("cannot locate the experiences.provenance CHECK in schema.sql")
	}
	schemaSet := map[string]bool{}
	for _, tok := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(m[1], -1) {
		schemaSet[tok[1]] = true
	}
	if len(schemaSet) == 0 {
		t.Fatal("parsed an empty provenance vocabulary from schema.sql")
	}

	s := testStore(t)
	// .
	if err := s.AddConversationTurn("operator", "vocabulary probe"); err != nil {
		t.Fatal(err)
	}
	opTurn, err := s.GetLatestOperatorTurn()
	if err != nil || opTurn == nil {
		t.Fatalf("seeded operator turn not found: %v", err)
	}

	ring := legalRing(t, ledger.EventExperienceCreate)
	probe := func(id, provenance string) error {
		pl := map[string]interface{}{"id": id, "content": "c", "provenance": provenance}
		switch provenance {
		case "operator":
			pl["source_turn"] = opTurn.TurnSeq
		case "external":
			pl["source_url"] = "https://example.test/source"
		}
		return s.ValidateEvent(ledger.EventExperienceCreate, ring, mustRaw(t, pl))
	}

	i := 0
	for tok := range schemaSet {
		i++
		if err := probe(fmt.Sprintf("prov_ok_%d", i), tok); err != nil {
			t.Errorf("schema-sanctioned provenance %q refused by preflight: %v", tok, err)
		}
	}

	for _, dead := range []string{"work", "no_such_provenance"} {
		if schemaSet[dead] {
			continue
		}
		err := probe("prov_dead_"+dead, dead)
		if err == nil {
			t.Fatalf("provenance %q passed preflight but is outside the schema CHECK — a real mint would die as a raw SQL error", dead)
		}
		msg := err.Error()
		if strings.Contains(msg, "constraint") || strings.Contains(msg, "CHECK") {
			t.Fatalf("provenance %q died as a raw constraint error, not a sanctioned refusal: %v", dead, err)
		}
		idx := strings.Index(msg, "sanctioned: ")
		if idx < 0 {
			t.Fatalf("provenance %q refusal lacks the sanctioned-vocabulary listing: %v", dead, err)
		}
		msgSet := map[string]bool{}
		for _, tok := range strings.Split(msg[idx+len("sanctioned: "):], ",") {
			msgSet[strings.TrimSpace(tok)] = true
		}
		for tok := range schemaSet {
			if !msgSet[tok] {
				t.Errorf("sanctioned listing omits schema token %q: %v", tok, err)
			}
		}
		for tok := range msgSet {
			if !schemaSet[tok] {
				t.Errorf("sanctioned listing advertises %q, which the schema CHECK refuses: %v", tok, err)
			}
		}
	}
}
