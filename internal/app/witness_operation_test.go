package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// operationFixture is the pinned version 1 operation file of a stage
// (internal/witness/testdata/operation-v1).
func operationFixture(t *testing.T, stage string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "witness", "testdata", "operation-v1", stage+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// archive writes raw as an operation archive beside the ledger in dir and
// returns its name.
func archive(t *testing.T, dir string, raw []byte) string {
	t.Helper()
	sum := sha256.Sum256(raw)
	name := hex.EncodeToString(sum[:]) + ".json"
	if err := os.MkdirAll(filepath.Join(dir, witness.AbandonedDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, witness.AbandonedDirName, name), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func archivesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, witness.AbandonedDirName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// sumsName says whether a kept snapshot's checksum list names member.
func sumsName(t *testing.T, set, member string) bool {
	t.Helper()
	sums, err := os.ReadFile(filepath.Join(set, continuity.SnapshotSums))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(sums), "  "+member+"\n")
}

// A LATER VERSION'S WITNESS OPERATION IS CARRIED THROUGH A RESTORE OVER
// NEWER STATE, A RESTART AND THE NEXT BACKUP, IN EVERY STAGE (the fixture
// matrix of the witness design's revision 7). The identity holds an
// operation of the stage, with its retained candidates, and two archives;
// a snapshot captures them. Then newer state displaces them: the record
// moves on, another operation stands and a third archive. The restore puts
// the snapshot's operation and its two archives back, byte for byte, and
// the newer ones go aside with the newer record — none of the newer
// archives is left beside the older record. They stand after a restart,
// and the next backup carries them.
func TestAWitnessOperationIsCarriedThroughARestoreARestartAndTheNextBackup(t *testing.T) {
	for _, stage := range []string{"staged", "sent", "receipt", "head", "config"} {
		t.Run(stage, func(t *testing.T) {
			r := newRestoreRig(t)
			dir := filepath.Dir(r.ledgerPath)
			a := r.boot(t)
			appendFixtureEvent(t, a, r.keyPath, "exp_before")
			operation := operationFixture(t, stage)
			if err := os.WriteFile(filepath.Join(dir, witness.OperationFileName), operation, 0o600); err != nil {
				stopApp(t, a)
				t.Fatal(err)
			}
			kept := []string{archive(t, dir, operationFixture(t, "staged")), archive(t, dir, operationFixture(t, "sent"))}
			maintenancePass(t, a)
			views := continuityDoor(a).View().Snapshots
			if len(views) == 0 {
				stopApp(t, a)
				t.Fatal("rig: no snapshot")
			}
			snap := views[0].Name
			at := a.ledger.LastSeq()

			// Newer state displaces it.
			appendFixtureEvent(t, a, r.keyPath, "exp_after")
			newer := operationFixture(t, "config")
			if stage == "config" {
				newer = operationFixture(t, "head")
			}
			if err := os.WriteFile(filepath.Join(dir, witness.OperationFileName), newer, 0o600); err != nil {
				stopApp(t, a)
				t.Fatal(err)
			}
			leftover := archive(t, dir, operationFixture(t, "receipt"))
			plan, err := continuityDoor(a).RestorePlan(continuity.SourceSnapshot, snap)
			if err != nil {
				stopApp(t, a)
				t.Fatal(err)
			}
			if err := continuityDoor(a).Restore(continuity.SourceSnapshot, snap, plan.ConfirmText); err != nil {
				stopApp(t, a)
				t.Fatal(err)
			}
			stopApp(t, a)

			// The restore, and then a restart.
			for _, start := range []string{"the restore's start", "a restart"} {
				a = r.boot(t)
				if a.ledger == nil || a.ledger.LastSeq() != at {
					reason, _ := a.SafeMode()
					stopApp(t, a)
					t.Fatalf("%s: THE RESTORE DID NOT PUT THE RECORD BACK TO THE SNAPSHOT'S %d (%s)", start, at, reason)
				}
				if got, err := os.ReadFile(filepath.Join(dir, witness.OperationFileName)); err != nil || !bytes.Equal(got, operation) {
					stopApp(t, a)
					t.Fatalf("%s: THE SNAPSHOT'S WITNESS OPERATION DOES NOT STAND BESIDE THE RESTORED RECORD (%v)", start, err)
				}
				if got := archivesIn(t, dir); len(got) != 2 || !(got[0] == kept[0] || got[0] == kept[1]) || !(got[1] == kept[0] || got[1] == kept[1]) {
					stopApp(t, a)
					t.Fatalf("%s: THE ARCHIVES BESIDE THE RESTORED RECORD ARE %v, WANT THE SNAPSHOT'S %v (and not the newer %s)", start, got, kept, leftover)
				}
				if start == "a restart" {
					break
				}
				stopApp(t, a)
			}
			sets := continuityDoor(a).View().SetAside
			if len(sets) != 1 {
				stopApp(t, a)
				t.Fatalf("rig: %d sets aside", len(sets))
			}
			if got, err := os.ReadFile(filepath.Join(sets[0].Path, witness.OperationFileName)); err != nil || !bytes.Equal(got, newer) {
				stopApp(t, a)
				t.Fatalf("THE NEWER OPERATION DID NOT GO ASIDE WITH THE NEWER RECORD (%v)", err)
			}
			if got := archivesIn(t, sets[0].Path); len(got) != 3 {
				stopApp(t, a)
				t.Fatalf("the newer record's archives did not go aside with it: %v", got)
			}

			// The next backup carries them.
			made, err := askedSnapshot(t, a)
			if err != nil || made.Name == snap {
				stopApp(t, a)
				t.Fatalf("rig: the next backup was not made: %+v %v", made, err)
			}
			set := filepath.Join(a.backupsDir(a.configSnapshot()), made.Name)
			stopApp(t, a)
			if !sumsName(t, set, witness.OperationFileName) || !sumsName(t, set, witness.AbandonedDirName+"/"+kept[0]) || !sumsName(t, set, witness.AbandonedDirName+"/"+kept[1]) {
				t.Fatal("THE NEXT BACKUP DOES NOT CARRY THE WITNESS OPERATION AND ITS ARCHIVES")
			}
			if got, err := os.ReadFile(filepath.Join(set, witness.OperationFileName)); err != nil || !bytes.Equal(got, operation) {
				t.Fatalf("the next backup's operation file is not the one that stood (%v)", err)
			}
		})
	}
}

// AN OPERATION FILE PARKS THE WITNESS, AND ONLY THE WITNESS. A running
// identity with a later version's operation beside its record is not
// SAFE; the anchor pass asks the witness nothing and changes nothing; the
// operator is told; and a full backup is still made, carrying the
// operation.
func TestAnOperationFileParksTheWitnessAndNothingElse(t *testing.T) {
	f := newSealedFixture(t)
	f.bootAndAnchor(t)
	dir := filepath.Dir(f.ledgerPath)
	if err := os.WriteFile(filepath.Join(dir, witness.OperationFileName), operationFixture(t, "head"), 0o600); err != nil {
		t.Fatal(err)
	}
	heldAt, heldIdentities := f.witness.Holds()
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	if reason, safe := a.SafeMode(); safe {
		t.Fatalf("AN OPERATION FILE PUT THE IDENTITY IN SAFE: %s", reason)
	}
	appendFixtureEvent(t, a, f.keyPath, "exp_while_parked")
	if err := a.anchorer.CheckAndAnchor(); !errors.Is(err, witness.ErrWitnessOperationActive) {
		t.Fatalf("THE ANCHOR PASS WAS NOT PARKED BEHIND THE OPERATION FILE: %v", err)
	}
	if at, identities := f.witness.Holds(); at != heldAt || identities != heldIdentities {
		t.Fatalf("the witness took a bookmark behind the operation file: record %d of %d identities, %d of %d before", at, identities, heldAt, heldIdentities)
	}
	ids, err := a.store.OutboxIDsUnder("witness_parked_")
	if err != nil || len(ids) != 1 {
		t.Fatalf("THE OPERATOR WAS NOT TOLD THE WITNESS IS PARKED: %q %v", ids, err)
	}
	if said, _ := a.store.OutboxContent(ids[0]); !strings.HasPrefix(said, witnessParkedSaid) {
		t.Fatalf("the notice is not the one for a parked witness: %q", said)
	}
	made, err := askedSnapshot(t, a)
	if err != nil {
		t.Fatalf("A FULL BACKUP WAS NOT MADE behind the operation file: %v", err)
	}
	if !sumsName(t, filepath.Join(a.backupsDir(a.configSnapshot()), made.Name), witness.OperationFileName) {
		t.Fatal("the backup does not carry the operation file")
	}
}
