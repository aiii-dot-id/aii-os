package continuitytest

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
)

func EscrowedKey(sealed, passphrase []byte) (keyFile []byte, fingerprint string, err error) {
	c, kp, err := escrow.Open(sealed, passphrase)
	if err != nil {
		return nil, "", err
	}
	defer c.Wipe()

	keyFile, fingerprint = append([]byte(nil), c.IdentityKey...), kp.Fingerprint()
	return keyFile, fingerprint, nil
}

func SnapshotKey(t testing.TB, keyPath string) (key []byte, recipient string) {
	t.Helper()
	key, err := os.ReadFile(escrow.SnapshotKeyPath(keyPath))
	if err != nil {
		t.Fatalf("the boot made no snapshot key: %v", err)
	}
	recipient, err = escrow.SnapshotRecipient(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, recipient
}

func CheckedEscrow(t testing.TB, keyPath string, kp *crypto.KeyPair, recipient string) {
	t.Helper()
	r, err := escrow.SignReceipt(kp, recipient, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	if err := os.WriteFile(escrow.ReceiptPath(keyPath), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func FreshSnapshotKey(t testing.TB) []byte {
	t.Helper()
	key, err := escrow.NewSnapshotKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func SealedWith(t testing.TB, keyFile, snapshotKey, passphrase []byte) []byte {
	t.Helper()
	sealed, err := escrow.Seal(escrow.Contents{IdentityKey: keyFile, SnapshotKey: snapshotKey}, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func CheckedReceipt(keyPath string, pub []byte, recipient string) error {
	raw, err := os.ReadFile(escrow.ReceiptPath(keyPath))
	if err != nil {
		return err
	}
	var r escrow.Receipt
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("the receipt does not read: %w", err)
	}
	return r.Verify(pub, recipient)
}

func Members(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file — a snapshot holds nothing else", d.Name())
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel = filepath.ToSlash(rel); rel != escrow.SumsName {
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

func CheckSums(dir string) error {
	present, err := Members(dir)
	if err != nil {
		return err
	}
	return escrow.CheckSums(dir, present)
}
