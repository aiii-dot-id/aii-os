package continuity

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/continuity/continuitytest"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
)

// signedSnapshot makes one daily snapshot of a fixture that has lived two
// turns, and returns the backups directory and the snapshot.
func signedSnapshot(t *testing.T) (*testApp, string, keptSnapshot) {
	t.Helper()
	a, _, keyPath := maintApp(t)
	dir := a.backupsDir(a.configSnapshot())
	live(t, a, 2)
	appendFixtureEvent(t, a, keyPath, "signed_a")
	a.continuity.runMaintenance(t.Context())
	kept, _ := keptSnapshots(dir)
	if len(kept) != 1 || kept[0].Encrypted {
		t.Fatalf("rig: one plaintext snapshot: %v", kept)
	}
	return a, dir, kept[0]
}

// forgeConversation rewrites the words of the operator's turns in a
// snapshot's database copy and the database's line in the checksum list to
// match: what someone who can write where snapshots are kept can do. The
// row count, the lived clock and the record are untouched, so every proof
// that reads the database still holds.
func forgeConversation(t *testing.T, set string) {
	t.Helper()
	path := filepath.Join(set, SnapshotDB)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode=DELETE`); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(`UPDATE conversations SET content = 'words the operator never typed' WHERE role = 'operator'`)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		t.Fatal("rig: no operator turn in the snapshot to forge")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	sumsPath := filepath.Join(set, SnapshotSums)
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		t.Fatal(err)
	}
	var resummed []string
	for _, line := range strings.Split(strings.TrimRight(string(sums), "\n"), "\n") {
		if strings.HasSuffix(line, "  "+SnapshotDB) {
			line = hex.EncodeToString(h[:]) + "  " + SnapshotDB
		}
		resummed = append(resummed, line)
	}
	if err := os.WriteFile(sumsPath, []byte(strings.Join(resummed, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	present, err := filesUnder(set)
	if err != nil {
		t.Fatal(err)
	}
	if err := escrow.CheckSums(set, present); err != nil {
		t.Fatalf("rig: the forged snapshot must pass its own checksum list: %v", err)
	}
}

func everyFileNamed(t *testing.T, set string, present []string) {
	t.Helper()
	sums, err := os.ReadFile(filepath.Join(set, SnapshotSums))
	if err != nil {
		t.Fatal(err)
	}
	named := map[string]bool{SnapshotSums: true}
	for _, line := range strings.Split(strings.TrimRight(string(sums), "\n"), "\n") {
		if _, name, ok := strings.Cut(line, "  "); ok {
			named[name] = true
		}
	}
	for _, p := range present {
		if !named[p] {
			t.Errorf("THE SNAPSHOT HOLDS %s, WHICH ITS SHA256SUMS DOES NOT NAME: 0.1.23 refuses it, so after a rollback it neither proves, restores nor opens", p)
		}
	}
}

// A SNAPSHOT IS SIGNED BY THE IDENTITY, and the signature names the record
// its receipt names. Stock tools still verify it, and so does a program that
// knows nothing of the signature: the list names it as its last line, and
// the list's own check takes it as one more file.
func TestASnapshotIsSignedByTheIdentitysKey(t *testing.T) {
	a, dir, k := signedSnapshot(t)
	set := filepath.Join(dir, k.Name)
	signed, err := escrow.VerifySumsSig(set, a.door.kp.PublicKeyBytes())
	if err != nil {
		t.Fatalf("THE SNAPSHOT IS NOT SIGNED BY THE IDENTITY: %v", err)
	}
	receipt := readReceipt(t, set)
	if signed.Identity != a.door.kp.Fingerprint() || signed.LastSeq != receipt.LastSeq || signed.LastHash != receipt.LastHash || signed.LastSeq != k.Record {
		t.Fatalf("the signature names %+v; the receipt %d %s; the name %d", signed, receipt.LastSeq, receipt.LastHash, k.Record)
	}
	present, err := filesUnder(set)
	if err != nil {
		t.Fatal(err)
	}
	if err := escrow.CheckSums(set, present); err != nil {
		t.Fatalf("a signed snapshot fails its own checksum list: %v", err)
	}
	everyFileNamed(t, set, present)
	other, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := escrow.VerifySumsSig(set, other.PublicKeyBytes()); !errors.Is(err, escrow.ErrSumsSig) {
		t.Fatalf("the signature verified under another identity's key: %v", err)
	}
	if out, err := a.continuity.verifyKept(t.Context(), k.Name, false, time.Now()); err != nil {
		t.Fatalf("a signed snapshot does not prove: %v\n%s", err, out)
	}
}

// AN EDIT THE CHECKSUM LIST HIDES IS REFUSED. The words of a conversation
// changed and the list rewritten to match: the chain, the database's own
// checks and the restore in scratch all hold, because none of them can tell
// one conversation from another. Only the key can, and the list it signed
// is not the list there now. Neither the proof nor a restore takes it.
func TestAnEditTheChecksumListHidesIsRefusedByTheSignature(t *testing.T) {
	a, dir, k := signedSnapshot(t)
	set := filepath.Join(dir, k.Name)
	forgeConversation(t, set)

	_, err := a.continuity.verifyKept(t.Context(), k.Name, false, time.Now())
	if err == nil {
		t.Fatal("A SNAPSHOT WITH A FORGED CONVERSATION PROVED: its checksum list was rewritten, and nothing held the list to the identity")
	}
	if !operatorWasTold(t, a, "SHA256SUMS is not the list that was signed") {
		t.Errorf("the operator is not told what failed: %v", err)
	}

	for _, takesUnsigned := range []bool{false, true} {
		staged := t.TempDir()
		_, _, err = a.continuity.stageAndProve(t.Context(), a.configSnapshot(), a.door.kp, set, &k,
			filepath.Join(staged, "set"), filepath.Join(staged, "proof"), takesUnsigned)
		if !errors.Is(err, escrow.ErrSumsSig) {
			t.Fatalf("A RESTORE TOOK A SNAPSHOT WITH A FORGED CONVERSATION (an unsigned one taken: %t): %v", takesUnsigned, err)
		}
	}
}

func TestASnapshotWithNoSignatureIsNotTakenAsTheIdentitys(t *testing.T) {
	a, dir, k := signedSnapshot(t)
	set := filepath.Join(dir, k.Name)
	if err := continuitytest.Unsign(set, nil); err != nil {
		t.Fatal(err)
	}
	_, err := a.continuity.verifyKept(t.Context(), k.Name, false, time.Now())
	refusedFor(t, err, "unsigned")
	if operatorWasTold(t, a, "snapshot-rot") {
		t.Error("a snapshot made before signing was reported as rot")
	}
	if pe := personError(err); !strings.Contains(pe.Error(), "Back up now") {
		t.Errorf("the page does not say what to do: %v", pe)
	}

	staged := t.TempDir()
	_, _, err = a.continuity.stageAndProve(t.Context(), a.configSnapshot(), a.door.kp, set, &k,
		filepath.Join(staged, "set"), filepath.Join(staged, "proof"), false)
	if !errors.Is(err, escrow.ErrUnsigned) || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("AN UNSIGNED SNAPSHOT WAS RESTORED, or the refusal names no way on: %v", err)
	}
	staged = t.TempDir()
	through, unsigned, err := a.continuity.stageAndProve(t.Context(), a.configSnapshot(), a.door.kp, set, &k,
		filepath.Join(staged, "set"), filepath.Join(staged, "proof"), true)
	if err != nil || !unsigned || through != k.Record {
		t.Fatalf("AN UNSIGNED SNAPSHOT WAS REFUSED WHERE NO RECORD OF THE IDENTITY IS, or not said unsigned: through %d, unsigned %t, %v", through, unsigned, err)
	}
}
