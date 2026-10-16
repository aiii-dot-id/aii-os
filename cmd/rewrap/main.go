package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	ledgerPath := fs.String("ledger", "", "path to ledger.jsonl (required)")
	keyPath := fs.String("key", "", "path to identity.sec (required)")
	outPath := fs.String("out", "", "output path (default: replace -ledger atomically)")
	db := fs.String("db", "", "the identity's projection (aii.db) to carry across the re-wrap: the mirror is rewritten from the new record, derived tables rebuild, every ephemeral table keeps every row")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *ledgerPath == "" || *keyPath == "" {
		fmt.Fprintln(stderr, "usage: rewrap -ledger ledger.jsonl -key identity.sec [-out new.jsonl] [-db aii.db]")
		return 2
	}

	kp, err := crypto.LoadKeyPair(*keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "rewrap: load key: %v\n", err)
		return 1
	}
	earlier, err := ledger.EarlierReceipts(*ledgerPath)
	if err != nil {
		fmt.Fprintf(stderr, "rewrap: %v\n", err)
		return 1
	}
	var verified store.MirrorHead
	n, err := ledger.Rewrap(*ledgerPath, kp, *outPath, func(path string) error {
		var err error
		verified, err = validateReplayHead(path)
		return err
	})
	if err != nil {
		fmt.Fprintf(stderr, "rewrap: %v\n", err)
		return 1
	}
	dst := *outPath
	if dst == "" {
		dst = *ledgerPath
	}
	abs, _ := filepath.Abs(dst)
	fmt.Fprintf(stdout, "rewrapped %d events as the record → %s (verified and replayable)\n", n, abs)

	retireSegments(*ledgerPath, dst == *ledgerPath, stdout, stderr)
	if *db != "" {

		report, err := store.CarryAcross(*db, dst, verified)
		if err != nil {
			fmt.Fprintf(stderr, "rewrap: the record is in place but the projection carry at %s could not be confirmed: %v\n  the database may be unchanged or fully upgraded; re-run with -db once the cause is fixed; do not delete the database or boot before the carry is confirmed\n", *db, err)
			return 1
		}
		fmt.Fprintln(stdout, report.String())
	}
	if earlier > 0 {

		retireWitnessTail(*ledgerPath, dst == *ledgerPath, stdout, stderr)
		fmt.Fprintf(stdout, "kept %d earlier witness receipt(s) in place as footnotes (receipt_before_rewrap): they attested the shape before the re-wrap and attest nothing now; reset this identity's row at the witness before it anchors again\n", earlier)
	}
	return 0
}

func validateReplayHead(candidatePath string) (head store.MirrorHead, retErr error) {
	projection, err := store.NewMemory()
	if err != nil {
		return head, fmt.Errorf("open replay projection: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, projection.Close()) }()
	err = projection.ReplayAll(func(yield func(*ledger.Event) error) error {
		return ledger.Stream(candidatePath, func(evt *ledger.Event) error {
			if err := yield(evt); err != nil {
				return err
			}
			head = store.MirrorHead{Seq: evt.Seq, Hash: evt.EntryHash()}
			return nil
		})
	})
	return head, err
}

func retireWitnessTail(source string, inPlace bool, stdout, stderr io.Writer) {
	tail := filepath.Join(filepath.Dir(source), "witness-tail.json")
	if _, err := os.Stat(tail); err != nil {
		return
	}
	if !inPlace {
		fmt.Fprintf(stdout, "the witness tail beside the source ledger attests the earlier record — move %s aside when the new record is installed there, or the next boot reports a fork that never happened\n", tail)
		return
	}
	retired := tail + ".retired-" + time.Now().UTC().Format("20060102T150405Z")
	if err := os.Rename(tail, retired); err != nil {
		fmt.Fprintf(stderr, "rewrap: witness tail file could not be retired (%v) — remove %s by hand before the next boot\n", err, tail)
		return
	}
	fmt.Fprintf(stdout, "retired %s → %s\n", tail, retired)
}

func retireSegments(source string, inPlace bool, stdout, stderr io.Writer) {
	dir := filepath.Dir(source)
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
		fmt.Fprintf(stdout, "%d sealed segment(s) beside the source ledger belong to the earlier record — install the new tail without them; the first bookmark seals the new record\n", len(segs))
		return
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for _, name := range segs {
		from := filepath.Join(dir, name)
		to := from + ".retired-" + stamp
		if err := os.Rename(from, to); err != nil {
			fmt.Fprintf(stderr, "rewrap: segment %s could not be retired (%v) — move it away by hand before the next boot\n", name, err)
			continue
		}
		fmt.Fprintf(stdout, "retired %s → %s\n", from, to)
	}
}
