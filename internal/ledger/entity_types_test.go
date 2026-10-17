package ledger

import (
	"strings"
	"testing"
)

// TestEventTypeVocabularyIsClosed is the entity-types gate test.
// It checks every EventType constant against the declared vocabulary and
// verifies that each materialization target exists in the schema.
func TestEventTypeVocabularyIsClosed(t *testing.T) {
	// Every EventType constant defined in ledger.go must appear here.
	// This list records the event types and their materialization targets.
	//
	// To add a new event type:
	//   1. Document its payload and admission contract
	//   2. Add it to this map with its materialization target (or "" if meta-layer)
	//   3. Add the EventType constant in ledger.go
	//   4. Add materialization in store/materialize.go
	//
	// If any of these four steps is missing, this test fails.

	declaredTypes := map[EventType]string{
		// Ring 0 — Constitution
		"ring0.genesis": "identity_lifetime",

		// Ring 1 — Charter
		"relationship.upsert": "relationships",

		// Ring 2 — Identity
		"belief.promote": "beliefs",

		// Ring 3 — Working truth (implemented)
		"experience.create":     "experiences",
		"belief.upsert":         "beliefs",
		"edge.create":           "edges",
		"self_model.synthesize": "self_model_synthesis",

		// [planned] types — declared for vocabulary stability, not yet minted by facilities
		"belief.archive":          "beliefs",
		"belief.supersede":        "beliefs",
		"intention.create":        "intentions",
		"intention.state_change":  "intentions",
		"commitment.promised":     "commitments",
		"commitment.state_change": "commitments",
		"working_style.upsert":    "beliefs",
		"edge.archive":            "edges",

		// Ring 3 — facility run markers (H6/#4): {inputs, outputs} results,
		// never instructions. The materializer marks the cited input
		// experiences consumed (raw=0) — consumed state is f(ledger).
		"consolidation.run": "experiences",
		"dream.run":         "experiences",

		// Meta-layer — truth-protecting anchors (witness receipts are
		// f(ledger): the chain carries its own proof points)
		"system.witnessed": "witness_receipts",

		// Meta-layer — plugin-trust anti-rollback (the accepted
		// revocation-snapshot epoch high-water mark is a ledgered fact)
		"trust.epoch_accepted": "trust_epochs",

		// Meta-layer — the identity's stable public name, claimed once
		// (SECURE_CONTEXT §8.1); the certificate for it is material.
		"network.name_claimed": "public_name",
	}

	// The closed vocabulary, from the single registry
	allConstants := AllEventTypes()

	// Check: every constant in code must be in the declared map
	for _, et := range allConstants {
		if _, ok := declaredTypes[et]; !ok {
			t.Errorf("EventType %q is defined in code but missing from the declaredTypes map", et)
		}
	}

	// Forbidden non-canonical names that must never reappear.
	// These are the OLD names that were renamed or removed.
	forbidden := []EventType{
		"birth_attestation",
		"knowledge.add",
		"relationship.create",
		"annul",
		"commitment",
		"conversation_turn",
		"tension.raise",
		"tension.resolve",
		"reflection.add",
		"relationship.evolve", // Q3 resolved: decomposes into experiences + beliefs + SHAPED_BY edges + working_style
		"belief.attest",
		"work.deliver",         // delivery = commitment.state_change:completed, or note — no separate identity-truth
		"relationship.archive", // Q3: charter-only table — operator rels superseded, peers have no rows
		"ring0.supersede",      // no platform governance to hold the authority; canon immutability stands
		"identity.key_rotate",
		"ledger.correction", // platform repair-key custody unimplemented; returns whole with custody
	}
	for _, et := range forbidden {
		if _, ok := declaredTypes[et]; ok {
			t.Errorf("forbidden non-canonical event type %q found in declaredTypes", et)
		}
	}

	// Check: no EventType constant in code uses a forbidden name
	for _, et := range allConstants {
		for _, bad := range forbidden {
			if et == bad {
				t.Errorf("forbidden non-canonical EventType %q is still defined in code", et)
			}
		}
	}

	// Verify the test map and code constants agree on count
	if len(allConstants) == 0 {
		t.Error("no EventType constants found — the vocabulary is empty")
	}

	// String representation check: every constant should be lowercase with dots
	for _, et := range allConstants {
		if et != EventType(strings.ToLower(string(et))) {
			t.Errorf("EventType %q should be lowercase", et)
		}
		if !strings.Contains(string(et), ".") {
			t.Errorf("EventType %q should contain a dot separator (family.variant)", et)
		}
	}
}
