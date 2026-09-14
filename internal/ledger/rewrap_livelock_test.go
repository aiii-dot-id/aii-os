package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// .
// .
// .
func TestRewrapOutputRefusesADanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.jsonl")
	if err := os.WriteFile(src, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	link := filepath.Join(dir, "out.jsonl")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := lockRewrapOutput(link, source)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a dangling symlink was accepted as the output ledger")
		}
		if !strings.Contains(err.Error(), "dangling symlink") {
			t.Fatalf("refused for the wrong reason: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lockRewrapOutput is spinning on a dangling symlink — the livelock")
	}
}
