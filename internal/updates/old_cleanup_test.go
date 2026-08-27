package updates

import (
	"path/filepath"
	"testing"
)

// A Windows swap leaves the displaced running image as .old (a running
// image renames, never removes). The swap's old comment promised a
// next-boot cleanup that did not exist anywhere; checkRollbackAt is
// that cleanup now, on every platform's every boot.
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
