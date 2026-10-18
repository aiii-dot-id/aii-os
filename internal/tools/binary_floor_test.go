package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestANameCarryingAiiOsIsTheIdentitys(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ home, file string }{
		{filepath.Join(base, "plain"), "my-aii-os-notes.md"},
		{filepath.Join(base, "aii-os-example"), "note.md"},
		{filepath.Join(base, "work", "aii-os"), filepath.Join("docs", "notes.md")},
	} {
		t.Run(filepath.Base(c.home)+"/"+c.file, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Join(c.home, filepath.Dir(c.file)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(c.home, c.file), []byte("alpha\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			r := NewRegistry(c.home, nil, Timeouts{})
			if res := modelCall(t, r, "read", map[string]any{"file_path": c.file}); !strings.Contains(res.Output, "alpha") {
				t.Fatalf("read: %+v", res)
			}
			if res := modelCall(t, r, "grep", map[string]any{"pattern": "alpha"}); !strings.Contains(res.Output, "alpha") {
				t.Fatalf("grep: %+v", res)
			}
			if res := modelCall(t, r, "ls", map[string]any{"recursive": true}); !strings.Contains(res.Output, filepath.Base(c.file)) {
				t.Fatalf("ls: %+v", res)
			}
			if _, ok := r.Get("write"); !ok {
				return // no write or edit on this host
			}
			if res := modelCall(t, r, "edit", map[string]any{"file_path": c.file, "old_string": "alpha", "new_string": "beta"}); res.Error != "" {
				t.Fatalf("edit: %+v", res)
			}
			if res := modelCall(t, r, "write", map[string]any{"file_path": "aii-os.md", "content": "gamma"}); res.Error != "" {
				t.Fatalf("write: %+v", res)
			}
			if b, err := os.ReadFile(filepath.Join(c.home, c.file)); err != nil || string(b) != "beta\n" {
				t.Fatalf("the edit did not land: %q %v", b, err)
			}
		})
	}
}
