package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

// .
// .

// .
func typed(replies ...string) passphraseReader {
	return func(string) ([]byte, error) {
		if len(replies) == 0 {
			return nil, errors.New("test: no passphrase left to type")
		}
		r := replies[0]
		replies = replies[1:]
		return []byte(r), nil
	}
}

func escrowRun(read passphraseReader, args ...string) (int, string) {
	var out, errb bytes.Buffer
	code := runEscrow(args, &out, &errb, read)
	return code, out.String() + errb.String()
}

// .
// .
func bornIdentity(t *testing.T, dir, name string) (fingerprint, ledgerPath string) {
	t.Helper()
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	root := genesistest.NewRoot(t)
	ledgerPath = filepath.Join(data, "ledger.jsonl")
	res, err := genesis.Birth(&genesis.BirthConfig{
		Name: name, Ring0Bundle: root.Ring0Bundle(t), Root: root.Env,
		KeyPath: filepath.Join(data, "identity.sec"), LedgerPath: ledgerPath, DBPath: filepath.Join(data, "aii.db"),
	})
	if err != nil {
		t.Fatalf("birth: %v", err)
	}
	res.Ledger.Close()
	hybrid, err := age.GenerateHybridIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "snapshot.key"), []byte(hybrid.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kp, err := crypto.LoadKeyPair(filepath.Join(data, "identity.sec"))
	if err != nil {
		t.Fatal(err)
	}
	return kp.Fingerprint(), ledgerPath
}

// .
// .
// .
func TestAnEscrowCarriesBothKeysFromOneMachineToAnother(t *testing.T) {
	home := t.TempDir()
	fp, ledgerPath := bornIdentity(t, home, "EscrowJourney")
	out := filepath.Join(t.TempDir(), "escrow.age")
	const pass = "correct horse battery staple"

	if code, said := escrowRun(typed(pass, "a different passphrase"), "create", "-dir", home, "-out", out); code != 1 || !strings.Contains(said, "differ") {
		t.Fatalf("two different passphrases made an escrow: %d %s", code, said)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("a refused create left a file")
	}
	code, said := escrowRun(typed(pass, pass), "create", "-dir", home, "-out", out)
	if code != 0 || !strings.Contains(said, "the identity key and the snapshot key") || !strings.Contains(said, fp) {
		t.Fatalf("create: %d %s", code, said)
	}
	if code, said := escrowRun(typed(pass, pass), "create", "-dir", home, "-out", out); code != 1 || !strings.Contains(said, "already exists") {
		t.Fatalf("an escrow was written over: %d %s", code, said)
	}
	sealed, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := os.ReadFile(filepath.Join(home, "data", "identity.sec"))
	snap, _ := os.ReadFile(filepath.Join(home, "data", "snapshot.key"))
	if bytes.Contains(sealed, key[4:36]) || bytes.Contains(sealed, snap[:40]) {
		t.Fatal("A SECRET IS IN THE ESCROW FILE IN THE CLEAR")
	}
	if info, _ := os.Stat(out); info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the escrow is readable by others: %v", info.Mode())
	}

	// .
	if code, said := escrowRun(typed(pass), "check", "-in", out, "-dir", home); code != 2 || !strings.Contains(said, "exactly one of -ledger or -fingerprint") {
		t.Fatalf("a check that names no identity ran: %d %s", code, said)
	}
	if code, said := escrowRun(typed("not the passphrase"), "check", "-in", out, "-ledger", ledgerPath, "-dir", home); code != 1 || !strings.Contains(said, "does not open this escrow") {
		t.Fatalf("the wrong passphrase: %d %s", code, said)
	}
	receiptPath := filepath.Join(home, "data", escrow.ReceiptFileName)
	if _, err := os.Stat(receiptPath); err == nil {
		t.Fatal("A FAILED CHECK WROTE A RECEIPT")
	}

	// .
	// .
	code, said = escrowRun(typed(pass), "check", "-in", out, "-ledger", ledgerPath, "-dir", home)
	if code != 0 || !strings.Contains(said, "CHECKED") || !strings.Contains(said, "Receipt written") {
		t.Fatalf("check: %d %s", code, said)
	}
	raw, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var r escrow.Receipt
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	recipient, _ := escrow.SnapshotRecipient(snap)
	kp, _ := crypto.LoadKeyPair(filepath.Join(home, "data", "identity.sec"))
	if err := r.Verify(kp.PublicKeyBytes(), recipient); err != nil {
		t.Fatalf("the receipt the check wrote does not verify for this identity and this snapshot key: %v", err)
	}

	// .
	// .
	away := t.TempDir()
	if err := os.MkdirAll(filepath.Join(away, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if code, said := escrowRun(typed(pass), "restore", "-in", out, "-fingerprint", strings.Repeat("0", 64), "-dir", away); code != 1 || !strings.Contains(said, "not "+strings.Repeat("0", 64)) {
		t.Fatalf("an escrow of another identity was restored: %d %s", code, said)
	}
	if entries, _ := os.ReadDir(filepath.Join(away, "data")); len(entries) != 0 {
		t.Fatalf("A REFUSED RESTORE WROTE SOMETHING: %v", entries)
	}
	code, said = escrowRun(typed(pass), "restore", "-in", out, "-fingerprint", fp, "-dir", away)
	if code != 0 || !strings.Contains(said, "RESTORED") {
		t.Fatalf("restore: %d %s", code, said)
	}
	for _, name := range []string{"identity.sec", "snapshot.key"} {
		want, _ := os.ReadFile(filepath.Join(home, "data", name))
		got, err := os.ReadFile(filepath.Join(away, "data", name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s did not come back byte for byte: %v", name, err)
		}
		if info, _ := os.Stat(filepath.Join(away, "data", name)); info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s was restored readable by others: %v", name, info.Mode())
		}
	}
	// .
	// .
	if entries, _ := os.ReadDir(filepath.Join(away, "data")); len(entries) != 2 {
		t.Fatalf("the restore left more than the two keys: %v", entries)
	}
	// .
	before, _ := os.ReadFile(filepath.Join(away, "data", "identity.sec"))
	if code, said := escrowRun(typed(pass), "restore", "-in", out, "-fingerprint", fp, "-dir", away); code != 0 || !strings.Contains(said, "NOTHING TO RESTORE") {
		t.Fatalf("a second restore over a complete home: %d %s", code, said)
	}
	if after, _ := os.ReadFile(filepath.Join(away, "data", "identity.sec")); !bytes.Equal(before, after) {
		t.Fatal("a second restore touched the key that was in place")
	}

	// .
	// .
	// .
	for _, existing := range []string{"identity.sec", "snapshot.key"} {
		half := t.TempDir()
		if err := os.MkdirAll(filepath.Join(half, "data"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(half, "data", existing), []byte("someone's existing file"), 0o600); err != nil {
			t.Fatal(err)
		}
		code, said := escrowRun(typed(), "restore", "-in", out, "-fingerprint", fp, "-dir", half)
		if code != 1 || !strings.Contains(said, existing+" exists and") || !strings.Contains(said, "never written over") {
			t.Fatalf("with a file that is no key at %s: %d %s", existing, code, said)
		}
		entries, _ := os.ReadDir(filepath.Join(half, "data"))
		if len(entries) != 1 {
			t.Fatalf("A RESTORE HALF-HAPPENED with %s in the way: %v", existing, entries)
		}
		if got, _ := os.ReadFile(filepath.Join(half, "data", existing)); string(got) != "someone's existing file" {
			t.Fatalf("%s was written over", existing)
		}
	}
}

// .
// .
func TestACheckElsewhereWritesNoReceipt(t *testing.T) {
	home := t.TempDir()
	fp, _ := bornIdentity(t, home, "EscrowElsewhere")
	out := filepath.Join(t.TempDir(), "escrow.age")
	if code, said := escrowRun(typed("p4ss phrase", "p4ss phrase"), "create", "-dir", home, "-out", out); code != 0 {
		t.Fatalf("create: %s", said)
	}
	elsewhere := t.TempDir()
	os.MkdirAll(filepath.Join(elsewhere, "data"), 0o700)
	code, said := escrowRun(typed("p4ss phrase"), "check", "-in", out, "-fingerprint", fp, "-dir", elsewhere)
	if code != 0 || !strings.Contains(said, "CHECKED") || !strings.Contains(said, "No receipt written") {
		t.Fatalf("check elsewhere: %d %s", code, said)
	}
	if entries, _ := os.ReadDir(filepath.Join(elsewhere, "data")); len(entries) != 0 {
		t.Fatalf("a check wrote something on a machine it does not cover: %v", entries)
	}
}

// .
// .
func TestTheRealPassphraseReaderRefusesAPipe(t *testing.T) {
	var errb bytes.Buffer
	if _, err := terminalPassphrase(&errb)("Passphrase: "); err == nil || !strings.Contains(err.Error(), "no terminal") {
		t.Fatalf("a passphrase was read with no terminal: %v", err)
	}
	if code, said := escrowRun(typed(), "frobnicate"); code != 2 || !strings.Contains(said, "aii escrow create") {
		t.Fatalf("usage: %d %s", code, said)
	}
}

// .
func sealedHome(t *testing.T, name string) (home, fp, ledgerPath, escrowFile string) {
	t.Helper()
	home = t.TempDir()
	fp, ledgerPath = bornIdentity(t, home, name)
	escrowFile = filepath.Join(t.TempDir(), "escrow.age")
	if code, said := escrowRun(typed(testPass, testPass), "create", "-dir", home, "-out", escrowFile); code != 0 {
		t.Fatalf("create: %s", said)
	}
	return home, fp, ledgerPath, escrowFile
}

const testPass = "correct horse battery staple"

// .
// .
// .
// .
func TestRestorePutsBackOnlyWhatIsMissing(t *testing.T) {
	home, fp, _, escrowFile := sealedHome(t, "RestoreMissing")
	snap := filepath.Join(home, "data", "snapshot.key")
	key := filepath.Join(home, "data", "identity.sec")
	wantSnap, _ := os.ReadFile(snap)
	keyBefore, _ := os.ReadFile(key)
	keyInfo, _ := os.Stat(key)

	// .
	if err := os.Remove(snap); err != nil {
		t.Fatal(err)
	}
	code, said := escrowRun(typed(testPass), "restore", "-in", escrowFile, "-fingerprint", fp, "-dir", home)
	if code != 0 || !strings.Contains(said, "RESTORED") || !strings.Contains(said, "already in place") {
		t.Fatalf("restoring a lost snapshot key beside a present identity key: %d %s", code, said)
	}
	if got, _ := os.ReadFile(snap); !bytes.Equal(got, wantSnap) {
		t.Fatal("the snapshot key did not come back byte for byte")
	}
	keyAfter, _ := os.ReadFile(key)
	infoAfter, _ := os.Stat(key)
	if !bytes.Equal(keyBefore, keyAfter) || !infoAfter.ModTime().Equal(keyInfo.ModTime()) {
		t.Fatal("THE IDENTITY KEY THAT WAS IN PLACE WAS TOUCHED")
	}

	// .
	// .
	other := t.TempDir()
	bornIdentity(t, other, "SomeoneElse")
	os.Remove(filepath.Join(other, "data", "snapshot.key"))
	code, said = escrowRun(typed(), "restore", "-in", escrowFile, "-fingerprint", fp, "-dir", other)
	if code != 1 || !strings.Contains(said, "holds identity") || !strings.Contains(said, "never written over") {
		t.Fatalf("another identity's key in the way: %d %s", code, said)
	}
	if _, err := os.Stat(filepath.Join(other, "data", "snapshot.key")); !os.IsNotExist(err) {
		t.Fatal("A REFUSED RESTORE WROTE THE SNAPSHOT KEY INTO ANOTHER IDENTITY'S HOME")
	}

	// .
	// .
	newer := filepath.Join(t.TempDir(), "newer")
	os.MkdirAll(filepath.Join(newer, "data"), 0o700)
	os.WriteFile(filepath.Join(newer, "data", "identity.sec"), keyBefore, 0o600)
	otherSnap, _ := os.ReadFile(filepath.Join(bornHomeWithSnapshotKey(t), "data", "snapshot.key"))
	os.WriteFile(filepath.Join(newer, "data", "snapshot.key"), otherSnap, 0o600)
	code, said = escrowRun(typed(testPass), "restore", "-in", escrowFile, "-fingerprint", fp, "-dir", newer)
	if code != 1 || !strings.Contains(said, "DIFFERENT snapshot key") || !strings.Contains(said, "-snapshot-key") {
		t.Fatalf("a different snapshot key in the way: %d %s", code, said)
	}
	if got, _ := os.ReadFile(filepath.Join(newer, "data", "snapshot.key")); !bytes.Equal(got, otherSnap) {
		t.Fatal("A SNAPSHOT KEY WAS WRITTEN OVER")
	}

	// .
	bare := t.TempDir()
	if code, said := escrowRun(typed(testPass), "restore", "-in", escrowFile, "-fingerprint", fp, "-dir", bare); code != 0 || !strings.Contains(said, "RESTORED") {
		t.Fatalf("restore onto a bare tree: %d %s", code, said)
	}
	if info, err := os.Stat(filepath.Join(bare, "data")); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the directory a restore made is not owner-only: %v %v", info, err)
	}
}

func bornHomeWithSnapshotKey(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	bornIdentity(t, d, "AnotherSnapshotKey")
	return d
}

// .
// .
// .
func TestRestoreByFingerprintHeedsTheLedgerInTheHome(t *testing.T) {
	_, fp, _, escrowFile := sealedHome(t, "RestoreA")
	homeB := t.TempDir()
	bornIdentity(t, homeB, "RestoreB")
	os.Remove(filepath.Join(homeB, "data", "identity.sec"))
	os.Remove(filepath.Join(homeB, "data", "snapshot.key"))
	code, said := escrowRun(typed(), "restore", "-in", escrowFile, "-fingerprint", fp, "-dir", homeB)
	if code != 1 || !strings.Contains(said, "the ledger in this home") {
		t.Fatalf("identity A's key into identity B's home: %d %s", code, said)
	}
	if entries, _ := os.ReadDir(filepath.Join(homeB, "data")); len(entries) == 0 {
		t.Fatal("the rig lost its ledger")
	}
	if _, err := os.Stat(filepath.Join(homeB, "data", "identity.sec")); !os.IsNotExist(err) {
		t.Fatal("IDENTITY A'S KEY WAS PUBLISHED INTO IDENTITY B'S HOME")
	}
}

// .
// .
// .
// .
func TestACheckComparesTheSnapshotKeyOnThisHost(t *testing.T) {
	home, fp, ledgerPath, escrowFile := sealedHome(t, "CheckCovers")
	receipt := filepath.Join(home, "data", escrow.ReceiptFileName)

	code, said := escrowRun(typed(testPass), "check", "-in", escrowFile, "-ledger", ledgerPath, "-dir", home)
	if code != 0 || !strings.Contains(said, "Receipt written") {
		t.Fatalf("check on the host it covers: %d %s", code, said)
	}
	if info, err := os.Stat(receipt); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the receipt: %v %v", info, err)
	}
	os.Remove(receipt)

	// .
	otherSnap, _ := os.ReadFile(filepath.Join(bornHomeWithSnapshotKey(t), "data", "snapshot.key"))
	os.WriteFile(filepath.Join(home, "data", "snapshot.key"), otherSnap, 0o600)
	code, said = escrowRun(typed(testPass), "check", "-in", escrowFile, "-fingerprint", fp, "-dir", home)
	if code != 1 || !strings.Contains(said, "CHECKED") || !strings.Contains(said, "NOT COVERED") || !strings.Contains(said, "aii escrow create") {
		t.Fatalf("check with another snapshot key on the host: %d %s", code, said)
	}
	if _, err := os.Stat(receipt); !os.IsNotExist(err) {
		t.Fatal("A RECEIPT WAS WRITTEN FOR A SNAPSHOT KEY THIS HOST DOES NOT USE")
	}

	// .
	os.Remove(filepath.Join(home, "data", "snapshot.key"))
	code, said = escrowRun(typed(testPass), "check", "-in", escrowFile, "-fingerprint", fp, "-dir", home)
	if code != 1 || !strings.Contains(said, "NOT COVERED") || !strings.Contains(said, "aii escrow restore") {
		t.Fatalf("check with no snapshot key on the host: %d %s", code, said)
	}
	if _, err := os.Stat(receipt); !os.IsNotExist(err) {
		t.Fatal("a receipt was written on a host with no snapshot key")
	}
}

// .
// .
// .
// .
func TestWithTheKeyElsewhereTheReceiptGoesBesideIt(t *testing.T) {
	home, fp, _, _ := sealedHome(t, "KeyElsewhere")
	keys := filepath.Join(t.TempDir(), "keys")
	os.MkdirAll(keys, 0o700)
	for _, n := range []string{"identity.sec", "snapshot.key"} {
		if err := os.Rename(filepath.Join(home, "data", n), filepath.Join(keys, n)); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Join(keys, "identity.sec")
	out := filepath.Join(t.TempDir(), "escrow.age")
	if code, said := escrowRun(typed(testPass, testPass), "create", "-dir", home, "-key", key, "-out", out); code != 0 || !strings.Contains(said, "the identity key and the snapshot key") {
		t.Fatalf("create with the key elsewhere did not seal the snapshot key beside it: %d %s", code, said)
	}
	code, said := escrowRun(typed(testPass), "check", "-in", out, "-fingerprint", fp, "-dir", home, "-key", key)
	if code != 0 || !strings.Contains(said, "Receipt written") {
		t.Fatalf("check: %d %s", code, said)
	}
	if _, err := os.Stat(escrow.ReceiptPath(key)); err != nil {
		t.Fatalf("the receipt is not where the runtime reads it (%s): %v", escrow.ReceiptPath(key), err)
	}
	if _, err := os.Stat(filepath.Join(home, "data", escrow.ReceiptFileName)); !os.IsNotExist(err) {
		t.Fatal("a receipt was written under -dir, where the runtime of this layout never looks")
	}
	// .
	os.WriteFile(escrow.ReceiptPath(key), []byte("{}"), 0o644)
	if err := os.Chmod(escrow.ReceiptPath(key), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, said := escrowRun(typed(testPass), "check", "-in", out, "-fingerprint", fp, "-dir", home, "-key", key); code != 0 {
		t.Fatalf("second check: %d %s", code, said)
	}
	if info, _ := os.Stat(escrow.ReceiptPath(key)); info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the receipt kept the older file's permissions: %v", info.Mode())
	}
}
