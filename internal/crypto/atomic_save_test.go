package crypto

import (
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestSaveKeyPairAtomicRoundTrip(t *testing.T) {
	dir := t.TempDir()
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "identity.sec")
	if published, err := SaveKeyPair(kp, path); err != nil || !published {
		t.Fatal(err)
	}

	loaded, err := LoadKeyPair(path)
	if err != nil {
		t.Fatalf("saved key must load: %v", err)
	}
	if loaded.Fingerprint() != kp.Fingerprint() {
		t.Fatal("fingerprint changed across save/load")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("temp residue left beside the key: %s", e.Name())
		}
	}

	restricted, err := fileperm.IsRestrictedToOwner(path)
	if err != nil {
		t.Fatal(err)
	}
	if !restricted {
		t.Fatal("the identity private key is readable beyond its owner")
	}
}

func TestSaveKeyPairRefusesExistingIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "identity.sec")
	first, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if published, err := SaveKeyPair(first, path); err != nil || !published {
		t.Fatal(err)
	}
	if published, err := SaveKeyPair(second, path); err == nil || published {
		t.Fatal("existing identity key was replaced")
	}
	loaded, err := LoadKeyPair(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Fingerprint() != first.Fingerprint() {
		t.Fatal("refused save changed the existing identity")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "identity.sec" {
		t.Fatalf("refused save left artifacts: %v", entries)
	}
}

func TestLoadKeyPairRejectsOversizeLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.sec")
	if err := os.WriteFile(path, []byte{0xff, 0xff, 0xff, 0xff}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyPair(path); err == nil {
		t.Fatal("oversize private-key length was accepted")
	}
}
