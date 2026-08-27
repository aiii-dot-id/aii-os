package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// (split from tools.go 2026-08-17 — one file per tool; registry and
// sandbox enforcement live in tools.go)

type LsTool struct{}

func (t *LsTool) Name() string { return "ls" }
func (t *LsTool) Description() string {
	return "List directory contents. Args: path (optional, defaults to .)"
}

func (t *LsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{"type": "string", "description": "Directory to list (default: .)"},
		},
	}
}

func (t *LsTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	var sb strings.Builder
	for _, e := range entries {
		info, _ := e.Info()
		if e.IsDir() {
			sb.WriteString(fmt.Sprintf("d  %s/\n", e.Name()))
		} else {
			sb.WriteString(fmt.Sprintf("f  %s  %d bytes\n", e.Name(), info.Size()))
		}
	}

	return Result{Output: sb.String()}, nil
}

// --- WebFetchTool ---
