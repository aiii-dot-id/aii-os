package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/witness"
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
// .
// .
// .
// .
// .
// .
// .
// .
// .
func runVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ledgerPath := fs.String("ledger", "", "path to ledger.jsonl (required)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: aii verify -ledger <path to ledger.jsonl>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Verifies a ledger's hash chain and signatures on its own terms:")
		fmt.Fprintln(stderr, "the genesis event carries the identity's public key, that key must")
		fmt.Fprintln(stderr, "bind to its fingerprint, and every line must verify under the gold")
		fmt.Fprintln(stderr, "envelope. Needs no config, no network and no running identity —")
		fmt.Fprintln(stderr, "point it at a backup copy and it answers about that copy.")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *ledgerPath == "" {
		fs.Usage()
		return 2
	}
	heads, err := witness.LoadHeadVerifier(filepath.Dir(*ledgerPath), genesis.PinnedRoot())
	if err != nil {
		fmt.Fprintf(stderr, "NOT VERIFIED: witness keys beside the ledger: %v\n", err)
		return 1
	}
	n, fp, err := genesis.VerifySelfContainedWith(*ledgerPath, heads)
	if err != nil {
		// .
		// .
		fmt.Fprintf(stderr, "NOT VERIFIED: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "VERIFIED: %d events, identity %s — gold envelope, self-contained; %d witness heads verified under %d persisted keys, %d tail heads unverified\n",
		n, fp, heads.Verified(), heads.Keys(), heads.Unverified())
	return 0
}
