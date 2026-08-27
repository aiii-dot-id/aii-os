package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// (split from tools.go 2026-08-17 — one file per tool; registry and
// sandbox enforcement live in tools.go)

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

	data, err := os.ReadFile(path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	content := string(data)
	if !strings.Contains(content, oldStr) {
		return Result{Error: "old_string not found in file"}, nil
	}

	// A NO-OP EDIT REPORTED SUCCESS. old_string == new_string finds its
	// match, replaces it with itself, writes identical bytes and returned
	// "Edited <path>" — so the identity was told a change happened when
	// nothing did, and would build on a file it believed it had fixed.
	// Observed live 2026-08-24: a resident's own tool emission arrived
	// corrupted this way twice in one turn, caught only because it
	// re-read the file afterwards. A tool that cannot be trusted to
	// report its own inaction forces that re-read on every call.
	if oldStr == newStr {
		return Result{Error: "old_string and new_string are identical — this edit would change nothing; " +
			"reissue it with the replacement text, or use read to check the file first"}, nil
	}

	// AN AMBIGUOUS EDIT CHOSE FOR YOU. Replace(…, 1) takes the FIRST
	// match, so an old_string appearing more than once edited whichever
	// came first and reported plain success — a wrong edit and a right
	// answer are indistinguishable at the call site. Naming the count
	// lets the caller widen the anchor instead of guessing.
	if n := strings.Count(content, oldStr); n > 1 {
		return Result{Error: fmt.Sprintf("old_string appears %d times in %s — this edit would silently take the first; "+
			"include surrounding lines so the anchor is unique", n, path)}, nil
	}

	newContent := strings.Replace(content, oldStr, newStr, 1)
	// Same O_NOFOLLOW law as WriteTool: the write-back must not follow a
	// link swapped in after validation (H3 check/use hardening).
	if err := writeFileNoFollow(path, []byte(newContent), 0644); err != nil {
		return Result{Error: err.Error()}, nil
	}

	return Result{Output: fmt.Sprintf("Edited %s", path)}, nil
}

// --- BashTool ---
