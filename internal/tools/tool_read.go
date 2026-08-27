package tools

import (
	"context"
	"os"
)

// (split from tools.go 2026-08-17 — one file per tool; registry and
// sandbox enforcement live in tools.go)

type ReadTool struct{ maxBytes int }

func (t *ReadTool) Name() string { return "read" }
func (t *ReadTool) Description() string {
	return "Read file contents. Args: file_path (required), offset (line number, optional), limit (max lines, optional)"
}

func (t *ReadTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file_path": map[string]interface{}{"type": "string", "description": "Path to the file to read"},
		},
		"required": []string{"file_path"},
	}
}

func (t *ReadTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	path, _ := args["file_path"].(string)
	if path == "" {
		return Result{Error: "file_path is required"}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	if len(data) > t.maxBytes {
		return Result{
			Output:    string(data[:t.maxBytes]),
			Truncated: true,
		}, nil
	}

	return Result{Output: string(data)}, nil
}

// --- WriteTool ---
