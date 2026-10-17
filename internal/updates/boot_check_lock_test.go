package updates

import (
	"context"
	"testing"
)

func TestASecondStartOfTheApplyingIdentityLeavesItsReplacementAlone(t *testing.T) {
	rel := newSharedRelease(t, "new binary")
	for _, step := range []string{"locked", "recorded", "backed-up", "marker-retired", "published"} {
		t.Run(step, func(t *testing.T) {
			exe, dirA := program(t), rel.identity(t)
			tgt := targetForTest(t, exe)
			previous := replaceStep
			t.Cleanup(func() { replaceStep = previous })
			reached := false
			replaceStep = func(at string) {
				if at != step {
					return
				}
				reached = true
				before := readEvidence(t, exe, dirA)
				if rolled := checkRollbackAt(dirA, exe); rolled != "" {
					t.Errorf("a second start at %s rolled back: %q", step, rolled)
				}
				if got := readEvidence(t, exe, dirA); got != before {
					t.Errorf("A SECOND START OF THE SAME IDENTITY AT %s CHANGED ITS REPLACEMENT:\n got %+v\nwant %+v", step, got, before)
				}
			}
			if err := rel.checker(dirA).applyTo(context.Background(), exe); err != nil {
				t.Fatalf("the update: %v", err)
			}
			replaceStep = previous
			if !reached {
				t.Fatalf("the replacement never reached %s", step)
			}
			ownerA := ownerForTest(t, dirA)
			assertApplyFile(t, exe, "new binary")
			assertApplyFile(t, tgt.backup, "old binary")
			assertRecord(t, tgt, ownerA, 0, "old binary", "new binary")

			// The update's first boot counts itself, and its rollback is armed…
			if rolled := checkRollbackAt(dirA, exe); rolled != "" {
				t.Fatalf("the update's first boot rolled back: %q", rolled)
			}
			assertRecord(t, tgt, ownerA, 1, "old binary", "new binary")
			if !rollbackArmedAt(dirA, exe) {
				t.Error("the counted first boot with no marker is not armed")
			}
			// …and its healthy end retires the update.
			writeBootMarkerAt(dirA, exe)
			if exists(tgt.backup) || exists(tgt.record) {
				t.Error("the healthy boot did not retire the update")
			}
			assertApplyFile(t, exe, "new binary")
		})
	}
}

// THE LOCK HELD ELSEWHERE DEFERS THE WHOLE DECISION. Every state the
// boot check settles — a healthy boot's leftovers (marker present), an
// update's first boot (attempt 0), a failed first boot (attempt 1, no
// marker: the rollback) — is left exactly as it is while another holder
// has the program's lock, and is settled as before by the boot after it
// is released. The rollback is decided and done under the one lock the
// check took: it takes none of its own (a second acquisition in this
// process would be refused by the first and defer every rollback —
// TestAFailedUpdateBootRollsBack).
func TestABootCheckWhileTheProgramIsLockedDefersAndChangesNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		marker bool // a healthy boot's marker stands
		counts int  // boots of the update counted before the check
		then   func(t *testing.T, exe, dir string, tgt target, rolled string)
	}{
		{"leftovers of a healthy boot", true, 1, func(t *testing.T, exe, dir string, tgt target, rolled string) {
			if rolled != "" || exists(tgt.backup) || exists(tgt.record) {
				t.Errorf("the healthy boot's leftovers were not retired (rolled %q)", rolled)
			}
			assertApplyFile(t, exe, "new binary")
		}},
		{"first boot of the update", false, 0, func(t *testing.T, exe, dir string, tgt target, rolled string) {
			if rolled != "" {
				t.Errorf("the first boot rolled back: %q", rolled)
			}
			assertApplyFile(t, exe, "new binary")
			assertRecord(t, tgt, ownerForTest(t, dir), 1, "old binary", "new binary")
		}},
		{"failed first boot", false, 1, func(t *testing.T, exe, dir string, tgt target, rolled string) {
			if rolled == "" {
				t.Error("the failed update did not roll back once the lock was free")
			}
			assertApplyFile(t, exe, "old binary")
			if exists(tgt.backup) || exists(tgt.record) {
				t.Error("the rollback did not retire its backup and record")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exe, dir := program(t), t.TempDir()
			writeFile(t, dir, markerFile, "ok") // the old binary booted healthy
			tgt, err := swapForTest(t, exe, []byte("new binary"), dir)
			if err != nil {
				t.Fatalf("swap: %v", err)
			}
			for range tc.counts {
				checkRollbackAt(dir, exe)
			}
			if tc.marker {
				writeFile(t, dir, markerFile, "ok")
			}
			release, err := lockTarget(tgt)
			if err != nil {
				t.Fatalf("hold the program's lock: %v", err)
			}
			before := readEvidence(t, exe, dir)
			if rolled := checkRollbackAt(dir, exe); rolled != "" {
				t.Errorf("a check under another holder's lock rolled back: %q", rolled)
			}
			if got := readEvidence(t, exe, dir); got != before {
				t.Errorf("A CHECK UNDER ANOTHER HOLDER'S LOCK CHANGED THE UPDATE STATE:\n got %+v\nwant %+v", got, before)
			}
			release()
			tc.then(t, exe, dir, tgt, checkRollbackAt(dir, exe))
		})
	}
}
