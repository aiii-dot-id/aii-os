package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestADeleteOrAMoveNeverTakesAGrantedRootWithIt(t *testing.T) {
	r, _, outside := writeHome(t, nil)
	proj := filepath.Join(outside, "proj")
	lib := filepath.Join(proj, "vendor", "lib")
	for _, d := range []string{lib, filepath.Join(proj, "other")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(lib, "keep.txt"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.SetExtraRoots([]string{proj, lib})
	for _, c := range []struct {
		name, code, says string
		args             map[string]any
	}{
		{"deleting the folder that holds a grant", ReasonRootInside, lib, map[string]any{"file_path": filepath.Join(proj, "vendor"), "action": "delete", "recursive": true}},
		{"moving the folder that holds a grant", ReasonRootInside, lib, map[string]any{"file_path": filepath.Join(proj, "vendor"), "action": "move", "to": filepath.Join(proj, "vendored")}},
		{"deleting the grant itself", ReasonRootItself, "granted root itself", map[string]any{"file_path": lib, "action": "delete", "recursive": true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := write(t, r, c.args)
			if !res.Refused || res.ReasonCode != c.code || !strings.Contains(res.Error, c.says) {
				t.Fatalf("want a %s refusal naming %q, got %+v", c.code, c.says, res)
			}
			if body(t, filepath.Join(lib, "keep.txt")) != "kept" {
				t.Fatal("the granted root was touched")
			}
		})
	}
	if res := write(t, r, map[string]any{"file_path": filepath.Join(proj, "other"), "action": "delete", "recursive": true}); res.Error != "" {
		t.Fatalf("a folder that holds no root is deleted as before: %+v", res)
	}
	if _, err := os.Lstat(filepath.Join(proj, "other")); !os.IsNotExist(err) {
		t.Fatalf("the folder that held no root is still there: %v", err)
	}
}
