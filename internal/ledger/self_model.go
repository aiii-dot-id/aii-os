package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type SelfModelSourceRef struct {
	Class string `json:"class"`
	ID    string `json:"id"`
}

type SelfModelSynthesisPayload struct {
	ID                  string               `json:"id"`
	SynthesisText       string               `json:"synthesis_text"`
	ContinuityThread    string               `json:"continuity_thread"`
	SourceEntityRefs    []SelfModelSourceRef `json:"source_entity_refs"`
	ChangesSinceLast    string               `json:"changes_since_last,omitempty"`
	PreviousSynthesisID string               `json:"previous_synthesis_id,omitempty"`
	ModelID             string               `json:"model_id,omitempty"`
}

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

func DecodeRecordedSelfModelSynthesis(raw []byte) (SelfModelSynthesisPayload, bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return SelfModelSynthesisPayload{}, false, err
	}
	if _, old := fields["content"]; !old {
		p, err := DecodeSelfModelSynthesisPayload(raw)
		return p, false, err
	}
	if _, ambiguous := fields["synthesis_text"]; ambiguous {
		return SelfModelSynthesisPayload{}, true, fmt.Errorf("self-model payload contains both historical and current narrative fields")
	}
	var old struct {
		ID         string   `json:"id"`
		Content    string   `json:"content"`
		Continuity string   `json:"continuity_thread"`
		Changes    string   `json:"changes_since_last"`
		Refs       []string `json:"source_entity_refs"`
		SourceSeqs []uint64 `json:"source_seqs"`
	}
	if err := json.Unmarshal(raw, &old); err != nil {
		return SelfModelSynthesisPayload{}, true, err
	}
	refs := make([]SelfModelSourceRef, len(old.Refs))
	for i, id := range old.Refs {
		refs[i] = SelfModelSourceRef{ID: id}
	}
	return SelfModelSynthesisPayload{ID: old.ID, SynthesisText: old.Content, ContinuityThread: old.Continuity, ChangesSinceLast: old.Changes, SourceEntityRefs: refs}, true, nil
}

func IsSelfModelSourceClass(class string) bool {
	switch class {
	case "beliefs", "values", "intentions", "reflections", "relationships", "notes", "experiences", "working_style":
		return true
	default:
		return false
	}
}
