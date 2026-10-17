package updates

import (
	"os"
	"path/filepath"
	"testing"
)

// A START THAT NEVER REACHED THE UPDATE'S BOOT LEAVES NO BOOT COUNTED. A
// configuration the program refused twice asked the boot's own check
// twice, to count and then to restore; a restore that did not complete
// left its count in the record, and the next start, its file corrected,
// was rolled back for a boot the update had never begun. Asked through
// RollBackUnsettled the record changes only when the restore completes:
// here the backup is not the backup, the restore is refused however often
// it is asked, and the start that then reaches its boot is the update's
// first, counted and kept.
func TestARestoreAskedBeforeAnyBootLeavesNoCountWhenItDoesNotComplete(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")
	tgt, err := swapForTest(t, exePath, []byte("new binary"), dir)
	if err != nil {
		t.Fatalf("swap: %v", err)
	}
	owner := ownerForTest(t, dir)
	if err := os.WriteFile(tgt.backup, []byte("old bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if rolled := rollBackUnsettledAt(dir, exePath); rolled != "" {
			t.Fatalf("a backup that is not the backup was restored: %q", rolled)
		}
		assertRecord(t, tgt, owner, 0, "old binary", "new binary")
		if rollbackArmedAt(dir, exePath) {
			t.Fatal("A RESTORE THAT DID NOT COMPLETE LEFT A BOOT COUNTED: the next start that loads would be rolled back for a start that never booted")
		}
	}

	// The backup is put right, and the next start reaches its boot.
	if err := os.WriteFile(tgt.backup, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if rolled := checkRollbackAt(dir, exePath); rolled != "" {
		t.Fatalf("A START THAT REACHED ITS BOOT WAS ROLLED BACK FOR ONE THAT NEVER DID (returned %q)", rolled)
	}
	if got, _ := os.ReadFile(exePath); string(got) != "new binary" {
		t.Fatalf("after the update's first boot the binary is %q", got)
	}
	assertRecord(t, tgt, owner, 1, "old binary", "new binary")
	writeBootMarkerAt(dir, exePath)
	if exists(tgt.backup) || exists(tgt.record) {
		t.Error("a healthy boot must retire the backup and the record")
	}
}

// And where the restore completes it is the boot's own rollback, with no
// boot counted first: the previous binary is back and the update's state
// retired.
func TestARestoreAskedBeforeAnyBootRestoresThePreviousProgram(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")
	tgt, err := swapForTest(t, exePath, []byte("new binary"), dir)
	if err != nil {
		t.Fatalf("swap: %v", err)
	}
	if rolled := rollBackUnsettledAt(dir, exePath); rolled == "" {
		t.Fatal("an unsettled update was not restored")
	}
	if got, _ := os.ReadFile(exePath); string(got) != "old binary" {
		t.Fatalf("after the restore the binary is %q, want the backup", got)
	}
	if exists(tgt.backup) || exists(tgt.record) {
		t.Error("the restore must retire the backup and the record")
	}
	// Nothing in flight: nothing to restore, and nothing changes.
	if rolled := rollBackUnsettledAt(dir, exePath); rolled != "" {
		t.Fatalf("restored with no update in flight: %q", rolled)
	}
}

// An earlier build's state has no record to restore by. Adopting it is
// counting, which is a boot's: this start leaves it as it found it, and
// the starts that reach their boot adopt it and then judge it, as before.
func TestARestoreAskedBeforeAnyBootLeavesAnEarlierBuildsStateToABoot(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "current binary")
	writeFile(t, dir, "aii.previous", "legacy backup")

	if rolled := rollBackUnsettledAt(dir, exePath); rolled != "" {
		t.Fatalf("a legacy backup was restored with nothing to check it against: %q", rolled)
	}
	if rollbackArmedAt(dir, exePath) {
		t.Fatal("A START THAT NEVER BOOTED ADOPTED AN EARLIER BUILD'S STATE, which counts a boot")
	}
	if got, _ := os.ReadFile(exePath); string(got) != "current binary" {
		t.Fatalf("the binary changed: %q", got)
	}
	if rolled := checkRollbackAt(dir, exePath); rolled != "" {
		t.Fatalf("a legacy backup was restored on a boot's first sighting: %q", rolled)
	}
	if rolled := checkRollbackAt(dir, exePath); rolled == "" {
		t.Fatal("the adopted legacy backup was never used")
	}
}
