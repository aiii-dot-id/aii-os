//go:build windows

package pluginhost

import (
	"crypto/sha256"
	"encoding/hex"
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
// .
func TestTheWindowsBindingIsLockedBeforeTheDigestIsTaken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.exe")
	body := []byte("the artifact")
	if err := os.WriteFile(path, body, 0o700); err != nil {
		t.Fatal(err)
	}

	img, err := bindImage(path)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	defer img.Close()

	// .
	// .
	if f, err := os.OpenFile(path, os.O_WRONLY, 0o600); err == nil {
		f.Close()
		t.Fatal("the artifact is still writable after binding — the share lock is not held, so a swap could land between bind and verify")
	}
	staging := filepath.Join(dir, "staging.exe")
	if err := os.WriteFile(staging, []byte("attacker"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, path); err == nil {
		t.Fatal("the artifact could be replaced while bound — the delete/rename share is not denied")
	}

	// .
	// .
	sum := sha256.Sum256(body)
	want := "sha256:" + hex.EncodeToString(sum[:])
	got, err := digestOfHandle(img.handle())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("digest through the locked handle = %s, want %s", got, want)
	}
}
