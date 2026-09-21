package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/escrow"
)

// .
// .
func madeSnapshot(t *testing.T, corrupt bool) (identityDir, snapshot string) {
	t.Helper()
	identityDir = t.TempDir()
	os.MkdirAll(filepath.Join(identityDir, "data"), 0o700)
	key, err := escrow.NewSnapshotKey()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(identityDir, "data", escrow.SnapshotKeyFileName), key, 0o600)
	recipient, _ := escrow.SnapshotRecipient(key)

	set := t.TempDir()
	var sums strings.Builder
	for _, n := range []string{"ledger.jsonl", "aii.db"} {
		body := []byte("the contents of " + n)
		os.WriteFile(filepath.Join(set, n), body, 0o600)
		h := sha256.Sum256(body)
		sums.WriteString(hex.EncodeToString(h[:]) + "  " + n + "\n")
	}
	os.WriteFile(filepath.Join(set, "SHA256SUMS"), []byte(sums.String()), 0o600)
	if corrupt {
		os.WriteFile(filepath.Join(set, "aii.db"), []byte("not what was listed"), 0o600)
	}
	snapshot = filepath.Join(t.TempDir(), "ledger-20260919T040000Z-seq2"+escrow.SnapshotSuffix)
	if err := escrow.EncryptSnapshot(set, []string{"ledger.jsonl", "aii.db", "SHA256SUMS"}, recipient, snapshot); err != nil {
		t.Fatal(err)
	}
	return identityDir, snapshot
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
	if code != 0 || !strings.Contains(said, "OPENED: 3 files") || !strings.Contains(said, "aii verify -ledger") {
		t.Fatalf("open: %d %s", code, said)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "aii.db")); string(b) != "the contents of aii.db" {
		t.Fatalf("aii.db: %q", b)
	}
	// .
	if code, said := snapshotRun("open", "-in", snap, "-out", out, "-dir", dir); code != 1 || !strings.Contains(said, "already exists") {
		t.Fatalf("a snapshot was unpacked over a directory: %d %s", code, said)
	}
}

// .
// .
// .
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

// .
// .
// .
func TestSnapshotOpenOpensOnlyWhatItUnpacked(t *testing.T) {
	identityDir := t.TempDir()
	os.MkdirAll(filepath.Join(identityDir, "data"), 0o700)
	key, err := escrow.NewSnapshotKey()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(identityDir, "data", escrow.SnapshotKeyFileName), key, 0o600)
	recipient, _ := escrow.SnapshotRecipient(key)

	// .
	// .
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
