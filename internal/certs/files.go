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
)

func writeFile(path string, data []byte, perm os.FileMode) error {
	published, err := atomicfile.WriteReplace(path, data, perm)
	if err != nil && published {
		return fmt.Errorf("%s is in place but its directory entry is not proven durable: %w", filepath.Base(path), err)
	}
	return err
}

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
	if err := writeFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("certificate key: %w", err)
	}
	if err := writeFile(certPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("certificate: %w", err)
	}
	return nil
}

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
	if err := writeFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, fmt.Errorf("account key: %w", err)
	}
	return key, nil
}
