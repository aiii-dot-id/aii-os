package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// internal/app's seams (seams.go) are safe because its tests run one at a
// time: a seam belongs to the test that swaps it. A t.Parallel in that
// package would let two tests hold one seam at once, so it is refused here
// for as long as seams.go exists.
//
// guard:tree
func TestAppTestsRunOneAtATimeWhileTheyShareSeams(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "internal", "app")
	if _, err := os.Stat(filepath.Join(dir, "seams.go")); err != nil {
		t.Skip("internal/app has no package seams: nothing forbids t.Parallel there")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no test files read in %s: %v", dir, err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), ".Parallel()") {
			t.Errorf("%s runs in parallel, but internal/app's tests share package seams (seams.go)", filepath.Base(f))
		}
	}
}
