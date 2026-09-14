package tools

import (
	"bytes"
	"encoding/json"
	"strings"
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
// .
// .
// .
// .
// .
// .
// .
// .
// .
func ConflictingArgKeys(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil
	}

	first := make(map[string][]byte)
	var conflicts []string
	reported := make(map[string]bool)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return conflicts
		}
		key, ok := keyTok.(string)
		if !ok {
			return conflicts
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return conflicts
		}
		compact := compactJSON(val)

		prev, seen := first[key]
		if !seen {
			first[key] = compact
			continue
		}
		if !bytes.Equal(prev, compact) && !reported[key] {
			conflicts = append(conflicts, key)
			reported[key] = true
		}
	}
	return conflicts
}

// .
// .
// .
func compactJSON(raw json.RawMessage) []byte {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return append([]byte(nil), raw...)
	}
	return buf.Bytes()
}
