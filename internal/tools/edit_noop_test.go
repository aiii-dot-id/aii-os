package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

func editTool(t *testing.T) (*EditTool, string) {
	t.Helper()
	dir := t.TempDir()
	return &EditTool{}, dir
}

func TestEditRefusesANoOp(t *testing.T) {
	e, dir := editTool(t)
	path := filepath.Join(dir, "f.go")
	const body = "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := e.Execute(context.Background(), map[string]interface{}{
		"file_path": path, "old_string": "func main() {}", "new_string": "func main() {}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatalf("an edit that changes nothing reported success: %+v", res)
	}
	if !strings.Contains(res.Error, "identical") {
		t.Fatalf("the refusal does not say what is wrong: %q", res.Error)
	}
	// .
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Fatal("a refused edit still wrote to the file")
	}
}

func TestEditRefusesAnAmbiguousAnchor(t *testing.T) {
	e, dir := editTool(t)
	path := filepath.Join(dir, "f.go")
	const body = "x := 1\ny := 2\nx := 1\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	res, _ := e.Execute(context.Background(), map[string]interface{}{
		"file_path": path, "old_string": "x := 1", "new_string": "x := 99",
	})
	if res.Error == "" {
		t.Fatalf("an ambiguous edit silently took the first match: %+v", res)
	}
	if !strings.Contains(res.Error, "2 times") {
		t.Fatalf("the refusal does not say HOW ambiguous it is, so the caller cannot fix it: %q", res.Error)
	}
	after, _ := os.ReadFile(path)
	if string(after) != body {
		t.Fatal("a refused edit still wrote to the file")
	}
}

// .
// .
// .
func TestEditStillPerformsAUniqueChange(t *testing.T) {
	e, dir := editTool(t)
	path := filepath.Join(dir, "f.go")
	if err := os.WriteFile(path, []byte("x := 1\ny := 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.Execute(context.Background(), map[string]interface{}{
		"file_path": path, "old_string": "x := 1", "new_string": "x := 99",
	})
	if err != nil || res.Error != "" {
		t.Fatalf("an ordinary edit was refused: %v %+v", err, res)
	}
	after, _ := os.ReadFile(path)
	if string(after) != "x := 99\ny := 2\n" {
		t.Fatalf("the edit did not land: %q", string(after))
	}
	if !strings.Contains(res.Output, "Edited") {
		t.Fatalf("a real edit did not report itself: %q", res.Output)
	}
}
