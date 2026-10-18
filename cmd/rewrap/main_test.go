package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestRunRefusesWithoutBothPathsBeforeTouchingAnything(t *testing.T) {
	for _, args := range [][]string{nil, {"-ledger", "x.jsonl"}, {"-key", "identity.sec"}} {
		var stdout, stderr strings.Builder
		if code := run(args, &stdout, &stderr); code != 2 || stdout.Len() != 0 ||
			stderr.String() != "usage: rewrap -ledger ledger.jsonl -key identity.sec [-out new.jsonl] [-db aii.db]\n" {
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

// WITH -out, THE SEGMENTS THE OPERATOR IS TOLD OF ARE THE SOURCE'S. The
// new record is one tail; sealed segments beside the old ledger belong to
// the record it re-signed, and the operator who installs the new tail
// must leave them behind. The notice says "beside the source ledger", and
// the wrapper looked beside the output: written to another directory, it
// found none there and said nothing.
//
// A segment's name is all the wrapper reads of it, so a stand-in file
// serves here: the re-wrap reads the source's tail, and its verification
// reads the candidate in the output's directory, where no segment stands.
// (In place a stand-in is read by that verification and refuses the
// re-wrap, which is why test/rewrap_cmd_test.go pins no segment line.)
func TestRunWithOutNamesTheSegmentsBesideTheSourceLedger(t *testing.T) {
	source := t.TempDir()
	ledgerPath, keyPath := born(t, source)
	segment := filepath.Join(source, "segment-1-2.jsonl.gz")
	if err := os.WriteFile(segment, []byte("a stand-in: only its name is read"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "rewrapped.jsonl")

	var stdout, stderr strings.Builder
	if code := run([]string{"-ledger", ledgerPath, "-key", keyPath, "-out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("run = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	const notice = "1 sealed segment(s) beside the source ledger belong to the earlier record — install the new tail without them; the first bookmark seals the new record\n"
	if !strings.Contains(stdout.String(), notice) {
		t.Errorf("the operator is not told of the segment beside the source ledger:\n%s", stdout.String())
	}
	// Written elsewhere, nothing beside the source is touched: the old
	// record is still whole where it stands.
	if _, err := os.Stat(segment); err != nil {
		t.Errorf("the source's segment was moved by a re-wrap written elsewhere: %v", err)
	}
	if strings.Contains(stdout.String(), "retired ") {
		t.Errorf("a re-wrap written elsewhere retired something beside the source:\n%s", stdout.String())
	}
}

// witnessed gives a born ledger one witnessed record that still carries its
// receipt — what makes a re-wrap handle the witness tail at all — and puts
// a tail beside it. The wrapper reads only the tail's name, so a stand-in
// file serves.
func witnessed(t *testing.T, ledgerPath, keyPath string) (tail string) {
	t.Helper()
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(ledger.EventSystemWitnessed, 0, map[string]any{
		"receipt": map[string]any{"ledger_ordinal": 1, "ledger_hash": lg.LastHash(), "witness_key_id": "k", "witness_sig_b64": "AAAA"},
	}, kp); err != nil {
		t.Fatal(err)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	tail = filepath.Join(filepath.Dir(ledgerPath), "witness-tail.json")
	if err := os.WriteFile(tail, []byte("a stand-in: only its name is read"), 0o600); err != nil {
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
