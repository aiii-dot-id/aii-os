//go:build !windows

package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

// The PUBLIC entry points were untested. The one test this package had
// drove the internal replace() with an injected sync, which proves the
// sequencing and nothing about the doors callers actually use — and
// these are the doors that stand between a crash and a torn ledger.

// PublishNew gives a prepared file its real name without replacing
// anything. Publication and removal of the temporary name become durable in
// one directory sync, using the platform's supported no-replace primitive.
func TestPublishNewGivesAPreparedFileItsName(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "ledger.jsonl.tmp")
	final := filepath.Join(dir, "ledger.jsonl")
	if err := os.WriteFile(tmp, []byte("seq 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	published, err := PublishNew(tmp, final)
	if err != nil {
		t.Fatalf("PublishNew: %v", err)
	}
	if !published {
		t.Fatal("PublishNew reported nothing was published after it succeeded")
	}
	got, err := os.ReadFile(final)
	if err != nil {
		t.Fatalf("the published name does not exist: %v", err)
	}
	if string(got) != "seq 1\n" {
		t.Fatalf("published content = %q", got)
	}
	// THE TEMPORARY NAME MUST BE GONE. A leftover .tmp beside a real
	// ledger is the shape a later reader mistakes for a crash.
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("the temporary name survived publication: %v", err)
	}
}

// PUBLISHED IS THE CALLER'S INSTRUCTION, NOT A STATUS LINE. False means
// nothing moved and the old state stands; true means the new name is
// live and the caller must follow it even though an error came back.
// Getting this backwards is how a caller retries over a file that is
// already published.
func TestPublishNewSaysNothingWasPublishedWhenDestinationExists(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "prepared")
	final := filepath.Join(dir, "final")
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A name that is already taken: publication refuses rather than replacing,
	// which is the whole difference between PublishNew and Replace.
	if err := os.WriteFile(final, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}

	published, err := PublishNew(tmp, final)
	if err == nil {
		t.Fatal("PublishNew silently replaced a file that already existed")
	}
	if published {
		t.Fatal("PublishNew reported a publication that did not happen — a caller would follow a name it does not own")
	}
	got, _ := os.ReadFile(final)
	if string(got) != "occupied" {
		t.Fatalf("the existing file was disturbed: %q", got)
	}
}

// Replace publishes over an existing name and syncs the directory.
func TestReplacePublishesOverAnExistingName(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "next")
	final := filepath.Join(dir, "live")
	if err := os.WriteFile(final, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	published, err := Replace(tmp, final)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if !published {
		t.Fatal("Replace reported nothing was published after it succeeded")
	}
	got, _ := os.ReadFile(final)
	if string(got) != "new" {
		t.Fatalf("live content = %q, want the replacement", got)
	}
}

// A DIRECTORY THAT CANNOT BE OPENED IS A PRE-PUBLICATION FAILURE. The
// rename has not happened, so the caller must NOT follow the new name —
// this is the false that keeps a caller from reading a file that was
// never written.
func TestReplaceReportsNoPublicationWhenTheDirectoryIsUnopenable(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "prepared")
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A parent that does not exist cannot be opened or synced.
	final := filepath.Join(dir, "no-such-dir", "live")

	published, err := Replace(tmp, final)
	if err == nil {
		t.Fatal("Replace succeeded into a directory that does not exist")
	}
	if published {
		t.Fatal("Replace reported a publication before the rename could have happened")
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Fatalf("the prepared file was lost on a failure that published nothing: %v", err)
	}
}
