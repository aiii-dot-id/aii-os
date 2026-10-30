package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
)

const snapshotUsage = `usage:
  aii snapshot open -in <ledger-…-seqN.tar.age> -out <new directory> [-dir <identity dir>] [-key <snapshot.key>] [-identity <identity.sec>]

Decrypts an encrypted snapshot with the snapshot key and unpacks it into a
directory that must not exist, then checks every file against the snapshot's
own SHA256SUMS, and SHA256SUMS against the identity's signature beside it:
under the identity key you name, or data/identity.sec, never a key the
snapshot carries. The snapshot key is data/snapshot.key on the host that made
the snapshot; on a machine that has lost either key, 'aii escrow restore'
puts it back.
The stock tool works too, and checks neither:  age -d -i snapshot.key <file> | tar x
Then:  aii verify -ledger <out>/ledger.jsonl   proves what it holds before you restore from it.
`

func runSnapshot(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "open" {
		fmt.Fprint(stderr, snapshotUsage)
		return 2
	}
	fs := flag.NewFlagSet("snapshot open", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, snapshotUsage) }
	dir := fs.String("dir", "", "identity install directory (default: the current directory)")
	keyPath := fs.String("key", "", "snapshot key (default: <dir>/data/snapshot.key)")
	in := fs.String("in", "", "encrypted snapshot to open")
	out := fs.String("out", "", "directory to unpack into; must not exist")
	identityPath := fs.String("identity", "", "the identity key the signature is held to (default: <dir>/data/identity.sec)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *in == "" || *out == "" || fs.NArg() != 0 {
		fmt.Fprint(stderr, snapshotUsage)
		return 2
	}
	if *keyPath == "" {
		*keyPath = filepath.Join(*dir, "data", escrow.SnapshotKeyFileName)
	}
	if *identityPath == "" {
		*identityPath = filepath.Join(*dir, "data", "identity.sec")
	}
	key, err := os.ReadFile(*keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "NOT OPENED: %v — the snapshot key stands beside the identity key (name it with -key if that is not <dir>/data); on a machine that has lost it, `aii escrow restore` puts it back\n", err)
		return 1
	}
	defer escrow.Wipe(key)

	kp, err := crypto.LoadKeyPair(*identityPath)
	if err != nil {
		fmt.Fprintf(stderr, "NOT OPENED: the identity key does not read (%v) — it is the key the snapshot's signature is held to; name it with -identity if that is not <dir>/data/identity.sec, and on a machine that has lost it, `aii escrow restore` puts it back\n", err)
		return 1
	}
	names, err := escrow.OpenSnapshot(*in, key, *out)
	if err != nil {
		fmt.Fprintf(stderr, "NOT OPENED: %v\n", err)
		return 1
	}
	if err := escrow.CheckSums(*out, names); err != nil {
		os.RemoveAll(*out)
		fmt.Fprintf(stderr, "NOT OPENED: it decrypts, and %v — nothing was kept\n", err)
		return 1
	}
	if err := signedByIdentity(*out, kp); err != nil {
		os.RemoveAll(*out)
		if errors.Is(err, escrow.ErrUnsigned) {
			fmt.Fprintf(stderr, "NOT OPENED: %v (snapshots made before they were signed carry none), so nothing holds what it holds to identity %s — nothing was kept. If it is the one you need, the stock tool opens it without this check: age -d -i snapshot.key <file> | tar x\n", err, kp.Fingerprint())
			return 1
		}
		fmt.Fprintf(stderr, "NOT OPENED: it decrypts and matches its SHA256SUMS, and %v — nothing was kept\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "OPENED: %d files in %s, each matching the snapshot's own SHA256SUMS, which identity %s signed.\nNext: aii verify -ledger %s\n",
		len(names), *out, kp.Fingerprint(), filepath.Join(*out, "ledger.jsonl"))
	return 0
}

func signedByIdentity(dir string, kp *crypto.KeyPair) error {
	signed, err := escrow.VerifySumsSig(dir, kp.PublicKeyBytes())
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "RECEIPT.json"))
	if err != nil {
		return fmt.Errorf("%w: it holds no receipt: %w", escrow.ErrSumsSig, err)
	}
	var receipt struct {
		LastSeq  uint64 `json:"last_seq"`
		LastHash string `json:"last_hash"`
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return fmt.Errorf("%w: its receipt does not read: %w", escrow.ErrSumsSig, err)
	}
	if receipt.LastSeq != signed.LastSeq || receipt.LastHash != signed.LastHash {
		return fmt.Errorf("%w: it signs record %d and its receipt names record %d", escrow.ErrSumsSig, signed.LastSeq, receipt.LastSeq)
	}
	return nil
}
