package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/escrow"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

const snapshotUsage = `usage:
  aii snapshot open -in <ledger-…-seqN.tar.age> -out <new directory> [-dir <identity dir>] [-key <snapshot.key>]

Decrypts an encrypted snapshot with the snapshot key and unpacks it into a
directory that must not exist, then checks every file against the snapshot's
own SHA256SUMS. The key is data/snapshot.key on the host that made the
snapshot; on a machine that has lost it, 'aii escrow restore' puts it back.
The stock tool works too:  age -d -i snapshot.key <file> | tar x
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
	key, err := os.ReadFile(*keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "NOT OPENED: %v — the snapshot key stands beside the identity key (name it with -key if that is not <dir>/data); on a machine that has lost it, `aii escrow restore` puts it back\n", err)
		return 1
	}
	defer escrow.Wipe(key)
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
	fmt.Fprintf(stdout, "OPENED: %d files in %s, each matching the snapshot's own SHA256SUMS.\nNext: aii verify -ledger %s\n",
		len(names), *out, filepath.Join(*out, "ledger.jsonl"))
	return 0
}
