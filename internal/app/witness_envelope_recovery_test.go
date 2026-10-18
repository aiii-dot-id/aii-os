package app

import (
	"os"
	"testing"
	"time"
)

func TestAWitnessedIdentityKeepsItsWitnessIdentityThroughALostDatabase(t *testing.T) {
	f := newSealedFixture(t)
	head := f.bootAndAnchor(t)
	enrolledBy := time.Now().UTC().Truncate(time.Second) // the envelope was minted at or before this second
	if _, identities := f.witness.Holds(); identities != 1 {
		t.Fatalf("fixture: the witness holds %d identities after the first anchor", identities)
	}
	// An envelope minted anew in the enrollment's own second would be the
	// original by accident, and this test would prove nothing about the
	// rebuild (a mutation that re-mints survived it until this wait). The
	// wait is for a condition: the clock leaving that second.
	for !time.Now().UTC().Truncate(time.Second).After(enrolledBy) {
		time.Sleep(20 * time.Millisecond)
	}

	// The database goes — every projection and every ephemeral table with it.
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(f.dbPath + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot on the record alone: %v", err)
	}
	if reason, safe := app.SafeMode(); safe {
		stopApp(t, app)
		t.Fatalf("the boot that rebuilt the database is SAFE: %s", reason)
	}
	if raw, err := app.store.LoadWitnessEnvelope(); err != nil || raw != nil {
		stopApp(t, app)
		t.Fatalf("fixture: the rebuilt database must start without the envelope row (%d bytes, %v)", len(raw), err)
	}
	appendFixtureEvent(t, app, f.keyPath, "exp_after_the_loss")
	if err := app.anchorer.CheckAndAnchor(); err != nil {
		stopApp(t, app)
		t.Fatalf("the identity must anchor again as itself: %v", err)
	}
	ordinal, identities := f.witness.Holds()
	if identities != 1 {
		stopApp(t, app)
		t.Fatalf("THE WITNESS NOW HOLDS %d IDENTITIES — the lost database forked the identity at the witness", identities)
	}
	if ordinal <= int64(head)-1 {
		stopApp(t, app)
		t.Fatalf("the witness holds ordinal %d, not past the first anchor's %d: the second anchor did not land", ordinal, head-1)
	}
	ids, _, _, err := app.store.WitnessEnrollment()
	stopApp(t, app)
	if err != nil || len(ids) != 1 {
		t.Fatalf("the record's receipts must name one identity: %v %v", ids, err)
	}

	next := New(f.cfg)
	if err := startLiveForTest(next); err != nil {
		t.Fatalf("the boot after: %v", err)
	}
	defer stopApp(t, next)
	if reason, safe := next.SafeMode(); safe {
		t.Fatalf("THE BOOT AFTER THE LOSS IS SAFE: %s", reason)
	}
}
