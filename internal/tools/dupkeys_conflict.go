package tools

import (
	"bytes"
	"encoding/json"
	"strings"
)

// ConflictingArgKeys reports the repeated top-level argument keys whose
// copies DISAGREE — the subset of DuplicateArgKeys where the value that
// executes is not the value that was first stated.
//
// The distinction is the whole design. Field measurement over 2010 real
// tool calls found 21 with a repeated key: 13 carried a drifted final copy
// (a path truncated mid-string, a sibling filename, prompt prose spliced
// into the value), and 8 restated the same value verbatim. Refusing all 21
// would block 8 calls whose intent was never ambiguous; executing all 21
// lets 13 dispatch on a value nobody chose and return "not found" — an
// answer that reads exactly like a fact about the world.
//
// So: count every repetition (telemetry), refuse only disagreement.
// Values are compared after compaction, so whitespace between two
// otherwise-identical copies is not a conflict.
//
// Scope, like DuplicateArgKeys, is the top level only. Returns nil for
// unparseable input — that is the parse seam's business.
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
			return conflicts // truncated: report what was established
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

// compactJSON strips insignificant whitespace so two copies that differ
// only in formatting are not mistaken for a disagreement. On error the
// raw bytes are returned — comparison then simply becomes byte equality.
func compactJSON(raw json.RawMessage) []byte {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return append([]byte(nil), raw...)
	}
	return buf.Bytes()
}
