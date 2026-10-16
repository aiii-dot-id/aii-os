package tools

import "context"

type GrepTool struct {
	deny func(ctx context.Context, path string) bool
}

func (t *GrepTool) Name() string { return "grep" }
func (t *GrepTool) Description() string {

	return "Search file contents (Go RE2 regex: a|b, (a); literal=true for plain text). Results are path:line:text."
}

func (t *GrepTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern":     map[string]any{"type": "string"},
			"path":        map[string]any{"type": "string", "description": "File or directory (default: your home)"},
			"ignore_case": map[string]any{"type": "boolean"},
			"literal":     map[string]any{"type": "boolean"},
			"include":     map[string]any{"type": "string", "description": "Glob: *.go, or src/**/*_test.go"},
			"context":     map[string]any{"type": "integer", "description": "Lines around each match"},
			"output":      map[string]any{"type": "string", "enum": []string{"content", "files", "count"}},
			"limit":       map[string]any{"type": "integer"},
			"offset":      map[string]any{"type": "integer"},
		},
		"required": []string{"pattern"},
	}
}

func (t *GrepTool) ReadOnly() bool { return true }
