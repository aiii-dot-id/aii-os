package tools

import (
	"encoding/json"
	"strings"
)

// DuplicateArgKeys reports the keys repeated in a tool call's top-level
// argument object, in the order first repetition was seen.
//
// Why this exists: encoding/json accepts a repeated key without complaint
// and keeps the LAST occurrence. A tool call whose emission degenerated
// mid-object — the same key restated many times, the final copy carrying a
// spliced or truncated value — is therefore VALID JSON that dispatches on
// its corrupted tail. No existing seam sees it: the parse seam succeeds,
// schema validation succeeds (the key is present), and target-miss
// telemetry fires only when the surviving value also fails to exist.
//
// Scope is deliberately the top level only. That is where argument keys
// live; descending into nested values would flag legitimate structures
// (an operator's JSON payload passed as a string argument is not a tool
// call's arguments) and buy no signal the observed channel produces.
//
// Pure observation: this function reads, decides nothing, and rewrites
// nothing. Callers count and log; the call proceeds unchanged.
func DuplicateArgKeys(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil // unparseable: the parse seam's business, not ours
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil // not an object: nothing named to repeat
	}

	seen := make(map[string]bool)
	var dups []string
	reported := make(map[string]bool)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return dups // truncated mid-object: report what we established
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

		// Consume the value whole — Decode handles nested objects and
		// arrays, leaving the decoder positioned at the next key.
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			return dups
		}
	}
	return dups
}

// CountDuplicateArgKeys records one tool call that repeated an argument key.
// Called from the app's dispatch seam, which holds the raw JSON. (P3.)
func (r *Registry) CountDuplicateArgKeys() { r.duplicateArgKeys.Add(1) }

// DuplicateArgKeyCount reports how many tool calls repeated an argument key
// since process start. Zero means no corruption OR no traffic; read it
// alongside call volume. (P3 telemetry.)
func (r *Registry) DuplicateArgKeyCount() uint64 { return r.duplicateArgKeys.Load() }
