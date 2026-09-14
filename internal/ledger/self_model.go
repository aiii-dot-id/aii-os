package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// .
// .
type SelfModelSourceRef struct {
	Class string `json:"class"`
	ID    string `json:"id"`
}

// .
// .
// .
type SelfModelSynthesisPayload struct {
	ID                  string               `json:"id"`
	SynthesisText       string               `json:"synthesis_text"`
	ContinuityThread    string               `json:"continuity_thread"`
	SourceEntityRefs    []SelfModelSourceRef `json:"source_entity_refs"`
	ChangesSinceLast    string               `json:"changes_since_last,omitempty"`
	PreviousSynthesisID string               `json:"previous_synthesis_id,omitempty"`
	ModelID             string               `json:"model_id,omitempty"`
}

// .
// .
// .
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

// .
// .
func IsSelfModelSourceClass(class string) bool {
	switch class {
	case "beliefs", "values", "intentions", "reflections", "relationships", "notes", "experiences", "working_style":
		return true
	default:
		return false
	}
}
