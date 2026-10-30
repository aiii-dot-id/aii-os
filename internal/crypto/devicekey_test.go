package crypto

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto/devicekeytest"
)

// holdDeviceKey gives this process k as its device key for one test.
func holdDeviceKey(t *testing.T, k DeviceKey) {
	t.Helper()
	SetDeviceKey(k)
	t.Cleanup(func() { SetDeviceKey(nil) })
}

func plainKeyFile(t *testing.T, path string) []byte {
	t.Helper()
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SaveKeyPair(kp, path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// inTheClear says whether raw holds the private key of the plain key file
// plain: its 32-byte seed, anywhere in it.
func inTheClear(raw, plain []byte) bool { return bytes.Contains(raw, plain[4:36]) }

// ON A PHONE A KEY IS NEVER WRITTEN IN THE CLEAR, and everything that reads
// it reads the one portable form. A key published where the process holds
// a device key is on disk only wrapped; it loads through the keystore; the
// bytes read are the bytes given; a process that holds no device key is
// refused it by a typed reason, and a key in the clear is refused where one
// is held.
func TestOnAPhoneAKeyIsWrittenOnlyWrappedAndReadUnwrapped(t *testing.T) {
	dir := t.TempDir()
	plain := plainKeyFile(t, filepath.Join(dir, "desktop.sec"))
	want, _ := ParseKeyPair(plain)

	ks := devicekeytest.New()
	holdDeviceKey(t, ks)
	path := filepath.Join(dir, "identity.sec")
	if published, err := PublishKeyFile(plain, path); err != nil || !published {
		t.Fatalf("publish on a phone: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if inTheClear(raw, plain) || !isWrapped(raw) {
		t.Fatal("A KEY PUBLISHED ON A PHONE IS ON ITS DISK IN THE CLEAR")
	}
	if got, err := ReadKeyFile(path); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("the key read through the keystore is not the key that was given: %v", err)
	}
	if kp, err := LoadKeyPair(path); err != nil || kp.Fingerprint() != want.Fingerprint() {
		t.Fatalf("load through the keystore: %v", err)
	}
	if _, err := LoadKeyPair(filepath.Join(dir, "desktop.sec")); !custodyIs(err, KeyInTheClear) {
		t.Fatalf("a key in the clear was loaded on a phone: %v", err)
	}
	other, _ := GenerateKeyPair()
	if published, err := SaveKeyPair(other, path); err == nil || published {
		t.Fatal("A WRAPPED KEY WAS WRITTEN OVER")
	}

	SetDeviceKey(nil)
	if _, err := LoadKeyPair(path); !custodyIs(err, KeyWrappedElsewhere) {
		t.Fatalf("a wrapped key without its keystore: %v", err)
	}
	if kp, err := LoadKeyPair(filepath.Join(dir, "desktop.sec")); err != nil || kp.Fingerprint() != want.Fingerprint() {
		t.Fatalf("a desktop's key in the clear no longer loads on a desktop: %v", err)
	}
}

// A KEY IN THE CLEAR IS WRAPPED ONCE, and the plain file goes in the same
// rename: under another name too (a hard link), nothing of it stays.
func TestAKeyInTheClearIsWrappedOnceAndNothingOfItStays(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "identity.sec")
	plain := plainKeyFile(t, path)
	other := filepath.Join(dir, "another-name.sec")
	if err := os.Link(path, other); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	ks := devicekeytest.New()
	holdDeviceKey(t, ks)
	if wrapped, err := WrapKeyFile(path); err != nil || !wrapped {
		t.Fatalf("wrap: %v %v", wrapped, err)
	}
	raw, _ := os.ReadFile(path)
	if inTheClear(raw, plain) {
		t.Fatal("THE KEY IS STILL IN THE CLEAR AFTER IT WAS WRAPPED")
	}
	if got, _ := os.ReadFile(other); inTheClear(got, plain) {
		t.Fatal("THE KEY IN THE CLEAR STAYS UNDER ANOTHER NAME")
	}
	if got, err := ReadKeyFile(path); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("the wrapped key does not read back: %v", err)
	}
	if wrapped, err := WrapKeyFile(path); err != nil || wrapped || ks.Wraps() != 1 {
		t.Fatalf("a wrapped key was wrapped again: %v %v, %d wraps", wrapped, err, ks.Wraps())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("the wrap left files behind: %v", entries)
	}
}

// A KEYSTORE THAT FAILS CHANGES NOTHING. One that refuses, and one that
// gives back something other than what it sealed, leave the key in the
// clear as it was and are a typed refusal; a publish they fail writes
// nothing.
func TestAKeystoreThatFailsLeavesTheKeyAsItWas(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "identity.sec")
	plain := plainKeyFile(t, path)
	ks := devicekeytest.New()
	holdDeviceKey(t, ks)
	for name, k := range map[string]DeviceKey{"refusing": ks, "giving back another key": wrongKeystore{ks}} {
		ks.Refuse(name == "refusing")
		SetDeviceKey(k)
		if wrapped, err := WrapKeyFile(path); wrapped || !custodyIs(err, KeystoreFailed) {
			t.Fatalf("%s: %v %v", name, wrapped, err)
		}
		if raw, _ := os.ReadFile(path); !bytes.Equal(raw, plain) {
			t.Fatalf("%s: the key was changed", name)
		}
		fresh := filepath.Join(dir, "fresh.sec")
		if published, err := PublishKeyFile(plain, fresh); published || !custodyIs(err, KeystoreFailed) {
			t.Fatalf("%s: publish: %v %v", name, published, err)
		}
		if _, err := os.Stat(fresh); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: a failed publish left a file", name)
		}
	}
}

type wrongKeystore struct{ *devicekeytest.Keystore }

func (w wrongKeystore) Unwrap(sealed []byte) ([]byte, error) {
	got, err := w.Keystore.Unwrap(sealed)
	if err == nil {
		got[len(got)-1] ^= 1
	}
	return got, err
}

func custodyIs(err error, want KeyCustody) bool {
	var custody *KeyCustodyError
	return errors.As(err, &custody) && custody.Reason == want
}
