package test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// guard:tree
func TestDrillsGrepCategoriesTheTreeStillEmits(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "packaging", "drills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("no drills to check: %v", err)
	}

	declared := map[string]bool{}
	for _, d := range logsink.Details() {
		declared[d.Name()] = true
	}
	// A category looks like subsystem.aspect; anything else a drill
	// greps for is prose and outside what this test can promise.
	shaped := regexp.MustCompile(`^[a-z]+\.[a-z]+$`)

	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range regexp.MustCompile(`saw_since_fresh\s+"([^"]+)"`).FindAllStringSubmatch(string(body), -1) {
			arg := m[1]
			if !shaped.MatchString(arg) {
				t.Errorf("%s greps %q, which is prose — grep a category (subsystem.aspect) so a rename cannot dissolve it", e.Name(), arg)
				continue
			}
			checked++
			if !declared[arg] {
				t.Errorf("%s greps the category %q, which nothing in the tree declares — the drill would never match, and a drill that cannot fail is worse than no drill", e.Name(), arg)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no drill greps were found at all — this test would pass while checking nothing")
	}
}
