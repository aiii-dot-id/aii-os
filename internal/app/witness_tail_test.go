package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// .
// .
// .
// .

// .
// .
func echoTail(t *testing.T, dir string, seq uint64, hash string) {
	t.Helper()
	body := fmt.Sprintf(`{"ledger_ordinal":%d,"ledger_hash":%q,"witnessed_at":"2026-09-01T00:00:00Z","witness_key_fingerprint":"fp"}`, seq, hash)
	if err := os.WriteFile(witness.TailPath(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// .
// .
// .
// .
func TestALedgerCutBehindItsWitnessTailBootsSafe(t *testing.T) {
	for _, cut := range []bool{false, true} {
		dir := t.TempDir()
		keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Tail")
		kp, err := crypto.LoadKeyPair(keyPath)
		if err != nil {
			t.Fatal(err)
		}
		lg, err := ledger.New(ledgerPath)
		if err != nil {
			t.Fatal(err)
		}
		var last *ledger.Event
		for i := 0; i < 3; i++ {
			if last, err = lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3, map[string]interface{}{
				"id": fmt.Sprintf("exp_tail_%d", i), "content": "observed", "category": "observation", "provenance": "self",
			}, kp); err != nil {
				t.Fatal(err)
			}
		}
		lg.Close()
		echoTail(t, dir, last.Seq, last.EntryHash())
		if cut {
			raw, err := os.ReadFile(ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.SplitAfter(string(raw), "\n")
			if err := os.WriteFile(ledgerPath, []byte(strings.Join(lines[:len(lines)-3], "")), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.VerifyChain(ledgerPath, kp.PublicKeyBytes(), nil); err != nil {
				t.Fatalf("rig: the cut ledger must be a chain that verifies — that is the attack: %v", err)
			}
		}
		buildPriorProjection(t, ledgerPath, dbPath)

		app := New(safebootConfig(t, dir, "Tail", keyPath, ledgerPath, dbPath))
		if err := startLiveForTest(app); err != nil {
			t.Fatalf("cut=%v: boot must come up, SAFE or not: %v", cut, err)
		}
		reason, safe := app.SafeMode()
		app.Stop()
		if cut && (!safe || !strings.Contains(reason, "witness-tail check failed") || !strings.Contains(reason, "LEDGER TRUNCATION")) {
			t.Errorf("A LEDGER CUT BEHIND ITS WITNESS TAIL BOOTED: safe=%v %q", safe, reason)
		}
		if !cut && safe {
			t.Errorf("a whole ledger with its tail beside it booted SAFE: %q", reason)
		}
	}
}

// .
func TestAWitnessTailThatDoesNotReadBootsSafe(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Tail")
	buildPriorProjection(t, ledgerPath, dbPath)
	if err := os.WriteFile(witness.TailPath(dir), []byte(`{"ledger_ordinal":`), 0o600); err != nil {
		t.Fatal(err)
	}
	app := New(safebootConfig(t, dir, "Tail", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer app.Stop()
	if reason, safe := app.SafeMode(); !safe || !strings.Contains(reason, "witness tail file corrupt") {
		t.Fatalf("a torn tail file: safe=%v %q", safe, reason)
	}
}

// .
// .
// .
// .
// .
func TestAnAnchorDuringTheCopyDoesNotReachTheSnapshotsTail(t *testing.T) {
	// .
	// .
	// .
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

	prev := snapshotStep
	t.Cleanup(func() { snapshotStep = prev })
	snapshotStep = func(step string) {
		if step != "captured" {
			return
		}
		// .
		// .
		appendFixtureEvent(t, a, keyPath, "exp_during_the_copy")
		later, err := ledger.ReadAll(cfg.Identity.LedgerPath)
		if err != nil {
			t.Fatal(err)
		}
		echoTail(t, srcDir, later[len(later)-1].Seq, later[len(later)-1].EntryHash())
	}
	a.runMaintenance(t.Context())
	snapshotStep = prev

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
	// .
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

// .
func TestASnapshotThatContradictsItsTailIsNotPublished(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	appendFixtureEvent(t, a, keyPath, "exp_before")
	echoTail(t, filepath.Dir(cfg.Identity.LedgerPath), a.ledger.LastSeq(), "sha256:"+strings.Repeat("f", 64))
	a.runMaintenance(t.Context())
	if snaps := backupFiles(t, a.backupsDir(cfg)); len(snaps) != 0 {
		t.Fatalf("a record the witness attested otherwise was published as a recovery point: %v", snaps)
	}
}

// .
// .
// .
// .
func TestTheLiveLedgersVerificationIsHeldToItsTail(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	appendFixtureEvent(t, a, keyPath, "exp_before")
	if _, err := a.verifyLiveLedger(cfg); err != nil {
		t.Fatalf("a whole ledger with no tail beside it: %v", err)
	}
	echoTail(t, filepath.Dir(cfg.Identity.LedgerPath), a.ledger.LastSeq()+3, "sha256:"+strings.Repeat("a", 64))
	_, err := a.verifyLiveLedger(cfg)
	var refusal *witness.TailRefusal
	if !errors.As(err, &refusal) || !refusal.Truncation {
		t.Fatalf("a live ledger that ends before the record its tail names: %v", err)
	}
	if !strings.Contains(err.Error(), "witness: ") {
		t.Errorf("the alert does not carry the witness's line: %v", err)
	}
}

// .
// .
// .
func TestALiveTailThatCannotBeReadIsNotLeftOutOfTheSnapshot(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	appendFixtureEvent(t, a, keyPath, "exp_before")
	// .
	// .
	if err := os.Mkdir(witness.TailPath(filepath.Dir(cfg.Identity.LedgerPath)), 0o700); err != nil {
		t.Fatal(err)
	}
	a.runMaintenance(t.Context())
	if snaps := backupFiles(t, a.backupsDir(cfg)); len(snaps) != 0 {
		t.Fatalf("a snapshot was published without the tail that could not be read: %v", snaps)
	}
}
