package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
)

// identityKey writes an identity key into an identity directory, where
// `aii snapshot open` looks for the key a signature is held to.
func identityKey(t *testing.T, identityDir string) *crypto.KeyPair {
	t.Helper()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(identityDir, "data"), 0o700)
	if _, err := crypto.SaveKeyPair(kp, filepath.Join(identityDir, "data", "identity.sec")); err != nil {
		t.Fatal(err)
	}
	return kp
}

// madeSnapshot is an encrypted snapshot as the daily pass makes one — its
// files, its own SHA256SUMS and the identity's signature over it — for an
// identity directory with its keys. signer signs it (nil: the identity's
// own key; unsigned leaves the signature out, as before signing).
func madeSnapshot(t *testing.T, corrupt bool) (identityDir, snapshot string) {
	return madeSnapshotSigned(t, corrupt, nil, false)
}

func madeSnapshotSigned(t *testing.T, corrupt bool, signer *crypto.KeyPair, unsigned bool) (identityDir, snapshot string) {
	t.Helper()
	identityDir = t.TempDir()
	kp := identityKey(t, identityDir)
	if signer == nil {
		signer = kp
	}
	key, err := escrow.NewSnapshotKey()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(identityDir, "data", escrow.SnapshotKeyFileName), key, 0o600)
	recipient, _ := escrow.SnapshotRecipient(key)

	set := t.TempDir()
	var sums strings.Builder
	files := map[string]string{"ledger.jsonl": "the contents of ledger.jsonl", "aii.db": "the contents of aii.db",
		"RECEIPT.json": `{"last_seq":2,"last_hash":"head"}`}
	members := []string{"ledger.jsonl", "aii.db", "RECEIPT.json"}
	for _, n := range members {
		body := []byte(files[n])
		os.WriteFile(filepath.Join(set, n), body, 0o600)
		h := sha256.Sum256(body)
		sums.WriteString(hex.EncodeToString(h[:]) + "  " + n + "\n")
	}
	os.WriteFile(filepath.Join(set, "SHA256SUMS"), []byte(sums.String()), 0o600)
	members = append(members, "SHA256SUMS")
	if !unsigned {
		if err := escrow.SignSumsIn(set, signer, 2, "head"); err != nil {
			t.Fatal(err)
		}
		members = append(members, escrow.SumsSigName)
	}
	if corrupt { // the file no longer matches the list it travels with
		os.WriteFile(filepath.Join(set, "aii.db"), []byte("not what was listed"), 0o600)
	}
	snapshot = filepath.Join(t.TempDir(), "ledger-20260919T040000Z-seq2"+escrow.SnapshotSuffix)
	if err := escrow.EncryptSnapshot(set, members, recipient, snapshot); err != nil {
		t.Fatal(err)
	}
	return identityDir, snapshot
}

// THE SIGNATURE IS HELD TO THE OPERATOR'S KEY, AS A RESTORE HOLDS IT. A
// snapshot with none, or signed by another identity, decrypts and matches
// its own list, and is still not kept: what is in it is nobody's word. The
// key is the one named or the machine's, never one the snapshot carries.
func TestSnapshotOpenHoldsTheSignatureToTheIdentitysKey(t *testing.T) {
	dir, snap := madeSnapshotSigned(t, false, nil, true)
	out := filepath.Join(t.TempDir(), "opened")
	code, said := snapshotRun("open", "-in", snap, "-out", out, "-dir", dir)
	if code != 1 || !strings.Contains(said, "not signed by the identity's key") || !strings.Contains(said, "age -d") {
		t.Fatalf("AN UNSIGNED SNAPSHOT WAS OPENED, or the refusal names no way on: %d %s", code, said)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("a refused open kept what it unpacked")
	}
	stranger, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	dir, snap = madeSnapshotSigned(t, false, stranger, false)
	code, said = snapshotRun("open", "-in", snap, "-out", out, "-dir", dir)
	if code != 1 || !strings.Contains(said, "signature does not verify") {
		t.Fatalf("A SNAPSHOT SIGNED BY ANOTHER IDENTITY WAS OPENED: %d %s", code, said)
	}
	// Named with -identity, the stranger's key is the operator's word.
	strangerKey := filepath.Join(t.TempDir(), "stranger.sec")
	if _, err := crypto.SaveKeyPair(stranger, strangerKey); err != nil {
		t.Fatal(err)
	}
	if code, said := snapshotRun("open", "-in", snap, "-out", out, "-dir", dir, "-identity", strangerKey); code != 0 || !strings.Contains(said, stranger.Fingerprint()) {
		t.Fatalf("the key the operator named is not the one held to: %d %s", code, said)
	}
}

func snapshotRun(args ...string) (int, string) {
	var out, errb bytes.Buffer
	code := runSnapshot(args, &out, &errb)
	return code, out.String() + errb.String()
}

func TestSnapshotOpenUnpacksAndChecksWhatItUnpacked(t *testing.T) {
	dir, snap := madeSnapshot(t, false)
	out := filepath.Join(t.TempDir(), "opened")
	code, said := snapshotRun("open", "-in", snap, "-out", out, "-dir", dir)
	if code != 0 || !strings.Contains(said, "OPENED: 5 files") || !strings.Contains(said, "which identity") || !strings.Contains(said, "aii verify -ledger") {
		t.Fatalf("open: %d %s", code, said)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "aii.db")); string(b) != "the contents of aii.db" {
		t.Fatalf("aii.db: %q", b)
	}
	// Never over anything.
	if code, said := snapshotRun("open", "-in", snap, "-out", out, "-dir", dir); code != 1 || !strings.Contains(said, "already exists") {
		t.Fatalf("a snapshot was unpacked over a directory: %d %s", code, said)
	}
}

// A SNAPSHOT THAT DOES NOT MATCH ITS OWN CHECKSUM LIST IS NOT KEPT: it
// decrypts, so the key is right and the file is whole — and what is in it
// is not what it says. Nothing is left for anyone to restore from.
func TestSnapshotOpenKeepsNothingThatFailsItsOwnChecksums(t *testing.T) {
	dir, snap := madeSnapshot(t, true)
	out := filepath.Join(t.TempDir(), "opened")
	code, said := snapshotRun("open", "-in", snap, "-out", out, "-dir", dir)
	if code != 1 || !strings.Contains(said, "aii.db does not match its checksum") {
		t.Fatalf("%d %s", code, said)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("A SNAPSHOT THAT FAILED ITS OWN CHECKSUMS WAS LEFT UNPACKED")
	}
}

func TestSnapshotOpenSaysWhereALostKeyComesFrom(t *testing.T) {
	_, snap := madeSnapshot(t, false)
	code, said := snapshotRun("open", "-in", snap, "-out", filepath.Join(t.TempDir(), "o"), "-dir", t.TempDir())
	if code != 1 || !strings.Contains(said, "aii escrow restore") {
		t.Fatalf("%d %s", code, said)
	}
	if code, said := snapshotRun("frobnicate"); code != 2 || !strings.Contains(said, "age -d -i snapshot.key") {
		t.Fatalf("usage must name the stock tool: %d %s", code, said)
	}
}

// The checksum list came out of the archive: a name in it is input. Only
// a member this command just unpacked is opened — a list that names a path
// outside the output directory reads nothing there, and nothing is kept.
func TestSnapshotOpenOpensOnlyWhatItUnpacked(t *testing.T) {
	identityDir := t.TempDir()
	identityKey(t, identityDir)
	key, err := escrow.NewSnapshotKey()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(identityDir, "data", escrow.SnapshotKeyFileName), key, 0o600)
	recipient, _ := escrow.SnapshotRecipient(key)

	// A file outside the output directory, and a list that names it with
	// its TRUE hash — so a reader that opens it says nothing is wrong.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	secret := []byte("a file this command was never given")
	os.WriteFile(outside, secret, 0o600)
	h := sha256.Sum256(secret)

	set := t.TempDir()
	body := []byte("the ledger")
	os.WriteFile(filepath.Join(set, "ledger.jsonl"), body, 0o600)
	lh := sha256.Sum256(body)
	out := filepath.Join(t.TempDir(), "opened")
	rel, err := filepath.Rel(out, outside)
	if err != nil {
		t.Fatal(err)
	}
	sums := hex.EncodeToString(lh[:]) + "  ledger.jsonl\n" + hex.EncodeToString(h[:]) + "  " + filepath.ToSlash(rel) + "\n"
	os.WriteFile(filepath.Join(set, "SHA256SUMS"), []byte(sums), 0o600)
	snapshot := filepath.Join(t.TempDir(), "ledger-20260919T040000Z-seq2"+escrow.SnapshotSuffix)
	if err := escrow.EncryptSnapshot(set, []string{"ledger.jsonl", "SHA256SUMS"}, recipient, snapshot); err != nil {
		t.Fatal(err)
	}

	code, said := snapshotRun("open", "-in", snapshot, "-out", out, "-dir", identityDir)
	if code == 0 || !strings.Contains(said, "which is not in it") {
		t.Fatalf("a checksum list naming a path outside the output got exit %d: %s", code, said)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a refused open kept %s", out)
	}
}
