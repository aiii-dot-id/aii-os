package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestEveryAdvertisedSelfModelFieldIsOneTheDecoderAccepts(t *testing.T) {
	def := selfModelCommitter{}.Definition()
	props, ok := def.Function.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("the commit definition has no properties map: %#v", def.Function.Parameters)
	}
	if len(props) == 0 {
		t.Fatal("the narrowed definition advertises nothing at all — the model would have no way to synthesize")
	}

	payload := map[string]any{}
	for name := range props {
		if name == "variant" {
			continue // the verb's selector, not a payload field
		}
		switch name {
		case "source_entity_refs":
			payload[name] = []map[string]string{{"id": "x", "class": "beliefs"}}
		default:
			payload[name] = "x"
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.DecodeSelfModelSynthesisPayload(raw); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("the schema advertises a field the decoder REFUSES — the model would be invited to write something that cannot land: %v", err)
		}
		// Type mismatches are this test's own crude values, not a
		// contract violation; only unknown fields are the defect.
		t.Logf("decoder returned a non-membership error (acceptable here): %v", err)
	}

	// And the facility commits exactly one variant, so the selector must
	// say so rather than offering the whole union.
	variant, ok := props["variant"].(map[string]any)
	if !ok {
		t.Fatal("variant must still be advertised — it selects the act")
	}
	enum, ok := variant["enum"].([]string)
	if !ok || len(enum) != 1 || enum[0] != "self_model.synthesize" {
		t.Fatalf("variant must be pinned to the one act this facility commits, got %v", variant["enum"])
	}
}

func TestTheSelfModelSchemaDropsRelationshipOnlyFields(t *testing.T) {
	def := selfModelCommitter{}.Definition()
	props, _ := def.Function.Parameters["properties"].(map[string]any)
	for _, foreign := range []string{"autonomy_level", "trust_level", "counterpart_name", "charter_text"} {
		if _, present := props[foreign]; present {
			t.Errorf("the self-model schema still advertises the relationship-only field %q; the strict payload decoder rejects it, so offering it can only produce a failed write", foreign)
		}
	}
	// The fields it genuinely needs must survive the narrowing.
	for _, needed := range []string{"synthesis_text", "continuity_thread", "source_entity_refs"} {
		if _, present := props[needed]; !present {
			t.Errorf("the narrowing removed %q, which the payload requires", needed)
		}
	}
	// Substrate-owned provenance is never the model's to claim.
	if _, present := props["model_id"]; present {
		t.Error("model_id is stamped by the engine and must not be advertised to the model")
	}
}
