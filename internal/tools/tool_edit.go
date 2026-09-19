package tools

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// .
// .
func errNotTheFileRead(path string) error {
	return fmt.Errorf("%s is no longer the file this edit read — it was replaced in between; nothing was written. Read it again and reissue the edit", path)
}

// .
// .

type EditTool struct{}

func (t *EditTool) Name() string { return "edit" }
func (t *EditTool) Description() string {
	return "Surgical text replacement in a file. Args: file_path (required), old_string (required), new_string (required)"
}

func (t *EditTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file_path":  map[string]interface{}{"type": "string", "description": "Path to the file"},
			"old_string": map[string]interface{}{"type": "string", "description": "Text to find"},
			"new_string": map[string]interface{}{"type": "string", "description": "Replacement text"},
		},
		"required": []string{"file_path", "old_string", "new_string"},
	}
}

func (t *EditTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	path, _ := args["file_path"].(string)
	oldStr, _ := args["old_string"].(string)
	newStr, _ := args["new_string"].(string)

	if path == "" || oldStr == "" {
		return Result{Error: "file_path and old_string are required"}, nil
	}

	// .
	// .
	// .
	// .
	// .
	// .
	f, st, err := openRegular(path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	content := string(data)
	if !strings.Contains(content, oldStr) {
		return Result{Error: "old_string not found in file"}, nil
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if oldStr == newStr {
		return Result{Error: "old_string and new_string are identical — this edit would change nothing; " +
			"reissue it with the replacement text, or use read to check the file first"}, nil
	}

	// .
	// .
	// .
	// .
	// .
	if n := strings.Count(content, oldStr); n > 1 {
		return Result{Error: fmt.Sprintf("old_string appears %d times in %s — this edit would silently take the first; "+
			"include surrounding lines so the anchor is unique", n, path)}, nil
	}

	newContent := strings.Replace(content, oldStr, newStr, 1)
	if err := ctx.Err(); err != nil {
		return Result{Error: fmt.Sprintf("edit cancelled before anything was written: %v", err)}, nil
	}
	// .
	// .
	// .
	// .
	if err := rewriteSameFile(path, []byte(newContent), st); err != nil {
		return Result{Error: err.Error()}, nil
	}

	return Result{Output: fmt.Sprintf("Edited %s", path)}, nil
}

// .
