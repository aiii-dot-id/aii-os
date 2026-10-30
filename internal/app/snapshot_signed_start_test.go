package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/continuity/continuitytest"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// A START THAT FIRST CARRIES SIGNING SIGNS THE SNAPSHOTS IT KEEPS. A snapshot
// kept from before snapshots were signed, and the last pass's statement from
// then: the next normal start signs the snapshot in the background, where the
// identity's record proves it, and says it is signed — so the page can
// restore from it, and the start after has nothing to do.
func TestAStartSignsTheSnapshotsKeptFromBeforeSigning(t *testing.T) {
	r := newRestoreRig(t)
	_, snap := r.lived(t, 1, "kept", true)
	a := r.boot(t)
	set := filepath.Join(a.backupsDir(a.configSnapshot()), snap)
	sig := filepath.Join(set, continuity.SnapshotSumsSig)
	if err := continuitytest.Unsign(set, nil); err != nil {
		stopApp(t, a)
		t.Fatalf("rig: the pass's snapshot is not signed: %v", err)
	}
	if err := a.store.SetContinuityStatus(store.ContinuityStatus{Outcome: store.ContinuityOK, Snapshot: snap}); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	stopApp(t, a)

	a = r.boot(t)
	defer stopApp(t, a)
	deadline := time.Now().Add(30 * time.Second)
	for {
		st, ok, _ := a.store.ContinuityStatus()
		if _, err := os.Stat(sig); err == nil && ok && st.Signed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("THE START DID NOT SIGN THE SNAPSHOT IT KEEPS: status %+v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if out, err := continuityDoor(a).Verify(t.Context(), snap); err != nil {
		t.Fatalf("the snapshot signed at the start does not prove: %v %s", err, out)
	}
}
