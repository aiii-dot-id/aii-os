// .
// .
// .
// .
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func main() {
	ledgerPath := flag.String("ledger", "", "path to ledger.jsonl (required)")
	keyPath := flag.String("key", "", "path to identity.sec (required)")
	outPath := flag.String("out", "", "output path (default: replace -ledger atomically)")
	db := flag.String("db", "", "the identity's projection (aii.db) to carry across the re-wrap: the mirror is rewritten from the new record, derived tables rebuild, every ephemeral table keeps every row")
	flag.Parse()
	if *ledgerPath == "" || *keyPath == "" {
		fmt.Fprintln(os.Stderr, "usage: rewrap -ledger ledger.jsonl -key identity.sec [-out new.jsonl] [-db aii.db]")
		os.Exit(2)
	}

	kp, err := crypto.LoadKeyPair(*keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rewrap: load key: %v\n", err)
		os.Exit(1)
	}
	earlier, err := ledger.EarlierReceipts(*ledgerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rewrap: %v\n", err)
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
	fmt.Printf("rewrapped %d events as the record → %s (verified and replayable)\n", n, abs)
	// .
	// .
	// .
	// .
	// .
	retireSegments(dst, dst == *ledgerPath)
	if *db != "" {
		// .
		// .
		// .
		// .
		report, err := store.CarryAcross(*db, dst)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rewrap: the record is in place but the projection at %s was NOT carried: %v\n  re-run with -db once the cause is fixed, or restore the projection from your backup; do not boot on it as it is\n", *db, err)
			os.Exit(1)
		}
		fmt.Println(report.String())
	}
	if earlier > 0 {
		// .
		// .
		// .
		// .
		tail := filepath.Join(filepath.Dir(dst), "witness-tail.json")
		if _, statErr := os.Stat(tail); statErr == nil {
			retired := tail + ".retired-" + time.Now().UTC().Format("20060102T150405Z")
			if err := os.Rename(tail, retired); err != nil {
				fmt.Fprintf(os.Stderr, "rewrap: witness tail file could not be retired (%v) — remove %s by hand before the next boot\n", err, tail)
			} else {
				fmt.Printf("retired %s → %s\n", tail, retired)
			}
		}
		fmt.Printf("kept %d earlier witness receipt(s) in place as footnotes (receipt_before_rewrap): they attested the shape before the re-wrap and attest nothing now; reset this identity's row at the witness before it anchors again\n", earlier)
	}
}

func validateReplay(candidatePath string) (retErr error) {
	projection, err := store.NewMemory()
	if err != nil {
		return fmt.Errorf("open replay projection: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, projection.Close()) }()
	return projection.ReplayFromFile(candidatePath)
}

// .
// .
// .
func retireSegments(path string, inPlace bool) {
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var segs []string
	for _, e := range entries {
		if !e.IsDir() && ledger.IsSegmentName(e.Name()) {
			segs = append(segs, e.Name())
		}
	}
	if len(segs) == 0 {
		return
	}
	if !inPlace {
		fmt.Printf("%d sealed segment(s) beside the source ledger belong to the earlier record — install the new tail without them; the first bookmark seals the new record\n", len(segs))
		return
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for _, name := range segs {
		from := filepath.Join(dir, name)
		to := from + ".retired-" + stamp
		if err := os.Rename(from, to); err != nil {
			fmt.Fprintf(os.Stderr, "rewrap: segment %s could not be retired (%v) — move it away by hand before the next boot\n", name, err)
			continue
		}
		fmt.Printf("retired %s → %s\n", from, to)
	}
}
