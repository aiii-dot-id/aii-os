package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSandboxToolFailuresCarryTheirReason(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, "f.txt")
	if err := os.WriteFile(file, []byte("one\ntwo two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(home, nil, Timeouts{})
	for _, c := range []struct {
		tool string
		args map[string]any
		code string
	}{
		{"ls", map[string]any{"path": filepath.Join(home, "missing")}, ReasonNotFound},
		{"read", map[string]any{"file_path": filepath.Join(home, "missing")}, ReasonNotFound},
		{"read", map[string]any{"file_path": filepath.Join(home, "d")}, ReasonNotAFile},
		{"read", map[string]any{"file_path": file, "offset": 0}, ReasonArgumentInvalid},
		{"read", map[string]any{"file_path": file, "offset": 9}, ReasonOffsetPastEnd},
		{"edit", map[string]any{"file_path": file, "old_string": "three", "new_string": "x"}, ReasonEditNoMatch},
		{"edit", map[string]any{"file_path": file, "old_string": "two", "new_string": "x"}, ReasonEditNotUnique},
		{"edit", map[string]any{"file_path": file, "old_string": "one", "new_string": "one"}, ReasonEditNoChange},
		{"edit", map[string]any{"file_path": filepath.Join(home, "missing"), "old_string": "a", "new_string": "b"}, ReasonNotFound},
	} {
		res, err := r.Execute(context.Background(), c.tool, c.args)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Failed() || res.ReasonCode != c.code {
			t.Errorf("%s %v: failed %v, reason %q, want %q (%s)", c.tool, c.args, res.Failed(), res.ReasonCode, c.code, res.Error)
		}
	}
}
