package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEveryGateReturnIsARefusalWithItsCode(t *testing.T) {
	r, _, outside := writeHome(t, map[string]string{"a.txt": "alpha"})
	r.SetToolEnabled("edit", false)
	r.SetSafeSource(func() (string, bool) { return "the record did not verify", true })
	safeless, _, _ := writeHome(t, nil)
	for _, c := range []struct {
		name, tool, code string
		reg              *Registry
		args             map[string]any
	}{
		{"disabled by the operator", "edit", ReasonOperatorDisabled, r, map[string]any{"file_path": "a.txt", "old_string": "a", "new_string": "b"}},
		{"arguments missing", "read", ReasonArgumentsRequired, r, map[string]any{}},
		{"SAFE", "write", ReasonSafeSuspended, r, map[string]any{"file_path": "b.txt", "content": "x"}},
		{"a path outside the sandbox", "read", ReasonPathRefused, safeless, map[string]any{"file_path": filepath.Join(outside, "x")}},
		{"the home as a delete's target", "write", ReasonRootItself, safeless, map[string]any{"file_path": ".", "action": "delete", "recursive": true}},
		{"the shell's wall", "shell", ReasonShellWall, safeless, map[string]any{"command": "cat " + filepath.Join(outside, "x")}},
		{"ls outside the sandbox", "ls", ReasonPathRefused, safeless, map[string]any{"path": outside}},
		{"grep outside the sandbox", "grep", ReasonPathRefused, safeless, map[string]any{"pattern": "a", "path": outside}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := c.reg.Get(c.tool); !ok {
				t.Skipf("%s is not on this host", c.tool)
			}
			res := modelCall(t, c.reg, c.tool, c.args)
			if !res.Refused || res.ReasonCode != c.code || res.Error == "" {
				t.Fatalf("want a refusal with %s, got %+v", c.code, res)
			}
		})
	}
	if res := modelCall(t, safeless, "read", map[string]any{"file_path": "missing.txt"}); res.Error == "" || res.Refused {
		t.Fatalf("a call that ran and failed is not a refusal: %+v", res)
	}
}

func TestAToolsOwnArgumentCheckIsARefusal(t *testing.T) {
	r, home, _ := writeHome(t, map[string]string{"keep.txt": "kept"})
	for _, c := range []struct {
		name, tool string
		args       map[string]any
	}{
		{"a write with no content", "write", map[string]any{"file_path": "keep.txt"}},
		{"a copy with no destination", "write", map[string]any{"file_path": "keep.txt", "action": "copy"}},
		{"a move with no destination", "write", map[string]any{"file_path": "keep.txt", "action": "move"}},
		{"a destination with no move or copy", "write", map[string]any{"file_path": "keep.txt", "content": "x", "to": "other.txt"}},
		{"an unknown action", "write", map[string]any{"file_path": "keep.txt", "action": "rename", "to": "other.txt"}},
		{"an empty pattern", "grep", map[string]any{"pattern": ""}},
		{"an empty command", "shell", map[string]any{"command": ""}},
		{"an empty url", "web_fetch", map[string]any{"url": ""}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := r.Get(c.tool); !ok {
				t.Skipf("%s is not on this host", c.tool)
			}
			res := modelCall(t, r, c.tool, c.args)
			if !res.Refused || res.ReasonCode != ReasonArgumentsRequired || res.Error == "" {
				t.Fatalf("want a refusal with %s, got %+v", ReasonArgumentsRequired, res)
			}
			if got := body(t, filepath.Join(home, "keep.txt")); got != "kept" {
				t.Fatalf("the refused call changed the file: %q", got)
			}
			if _, err := os.Lstat(filepath.Join(home, "other.txt")); !os.IsNotExist(err) {
				t.Fatalf("the refused call made a file: %v", err)
			}
		})
	}
}
