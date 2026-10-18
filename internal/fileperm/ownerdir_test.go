package fileperm

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestAnOwnerOnlyDirIsMadeNewAndNeverAdopted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "home", ".aii", "identity-0")
	if err := MkdirOwnerOnly(dir); err != nil {
		t.Fatalf("MkdirOwnerOnly under missing parents: %v", err)
	}
	if ok, err := IsOwnerOnlyDir(dir); err != nil || !ok {
		t.Fatalf("IsOwnerOnlyDir(%s) = %v, %v; want true", dir, ok, err)
	}

	ledger := filepath.Join(dir, "ledger.jsonl")
	if err := os.WriteFile(ledger, []byte("{\"seq\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MkdirOwnerOnly(dir); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("MkdirOwnerOnly over an existing directory = %v; want fs.ErrExist", err)
	}
	if b, err := os.ReadFile(ledger); err != nil || string(b) != "{\"seq\":1}\n" {
		t.Fatalf("a refused directory must be left as it was: %q, %v", b, err)
	}
}
