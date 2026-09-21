package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
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
type passphraseReader func(prompt string) ([]byte, error)

// .
// .
func terminalPassphrase(stderr io.Writer) passphraseReader {
	return func(prompt string) ([]byte, error) {
		fd := int(os.Stdin.Fd())
		if !term.IsTerminal(fd) {
			return nil, errors.New("no terminal: the passphrase is typed, never piped, passed or stored")
		}
		fmt.Fprint(stderr, prompt)
		pass, err := term.ReadPassword(fd)
		fmt.Fprintln(stderr)
		return pass, err
	}
}

const escrowUsage = `usage:
  aii escrow create  -out <escrow.age> [-dir <identity dir>] [-key <identity.sec>] [-snapshot-key <snapshot.key>]
  aii escrow check   -in <escrow.age> (-ledger <ledger.jsonl> | -fingerprint <hex>) [-dir <identity dir>]
  aii escrow restore -in <escrow.age> (-ledger <ledger.jsonl> | -fingerprint <hex>) [-dir <identity dir>]

Seals the identity's key file — and its snapshot key, when it has one — under
a passphrase, in age's own format: 'age -d escrow.age | tar x' opens it with
stock tools. Keep the file AND the passphrase somewhere that survives the loss
of this machine; one without the other restores nothing.

check proves an escrow without writing a secret anywhere: it must open, hold
THIS identity's key (say which with -ledger or -fingerprint — a check against
nothing proves only that the file decrypts), and the key must sign. On the host
it covers — this identity's key, and the SAME snapshot key — it leaves a receipt
beside the identity key; anywhere else it says why it did not.
restore does the same and then puts back whichever of the two files is missing.
It writes over nothing: a key already in place is left alone when it is this
identity's (or the same snapshot key), and refused otherwise.
`

var fingerprintRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

func runEscrow(args []string, stdout, stderr io.Writer, readPass passphraseReader) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, escrowUsage)
		return 2
	}
	fs := flag.NewFlagSet("escrow "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, escrowUsage) }
	dir := fs.String("dir", "", "identity install directory (default: the current directory)")
	keyPath := fs.String("key", "", "identity key file (default: <dir>/data/identity.sec; with a custom identity.key_path, name it here)")
	snapPath := fs.String("snapshot-key", "", "snapshot key file (default: beside the identity key)")
	in := fs.String("in", "", "escrow file to open")
	out := fs.String("out", "", "escrow file to create; must not exist")
	ledgerPath := fs.String("ledger", "", "a ledger that verifies on its own: the escrow must hold ITS identity's key")
	wantFP := fs.String("fingerprint", "", "the identity fingerprint you retained: the escrow must hold that key")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "escrow: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if *keyPath == "" {
		*keyPath = filepath.Join(*dir, "data", "identity.sec")
	}
	// .
	// .
	// .
	if *snapPath == "" {
		*snapPath = escrow.SnapshotKeyPath(*keyPath)
	}
	switch args[0] {
	case "create":
		if *out == "" {
			fmt.Fprint(stderr, escrowUsage)
			return 2
		}
		return escrowCreate(*keyPath, *snapPath, *out, stdout, stderr, readPass)
	case "check", "restore":
		if *in == "" || (*ledgerPath == "") == (*wantFP == "") {
			fmt.Fprintln(stderr, "escrow: name the escrow with -in, and the identity it must hold with exactly one of -ledger or -fingerprint")
			return 2
		}
		return escrowOpen(args[0] == "restore", *in, *ledgerPath, *wantFP, *dir, *keyPath, *snapPath, *ledgerPath == "", stdout, stderr, readPass)
	default:
		fmt.Fprint(stderr, escrowUsage)
		return 2
	}
}

func escrowCreate(keyPath, snapPath, out string, stdout, stderr io.Writer, readPass passphraseReader) int {
	if _, err := os.Stat(out); err == nil {
		fmt.Fprintf(stderr, "NOT CREATED: %s already exists — an escrow is never written over\n", out)
		return 1
	}
	var c escrow.Contents
	defer c.Wipe()
	var err error
	if c.IdentityKey, err = os.ReadFile(keyPath); err != nil {
		fmt.Fprintf(stderr, "NOT CREATED: %v\n", err)
		return 1
	}
	// .
	// .
	// .
	if c.SnapshotKey, err = os.ReadFile(snapPath); errors.Is(err, os.ErrNotExist) {
		c.SnapshotKey = nil
	} else if err != nil {
		fmt.Fprintf(stderr, "NOT CREATED: %v\n", err)
		return 1
	}
	pass, err := readPass("Passphrase for the new escrow: ")
	if err != nil {
		fmt.Fprintf(stderr, "NOT CREATED: %v\n", err)
		return 1
	}
	defer escrow.Wipe(pass)
	again, err := readPass("Again: ")
	if err != nil {
		fmt.Fprintf(stderr, "NOT CREATED: %v\n", err)
		return 1
	}
	defer escrow.Wipe(again)
	if !bytes.Equal(pass, again) {
		fmt.Fprintln(stderr, "NOT CREATED: the two passphrases differ")
		return 1
	}
	sealed, err := escrow.Seal(c, pass)
	if err != nil {
		fmt.Fprintf(stderr, "NOT CREATED: %v\n", err)
		return 1
	}
	if published, err := escrow.PublishSecret(sealed, out); err != nil && !published {
		fmt.Fprintf(stderr, "NOT CREATED: %v\n", err)
		return 1
	} else if err != nil {
		fmt.Fprintf(stderr, "created, with a warning: %v\n", err)
	}
	kp, _ := crypto.ParseKeyPair(c.IdentityKey)
	holds := "the identity key"
	if c.SnapshotKey != nil {
		holds = "the identity key and the snapshot key"
	}
	fmt.Fprintf(stdout, "CREATED: %s holds %s of identity %s.\nIt is not proved until it is checked: run `aii escrow check -in %s -fingerprint %s`.\nKeep the file and the passphrase where the loss of this machine cannot reach them.\n",
		out, holds, kp.Fingerprint(), out, kp.Fingerprint())
	return 0
}

func escrowOpen(restore bool, in, ledgerPath, wantFP, dir, keyPath, snapPath string, byFingerprint bool, stdout, stderr io.Writer, readPass passphraseReader) int {
	verb := "NOT CHECKED"
	if restore {
		verb = "NOT RESTORED"
	}
	refuse := func(format string, a ...any) int {
		fmt.Fprintf(stderr, verb+": "+format+"\n", a...)
		return 1
	}
	// .
	if ledgerPath != "" {
		fp, err := ledgerIdentity(ledgerPath)
		if err != nil {
			return refuse("%v", err)
		}
		wantFP = fp
	} else if !fingerprintRe.MatchString(wantFP) {
		return refuse("-fingerprint is 64 lowercase hex characters")
	}
	haveKey := false
	if restore {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if here, err := crypto.LoadKeyPair(keyPath); err == nil {
			if here.Fingerprint() != wantFP {
				return refuse("%s holds identity %s, not %s — a key is never written over; move it aside first", keyPath, here.Fingerprint(), wantFP)
			}
			haveKey = true
		} else if _, statErr := os.Stat(keyPath); statErr == nil {
			return refuse("%s exists and is not a key this tool can read (%v) — a key is never written over; move it aside first", keyPath, err)
		}
		// .
		// .
		// .
		if home := filepath.Join(dir, "data", "ledger.jsonl"); byFingerprint {
			if _, err := os.Stat(home); err == nil {
				if fp, err := ledgerIdentity(home); err != nil {
					fmt.Fprintf(stderr, "note: %s is here and does not verify (%v) — going by the fingerprint you gave\n", home, err)
				} else if fp != wantFP {
					return refuse("the ledger in this home, %s, is identity %s — not %s", home, fp, wantFP)
				}
			}
		}
	}
	// .
	// .
	// .
	hostRecipient, hostSnapErr := "", error(nil)
	if raw, err := os.ReadFile(snapPath); err == nil {
		hostRecipient, hostSnapErr = escrow.SnapshotRecipient(raw)
		escrow.Wipe(raw)
	} else if !errors.Is(err, os.ErrNotExist) {
		hostSnapErr = err
	}
	if restore && hostSnapErr != nil {
		return refuse("%s exists and does not read as a snapshot key (%v) — a key is never written over; move it aside first", snapPath, hostSnapErr)
	}
	// .
	// .
	f, err := os.Open(in)
	if err != nil {
		return refuse("%v", err)
	}
	sealed, err := io.ReadAll(io.LimitReader(f, escrow.MaxSealedBytes+1))
	f.Close()
	if err != nil {
		return refuse("%v", err)
	}
	if len(sealed) > escrow.MaxSealedBytes {
		return refuse("%v: more than %d bytes", escrow.ErrTooLarge, escrow.MaxSealedBytes)
	}
	pass, err := readPass("Passphrase: ")
	if err != nil {
		return refuse("%v", err)
	}
	defer escrow.Wipe(pass)
	c, kp, recipient, err := escrow.OpenFor(sealed, pass, wantFP)
	if err != nil {
		return refuse("%v", err)
	}
	defer c.Wipe()
	if restore {
		return escrowRestore(c, kp, recipient, hostRecipient, haveKey, keyPath, snapPath, stdout, stderr, refuse)
	}

	fmt.Fprintf(stdout, "CHECKED: %s opens, holds identity %s, and the key signs and verifies.\n", in, kp.Fingerprint())
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if recipient == "" {
		fmt.Fprintln(stdout, "It holds no snapshot key, so there is nothing for a receipt to attest.")
		return 0
	}
	here, err := crypto.LoadKeyPair(keyPath)
	if err != nil || here.Fingerprint() != kp.Fingerprint() {
		fmt.Fprintf(stdout, "No receipt written: %s is not this identity's key, so this is not the host whose snapshots the escrow covers.\n", keyPath)
		return 0
	}
	var nc *escrow.NotCovered
	if err := escrow.Covers(recipient, hostRecipient, hostSnapErr); errors.As(err, &nc) {
		switch nc.Why {
		case escrow.HostSnapshotKeyUnreadable:
			fmt.Fprintf(stderr, "CHECKED, AND NOT COVERED: the snapshot key on this host (%s) does not read: %v — no receipt written\n", snapPath, hostSnapErr)
		case escrow.NoSnapshotKeyOnHost:
			fmt.Fprintf(stderr, "CHECKED, AND NOT COVERED: this host has no snapshot key at %s, so its snapshots are not encrypted to the one this escrow holds — put it back with `aii escrow restore`, or start the identity (it makes one) and make a new escrow. No receipt written.\n", snapPath)
		default:
			fmt.Fprintf(stderr, "CHECKED, AND NOT COVERED: the snapshot key on this host is NOT the one this escrow holds (here %s, in the escrow %s) — snapshots made here could not be opened from this escrow. Make a new escrow with `aii escrow create`. No receipt written.\n", hostRecipient, recipient)
		}
		return 1
	}
	r, published, err := escrow.WriteReceipt(kp, recipient, keyPath, time.Now())
	receiptPath := escrow.ReceiptPath(keyPath)
	if err != nil && !published {
		// .
		fmt.Fprintf(stderr, "CHECKED, AND NO RECEIPT: it could not be written: %v — the daily pass goes on as if no escrow had been checked\n", err)
		return 1
	} else if err != nil {
		fmt.Fprintf(stderr, "receipt written, with a warning: %v\n", err)
	}
	_ = r
	fmt.Fprintf(stdout, "Receipt written: %s — this identity, snapshot recipient %s.\n", receiptPath, recipient)
	return 0
}

// .
func ledgerIdentity(ledgerPath string) (string, error) {
	heads, err := witness.LoadHeadVerifier(filepath.Dir(ledgerPath), genesis.PinnedRoot())
	if err != nil {
		return "", fmt.Errorf("witness keys beside the ledger: %w", err)
	}
	_, fp, err := genesis.VerifySelfContainedWith(ledgerPath, heads)
	if err != nil {
		return "", fmt.Errorf("the ledger does not verify, so it names no identity: %w", err)
	}
	return fp, nil
}

// .
// .
func escrowRestore(c escrow.Contents, kp *crypto.KeyPair, recipient, hostRecipient string, haveKey bool, keyPath, snapPath string, stdout, stderr io.Writer, refuse func(string, ...any) int) int {
	needSnap := false
	switch {
	case c.SnapshotKey == nil:
		// .
	case hostRecipient == "":
		needSnap = true
	case hostRecipient != recipient:
		return refuse("%s is a DIFFERENT snapshot key from the one this escrow holds — a key is never written over. To open snapshots made with the escrowed key, restore it elsewhere (-snapshot-key <path>) and give `aii snapshot open` that path", snapPath)
	}
	if haveKey && !needSnap {
		fmt.Fprintf(stdout, "NOTHING TO RESTORE: what this escrow holds is already in place for identity %s, and matches.\n", kp.Fingerprint())
		return 0
	}
	for _, p := range []string{keyPath, snapPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return refuse("%v", err)
		}
	}
	var did, warned []string
	if !haveKey {
		published, err := crypto.PublishKeyFile(c.IdentityKey, keyPath)
		if err != nil && !published {
			return refuse("%v", err)
		}
		if err != nil {
			warned = append(warned, err.Error())
		}
		did = append(did, keyPath)
	}
	if needSnap {
		published, err := escrow.PublishSecret(c.SnapshotKey, snapPath)
		if err != nil && !published {
			fmt.Fprintf(stderr, "RESTORED IN PART: %s is in place and the snapshot key is NOT: %v — run the restore again once that is put right; what is in place is left alone\n", keyPath, err)
			return 1
		}
		if err != nil {
			warned = append(warned, err.Error())
		}
		did = append(did, snapPath)
	}
	for _, w := range warned {
		fmt.Fprintf(stderr, "restored, with a warning: %s\n", w)
	}
	already := ""
	if haveKey {
		already = " (" + keyPath + " was already in place, and is this identity's)"
	}
	fmt.Fprintf(stdout, "RESTORED: %s — identity %s; the key signs and verifies.%s\n", strings.Join(did, " and "), kp.Fingerprint(), already)
	return 0
}
