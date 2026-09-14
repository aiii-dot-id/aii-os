package tools

import ()

// .
// .

type GrepTool struct {
	// .
	// .
	deny func(path string) bool
}

func (t *GrepTool) Name() string { return "grep" }
func (t *GrepTool) Description() string {
	// .
	// .
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

// .

// .

// .
func (t *GrepTool) ReadOnly() bool { return true }
