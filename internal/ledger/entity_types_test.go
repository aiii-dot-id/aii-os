package ledger

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestEventTypeVocabularyIsClosed(t *testing.T) {
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

	declaredTypes := map[EventType]string{
		// .
		"ring0.genesis": "identity_lifetime",

		// .
		"relationship.upsert": "relationships",

		// .
		"belief.promote": "beliefs",

		// .
		"experience.create":     "experiences",
		"belief.upsert":         "beliefs",
		"edge.create":           "edges",
		"self_model.synthesize": "self_model_synthesis",

		// .
		"belief.archive":          "beliefs",
		"belief.supersede":        "beliefs",
		"intention.create":        "intentions",
		"intention.state_change":  "intentions",
		"commitment.promised":     "commitments",
		"commitment.state_change": "commitments",
		"working_style.upsert":    "beliefs",
		"edge.archive":            "edges",

		// .
		// .
		// .
		"consolidation.run": "experiences",
		"dream.run":         "experiences",

		// .
		// .
		"system.witnessed": "witness_receipts",

		// .
		// .
		"trust.epoch_accepted": "trust_epochs",

		// .
		// .
		"network.name_claimed": "public_name",
	}

	// .
	allConstants := AllEventTypes()

	// .
	for _, et := range allConstants {
		if _, ok := declaredTypes[et]; !ok {
			t.Errorf("EventType %q is defined in code but not declared in ENTITY_TYPES.md (declaredTypes map)", et)
		}
	}

	// .
	// .
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
		"relationship.evolve",
		"belief.attest",
		"work.deliver",
		"relationship.archive",
		"ring0.supersede",
		"identity.key_rotate",
		"ledger.correction",
	}
	for _, et := range forbidden {
		if _, ok := declaredTypes[et]; ok {
			t.Errorf("forbidden non-canonical event type %q found in declaredTypes", et)
		}
	}

	// .
	for _, et := range allConstants {
		for _, bad := range forbidden {
			if et == bad {
				t.Errorf("forbidden non-canonical EventType %q is still defined in code", et)
			}
		}
	}

	// .
	if len(allConstants) == 0 {
		t.Error("no EventType constants found — the vocabulary is empty")
	}

	// .
	for _, et := range allConstants {
		if et != EventType(strings.ToLower(string(et))) {
			t.Errorf("EventType %q should be lowercase", et)
		}
		if !strings.Contains(string(et), ".") {
			t.Errorf("EventType %q should contain a dot separator (family.variant)", et)
		}
	}
}
