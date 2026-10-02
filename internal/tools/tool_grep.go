package tools

import ()

type GrepTool struct {
	deny func(path string) bool
}

func (t *GrepTool) Name() string { return "grep" }
func (t *GrepTool) Description() string {

	return "Search file contents (Go RE2 regex: a|b, (a); literal=true for plain text). Results are path:line:text."
}

func (t *GrepTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern":     map[string]interface{}{"type": "string"},
			"path":        map[string]interface{}{"type": "string", "description": "File or directory (default: your home)"},
			"ignore_case": map[string]interface{}{"type": "boolean"},
			"literal":     map[string]interface{}{"type": "boolean"},
			"include":     map[string]interface{}{"type": "string", "description": "Glob: *.go, or src/**/*_test.go"},
			"context":     map[string]interface{}{"type": "integer", "description": "Lines around each match"},
			"output":      map[string]interface{}{"type": "string", "enum": []string{"content", "files", "count"}},
			"limit":       map[string]interface{}{"type": "integer"},
			"offset":      map[string]interface{}{"type": "integer"},
		},
		"required": []string{"pattern"},
	}
}

func (t *GrepTool) ReadOnly() bool { return true }
