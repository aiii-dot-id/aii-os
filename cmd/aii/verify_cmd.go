package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

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
	var cross crossFlag
	fs.Var(&cross, "cross", "another identity's ledger.jsonl to check this ledger's citations against; may repeat")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: aii verify -ledger <path to ledger.jsonl> [-cross <another identity's ledger.jsonl>]...")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Verifies a ledger's hash chain and signatures on its own terms:")
		fmt.Fprintln(stderr, "the genesis event carries the identity's public key, that key must")
		fmt.Fprintln(stderr, "bind to its fingerprint, and every line must verify under the gold")
		fmt.Fprintln(stderr, "envelope. Needs no config, no network and no running identity —")
		fmt.Fprintln(stderr, "point it at a backup copy and it answers about that copy.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "-cross checks this ledger's citations against ledgers YOU supply: each is")
		fmt.Fprintln(stderr, "verified under the keys beside it, and every citation is matched,")
		fmt.Fprintln(stderr, "mismatched, or not checked. Nothing is fetched. Exit 0: verified and every")
		fmt.Fprintln(stderr, "citation matched; 1: not verified or a mismatch; 3: verified, but some")
		fmt.Fprintln(stderr, "citations could not be checked.")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *ledgerPath == "" {
		fs.Usage()
		return 2
	}
	// .
	// .
	out, errOut, code := genesis.VerifyCommand(*ledgerPath, cross, besideLedger)
	fmt.Fprint(stdout, out)
	fmt.Fprint(stderr, errOut)
	return code
}

// .
// .
// .
// .
// .
// .
func besideLedger(ledgerDir string) (genesis.Beside, error) {
	beside, err := witness.LoadBeside(ledgerDir, genesis.PinnedRoot())
	if err != nil {
		return nil, err
	}
	return beside, nil
}

// .
type crossFlag []string

func (c *crossFlag) String() string     { return strings.Join(*c, ",") }
func (c *crossFlag) Set(v string) error { *c = append(*c, v); return nil }
