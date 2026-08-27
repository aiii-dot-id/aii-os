// rewrap re-signs an entire ledger under the CURRENT gold envelope —
// the routine tool for pre-gold format changes (2026-08-17 ruling: no
// v1/v2 eras; re-wrap and move on). All logic lives in ledger.Rewrap;
// this is the flag wrapper.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func main() {
	ledgerPath := flag.String("ledger", "", "path to ledger.jsonl (required)")
	keyPath := flag.String("key", "", "path to identity.sec (required)")
	outPath := flag.String("out", "", "output path (default: replace -ledger atomically)")
	flag.Parse()
	if *ledgerPath == "" || *keyPath == "" {
		fmt.Fprintln(os.Stderr, "usage: rewrap -ledger ledger.jsonl -key identity.sec [-out new.jsonl]")
		os.Exit(2)
	}

	kp, err := crypto.LoadKeyPair(*keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rewrap: load key: %v\n", err)
		os.Exit(1)
	}
	n, err := ledger.Rewrap(*ledgerPath, kp, *outPath, validateReplay)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rewrap: %v\n", err)
		os.Exit(1)
	}
	dst := *outPath
	if dst == "" {
		dst = *ledgerPath
	}
	abs, _ := filepath.Abs(dst)
	fmt.Printf("rewrapped %d events under the gold envelope → %s (verified and replayable)\n", n, abs)
}

func validateReplay(events []ledger.Event) (retErr error) {
	projection, err := store.NewMemory()
	if err != nil {
		return fmt.Errorf("open replay projection: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, projection.Close()) }()
	return projection.ReplayAll(events)
}
