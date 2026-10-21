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
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

const usage = "usage: rewrap -ledger ledger.jsonl -key identity.sec [-out new.jsonl] [-db aii.db] [-platform-pubkey platform.pub.json] [-prior-shape-unchecked]"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	ledgerPath := fs.String("ledger", "", "path to ledger.jsonl (required)")
	keyPath := fs.String("key", "", "path to identity.sec (required)")
	outPath := fs.String("out", "", "output path (default: replace -ledger atomically)")
	db := fs.String("db", "", "the identity's projection (aii.db) to carry across the re-wrap: the mirror is rewritten from the new record, derived tables rebuild, every ephemeral table keeps every row")
	platformPath := fs.String("platform-pubkey", "", "the platform public key envelope the witness keys beside the ledger verify under, when the identity's config names one (witness.platform_pubkey_path); default: the root this build pins")
	priorShape := fs.Bool("prior-shape-unchecked", false, "re-wrap a ledger in the shape written before the record ruling: this build reads that shape and cannot check its links or its proofs, and the output says they were not checked")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *ledgerPath == "" || *keyPath == "" {
		fmt.Fprintln(stderr, usage)
		return 2
	}

	kp, err := crypto.LoadKeyPair(*keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "rewrap: load key: %v\n", err)
		return 1
	}
	platform, err := witness.LoadPlatformEnvelope(*platformPath)
	if err != nil {
		fmt.Fprintf(stderr, "rewrap: %v\n", err)
		return 1
	}
	dir := filepath.Dir(*ledgerPath)
	dst := *outPath
	if dst == "" {
		dst = *ledgerPath
	}
	inPlace := dst == *ledgerPath
	if inPlace {
		if refusal := pendingBeside(dir); refusal != "" {
			fmt.Fprintf(stderr, "rewrap: %s\n", refusal)
			return 1
		}
	}
	var verified store.MirrorHead

	attempt := func() (done ledger.Rewrapped, said string, err error) {
		heads, err := witness.LoadHeadVerifier(dir, platform)
		if err != nil {
			return ledger.Rewrapped{}, "", fmt.Errorf("witness keys beside the ledger: %w", err)
		}
		beside := &witness.Beside{Heads: heads}
		var tailErr error
		var through uint64
		asked := false
		done, err = rewrapWith(*ledgerPath, kp, *outPath, func(path string) error {
			var err error
			verified, err = validateReplayHead(path)
			return err
		}, ledger.RewrapChecks{
			Heads: heads,
			Visit: func(evt *ledger.Event) error {
				if !asked {
					asked = true
					var tail *witness.LocalTail
					tail, tailErr = witness.ReadLocalTail(dir)
					beside.Tail = witness.NewTailCheck(tail)
				}
				through = evt.Seq
				return beside.Visit(evt)
			},
			Held: func() error {
				if tailErr != nil {
					return fmt.Errorf("beside the ledger: %w", tailErr)
				}
				if err := beside.Held(); err != nil {
					return err
				}
				if *db != "" {
					return store.CheckAcknowledgedPrefix(*db, through)
				}
				return nil
			},
			PriorShapeUnchecked: *priorShape,
		})
		return done, beside.Summary(), err
	}
	done, said, err := attempt()
	sweptSaid(done, stdout)

	var unfinished *ledger.UnfinishedRewrap
	if errors.As(err, &unfinished) && inPlace {
		fmt.Fprintf(stdout, "an earlier re-wrap of this ledger published its tail and was stopped before it retired the record it replaced; proved, and finished now: the tail alone verifies as the whole record (%d records), and each of the %d records in the %d sealed segment(s) beside it is in it\n", unfinished.Records, unfinished.Sealed, unfinished.Segments)
		if !retireWitnessTail(*ledgerPath, true, stdout, stderr) || !retireSegments(*ledgerPath, true, stdout, stderr) {
			fmt.Fprintln(stderr, "rewrap: the earlier re-wrap could not be finished: what could not be moved is named above; move it aside and run rewrap again")
			return 1
		}
		if unfinished.Footnotes > 0 {
			fmt.Fprintf(stdout, "kept %d earlier witness receipt(s) in place as footnotes (receipt_before_rewrap): they attested the shape before the re-wrap and attest nothing now; reset this identity's row at the witness before it anchors again\n", unfinished.Footnotes)
		}
		done, said, err = attempt()
		sweptSaid(done, stdout)
	}
	if err != nil && !done.Published {
		fmt.Fprintf(stderr, "rewrap: %v\n", err)
		if errors.Is(err, ledger.ErrPriorShapeUnchecked) {
			fmt.Fprintln(stderr, "  to re-wrap it with its links and proofs unchecked, and be told so in the output, run again with -prior-shape-unchecked")
		}
		return 1
	}

	abs, _ := filepath.Abs(dst)
	fmt.Fprint(stdout, readLine(done, said))
	fmt.Fprintf(stdout, "rewrapped %d events as the record → %s (verified and replayable)\n", done.Records, abs)
	retired := true
	if done.Footnoted > 0 {

		retired = retireWitnessTail(*ledgerPath, inPlace, stdout, stderr)
		fmt.Fprintf(stdout, "kept %d earlier witness receipt(s) in place as footnotes (receipt_before_rewrap): they attested the shape before the re-wrap and attest nothing now; reset this identity's row at the witness before it anchors again\n", done.Footnoted)
	}

	retired = retireSegments(*ledgerPath, inPlace, stdout, stderr) && retired
	owed := ""
	if *db != "" {
		owed = "\n  the projection at " + *db + " was not carried: re-run with -db once the cause is fixed; do not delete the database or boot before the carry is confirmed"
	}
	if err != nil {
		fmt.Fprintf(stderr, "rewrap: the record is published at %s, and a step after its publication failed: %v\n  what stood beside the earlier record is retired or named above%s\n", abs, err, owed)
		return 1
	}
	if !retired {
		fmt.Fprintf(stderr, "rewrap: the record is published at %s, and not everything beside the earlier record could be retired: move what is named above aside before the next boot%s\n", abs, owed)
		return 1
	}
	if *db != "" {

		report, err := carryAcross(*db, dst, verified)
		if err != nil {
			fmt.Fprintf(stderr, "rewrap: the record is in place but the projection carry at %s could not be confirmed: %v\n  the database may be unchanged or fully upgraded; re-run with -db once the cause is fixed; do not delete the database or boot before the carry is confirmed\n", *db, err)
			return 1
		}
		fmt.Fprintln(stdout, report.String())
	}
	return 0
}

var pendingFiles = []string{witness.PendingReceiptFileName}

func pendingBeside(dir string) string {
	for _, name := range pendingFiles {
		path := filepath.Join(dir, name)
		_, err := os.Lstat(path)
		switch {
		case err == nil:
			return fmt.Sprintf("%s stands beside the ledger: it names a hash of this record that a re-wrap in place replaces, and could never be settled after it (nothing replaced) — boot the identity once, which settles it and removes the file, then run rewrap again; where a boot cannot settle it (it stops at SAFE, or refuses the receipt and says why), move the file aside by hand or write the re-wrap elsewhere with -out", path)
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Sprintf("whether %s stands beside the ledger cannot be told (%v): nothing replaced", path, err)
		}
	}
	return ""
}

var (
	rewrapWith  = ledger.RewrapWith
	carryAcross = store.CarryAcross
)

func sweptSaid(done ledger.Rewrapped, stdout io.Writer) {
	for _, stale := range done.Swept {
		fmt.Fprintf(stdout, "removed %s: the unfinished candidate of a re-wrap that was stopped before it published (the record re-signed, never installed)\n", stale)
	}
}

func readLine(done ledger.Rewrapped, beside string) string {
	if done.PriorShape {
		return fmt.Sprintf("read %d records in the prior shape: each payload held to its content hash and each line's signer to this key. NOT CHECKED: its links and its proofs (this build reads the prior shape and holds no verifier for it), and the witness keys and witness tail beside it, which were not asked\n", done.Records)
	}
	return fmt.Sprintf("read %d records, %d of them from sealed segments, and held them as a boot does: every link, every payload hash and every proof verified; %s\n", done.Records, done.Sealed, beside)
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

func retireWitnessTail(source string, inPlace bool, stdout, stderr io.Writer) bool {
	tail := witness.TailPath(filepath.Dir(source))
	if _, err := os.Stat(tail); err != nil {
		return true
	}
	if !inPlace {
		fmt.Fprintf(stdout, "the witness tail beside the source ledger attests the earlier record — move %s aside when the new record is installed there, or the next boot reports a fork that never happened\n", tail)
		return true
	}
	retired := tail + ".retired-" + time.Now().UTC().Format("20060102T150405Z")
	if err := os.Rename(tail, retired); err != nil {
		fmt.Fprintf(stderr, "rewrap: witness tail file could not be retired (%v) — remove %s by hand before the next boot\n", err, tail)
		return false
	}
	fmt.Fprintf(stdout, "retired %s → %s\n", tail, retired)
	return true
}

func retireSegments(source string, inPlace bool, stdout, stderr io.Writer) bool {
	dir := filepath.Dir(source)
	segs, err := ledger.SegmentNames(dir)
	if err != nil {
		fmt.Fprintf(stderr, "rewrap: the sealed segments beside %s could not be listed (%v) — move every segment-*.jsonl.gz there aside by hand before the next boot\n", source, err)
		return false
	}
	if len(segs) == 0 {
		return true
	}
	if !inPlace {
		fmt.Fprintf(stdout, "%d sealed segment(s) beside the source ledger belong to the earlier record — install the new tail without them; the first bookmark seals the new record\n", len(segs))
		return true
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	all := true
	for i := len(segs) - 1; i >= 0; i-- {
		from := filepath.Join(dir, segs[i])
		to := from + ".retired-" + stamp
		if err := os.Rename(from, to); err != nil {
			fmt.Fprintf(stderr, "rewrap: segment %s could not be retired (%v) — move it away by hand before the next boot\n", segs[i], err)
			all = false
			break
		}
		fmt.Fprintf(stdout, "retired %s → %s\n", from, to)
	}
	return all
}
