package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func errNotTheFileRead(path string) error {
	return fmt.Errorf("%s is no longer the file this edit read — it was replaced in between; nothing was written. Read it again and reissue the edit", path)
}

type EditTool struct {
	held func(ctx context.Context, path string, file os.FileInfo, by *os.File) error

	read func(ctx context.Context, path string, file os.FileInfo, by *os.File) error

	window func(at, path string)
}

func (t *EditTool) Name() string { return "edit" }
func (t *EditTool) Description() string {
	return "Replace exact text in a file."
}

func (t *EditTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path":  map[string]any{"type": "string"},
			"old_string": map[string]any{"type": "string", "description": "Exact text, without read's line numbers"},
			"new_string": map[string]any{"type": "string"},
		},
		"required": []string{"file_path", "old_string", "new_string"},
	}
}

func (t *EditTool) Execute(ctx context.Context, args map[string]any) (Result, error) {
	path, _ := args["file_path"].(string)
	oldStr, _ := args["old_string"].(string)
	newStr, _ := args["new_string"].(string)

	if path == "" || oldStr == "" {
		return Result{Error: "file_path and old_string are required"}, nil
	}
	if t.window != nil {
		t.window("gate", path)
	}

	f, st, err := openRegular(path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	if t.read != nil {
		if kept := t.read(ctx, path, st, f); kept != nil {
			f.Close()
			return Refusal(ReasonPathRefused, "access denied: "+kept.Error()), nil
		}
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	content := string(data)
	if !strings.Contains(content, oldStr) {
		if numberedLines(oldStr) {
			return Result{Error: "old_string not found in file: every line of it begins with a line number and a tab, " +
				"the prefix read shows and the file does not hold; reissue the edit with the text after each tab"}, nil
		}
		return Result{Error: "old_string not found in file"}, nil
	}

	if oldStr == newStr {
		return Result{Error: "old_string and new_string are identical — this edit would change nothing; " +
			"reissue it with the replacement text, or use read to check the file first"}, nil
	}

	if n := strings.Count(content, oldStr); n > 1 {
		return Result{Error: fmt.Sprintf("old_string appears %d times in %s — this edit would silently take the first; "+
			"include surrounding lines so the anchor is unique", n, path)}, nil
	}

	newContent := strings.Replace(content, oldStr, newStr, 1)
	if err := ctx.Err(); err != nil {
		return Result{Error: fmt.Sprintf("edit cancelled before anything was written: %v", err)}, nil
	}

	var held func(string, os.FileInfo, *os.File) error
	if t.held != nil {
		held = func(p string, f os.FileInfo, by *os.File) error { return t.held(ctx, p, f, by) }
	}
	if err := rewriteSameFile(path, []byte(newContent), st, held); err != nil {

		var kept *SubstrateError
		if errors.As(err, &kept) {
			return Refusal(ReasonPathRefused, "access denied: "+err.Error()), nil
		}

		var emptied *emptiedError
		if !errors.As(err, &emptied) {
			return Result{Error: err.Error()}, nil
		}
		return Result{Error: editFailure(path, emptied, rewriteSameFile(path, data, st, held))}, nil
	}

	return Result{Output: fmt.Sprintf("Edited %s", path)}, nil
}

func numberedLines(s string) bool {
	for _, line := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		rest := strings.TrimLeft(line, " ")
		digits := len(rest) - len(strings.TrimLeft(rest, "0123456789"))
		if digits == 0 || !strings.HasPrefix(rest[digits:], "\t") {
			return false
		}
	}
	return true
}

func editFailure(path string, first *emptiedError, back error) string {
	if back == nil {
		return fmt.Sprintf("%v — the edit was not made. %s had been emptied for the write, and what it held before has been put back",
			first.err, path)
	}
	var again *emptiedError
	if errors.As(back, &again) {
		return fmt.Sprintf("%v — the edit was not made. %s had been emptied for the write, and putting back what it held stopped as well (%v): it now holds the first %d of the %d bytes it held before, and nothing of the edit",
			first.err, path, again.err, again.wrote, again.of)
	}
	return fmt.Sprintf("%v. Putting back what it held before was refused: %v. The edit was not made", first, back)
}
