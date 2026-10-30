package crypto

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync/atomic"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

type DeviceKey interface {
	Wrap(plain []byte) ([]byte, error)
	Unwrap(sealed []byte) ([]byte, error)
	Holder() string
}

type deviceKeyBox struct{ k DeviceKey }

var deviceKey atomic.Pointer[deviceKeyBox]

func SetDeviceKey(k DeviceKey) {
	if k == nil {
		deviceKey.Store(nil)
		return
	}
	deviceKey.Store(&deviceKeyBox{k: k})
}

func heldDeviceKey() DeviceKey {
	if b := deviceKey.Load(); b != nil {
		return b.k
	}
	return nil
}

func DeviceKeyHolder() string {
	if k := heldDeviceKey(); k != nil {
		return k.Holder()
	}
	return ""
}

const wrappedHeader = "AII OS identity key, wrapped by the keystore of the phone that holds it, v1\n"

func isWrapped(raw []byte) bool { return bytes.HasPrefix(raw, []byte(wrappedHeader)) }

type KeyCustody string

const (
	KeyWrappedElsewhere KeyCustody = "KEY_WRAPPED_BY_A_PHONE"

	KeyInTheClear KeyCustody = "KEY_NOT_WRAPPED"

	KeystoreFailed KeyCustody = "KEYSTORE_FAILED"
)

type KeyCustodyError struct {
	Reason KeyCustody
	Path   string
	Holder string
	Op     string
	Err    error
}

func (e *KeyCustodyError) Error() string {
	switch e.Reason {
	case KeyWrappedElsewhere:
		return fmt.Sprintf("the identity's key at %s is wrapped by the keystore of the phone that holds it, and opens only in the app on that phone; an escrow file carries the key to any other machine", e.Path)
	case KeyInTheClear:
		return fmt.Sprintf("the identity's key at %s is not wrapped by this phone's keystore, and on a phone the key is used only wrapped; the next start wraps it", e.Path)
	}
	what := "could not wrap"
	if e.Op == "unwrap" {
		what = "did not give back"
	}
	if e.Err != nil {
		return fmt.Sprintf("this phone's keystore (%s) %s the identity's key at %s: %v", e.Holder, what, e.Path, e.Err)
	}
	return fmt.Sprintf("this phone's keystore (%s) gave back something other than the identity's key at %s when it was asked to %s it", e.Holder, e.Path, e.Op)
}

func (e *KeyCustodyError) Unwrap() error { return e.Err }

func ReadKeyFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	k := heldDeviceKey()
	switch {
	case isWrapped(raw) && k == nil:
		return nil, &KeyCustodyError{Reason: KeyWrappedElsewhere, Path: path}
	case isWrapped(raw):
		plain, err := k.Unwrap(raw[len(wrappedHeader):])
		if err != nil {
			return nil, &KeyCustodyError{Reason: KeystoreFailed, Path: path, Holder: k.Holder(), Op: "unwrap", Err: err}
		}
		return plain, nil
	case k != nil:
		defer wipe(raw)
		if _, err := ParseKeyPair(raw); err != nil {
			return nil, err
		}
		return nil, &KeyCustodyError{Reason: KeyInTheClear, Path: path}
	}
	return raw, nil
}

func wrapVerified(k DeviceKey, plain []byte, path string) ([]byte, error) {
	sealed, err := k.Wrap(plain)
	if err != nil {
		return nil, &KeyCustodyError{Reason: KeystoreFailed, Path: path, Holder: k.Holder(), Op: "wrap", Err: err}
	}
	back, err := k.Unwrap(sealed)
	same := err == nil && bytes.Equal(back, plain)
	wipe(back)
	if err != nil {
		return nil, &KeyCustodyError{Reason: KeystoreFailed, Path: path, Holder: k.Holder(), Op: "unwrap", Err: err}
	}
	if !same || len(sealed) == 0 {
		return nil, &KeyCustodyError{Reason: KeystoreFailed, Path: path, Holder: k.Holder(), Op: "wrap"}
	}
	return append([]byte(wrappedHeader), sealed...), nil
}

func WrapKeyFile(path string) (wrapped bool, err error) {
	k := heldDeviceKey()
	if k == nil {
		return false, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer wipe(raw)
	if isWrapped(raw) {
		return false, nil
	}
	if _, err := ParseKeyPair(raw); err != nil {
		return false, nil
	}
	sealed, err := wrapVerified(k, raw, path)
	if err != nil {
		return false, err
	}
	published, err := atomicfile.WriteReplace(path, sealed, 0o600)
	if err != nil {
		if published {
			return true, fmt.Errorf("the identity's key at %s is wrapped and the directory's durability is unconfirmed: %w", path, err)
		}
		return false, fmt.Errorf("the wrapped identity key could not be written at %s, and the key there is as it was: %w", path, err)
	}
	back, err := ReadKeyFile(path)
	same := err == nil && bytes.Equal(back, raw)
	wipe(back)
	if !same {
		return true, &KeyCustodyError{Reason: KeystoreFailed, Path: path, Holder: k.Holder(), Op: "unwrap", Err: err}
	}
	return true, nil
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
