package ledger

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// THE FIRST RECORD'S FILE MUST SURVIVE WITH ITS NAME. An append fsyncs the
// file's bytes; only a directory sync makes a newly created file's name
// durable. An open that finds the tail empty asks for one, after the file
// exists and before any record can be appended; an open of a tail that
// holds a record asks for none; and a directory that cannot be synced
// refuses the open, and the open after it asks again, because the file an
// earlier open left behind is no proof its name reached the disk.
func TestAnOpenOfAnEmptyTailSyncsItsDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)
	type call struct {
		dir       string
		tailThere bool
		tailBytes int64
	}
	var calls []call
	var fail error
	real := syncLedgerDir
	syncLedgerDir = func(d string) error {
		c := call{dir: d}
		if info, err := os.Stat(filepath.Join(d, "ledger.jsonl")); err == nil {
			c.tailThere, c.tailBytes = true, info.Size()
		}
		calls = append(calls, c)
		if fail != nil {
			return fail
		}
		return real(d)
	}
	defer func() { syncLedgerDir = real }()

	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].dir != dir || !calls[0].tailThere || calls[0].tailBytes != 0 {
		t.Fatalf("creating the tail must sync its directory once, with the file there and still empty: %+v", calls)
	}
	if _, err := l.Append(EventRing0Genesis, 0, map[string]any{"name": "TestIdentity", "pubkey": kp.PublicKeyB64()}, kp); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("an append must not sync the directory: %+v", calls)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// The tail holds a record now: opening it syncs nothing.
	calls = nil
	l, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("opening a tail that holds a record synced the directory %d time(s)", len(calls))
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// A birth is the same open under its own precondition.
	birthDir := t.TempDir()
	calls = nil
	l, err = NewForBirth(filepath.Join(birthDir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].dir != birthDir || !calls[0].tailThere {
		t.Fatalf("a birth's open must sync the new tail's directory: %+v", calls)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// A directory that cannot be synced refuses the open with its cause,
	// and the refused open leaves the place as it found it: a birth needs
	// an empty record, and an empty file left behind would refuse every
	// later birth there.
	failDir := t.TempDir()
	failPath := filepath.Join(failDir, "ledger.jsonl")
	fail = errors.New("the disk said no")
	if l, err := NewForBirth(failPath); err == nil || !errors.Is(err, fail) {
		if l != nil {
			_ = l.Close()
		}
		t.Fatalf("a failed directory sync must refuse the open with its cause: %v", err)
	}
	if _, err := os.Lstat(failPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the refused open must take away the empty file it created: %v", err)
	}
	fail = nil
	calls = nil
	l, err = NewForBirth(failPath)
	if err != nil {
		t.Fatalf("a birth after a refused one must find the place empty and not be locked out: %v", err)
	}
	if len(calls) != 1 || calls[0].dir != failDir {
		t.Fatalf("the open after a refused one must sync the directory itself: %+v", calls)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// An empty tail that was already there is not this open's to remove:
	// a refused open leaves it, and the next open syncs for it.
	heldDir := t.TempDir()
	heldPath := filepath.Join(heldDir, "ledger.jsonl")
	if err := os.WriteFile(heldPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fail = errors.New("the disk said no again")
	if l, err := New(heldPath); err == nil || !errors.Is(err, fail) {
		if l != nil {
			_ = l.Close()
		}
		t.Fatalf("a failed directory sync must refuse the open: %v", err)
	}
	if _, err := os.Lstat(heldPath); err != nil {
		t.Fatalf("an empty tail the open did not create must be left where it was: %v", err)
	}
	fail = nil
	calls = nil
	l, err = New(heldPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("an empty tail left by an earlier open is no proof its name is durable; the open must sync: %+v", calls)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}
