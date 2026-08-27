package tools

import ()

// (split from tools.go 2026-08-17 — one file per tool; registry and
// sandbox enforcement live in tools.go)

type GrepTool struct {
	// deny is the substrate floor, injected at registration so a walk
	// cannot read what `read` would refuse.
	deny func(path string) bool
}

func (t *GrepTool) Name() string { return "grep" }
func (t *GrepTool) Description() string {
	// The dialect belongs HERE, where it is read before the first call —
	// not only in the reply to a search that already failed.
	return "Search file contents by regular expression (Go RE2, NOT grep(1) BRE: " +
		"alternation is a|b, grouping is (a); \\| and \\( are literal characters). " +
		"Args: pattern (required), path (optional, defaults to .)"
}

func (t *GrepTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern": map[string]interface{}{"type": "string", "description": "Search pattern"},
			"path":    map[string]interface{}{"type": "string", "description": "Directory or file to search (default: .)"},
		},
		"required": []string{"pattern"},
	}
}

// (Execute is the portable pure-Go implementation in grep_purego.go)

// --- LsTool ---
