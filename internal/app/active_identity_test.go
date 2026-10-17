package app

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/continuity/continuitytest"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

// A STAGED CONFIGURATION DOES NOT MOVE THE IDENTITY THAT IS RUNNING. The
// operator may save another identity's paths for the next start; a reload
// publishes them, and says they are saved for the next boot. Until that
// boot, what the running identity does stays with the files it has open:
// a credential it stores, the escrow of its key, its snapshots, and the
// floor that keeps its substrate from its own tools. Each used to follow
// the saved paths to the other identity's files.
func TestAStagedConfigurationDoesNotMoveTheRunningIdentity(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	keyA, ledgerA, dbA := birthFixture(t, dirA, "Aster")
	keyB, ledgerB, dbB := birthFixture(t, dirB, "Bryn")
	a := newBareApp(t, safebootConfig(t, dirA, "Aster", keyA, ledgerA, dbA))
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	running := a.door.kp.Fingerprint()

	staged := a.configSnapshot()
	staged.Identity = IdentityConfig{KeyPath: keyB, LedgerPath: ledgerB, DBPath: dbB}
	if _, err := saveConfig(&staged); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if got := a.configSnapshot().Identity.LedgerPath; got != ledgerB {
		t.Fatalf("the fixture did not publish the next start's paths: %s", got)
	}

	inA := func(what, path string) {
		t.Helper()
		if !inside(realPath(dirA), realPath(path)) {
			t.Errorf("%s is %s — not the running identity's (%s)", what, path, dirA)
		}
	}
	cred, err := a.ownedCredentialPath(oauth.Kinds()[0])
	if err != nil {
		t.Fatal(err)
	}
	inA("an owned credential", cred)
	profiles, err := a.credentialsDir()
	if err != nil {
		t.Fatal(err)
	}
	inA("a profile's files", profiles)
	inA("the backups", a.backupsDir(a.configSnapshot()))
	inA("the snapshot key", a.continuity.SnapshotKeyPathForTest())

	file, _, err := continuityDoor(a).EscrowCreate([]byte(pagePass))
	if err != nil {
		t.Fatalf("an escrow of the running identity: %v", err)
	}
	_, sealedFor, err := continuitytest.EscrowedKey(file, []byte(pagePass))
	if err != nil {
		t.Fatal(err)
	}
	if sealedFor != running {
		t.Fatalf("THE ESCROW NAMED FOR THIS IDENTITY HOLDS ANOTHER'S KEY: %s, running %s", sealedFor, running)
	}

	kept := false
	for _, place := range a.ensureRing5Policy().Places() {
		if inside(place, realPath(ledgerA)) {
			kept = true
		}
	}
	if !kept {
		t.Errorf("the floor no longer keeps the running identity's record %s: %v", ledgerA, a.ensureRing5Policy().Places())
	}

	// And the saved paths are what the next start opens.
	stopApp(t, a)
	saved, err := ReadConfig(staged.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	next := newBareApp(t, saved)
	if err := startLiveForTest(next); err != nil {
		t.Fatal(err)
	}
	if got := next.activeIdentity(next.configSnapshot().Identity); got.LedgerPath != ledgerB || got.KeyPath != keyB {
		t.Fatalf("the next start did not open the saved identity: %+v", got)
	}
	if next.door.kp.Fingerprint() == running {
		t.Fatal("the next start runs the identity that was replaced")
	}
}
