package llm

import "encoding/json"

// .
// .
func (b *anthContent) UnmarshalJSON(data []byte) error {
	type plain anthContent
	var parsed plain
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*b = anthContent(parsed)
	b.Raw = append(json.RawMessage(nil), data...)
	return nil
}
func (b anthContent) MarshalJSON() ([]byte, error) {
	type plain anthContent
	if len(b.Raw) == 0 {
		return json.Marshal(plain(b))
	}
	if b.CacheControl == nil {
		return b.Raw, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b.Raw, &fields); err != nil {
		return nil, err
	}
	marker, err := json.Marshal(b.CacheControl)
	if err != nil {
		return nil, err
	}
	fields["cache_control"] = marker
	return json.Marshal(fields)
}
func (u *anthUsage) UnmarshalJSON(data []byte) error {
	type plain anthUsage
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*u = anthUsage(p)
	var fields map[string]*json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	u.missing = fields["input_tokens"] == nil || fields["output_tokens"] == nil
	u.readReported = fields["cache_read_input_tokens"] != nil
	u.writeReported = fields["cache_creation_input_tokens"] != nil
	return nil
}

// .
// .
func ResetThinking(messages []Message) int {
	removed := 0
	for i := range messages {
		m := &messages[i]
		if len(m.NativeContent) > 0 {
			var blocks []json.RawMessage
			if json.Unmarshal(m.NativeContent, &blocks) != nil {
				continue
			}
			kept := make([]json.RawMessage, 0, len(blocks))
			count := 0
			for _, b := range blocks {
				var kind struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(b, &kind) == nil && (kind.Type == "thinking" || kind.Type == "redacted_thinking" || kind.Type == "reasoning") {
					count++
					continue
				}
				kept = append(kept, b)
			}
			if count == 0 {
				continue
			}
			removed += count
			m.NativeContent, _ = json.Marshal(kept)
		} else {
			if len(m.Thinking) == 0 {
				continue
			}
			removed += len(m.Thinking)
		}
		m.Thinking = nil
		m.OutputTokens = 0
	}
	return removed
}
