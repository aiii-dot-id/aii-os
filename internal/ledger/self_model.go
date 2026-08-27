package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// SelfModelSourceRef is a durable source used by a self-model synthesis.
// Class and ID are sufficient: copied display names would become stale facts.
type SelfModelSourceRef struct {
	Class string `json:"class"`
	ID    string `json:"id"`
}

// SelfModelSynthesisPayload is the complete self_model.synthesize wire shape.
// Operational counts, verification states, and model diagnostics do not belong
// in identity truth and are intentionally absent.
type SelfModelSynthesisPayload struct {
	ID                  string               `json:"id"`
	SynthesisText       string               `json:"synthesis_text"`
	ContinuityThread    string               `json:"continuity_thread"`
	SourceEntityRefs    []SelfModelSourceRef `json:"source_entity_refs"`
	ChangesSinceLast    string               `json:"changes_since_last,omitempty"`
	PreviousSynthesisID string               `json:"previous_synthesis_id,omitempty"`
	ModelID             string               `json:"model_id,omitempty"` // substrate-owned provenance (R58)
}

// DecodeSelfModelSynthesisPayload decodes exactly one strict payload. Unknown
// fields fail closed so decorative lifecycle or operational facts cannot enter
// the event unnoticed.
func DecodeSelfModelSynthesisPayload(raw []byte) (SelfModelSynthesisPayload, error) {
	var payload SelfModelSynthesisPayload
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		return payload, fmt.Errorf("decode self_model.synthesize payload: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("trailing JSON value")
		}
		return payload, fmt.Errorf("decode self_model.synthesize payload: %w", err)
	}
	return payload, nil
}

// IsSelfModelSourceClass reports whether class belongs to the canonical
// self-model evidence vocabulary.
func IsSelfModelSourceClass(class string) bool {
	switch class {
	case "beliefs", "values", "intentions", "reflections", "relationships", "notes", "experiences", "working_style":
		return true
	default:
		return false
	}
}
