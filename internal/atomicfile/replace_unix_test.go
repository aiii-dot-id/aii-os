//go:build !windows

package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestTheNamesMoveIsDurableBeforeTheEarlierFileIsEmptied(t *testing.T) {
	t.Run("a staged file, with the swap where it is made", func(t *testing.T) {
		theNamesMoveIsDurableBeforeTheEarlierFileIsEmptied(t, replaceStagedWith)
	})
	t.Run("a staged file, without a swap", func(t *testing.T) {
		noSwap(t)
		theNamesMoveIsDurableBeforeTheEarlierFileIsEmptied(t, replaceStagedWith)
	})
	t.Run("a named file, by the rename", func(t *testing.T) {
		theNamesMoveIsDurableBeforeTheEarlierFileIsEmptied(t, replace)
	})
}

func theNamesMoveIsDurableBeforeTheEarlierFileIsEmptied(t *testing.T, replace func(oldPath, newPath string, syncDir func() error) (bool, error)) {
	dir := t.TempDir()
	source := filepath.Join(dir, "candidate")
	target := filepath.Join(dir, "current")
	other := filepath.Join(dir, "elsewhere")
	setUp := func() {
		t.Helper()
		os.Remove(other)
		if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("THE EARLIER SECRET"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(target, other); err != nil {
			t.Skipf("hard links unavailable here: %v", err)
		}
	}
	setUp()
	var atSync []string
	published, err := replace(source, target, func() error {
		got, _ := os.ReadFile(other)
		atSync = append(atSync, string(got))
		return nil
	})
	if err != nil || !published {
		t.Fatalf("replace: published=%v err=%v", published, err)
	}
	if len(atSync) == 0 || atSync[0] != "THE EARLIER SECRET" {
		t.Errorf("when the directory is synced after the move the earlier file is not emptied yet: at each sync it held %q", atSync)
	}
	if got, _ := os.ReadFile(other); len(got) != 0 {
		t.Errorf("after the sync the earlier file is emptied: %q", got)
	}

	setUp()
	failed := errors.New("directory sync failed")
	published, err = replace(source, target, func() error { return failed })
	if !published || !errors.Is(err, failed) || !strings.Contains(err.Error(), "was not emptied") {
		t.Errorf("a sync that fails: published, its error, and that the earlier file was not emptied: published=%v err=%v", published, err)
	}
	if got, _ := os.ReadFile(other); string(got) != "THE EARLIER SECRET" {
		t.Errorf("a sync that fails leaves the earlier file as it is: %q", got)
	}
}
