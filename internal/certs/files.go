package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
)

// .
// .
// .
// .
// .
// .
// .

func writeOwnerOnly(path string, data []byte) error {
	return writeFile(path, data, true)
}

func writeFile(path string, data []byte, secret bool) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if secret {
		if err := fileperm.RestrictToOwner(f); err != nil {
			f.Close()
			return fmt.Errorf("protect %s: %w", filepath.Base(path), err)
		}
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if published, err := atomicfile.Replace(tmp, path); err != nil {
		if published {
			return fmt.Errorf("%s is in place but its directory entry is not proven durable: %w", filepath.Base(path), err)
		}
		return err
	}
	return nil
}

// .
// .
// .
// .
func writePair(certPath, keyPath string, chain [][]byte, key *ecdsa.PrivateKey) error {
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	var certPEM []byte
	for _, der := range chain {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	if err := writeFile(keyPath, keyPEM, true); err != nil {
		return fmt.Errorf("certificate key: %w", err)
	}
	if err := writeFile(certPath, certPEM, false); err != nil {
		return fmt.Errorf("certificate: %w", err)
	}
	return nil
}

// .
// .
func loadOrMintAccountKey(path string) (*ecdsa.PrivateKey, error) {
	if raw, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(raw)
		if block == nil {
			return nil, errors.New("account key is not PEM")
		}
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("account key: %w", err)
		}
		return key, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := writeFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), true); err != nil {
		return nil, fmt.Errorf("account key: %w", err)
	}
	return key, nil
}
