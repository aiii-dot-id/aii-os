package devicekeytest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"sync/atomic"
)

var ErrLocked = errors.New("the keystore is not available: the phone is locked")

type Keystore struct {
	aead           cipher.AEAD
	refuse         atomic.Bool
	wraps, unwraps atomic.Int32
}

func New() *Keystore {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return &Keystore{aead: aead}
}

func (k *Keystore) Refuse(on bool) { k.refuse.Store(on) }

func (k *Keystore) Wraps() int   { return int(k.wraps.Load()) }
func (k *Keystore) Unwraps() int { return int(k.unwraps.Load()) }

func (k *Keystore) Wrap(plain []byte) ([]byte, error) {
	if k.refuse.Load() {
		return nil, ErrLocked
	}
	k.wraps.Add(1)
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return k.aead.Seal(nonce, nonce, plain, nil), nil
}

func (k *Keystore) Unwrap(sealed []byte) ([]byte, error) {
	if k.refuse.Load() {
		return nil, ErrLocked
	}
	k.unwraps.Add(1)
	n := k.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("not sealed by this keystore")
	}
	return k.aead.Open(nil, sealed[:n], sealed[n:], nil)
}

func (k *Keystore) Holder() string { return "a test keystore" }
