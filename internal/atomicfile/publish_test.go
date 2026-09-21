//go:build !windows

package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .

// .
// .
// .
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
	// .
	// .
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("the temporary name survived publication: %v", err)
	}
}

// .
// .
// .
// .
// .
func TestPublishNewSaysNothingWasPublishedWhenDestinationExists(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "prepared")
	final := filepath.Join(dir, "final")
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// .
	// .
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

// .
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

// .
// .
// .
// .
func TestReplaceReportsNoPublicationWhenTheDirectoryIsUnopenable(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "prepared")
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// .
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
