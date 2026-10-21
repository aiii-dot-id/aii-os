package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/witness"
	"github.com/aiii-dot-id/aii-os/internal/witness/witnesstest"
)

func TestRunRefusesWithoutBothPathsBeforeTouchingAnything(t *testing.T) {
	for _, args := range [][]string{nil, {"-ledger", "x.jsonl"}, {"-key", "identity.sec"}} {
		var stdout, stderr strings.Builder
		if code := run(args, &stdout, &stderr); code != 2 || stdout.Len() != 0 ||
			stderr.String() != "usage: rewrap -ledger ledger.jsonl -key identity.sec [-out new.jsonl] [-db aii.db] [-platform-pubkey platform.pub.json] [-prior-shape-unchecked]\n" {
			t.Errorf("run(%q) = %d, stdout %q, stderr %q", args, code, stdout.String(), stderr.String())
		}
	}
}

// born returns a real ledger and its key in dir: a chain a stranger could
// verify, closed so the re-wrap can take its lock.
func born(t *testing.T, dir string) (ledgerPath, keyPath string) {
	t.Helper()
	ledgerPath = filepath.Join(dir, "ledger.jsonl")
	keyPath = filepath.Join(dir, "identity.sec")
	root := genesistest.NewRoot(t)
	result, err := genesis.Birth(&genesis.BirthConfig{
		Name:        "RewrapTest",
		Ring0Bundle: root.Ring0Bundle(t),
		Root:        root.Env,
		KeyPath:     keyPath,
		LedgerPath:  ledgerPath,
		DBPath:      filepath.Join(dir, "aii.db"),
	})
	if err != nil {
		t.Fatalf("birth: %v", err)
	}
	if result.Ledger != nil {
		result.Ledger.Close()
	}
	return ledgerPath, keyPath
}

// The anchorer's three doors, as the witness package's own tests keep
// them: the envelope row, no earlier receipt, and a minter that appends
// the head the runtime mints (the whole receipt, so the head verifier
// can check the chain of receipts and the witness's signature).
type envelopeRow struct{ saved []byte }

func (e *envelopeRow) SaveWitnessEnvelope(j []byte) error {
	if e.saved == nil {
		e.saved = j
	}
	return nil
}
func (e *envelopeRow) ReplaceWitnessEnvelope(j []byte) error { e.saved = j; return nil }
func (e *envelopeRow) LoadWitnessEnvelope() ([]byte, error)  { return e.saved, nil }
func (e *envelopeRow) WitnessEnrollment() ([]string, time.Time, time.Time, error) {
	return nil, time.Time{}, time.Time{}, nil
}

type noEarlierReceipt struct{}

func (noEarlierReceipt) LastWitnessReceipt() (int64, []byte, error) { return 0, nil, nil }

type headMinter struct {
	lg *ledger.Ledger
	kp *crypto.KeyPair
}

func (m headMinter) MintWitnessed(receipt witness.WitnessReceipt, witnessKeyID string) (*ledger.Event, error) {
	return m.lg.Append(ledger.EventSystemWitnessed, 0, map[string]any{
		"receipt": map[string]any{
			"identity_id":                       receipt.IdentityID,
			"previous_witnessed_ledger_ordinal": receipt.PreviousWitnessedLedgerOrdinal,
			"previous_witnessed_ledger_hash":    receipt.PreviousWitnessedLedgerHash,
			"ledger_ordinal":                    receipt.LedgerOrdinal,
			"ledger_hash":                       receipt.LedgerHash,
			"witnessed_at":                      receipt.WitnessedAt,
			"witness_key_id":                    witnessKeyID,
			"witness_sig_b64":                   receipt.WitnessSignature.SigB64,
		},
	}, m.kp)
}

// sealed anchors a born ledger once with a witness a platform vouches
// for, as a running identity does: the head is minted, the witness key
// and the platform's manifest for it are persisted beside the ledger, the
// witness tail is written, and the record through the head is sealed into
// a segment, leaving an empty tail. It returns the path of the platform's
// envelope: what the identity's config would name, and what
// -platform-pubkey is given.
func sealed(t *testing.T, ledgerPath, keyPath string) (platformPath string) {
	t.Helper()
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	platform := witnesstest.NewPlatform(t)
	fw := witnesstest.NewWitness(t, platform)
	platformPath = platform.WriteEnv(t, t.TempDir())
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	a := witness.NewAnchorer(witness.New(fw.URL(), ""), lg, witness.AsIdentityKey(kp), &envelopeRow{}, noEarlierReceipt{}, headMinter{lg, kp}, 1, platformPath)
	a.SetSealer(lg)
	if err := a.CheckAndAnchor(); err != nil {
		lg.Close()
		t.Fatalf("anchor: %v", err)
	}
	last, through := lg.LastSeq(), lg.SealedSeq()
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	if through == 0 || through != last {
		t.Fatalf("fixture: sealed through record %d of %d — the head should have sealed everything through itself", through, last)
	}
	return platformPath
}

// segmentsBeside lists the sealed segments in a directory.
func segmentsBeside(t *testing.T, dir string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(dir, "segment-*.jsonl.gz"))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// WITH -out, THE SEGMENTS THE OPERATOR IS TOLD OF ARE THE SOURCE'S. The
// new record is one tail; sealed segments beside the old ledger belong to
// the record it re-signed, and the operator who installs the new tail
// must leave them behind. The notice says "beside the source ledger", and
// the wrapper looked beside the output: written to another directory, it
// found none there and said nothing.
//
// The record here is really sealed. (A stand-in file served while the
// re-wrap read the tail alone and never opened a segment; it reads the
// whole record now, and a segment is read, verified under its witness
// key, and counted in what the operator is told.)
func TestRunWithOutNamesTheSegmentsBesideTheSourceLedger(t *testing.T) {
	source := t.TempDir()
	ledgerPath, keyPath := born(t, source)
	platformPath := sealed(t, ledgerPath, keyPath)
	segments := segmentsBeside(t, source)
	if len(segments) != 1 {
		t.Fatalf("fixture: segments beside the source: %v", segments)
	}
	whole, err := ledger.ReadAll(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "rewrapped.jsonl")

	var stdout, stderr strings.Builder
	if code := run([]string{"-ledger", ledgerPath, "-key", keyPath, "-out", out, "-platform-pubkey", platformPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("run = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	n := strconv.Itoa(len(whole))
	if read := "read " + n + " records, " + n + " of them from sealed segments, and held them as a boot does: every link, every payload hash and every proof verified; 1 witness heads verified under 1 persisted keys, witnessed through record "; !strings.HasPrefix(stdout.String(), read) {
		t.Errorf("the operator is not told first what was read and what it was held to:\n%s", stdout.String())
	}
	const notice = "1 sealed segment(s) beside the source ledger belong to the earlier record — install the new tail without them; the first bookmark seals the new record\n"
	if !strings.Contains(stdout.String(), notice) {
		t.Errorf("the operator is not told of the segment beside the source ledger:\n%s", stdout.String())
	}
	// Written elsewhere, nothing beside the source is touched: the old
	// record is still whole where it stands.
	if _, err := os.Stat(segments[0]); err != nil {
		t.Errorf("the source's segment was moved by a re-wrap written elsewhere: %v", err)
	}
	if strings.Contains(stdout.String(), "retired ") {
		t.Errorf("a re-wrap written elsewhere retired something beside the source:\n%s", stdout.String())
	}
	// The whole record came across, as one tail a stranger verifies with
	// nothing beside it.
	if got, _, err := genesis.VerifySelfContained(out); err != nil || got != len(whole) {
		t.Errorf("the result holds %d of %d records, verified: %v", got, len(whole), err)
	}
}

// AN IDENTITY WITH A SEALED SEGMENT IS RE-WRAPPED WHOLE, IN PLACE. This
// is the run the tool refused with "record 1 is system.witnessed, not
// ring0.genesis" or "ledger is empty": it read the tail alone, and after
// a seal the tail starts after the head. The segment is read under the
// witness key persisted beside the ledger, the key under the platform
// root the identity's config names; the new record replaces the tail; the
// segment and the witness tail are retired beside it, never deleted; and
// what is left is what a boot verifies, under the keys still beside it.
func TestRunInPlaceRewrapsASealedRecordWholeAndRetiresItsSegments(t *testing.T) {
	source := t.TempDir()
	ledgerPath, keyPath := born(t, source)
	platformPath := sealed(t, ledgerPath, keyPath)
	whole, err := ledger.ReadAll(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}

	// Without the root the witness key verifies under, the segment has
	// nothing to stand on: refused, and nothing beside the ledger moves.
	var stdout, stderr strings.Builder
	if code := run([]string{"-ledger", ledgerPath, "-key", keyPath}, &stdout, &stderr); code != 1 || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "witness keys beside the ledger") {
		t.Fatalf("under the wrong platform root: run = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if left := segmentsBeside(t, source); len(left) != 1 {
		t.Fatalf("a refused re-wrap moved the segments: %v", left)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-ledger", ledgerPath, "-key", keyPath, "-platform-pubkey", platformPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("run = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if left := segmentsBeside(t, source); len(left) != 0 {
		t.Errorf("the earlier record's segments still stand beside the new one: %v", left)
	}
	retired, err := filepath.Glob(filepath.Join(source, "segment-*.jsonl.gz.retired-*"))
	if err != nil || len(retired) != 1 {
		t.Fatalf("the segment was not retired beside the ledger: %v, %v", retired, err)
	}
	if !strings.Contains(stdout.String(), " → "+retired[0]+"\n") {
		t.Errorf("the operator is not told where the segment went:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "kept 1 earlier witness receipt(s) in place as footnotes") {
		t.Errorf("the operator is not told what became of the witness's receipt:\n%s", stdout.String())
	}

	// What a boot of the result verifies: every record under its own
	// proof, and the one witnessed record a footnote, never a head.
	platform, err := witness.LoadPlatformEnvelope(platformPath)
	if err != nil {
		t.Fatal(err)
	}
	heads, err := witness.LoadHeadVerifier(source, platform)
	if err != nil {
		t.Fatal(err)
	}
	if got, _, err := genesis.VerifySelfContainedWith(ledgerPath, heads); err != nil || got != len(whole) {
		t.Fatalf("the result holds %d of %d records, verified: %v", got, len(whole), err)
	}
	if heads.Footnotes() != 1 || heads.Verified() != 0 {
		t.Errorf("the result carries %d footnotes and %d heads; want the one receipt as a footnote", heads.Footnotes(), heads.Verified())
	}
}

// witnessed gives a born ledger one witnessed record that still carries its
// receipt — what makes a re-wrap handle the witness tail at all — and puts
// the tail beside it. The re-wrap holds the record to both, as a boot
// does, so both are what a boot accepts: a first receipt for the record
// before the head, under a witness key that is not persisted here (the
// head stands on the identity's proof and is counted unverified), and a
// tail that names the record the receipt attests.
func witnessed(t *testing.T, ledgerPath, keyPath string) (tail string) {
	t.Helper()
	return witnessedTimes(t, ledgerPath, keyPath, 1)
}

// witnessedTimes is witnessed with that many heads, each attesting the
// record before it and continuing from the one before; the tail names
// what the newest attests. From the second head on, the record the tail
// names comes after a head, so its entry hash is one a re-wrap changes.
func witnessedTimes(t *testing.T, ledgerPath, keyPath string, heads int) (tail string) {
	t.Helper()
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	var attested witness.LocalTail
	previous, previousHash := int64(-1), witness.ZeroLedgerHash
	for i := 0; i < heads; i++ {
		attested = witness.LocalTail{LedgerOrdinal: int64(lg.LastSeq()), LedgerHash: lg.LastHash(), WitnessedAt: "2026-09-03T00:00:00Z", WitnessKeyFingerprint: "k"}
		if _, err := lg.Append(ledger.EventSystemWitnessed, 0, map[string]any{
			"receipt": map[string]any{
				"identity_id":                       "did:test:rewrap",
				"previous_witnessed_ledger_ordinal": previous,
				"previous_witnessed_ledger_hash":    previousHash,
				"ledger_ordinal":                    attested.LedgerOrdinal,
				"ledger_hash":                       attested.LedgerHash,
				"witnessed_at":                      attested.WitnessedAt,
				"witness_key_id":                    "k",
				"witness_sig_b64":                   "AAAA",
			},
		}, kp); err != nil {
			t.Fatal(err)
		}
		previous, previousHash = attested.LedgerOrdinal, attested.LedgerHash
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(attested)
	if err != nil {
		t.Fatal(err)
	}
	tail = witness.TailPath(filepath.Dir(ledgerPath))
	if err := os.WriteFile(tail, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return tail
}

// tailNotice is what the operator is told of a witness tail that a re-wrap
// written elsewhere left beside the source ledger.
func tailNotice(tail string) string {
	return "the witness tail beside the source ledger attests the earlier record — move " + tail + " aside when the new record is installed there, or the next boot reports a fork that never happened\n"
}

// THE WITNESS TAIL THAT A RE-WRAP HANDLES IS THE SOURCE'S, AS THE SEGMENTS
// ARE. The tail attests the earlier record. Replaced in place, the new
// record must not be held to it, so it is retired beside the ledger.
// Written elsewhere, the earlier record still stands where it was and the
// tail is still its own: it is left in place and the operator is told what
// to do when the new record is installed. The wrapper looked beside the
// OUTPUT instead: written to another directory it said nothing of the
// source's tail and retired whatever tail stood beside the output, another
// record's; written beside the source it retired the tail of a record it
// had not replaced.
func TestRunWithOutLeavesTheWitnessTailBesideTheSourceAndNamesIt(t *testing.T) {
	for name, outDir := range map[string]func(source string) string{
		"written to another directory": func(string) string { return t.TempDir() },
		"written beside the source":    func(source string) string { return source },
	} {
		t.Run(name, func(t *testing.T) {
			source := t.TempDir()
			ledgerPath, keyPath := born(t, source)
			tail := witnessed(t, ledgerPath, keyPath)
			dir := outDir(source)
			// Another record's tail, where the output is written: not
			// this re-wrap's to move.
			foreign := filepath.Join(dir, "witness-tail.json")
			if dir != source {
				if err := os.WriteFile(foreign, []byte("another record's tail"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			var stdout, stderr strings.Builder
			if code := run([]string{"-ledger", ledgerPath, "-key", keyPath, "-out", filepath.Join(dir, "rewrapped.jsonl")}, &stdout, &stderr); code != 0 {
				t.Fatalf("run = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), tailNotice(tail)) {
				t.Errorf("the operator is not told of the witness tail beside the source ledger:\n%s", stdout.String())
			}
			if strings.Contains(stdout.String(), "retired ") {
				t.Errorf("a re-wrap written elsewhere retired something:\n%s", stdout.String())
			}
			for _, kept := range []string{tail, foreign} {
				if _, err := os.Stat(kept); err != nil {
					t.Errorf("%s was moved by a re-wrap that replaced nothing: %v", kept, err)
				}
			}
		})
	}
}

// In place, the tail beside the ledger is retired, never deleted, and the
// operator is told where it went: what the wrapper did before, pinned so
// the source's-directory rule cannot take it away.
func TestRunInPlaceRetiresTheWitnessTailBesideTheLedger(t *testing.T) {
	source := t.TempDir()
	ledgerPath, keyPath := born(t, source)
	tail := witnessed(t, ledgerPath, keyPath)

	var stdout, stderr strings.Builder
	if code := run([]string{"-ledger", ledgerPath, "-key", keyPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("run = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(tail); !os.IsNotExist(err) {
		t.Errorf("the tail of the record that was replaced still stands beside the new one (stat: %v)", err)
	}
	retired, err := filepath.Glob(tail + ".retired-*")
	if err != nil || len(retired) != 1 {
		t.Fatalf("the tail was not retired beside the ledger: %v, %v", retired, err)
	}
	if !strings.Contains(stdout.String(), "retired "+tail+" → "+retired[0]+"\n") {
		t.Errorf("the operator is not told where the tail went:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "the witness tail beside the source ledger") {
		t.Errorf("an in-place re-wrap told the operator to move a tail it had already retired:\n%s", stdout.String())
	}
}

// heldAsABootHoldsIt verifies the record at ledgerPath under what stands
// beside it, the witness tail included, as a boot does before it opens
// the ledger.
func heldAsABootHoldsIt(t *testing.T, ledgerPath, platformPath string) error {
	t.Helper()
	platform, err := witness.LoadPlatformEnvelope(platformPath)
	if err != nil {
		t.Fatal(err)
	}
	beside, err := witness.LoadBeside(filepath.Dir(ledgerPath), platform)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := genesis.VerifySelfContainedVisiting(ledgerPath, beside, func(string) func(*ledger.Event) error { return beside.Visit }); err != nil {
		return err
	}
	return beside.Held()
}

// A CARRY THAT FAILS LEAVES A STATE THE RE-RUN IT ASKS FOR CAN FINISH.
// The tool answers a failed carry with "re-run with -db once the cause is
// fixed". The witness tail was retired below the carry, so on that path
// it never was: it still named a record of the earlier chain, and from
// the second head on that record's hash is one the re-wrap changed. The
// re-run was refused as LEDGER FORK, of a record that had not forked, and
// so was a boot. The tail goes before the carry now.
func TestAFailedCarryLeavesAStateTheRerunFinishes(t *testing.T) {
	source := t.TempDir()
	ledgerPath, keyPath := born(t, source)
	tail := witnessedTimes(t, ledgerPath, keyPath, 2)
	db := filepath.Join(source, "aii.db")

	was := carryAcross
	t.Cleanup(func() { carryAcross = was })
	carryAcross = func(string, string, store.MirrorHead) (store.CarryReport, error) {
		return store.CarryReport{}, errors.New("the database is held by another process")
	}
	var stdout, stderr strings.Builder
	if code := run([]string{"-ledger", ledgerPath, "-key", keyPath, "-db", db}, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "could not be confirmed") || !strings.Contains(stderr.String(), "re-run with -db") {
		t.Fatalf("a failed carry: run = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(tail); !os.IsNotExist(err) {
		t.Fatalf("the earlier record's witness tail still stands beside the new record after a failed carry (stat: %v)", err)
	}
	if !strings.Contains(stdout.String(), "kept 2 earlier witness receipt(s) in place as footnotes") {
		t.Errorf("a failed carry kept the operator from being told to reset the row at the witness:\n%s", stdout.String())
	}
	// The state in between is not a fork to a boot.
	if err := heldAsABootHoldsIt(t, ledgerPath, ""); err != nil {
		t.Fatalf("a boot of the record a failed carry left: %v", err)
	}

	carryAcross = was
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-ledger", ledgerPath, "-key", keyPath, "-db", db}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "carried ") {
		t.Fatalf("the re-run the tool asked for: run = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

// PUBLISHED, AND THEN A FAILURE: WHAT STOOD BESIDE THE EARLIER RECORD IS
// STILL RETIRED. A step after publication can fail (the directory's
// sync). The wrapper printed that error and stopped, with the new tail
// beside the old segments and witness tail and not a word about them: a
// state every reader refuses. It finishes now, says the record is in
// place, and still answers 1; and the next run is an ordinary one.
func TestRunRetiresTheEarlierRecordWhenAStepAfterPublicationFails(t *testing.T) {
	source := t.TempDir()
	ledgerPath, keyPath := born(t, source)
	platformPath := sealed(t, ledgerPath, keyPath)
	whole, err := ledger.ReadAll(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}

	was := rewrapWith
	t.Cleanup(func() { rewrapWith = was })
	rewrapWith = func(ledgerPath string, kp *crypto.KeyPair, outPath string, replay func(string) error, checks ledger.RewrapChecks) (ledger.Rewrapped, error) {
		done, err := was(ledgerPath, kp, outPath, replay, checks)
		if err != nil {
			return done, err
		}
		return done, errors.New("the directory would not sync")
	}
	var stdout, stderr strings.Builder
	if code := run([]string{"-ledger", ledgerPath, "-key", keyPath, "-platform-pubkey", platformPath}, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "the record is published at "+ledgerPath) || !strings.Contains(stderr.String(), "the directory would not sync") {
		t.Fatalf("published and then failed: run = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if left := segmentsBeside(t, source); len(left) != 0 {
		t.Errorf("the earlier record's segments still stand beside the published one: %v", left)
	}
	if _, err := os.Stat(witness.TailPath(source)); !os.IsNotExist(err) {
		t.Errorf("the earlier record's witness tail still stands beside the published one (stat: %v)", err)
	}
	if !strings.Contains(stdout.String(), "kept 1 earlier witness receipt(s) in place as footnotes") {
		t.Errorf("the operator is not told what became of the witness's receipt:\n%s", stdout.String())
	}
	if err := heldAsABootHoldsIt(t, ledgerPath, platformPath); err != nil {
		t.Fatalf("a boot of what was left: %v", err)
	}

	rewrapWith = was
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-ledger", ledgerPath, "-key", keyPath, "-platform-pubkey", platformPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("the next run: %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if got, _, err := genesis.VerifySelfContained(ledgerPath); err != nil || got != len(whole) {
		t.Fatalf("the record holds %d of %d records, verified: %v", got, len(whole), err)
	}
}

// A RE-WRAP KILLED BETWEEN PUBLISHING AND RETIRING IS FINISHED BY RUNNING
// IT AGAIN. What the kill leaves is what the ledger package leaves when
// it returns: the new tail, from record 1, beside the earlier record's
// segment and witness tail. A boot refuses that as a tail conflict and
// says the way out; with -out the tool refuses it and says the same; and
// run in place it proves the state, retires the segment and the tail,
// says so, and re-wraps the record as any other.
func TestRunFinishesARewrapThatWasStoppedBeforeItRetiredTheSegments(t *testing.T) {
	source := t.TempDir()
	ledgerPath, keyPath := born(t, source)
	platformPath := sealed(t, ledgerPath, keyPath)
	whole, err := ledger.ReadAll(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	platform, err := witness.LoadPlatformEnvelope(platformPath)
	if err != nil {
		t.Fatal(err)
	}
	heads, err := witness.LoadHeadVerifier(source, platform)
	if err != nil {
		t.Fatal(err)
	}
	// The first run, as far as its publication and no further.
	if _, err := ledger.RewrapWith(ledgerPath, kp, "", func(string) error { return nil }, ledger.RewrapChecks{Heads: heads}); err != nil {
		t.Fatal(err)
	}
	if left := segmentsBeside(t, source); len(left) != 1 {
		t.Fatalf("fixture: segments beside the published tail: %v", left)
	}

	err = heldAsABootHoldsIt(t, ledgerPath, platformPath)
	if !errors.Is(err, ledger.ErrTailConflict) || !strings.Contains(err.Error(), "run rewrap on this ledger again") {
		t.Fatalf("a boot of a new tail beside the old segment must refuse it as a tail conflict and say the way out: %v", err)
	}

	var stdout, stderr strings.Builder
	elsewhere := filepath.Join(t.TempDir(), "rewrapped.jsonl")
	if code := run([]string{"-ledger", ledgerPath, "-key", keyPath, "-platform-pubkey", platformPath, "-out", elsewhere}, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "run rewrap on this ledger in place") {
		t.Fatalf("with -out: run = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if left := segmentsBeside(t, source); len(left) != 1 {
		t.Fatalf("a run with -out moved the source's segments: %v", left)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-ledger", ledgerPath, "-key", keyPath, "-platform-pubkey", platformPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("in place: run = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "an earlier re-wrap of this ledger published its tail and was stopped before it retired the record it replaced; proved, and finished now: ") {
		t.Errorf("the operator is not told first that an earlier re-wrap was finished:\n%s", stdout.String())
	}
	if left := segmentsBeside(t, source); len(left) != 0 {
		t.Errorf("the earlier record's segment still stands: %v", left)
	}
	if retired, _ := filepath.Glob(filepath.Join(source, "segment-*.jsonl.gz.retired-*")); len(retired) != 1 {
		t.Errorf("the segment was not retired beside the ledger: %v", retired)
	}
	if _, err := os.Stat(witness.TailPath(source)); !os.IsNotExist(err) {
		t.Errorf("the earlier record's witness tail still stands (stat: %v)", err)
	}
	if !strings.Contains(stdout.String(), "kept 1 earlier witness receipt(s) in place as footnotes") {
		t.Errorf("the operator is not told what became of the witness's receipt:\n%s", stdout.String())
	}
	if err := heldAsABootHoldsIt(t, ledgerPath, platformPath); err != nil {
		t.Fatalf("a boot of the finished record: %v", err)
	}
	if got, _, err := genesis.VerifySelfContained(ledgerPath); err != nil || got != len(whole) {
		t.Fatalf("the record holds %d of %d records, verified: %v", got, len(whole), err)
	}
}

// A WITNESS RECEIPT PENDING BESIDE THE LEDGER STOPS A RE-WRAP IN PLACE.
// The file names a hash of the record as it stands; a re-wrap in place
// changes every hash, so the receipt could never be minted after it.
// Until the operator rules what a re-wrap does with it, the run refuses
// before anything is read or replaced, and says how it is settled. With
// -out the record beside it is untouched, and the run goes on.
func TestRunInPlaceRefusesWhileAWitnessReceiptIsPending(t *testing.T) {
	source := t.TempDir()
	ledgerPath, keyPath := born(t, source)
	platformPath := sealed(t, ledgerPath, keyPath)
	before, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	pending := witness.PendingReceiptPath(source)
	if err := os.WriteFile(pending, []byte(`{"identity_id":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	if code := run([]string{"-ledger", ledgerPath, "-key", keyPath, "-platform-pubkey", platformPath}, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), pending+" stands beside the ledger") || !strings.Contains(stderr.String(), "nothing replaced") {
		t.Fatalf("in place with a pending receipt: run = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if after, _ := os.ReadFile(ledgerPath); !bytes.Equal(before, after) || len(segmentsBeside(t, source)) == 0 {
		t.Fatal("a refused re-wrap changed the ledger or moved its segments")
	}
	if _, err := os.Stat(pending); err != nil {
		t.Fatalf("a refused re-wrap moved the pending receipt: %v", err)
	}

	out := filepath.Join(t.TempDir(), "ledger.jsonl")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-ledger", ledgerPath, "-key", keyPath, "-out", out, "-platform-pubkey", platformPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("with -out the record beside the receipt is untouched and the run goes on: %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}
