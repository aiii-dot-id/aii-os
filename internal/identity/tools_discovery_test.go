package identity

import (
	"context"
	"strings"
	"testing"
)

// The seeded docs are discoverable through the discovery organ — the
// pointer SKILLS.md carried under "Not yet" until 2026-08-26. Firstboot
// is deliberately untouched: that ceremony is the bootstrap bundle's,
// only. At every depth: a fresh identity's very first `tools` call, at
// any verbosity, tells them the docs exist.
func TestToolsVerbPointsAtTheSeededDocs(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	for _, depth := range []int{1, 2, 3} {
		res, err := engine.ExecuteAction(context.Background(), "verb", "tools",
			map[string]interface{}{"depth": depth})
		if err != nil {
			t.Fatal(err)
		}
		for _, doc := range []string{"SKILLS.md", "METHOD.md"} {
			if !strings.Contains(res, doc) {
				t.Fatalf("depth %d: the discovery organ does not name %s", depth, doc)
			}
		}
		if !strings.Contains(res, "your edits win") {
			t.Fatalf("depth %d: the pointer does not say the docs are theirs", depth)
		}
	}
}
