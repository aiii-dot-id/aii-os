package updates

import (
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
func TestBootRemovesDisplacedOldBinary(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "current")
	writeFile(t, dir, "aii.old", "displaced by the last swap")

	checkRollbackAt(dir, exePath)

	if fileExists(dir, "aii.old") {
		t.Fatal("the displaced .old image survived a boot")
	}
}
