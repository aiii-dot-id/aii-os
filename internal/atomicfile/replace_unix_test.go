//go:build !windows

package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceReportsPostPublicationSyncFailure(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "candidate")
	target := filepath.Join(dir, "current")
	if err := os.WriteFile(source, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}

	wantErr := errors.New("directory sync failed")
	published, err := replace(source, target, func() error { return wantErr })
	if !published || !errors.Is(err, wantErr) {
		t.Fatalf("published=%v err=%v, want published sync failure", published, err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("published bytes = %q, want new candidate", got)
	}
}
