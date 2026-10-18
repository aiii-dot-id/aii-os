package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// NO PATH IS THE HOME. The gate admitted grep's and ls's path only when
// one was given, and the tools then opened ".", the process's directory:
// with the home set elsewhere (tools.cwd), ls and grep with no path listed
// and searched a directory the gate refuses by name. The gate gives them
// the home now.
func TestNoPathIsTheHome(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "inside.txt"), []byte("INSIDE_MARK\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "outside-secret.txt"), []byte("OUTSIDE_SECRET_MARK\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(home, nil, Timeouts{})
	t.Chdir(outside)
	ctx := context.Background()

	// Control: named, the outside directory is refused.
	if res, _ := r.ExecuteModel(ctx, "ls", map[string]any{"path": outside}); !strings.Contains(res.Error, "outside sandbox") {
		t.Fatalf("inconclusive: the outside directory was not refused by name: %+v", res)
	}
	res, _ := r.ExecuteModel(ctx, "ls", map[string]any{})
	if strings.Contains(res.Output, "outside-secret.txt") || !strings.Contains(res.Output, "inside.txt") {
		t.Errorf("ls with no path listed %q (%s), not the home", res.Output, res.Error)
	}
	res, _ = r.ExecuteModel(ctx, "grep", map[string]any{"pattern": "_MARK"})
	if strings.Contains(res.Output, "OUTSIDE_SECRET_MARK") || !strings.Contains(res.Output, "INSIDE_MARK") {
		t.Errorf("grep with no path searched %q (%s), not the home", res.Output, res.Error)
	}
}
