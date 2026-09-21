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
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

func main() {
	ledgerPath := flag.String("ledger", "", "path to ledger.jsonl (required)")
	var cross crossFlag
	flag.Var(&cross, "cross", "another identity's ledger.jsonl to check this ledger's citations against; may repeat")
	flag.Parse()
	if *ledgerPath == "" {
		fmt.Fprintln(os.Stderr, "usage: verify -ledger ledger.jsonl [-cross other-ledger.jsonl]...")
		os.Exit(genesis.ExitUsage)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	out, errOut, code := genesis.VerifyCommand(*ledgerPath, cross, func(dir string) (genesis.Beside, error) {
		beside, err := witness.LoadBeside(dir, genesis.PinnedRoot())
		if err != nil {
			return nil, err
		}
		return beside, nil
	})
	fmt.Fprint(os.Stdout, out)
	fmt.Fprint(os.Stderr, errOut)
	os.Exit(code)
}

// .
type crossFlag []string

func (c *crossFlag) String() string     { return strings.Join(*c, ",") }
func (c *crossFlag) Set(v string) error { *c = append(*c, v); return nil }
