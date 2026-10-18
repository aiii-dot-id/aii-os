package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ONE PATH, ONE LAW. Admit resolves a path through its deepest existing
// ancestor, and the grant test resolved it with EvalSymlinks, falling back
// to the raw path for a file that does not exist yet. So a new file
// written through a symlink inside a grant that points into the home was
// placed in the home by Admit and in the grant by the grant test, which
// switched off the home's name rules: the write created providers.json in
// the home, while the same write to an existing file, or straight into
// the home, was refused.
func TestANewFileThroughAGrantLinkIsHeldToTheHomesRules(t *testing.T) {
	home, grant := t.TempDir(), t.TempDir()
	if err := os.Symlink(home, filepath.Join(grant, "link")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	r := NewRegistry(home, nil, Timeouts{})
	r.SetExtraRoots([]string{grant})
	ctx := context.Background()

	// Control: written straight into the home, the name is refused.
	if res, _ := r.ExecuteModel(ctx, "write", map[string]any{"file_path": filepath.Join(home, "providers.json"), "content": "x"}); !strings.Contains(res.Error, "protected path") {
		t.Fatalf("inconclusive: the home's providers.json was not protected: %+v", res)
	}
	res, _ := r.ExecuteModel(ctx, "write", map[string]any{"file_path": filepath.Join(grant, "link", "providers.json"), "content": "x"})
	if !strings.Contains(res.Error, "protected path") {
		t.Errorf("a new file through the grant's link to the home was not held to the home's rules: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(home, "providers.json")); err == nil {
		t.Error("the write created providers.json in the home")
	}
}
