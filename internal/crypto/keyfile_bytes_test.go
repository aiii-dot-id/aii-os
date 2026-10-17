package crypto

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/fileperm"
)

// ONE DECODER AND ONE PUBLISHER, for the disk and for an escrow. A restore
// publishes the exact bytes that were sealed, so the publisher takes bytes
// — and it holds them to the key file's own decoder first: what is not a
// key, or is a key whose public half is not its private half's, is never
// put where an identity will load it.
func TestTheKeyFilePublisherPublishesOnlyAKeyAndNeverOverOne(t *testing.T) {
	dir := t.TempDir()
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(dir, "first.sec")
	if _, err := SaveKeyPair(kp, first); err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if parsed, err := ParseKeyPair(good); err != nil || parsed.Fingerprint() != kp.Fingerprint() {
		t.Fatalf("the bytes form of the decoder disagrees with the file form: %v", err)
	}

	swapped := bytes.Clone(good)
	swapped[len(swapped)-1] ^= 0x01
	for name, data := range map[string][]byte{"not a key": []byte("not a key file"), "empty": nil, "a swapped public half": swapped} {
		path := filepath.Join(dir, "refused.sec")
		if published, err := PublishKeyFile(data, path); err == nil || published {
			t.Fatalf("%s was published as a key file", name)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s: a refused publish left a file", name)
		}
	}

	restored := filepath.Join(dir, "restored.sec")
	if published, err := PublishKeyFile(good, restored); err != nil || !published {
		t.Fatalf("publish: %v", err)
	}
	got, _ := os.ReadFile(restored)
	if !bytes.Equal(got, good) {
		t.Fatal("the published bytes are not the bytes that were given")
	}
	if ok, err := fileperm.IsRestrictedToOwner(restored); err != nil || !ok {
		t.Fatalf("the key was published readable by others: %v", err)
	}
	other, _ := GenerateKeyPair()
	if published, err := SaveKeyPair(other, restored); err == nil || published {
		t.Fatal("A KEY WAS WRITTEN OVER")
	}
	if now, _ := os.ReadFile(restored); !bytes.Equal(now, good) {
		t.Fatal("the existing key was changed")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("temp debris was left beside the keys: %v", entries)
	}
}
