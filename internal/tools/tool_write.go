package tools

import (
	"context"
	"fmt"
)

// .
// .

type WriteTool struct{}

func (t *WriteTool) Name() string { return "write" }
func (t *WriteTool) Description() string {
	return "Write content to a file (overwrites). Args: file_path (required), content (required)"
}

func (t *WriteTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file_path": map[string]interface{}{"type": "string", "description": "Path to the file to write"},
			"content":   map[string]interface{}{"type": "string", "description": "Content to write"},
		},
		"required": []string{"file_path", "content"},
	}
}

func (t *WriteTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	path, _ := args["file_path"].(string)
	content, _ := args["content"].(string)
	if path == "" {
		return Result{Error: "file_path is required"}, nil
	}

	// .
	// .
	// .
	if err := writeFileNoFollow(path, []byte(content), 0644); err != nil {
		return Result{Error: err.Error()}, nil
	}

	return Result{Output: fmt.Sprintf("Wrote %d bytes to %s", len(content), path)}, nil
}

// .
