// .
// .
// .
// .
// .
// .
// .
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

func main() {
	ledgerPath := flag.String("ledger", "", "path to ledger.jsonl (required)")
	flag.Parse()
	if *ledgerPath == "" {
		fmt.Fprintln(os.Stderr, "usage: verify -ledger ledger.jsonl")
		os.Exit(2)
	}
	// .
	// .
	// .
	heads, err := witness.LoadHeadVerifier(filepath.Dir(*ledgerPath), genesis.PinnedRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "NOT VERIFIED: witness keys beside the ledger: %v\n", err)
		os.Exit(1)
	}
	n, fp, err := genesis.VerifySelfContainedWith(*ledgerPath, heads)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NOT VERIFIED: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("VERIFIED: %d events, identity %s — gold envelope, self-contained; %d witness heads verified under %d persisted keys, %d tail heads unverified\n",
		n, fp, heads.Verified(), heads.Keys(), heads.Unverified())
}
