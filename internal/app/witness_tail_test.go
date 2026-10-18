package app

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// witness_tail_test.go — the witness's local tail, where the app asks it:
// at boot. SAFE mode's walk of the live containers and the snapshot, which
// carries a tail that is true of ITS ledger and is held to it before it is
// published, are internal/continuity's (witness_tail_test.go there).

// echoTail writes the tail as the anchorer leaves it after a verified
// receipt for the given record: the file's own format.
func echoTail(t *testing.T, dir string, seq uint64, hash string) {
	t.Helper()
	body := fmt.Sprintf(`{"ledger_ordinal":%d,"ledger_hash":%q,"witnessed_at":"2026-09-01T00:00:00Z","witness_key_fingerprint":"fp"}`, seq, hash)
	if err := os.WriteFile(witness.TailPath(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A LEDGER CUT BEHIND ITS WITNESS TAIL BOOTS SAFE — and the same ledger
// whole, the same tail beside it, boots and runs. The cut ledger is a
// shorter chain every link of which verifies; the projection is built from
// the cut ledger, so nothing but the tail can know.
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
			if last, err = lg.Append(ledger.EventExperienceCreate, 3, map[string]any{
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
		stopApp(t, app)
		if cut && (!safe || !strings.Contains(reason, "witness-tail check failed") || !strings.Contains(reason, "LEDGER TRUNCATION")) {
			t.Errorf("A LEDGER CUT BEHIND ITS WITNESS TAIL BOOTED: safe=%v %q", safe, reason)
		}
		if !cut && safe {
			t.Errorf("a whole ledger with its tail beside it booted SAFE: %q", reason)
		}
	}
}

// A TAIL THAT IS PRESENT AND DOES NOT READ IS SAFE TOO, never an absent one.
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
	defer stopApp(t, app)
	if reason, safe := app.SafeMode(); !safe || !strings.Contains(reason, "witness tail file corrupt") {
		t.Fatalf("a torn tail file: safe=%v %q", safe, reason)
	}
}
