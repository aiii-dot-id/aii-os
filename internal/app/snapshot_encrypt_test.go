package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .

// .
// .
func hostSnapshotKey(t *testing.T, a *App) (key []byte, recipient string) {
	t.Helper()
	cfg := a.configSnapshot()
	a.ensureSnapshotKey(cfg)
	key, err := os.ReadFile(a.snapshotKeyPath(cfg))
	if err != nil {
		t.Fatalf("the boot made no snapshot key: %v", err)
	}
	recipient, err = escrow.SnapshotRecipient(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, recipient
}

// .
// .
func checkedEscrow(t *testing.T, a *App, kp *crypto.KeyPair, recipient string) {
	t.Helper()
	r, err := escrow.SignReceipt(kp, recipient, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	if err := os.WriteFile(a.escrowReceiptPath(a.configSnapshot()), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func snapshotsIn(t *testing.T, dir string) (plain, sealed []string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		switch {
		case strings.HasPrefix(e.Name(), "."):
			t.Fatalf("debris in the snapshot set: %s", e.Name())
		case strings.HasSuffix(e.Name(), escrow.SnapshotSuffix):
			sealed = append(sealed, e.Name())
		case backupSeqRe.MatchString(e.Name()):
			plain = append(plain, e.Name())
		}
	}
	return plain, sealed
}

// .
// .
// .
// .
// .
func TestWithoutAVerifiedEscrowReceiptASnapshotIsPlaintextAndTheOperatorIsTold(t *testing.T) {
	stranger, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	for name, prepare := range map[string]func(t *testing.T, a *App){
		"no snapshot key at all": func(*testing.T, *App) {},
		"a key and no receipt":   func(t *testing.T, a *App) { hostSnapshotKey(t, a) },
		"a receipt for a key since rotated": func(t *testing.T, a *App) {
			_, old := hostSnapshotKey(t, a)
			checkedEscrow(t, a, a.door.kp, old)
			os.Remove(a.snapshotKeyPath(a.configSnapshot()))
			hostSnapshotKey(t, a)
		},
		"a receipt signed by another identity": func(t *testing.T, a *App) {
			_, recipient := hostSnapshotKey(t, a)
			checkedEscrow(t, a, stranger, recipient)
		},
		"a receipt that does not read": func(t *testing.T, a *App) {
			hostSnapshotKey(t, a)
			os.WriteFile(a.escrowReceiptPath(a.configSnapshot()), []byte("{not json"), 0o600)
		},
	} {
		t.Run(name, func(t *testing.T) {
			a, _, _ := maintApp(t)
			prepare(t, a)
			dir := a.backupsDir(a.configSnapshot())
			a.runMaintenance(t.Context())
			plain, sealed := snapshotsIn(t, dir)
			if len(sealed) != 0 {
				t.Fatalf("A SNAPSHOT WAS ENCRYPTED WITH NO VERIFIED ESCROW OF ITS KEY: %v", sealed)
			}
			if len(plain) != 1 {
				t.Fatalf("the fallback is a plaintext snapshot, not no snapshot: %v", plain)
			}
			if !operatorWasTold(t, a, "NOT encrypted") {
				t.Fatal("the operator was not told the snapshot is unencrypted, or why")
			}
		})
	}
}

// .
// .
// .
func TestWithAVerifiedEscrowReceiptTheSnapshotIsEncryptedAndIsTheProvedSet(t *testing.T) {
	a, _, _ := maintApp(t)
	live(t, a, 3)
	key, recipient := hostSnapshotKey(t, a)
	checkedEscrow(t, a, a.door.kp, recipient)
	dir := a.backupsDir(a.configSnapshot())
	captured := a.ledger.LastSeq()

	a.runMaintenance(t.Context())
	plain, sealed := snapshotsIn(t, dir)
	if len(plain) != 0 || len(sealed) != 1 {
		t.Fatalf("want one encrypted snapshot and nothing in the clear: plain %v sealed %v", plain, sealed)
	}
	if snapshotSeq(t, sealed[0]) != captured {
		t.Fatalf("named for record %d, captured at %d", snapshotSeq(t, sealed[0]), captured)
	}
	if operatorWasTold(t, a, "NOT encrypted") {
		t.Fatal("an encrypted snapshot raised the unencrypted alert")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, sealed[0]))
	if strings.Contains(string(raw), "a turn the record never holds") {
		t.Fatal("A CONVERSATION IS IN THE ENCRYPTED SNAPSHOT IN THE CLEAR")
	}
	out := filepath.Join(t.TempDir(), "opened")
	names, err := escrow.OpenSnapshot(filepath.Join(dir, sealed[0]), key, out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ledger.jsonl", snapshotDB, snapshotReceiptName, snapshotSums} {
		if !strings.Contains(","+strings.Join(names, ",")+",", ","+want+",") {
			t.Fatalf("the encrypted snapshot lacks %s: %v", want, names)
		}
	}
	head, held, err := store.InspectCopy(t.Context(), filepath.Join(out, snapshotDB))
	if err != nil || head.Seq != captured || held.Ephemeral["conversations"] != 3 {
		t.Fatalf("the database inside is not the captured one: head %d, %+v, %v", head.Seq, held, err)
	}
	// .
	os.Remove(a.escrowReceiptPath(a.configSnapshot()))
	nextSecond()
	a.runMaintenance(t.Context())
	if plain, sealed := snapshotsIn(t, dir); len(plain) != 1 || len(sealed) != 1 {
		t.Fatalf("with the receipt gone the next pass must fall back: plain %v sealed %v", plain, sealed)
	}
}

// .
// .
// .
// .
func TestACiphertextThatIsNotTheProvedSetIsNeverPublished(t *testing.T) {
	a, _, _ := maintApp(t)
	_, recipient := hostSnapshotKey(t, a)
	checkedEscrow(t, a, a.door.kp, recipient)
	dir := a.backupsDir(a.configSnapshot())
	a.runMaintenance(t.Context())
	_, before := snapshotsIn(t, dir)
	if len(before) != 1 {
		t.Fatalf("fixture: %v", before)
	}

	prev := snapshotStep
	t.Cleanup(func() { snapshotStep = prev })
	snapshotStep = func(step string) {
		path, ok := strings.CutPrefix(step, "encrypted:")
		if !ok {
			return
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("hook: %v", err)
			return
		}
		raw[len(raw)/2] ^= 0x01
		os.WriteFile(path, raw, 0o600)
	}
	nextSecond()
	a.runMaintenance(t.Context())
	snapshotStep = prev

	plain, after := snapshotsIn(t, dir)
	if len(plain) != 0 || len(after) != 1 || after[0] != before[0] {
		t.Fatalf("A CIPHERTEXT THAT DOES NOT VERIFY WAS PUBLISHED, or the good snapshot is gone: plain %v sealed %v", plain, after)
	}
	if !operatorWasTold(t, a, "does not verify") {
		t.Fatal("the operator was not told")
	}
}

// .
// .
// .
func TestPruningTreatsPlaintextAndEncryptedSnapshotsAsOneSet(t *testing.T) {
	dir := t.TempDir()
	mkPlain := func(name string) {
		os.MkdirAll(filepath.Join(dir, name), 0o700)
		os.WriteFile(filepath.Join(dir, name, snapshotSums), []byte("x  y\n"), 0o600)
	}
	mkPlain("ledger-20260917T040000Z-seq7")
	mkPlain("ledger-20260918T040000Z-seq7")
	os.WriteFile(filepath.Join(dir, "ledger-20260919T040000Z-seq7"+escrow.SnapshotSuffix), []byte("c"), 0o600)
	os.WriteFile(filepath.Join(dir, "ledger-20260916T040000Z-seq7"+escrow.SnapshotSuffix), []byte("c"), 0o600)
	os.MkdirAll(filepath.Join(dir, "ledger-20260920T040000Z-seq7"), 0o700)

	if removed := pruneBackups(dir, 2); removed != 2 {
		t.Fatalf("pruned %d, want 2", removed)
	}
	var left []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		left = append(left, e.Name())
	}
	want := "ledger-20260918T040000Z-seq7,ledger-20260919T040000Z-seq7" + escrow.SnapshotSuffix
	if strings.Join(left, ",") != want {
		t.Fatalf("kept %v, want the two newest by stamp whatever their kind: %s", left, want)
	}
}

// .
// .
func TestTheSnapshotKeyIsMadeOnceAndNeverInSafe(t *testing.T) {
	a, _, _ := maintApp(t)
	cfg := a.configSnapshot()
	path := a.snapshotKeyPath(cfg)
	if filepath.Dir(path) != filepath.Dir(cfg.Identity.KeyPath) {
		t.Fatalf("the snapshot key is not beside the identity key: %s", path)
	}
	a.ensureSnapshotKey(cfg)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the snapshot key is readable by others: %v", info.Mode())
	}
	a.ensureSnapshotKey(cfg)
	if again, _ := os.ReadFile(path); string(again) != string(first) {
		t.Fatal("AN EXISTING SNAPSHOT KEY WAS REPLACED: every snapshot encrypted to it is now unreadable")
	}

	safe, _, _ := maintApp(t)
	safe.enterSafe("test: no new files in SAFE")
	safe.ensureSnapshotKey(safe.configSnapshot())
	if _, err := os.Stat(safe.snapshotKeyPath(safe.configSnapshot())); !os.IsNotExist(err) {
		t.Fatal("SAFE made a snapshot key")
	}
}
