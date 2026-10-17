//go:build windows

package main

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// THE HOST HOLDS ITS LEDGER OPEN, AND THE OPERATOR STILL READS IT. On
// Windows a file's share mode is mandatory: an open that does not share
// what another handle holds is refused, and a byte range another handle
// has locked cannot be read. The host opens its ledger through
// atomicfile.OpenAppendRemovable (append and read, sharing read, write and
// delete) and locks it at a sentinel offset beyond any byte of the file
// (filelock); the reader opens the tail through atomicfile.Open (read,
// sharing all three) and each segment through os.Open (read, sharing read
// and write). This holds the ledger open as a running host does, by the
// handle its birth opened, appends through it, and reads; then the host
// seals, which replaces the tail under its name, to show the reading left
// nothing in its way, and the ledger is read again.
func TestLedgerShowReadsTheLedgerTheHostHoldsOpen(t *testing.T) {
	f := newShowFixture(t, true)
	lg := f.born.Ledger
	appended, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]any{"id": "held", "content": "written while held open"}, f.born.KeyPair)
	if err != nil {
		t.Fatal(err)
	}
	read := func(want uint64) {
		t.Helper()
		code, out, errOut := runShow(t, trustHeads, "-dir", f.dir)
		if code != 0 || errOut != "" {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
		rows := showRows(t, out)
		if uint64(len(rows)) != want {
			t.Fatalf("printed %d rows, want %d:\n%s", len(rows), want, out)
		}
		for i, r := range rows {
			if r.seq != uint64(i+1) || r.unverified {
				t.Fatalf("row %d is %+v", i, r)
			}
		}
		if !strings.Contains(out, "written while held open") {
			t.Fatal("the record appended through the host's handle is not printed")
		}
	}
	read(appended.Seq)

	head, err := lg.Append(ledger.EventSystemWitnessed, 0,
		map[string]any{"receipt": map[string]any{"ledger_ordinal": lg.LastSeq(), "ledger_hash": lg.LastHash()}}, f.born.KeyPair)
	if err != nil {
		t.Fatal(err)
	}
	if err := lg.Seal(head.Seq); err != nil {
		t.Fatalf("the host could not seal after the reading: %v", err)
	}
	read(head.Seq)
}
