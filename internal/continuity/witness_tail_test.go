package continuity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// witness_tail_test.go — a snapshot carries the witness tail that is true of
// its own ledger, and every proof holds the chain to the tail beside it.

// echoTail writes the tail as the anchorer leaves it after a verified
// receipt for the given record: the file's own format.
func echoTail(t *testing.T, dir string, seq uint64, hash string) {
	t.Helper()
	body := fmt.Sprintf(`{"ledger_ordinal":%d,"ledger_hash":%q,"witnessed_at":"2026-09-01T00:00:00Z","witness_key_fingerprint":"fp"}`, seq, hash)
	if err := os.WriteFile(witness.TailPath(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// THE SNAPSHOT'S TAIL IS TRUE OF THE SNAPSHOT'S LEDGER. The tail is taken
// before the capture: an anchor that lands while the database is being
// copied names a record the snapshot does not hold, and a tail copied after
// it made the snapshot say of itself that it had been cut. Here the anchor
// lands in exactly that window.
func TestAnAnchorDuringTheCopyDoesNotReachTheSnapshotsTail(t *testing.T) {
	// The database in a directory of its own: the tail stands beside the
	// LEDGER, where the anchorer writes it, and the pass once looked for it
	// beside the database.
	a, _, keyPath := maintAppWithDatabaseIn(t, "db")
	cfg := a.configSnapshot()
	srcDir := filepath.Dir(cfg.Identity.LedgerPath)
	appendFixtureEvent(t, a, keyPath, "exp_before")
	events, err := ledger.ReadAll(cfg.Identity.LedgerPath)
	if err != nil {
		t.Fatal(err)
	}
	attested := events[len(events)-1]
	echoTail(t, srcDir, attested.Seq, attested.EntryHash())

	t.Cleanup(func() { a.continuity.SnapshotStep = nil })
	a.continuity.SnapshotStep = func(step string) {
		if step != "captured" {
			return
		}
		// The identity goes on living, and the witness attests the new
		// record: the live tail now names a record past the capture.
		appendFixtureEvent(t, a, keyPath, "exp_during_the_copy")
		later, err := ledger.ReadAll(cfg.Identity.LedgerPath)
		if err != nil {
			t.Fatal(err)
		}
		echoTail(t, srcDir, later[len(later)-1].Seq, later[len(later)-1].EntryHash())
	}
	a.continuity.runMaintenance(t.Context())
	a.continuity.SnapshotStep = nil

	snaps := backupFiles(t, a.backupsDir(cfg))
	if len(snaps) != 1 {
		t.Fatalf("AN ANCHOR DURING THE COPY COST THE DAY'S SNAPSHOT: %v", snaps)
	}
	snap := filepath.Join(a.backupsDir(cfg), snaps[0])
	tail, err := witness.ReadLocalTail(snap)
	if err != nil || tail == nil {
		t.Fatalf("the snapshot carries no tail: %v", err)
	}
	if uint64(tail.LedgerOrdinal) != attested.Seq || tail.LedgerHash != attested.EntryHash() {
		t.Fatalf("the snapshot's tail names record %d; the capture was preceded by the tail for record %d", tail.LedgerOrdinal, attested.Seq)
	}
	// And anyone verifying the snapshot reads a chain that holds its tail.
	out, errOut, code := genesis.VerifyCommand(filepath.Join(snap, "ledger.jsonl"), nil, func(dir string) (genesis.Beside, error) {
		beside, err := witness.LoadBeside(dir, genesis.PinnedRoot())
		if err != nil {
			return nil, err
		}
		return beside, nil
	})
	if code != genesis.ExitVerified || !strings.Contains(out, fmt.Sprintf("holds record %d ", attested.Seq)) {
		t.Fatalf("the published snapshot does not verify against its own tail: exit %d\n%s%s", code, out, errOut)
	}
}

// A SNAPSHOT WHOSE LEDGER CONTRADICTS THE TAIL BESIDE IT IS NOT PUBLISHED.
func TestASnapshotThatContradictsItsTailIsNotPublished(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	appendFixtureEvent(t, a, keyPath, "exp_before")
	echoTail(t, filepath.Dir(cfg.Identity.LedgerPath), a.ledger.LastSeq(), "sha256:"+strings.Repeat("f", 64))
	a.continuity.runMaintenance(t.Context())
	if snaps := backupFiles(t, a.backupsDir(cfg)); len(snaps) != 0 {
		t.Fatalf("a record the witness attested otherwise was published as a recovery point: %v", snaps)
	}
}

// SAFE MODE'S WALK OF THE LIVE CONTAINERS IS HELD TO THE TAIL TOO. Outside
// SAFE the day's verification is the snapshot's prove, held to the same
// tail (TestASnapshotThatContradictsItsTailIsNotPublished); in SAFE no
// snapshot is made, and this walk is the day's evidence.
func TestTheLiveLedgersVerificationIsHeldToItsTail(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	appendFixtureEvent(t, a, keyPath, "exp_before")
	if _, err := a.continuity.verifyLiveLedger(cfg); err != nil {
		t.Fatalf("a whole ledger with no tail beside it: %v", err)
	}
	echoTail(t, filepath.Dir(cfg.Identity.LedgerPath), a.ledger.LastSeq()+3, "sha256:"+strings.Repeat("a", 64))
	_, err := a.continuity.verifyLiveLedger(cfg)
	var refusal *witness.TailRefusal
	if !errors.As(err, &refusal) || !refusal.Truncation {
		t.Fatalf("a live ledger that ends before the record its tail names: %v", err)
	}
	if !strings.Contains(err.Error(), "witness: ") {
		t.Errorf("the alert does not carry the witness's line: %v", err)
	}
}

// A LIVE TAIL THAT IS THERE AND CANNOT BE READ STOPS THE PASS. A snapshot
// published without it would be a snapshot nothing holds to anything, made
// on the one day something was wrong with the file that does the holding.
func TestALiveTailThatCannotBeReadIsNotLeftOutOfTheSnapshot(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	appendFixtureEvent(t, a, keyPath, "exp_before")
	// A directory where the file should be: unreadable as a file on every
	// platform and for every user, root included.
	if err := os.Mkdir(witness.TailPath(filepath.Dir(cfg.Identity.LedgerPath)), 0o700); err != nil {
		t.Fatal(err)
	}
	a.continuity.runMaintenance(t.Context())
	if snaps := backupFiles(t, a.backupsDir(cfg)); len(snaps) != 0 {
		t.Fatalf("a snapshot was published without the tail that could not be read: %v", snaps)
	}
}
