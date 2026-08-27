// verify is the PUBLIC conformance check for the gold ledger format
// (R61: two implementations, one format, open core — and common means
// anyone verifies any identity's chain, from either implementation,
// without trusting anyone). Everything needed travels IN the chain:
// the genesis event carries the identity public key, the key must bind
// to its fingerprint, and every line must verify under the gold
// envelope (docs/LEDGER_GOLD_FORMAT.md).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
)

func main() {
	ledgerPath := flag.String("ledger", "", "path to ledger.jsonl (required)")
	flag.Parse()
	if *ledgerPath == "" {
		fmt.Fprintln(os.Stderr, "usage: verify -ledger ledger.jsonl")
		os.Exit(2)
	}
	n, fp, err := genesis.VerifySelfContained(*ledgerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NOT VERIFIED: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("VERIFIED: %d events, identity %s — gold envelope, self-contained\n", n, fp)
}
