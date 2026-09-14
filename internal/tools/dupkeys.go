package tools

import (
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
func DuplicateArgKeys(raw string) []string {
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

	seen := make(map[string]bool)
	var dups []string
	reported := make(map[string]bool)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return dups
		}
		key, ok := keyTok.(string)
		if !ok {
			return dups
		}
		if seen[key] && !reported[key] {
			dups = append(dups, key)
			reported[key] = true
		}
		seen[key] = true

		// .
		// .
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			return dups
		}
	}
	return dups
}

// .
// .
func (r *Registry) CountDuplicateArgKeys() { r.duplicateArgKeys.Add(1) }

// .
// .
// .
func (r *Registry) DuplicateArgKeyCount() uint64 { return r.duplicateArgKeys.Load() }
