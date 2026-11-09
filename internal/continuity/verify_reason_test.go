package continuity

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A FAILED PROOF NAMES WHICH PROOF, SPENDS NO SPACING, AND A HOST FAULT
// SAYS THE SNAPSHOT WAS NOT JUDGED.
func TestAFailedProofNamesItselfAndSpendsNoSpacing(t *testing.T) {
	a, home, keyPath := maintApp(t)
	dir := a.backupsDir(a.configSnapshot())
	live(t, a, 2)
	appendFixtureEvent(t, a, keyPath, "exp_a")
	a.continuity.runMaintenance(t.Context())
	kept, _ := keptSnapshots(dir)
	if len(kept) != 1 {
		t.Fatalf("rig: %v", kept)
	}
	now := time.Now()
	// Rot: one byte of the database copy; the checksums are the proof
	// that finds it.
	db := filepath.Join(dir, kept[0].Name, SnapshotDB)
	raw, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	good := append([]byte(nil), raw...)
	raw[len(raw)/2] ^= 0xff
	if err := os.WriteFile(db, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = a.continuity.verifyKept(t.Context(), kept[0].Name, true, now)
	if err == nil || !strings.Contains(err.Error(), "does NOT prove: its checksums proof failed") {
		t.Fatalf("the failed proof is not named: %v", err)
	}
	if strings.Contains(err.Error(), home) || strings.Contains(err.Error(), SnapshotDB) {
		t.Errorf("the identity was shown a path: %v", err)
	}
	var refusal *Refusal
	// The spacing is not spent by a proof that failed: the next, spaced,
	// is not refused as spaced.
	if _, err := a.continuity.verifyKept(t.Context(), kept[0].Name, true, now.Add(time.Second)); errors.As(err, &refusal) && refusal.Reason == "spaced" {
		t.Fatalf("a failed proof spent the spacing: %v", err)
	}
	if err := os.WriteFile(db, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.continuity.verifyKept(t.Context(), kept[0].Name, true, now.Add(2*time.Second)); err != nil {
		t.Fatalf("restored, it proves: %v", err)
	}
	// A proof that ran and held spends it.
	_, err = a.continuity.verifyKept(t.Context(), kept[0].Name, true, now.Add(3*time.Second))
	if !errors.As(err, &refusal) || refusal.Reason != "spaced" || refusal.ReasonCode() != "backup.spaced" || !refusal.RefusedBeforeEffect() {
		t.Fatalf("a proof that held did not spend the spacing, or the refusal has no code: %v", err)
	}
	// A host fault: the working set cannot be made (through the package's
	// seam, so root meets it too). The snapshot is not judged; the refusal
	// says so, names no path, and spends no spacing.
	made := makeWorkingSet
	makeWorkingSet = func(dir, pattern string) (string, error) {
		return "", errors.New("mkdir " + filepath.Join(dir, "x") + ": no space left on device")
	}
	t.Cleanup(func() { makeWorkingSet = made })
	_, err = a.continuity.verifyKept(t.Context(), kept[0].Name, false, now.Add(4*time.Second))
	if !errors.As(err, &refusal) || refusal.Reason != "host" || !strings.Contains(err.Error(), "was not judged") || !strings.Contains(err.Error(), "working set") {
		t.Fatalf("a host fault is not a refusal saying the snapshot was not judged: %v", err)
	}
	if strings.Contains(err.Error(), home) {
		t.Errorf("the identity was shown a path: %v", err)
	}
	makeWorkingSet = made
	if _, err := a.continuity.verifyKept(t.Context(), kept[0].Name, false, now.Add(5*time.Second)); err != nil {
		t.Fatalf("after the fault it proves again: %v", err)
	}
}
