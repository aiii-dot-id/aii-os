package dashboard

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

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
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

const (
	// .
	// .
	caLifetime   = 10 * 365 * 24 * time.Hour
	leafLifetime = 2 * 365 * 24 * time.Hour
)

// .
type TLSMaterial struct {
	CACertPath  string
	LeafCert    string
	LeafKey     string
	Regenerated bool
}

// .
// .
// .
// .
// .
func EnsureTLS(dir, host string) (*TLSMaterial, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("tls dir: %w", err)
	}
	// .
	// .
	// .
	if abs, aerr := filepath.Abs(dir); aerr == nil {
		dir = abs
	}
	m := &TLSMaterial{
		CACertPath: filepath.Join(dir, "dashboard-ca.crt"),
		LeafCert:   filepath.Join(dir, "dashboard.crt"),
		LeafKey:    filepath.Join(dir, "dashboard.key"),
	}
	if usable(m.LeafCert, m.LeafKey, m.CACertPath, host) {
		return m, nil
	}

	caKeyPath := filepath.Join(dir, "dashboard-ca.key")
	caCert, caKey, err := loadOrMintCA(m.CACertPath, caKeyPath)
	if err != nil {
		return nil, err
	}
	if err := mintLeaf(caCert, caKey, host, m.LeafCert, m.LeafKey); err != nil {
		return nil, err
	}
	m.Regenerated = true
	return m, nil
}

// .
// .
// .
// .
// .
// .
// .
// .
func usable(certPath, keyPath, caPath, host string) bool {
	c := parsePEMCert(certPath)
	if c == nil {
		return false
	}
	// .
	// .
	if time.Now().Add(30 * 24 * time.Hour).After(c.NotAfter) {
		return false
	}
	if c.VerifyHostname(host) != nil {
		return false
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return false
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return false
	}
	k, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return false
	}
	pub, ok := c.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&k.PublicKey) {
		return false
	}
	ca := parsePEMCert(caPath)
	if ca == nil {
		return false
	}
	if time.Now().Add(30 * 24 * time.Hour).After(ca.NotAfter) {
		return false
	}
	return c.CheckSignatureFrom(ca) == nil
}

func parsePEMCert(path string) *x509.Certificate {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	return c
}

func loadOrMintCA(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if certPEM, err := os.ReadFile(certPath); err == nil {
		if keyPEM, kerr := os.ReadFile(keyPath); kerr == nil {
			cb, _ := pem.Decode(certPEM)
			kb, _ := pem.Decode(keyPEM)
			if cb != nil && kb != nil {
				c, cerr := x509.ParseCertificate(cb.Bytes)
				k, kerr := x509.ParseECPrivateKey(kb.Bytes)
				if cerr == nil && kerr == nil {
					pub, ok := c.PublicKey.(*ecdsa.PublicKey)
					switch {
					case !ok || !pub.Equal(&k.PublicKey):
						// .
						// .
						// .
						// .
						// .
						logsink.Warn("dashboard.decision", "TLS root cert and key do not correspond — reminting the root; install the new %s once", certPath)
					case time.Now().Add(30 * 24 * time.Hour).After(c.NotAfter):
						logsink.Warn("dashboard.decision", "TLS root is expiring — reminting; install the new %s once", certPath)
					default:
						// .
						// .
						return c, k, nil
					}
				}
			}
		}
	}

	// .
	// .
	// .
	if _, serr := os.Stat(certPath); serr == nil {
		aside := certPath + ".replaced-" + time.Now().UTC().Format("20060102T150405Z")
		if rerr := os.Rename(certPath, aside); rerr == nil {
			logsink.Info("dashboard.decision", "previous TLS root set aside as %s", filepath.Base(aside))
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			// .
			// .
			// .
			CommonName:   "AII OS local dashboard CA (this machine only)",
			Organization: []string{"AII OS — local, not an AIII root"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(caLifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		// .
		// .
		PermittedDNSDomains:         []string{"localhost"},
		PermittedDNSDomainsCritical: false,
		PermittedIPRanges: []*net.IPNet{
			{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)},
			{IP: net.IPv4(10, 0, 0, 0), Mask: net.CIDRMask(8, 32)},
			{IP: net.IPv4(172, 16, 0, 0), Mask: net.CIDRMask(12, 32)},
			{IP: net.IPv4(192, 168, 0, 0), Mask: net.CIDRMask(16, 32)},
			{IP: net.IPv6loopback, Mask: net.CIDRMask(128, 128)},
		},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	// .
	// .
	// .
	// .
	// .
	if err := writePEM(keyPath, "EC PRIVATE KEY", kder, 0o600); err != nil {
		return nil, nil, err
	}
	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func mintLeaf(ca *x509.Certificate, caKey *ecdsa.PrivateKey, host, certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(leafLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	// .
	// .
	// .
	// .
	for _, name := range []string{host, "localhost"} {
		if ip := net.ParseIP(name); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, name)
		}
	}
	tmpl.IPAddresses = append(tmpl.IPAddresses, net.IPv4(127, 0, 0, 1), net.IPv6loopback)

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	// .
	// .
	if err := writePEM(keyPath, "EC PRIVATE KEY", kder, 0o600); err != nil {
		return err
	}
	return writePEM(certPath, "CERTIFICATE", der, 0o644)
}

// .
// .
// .
// .
// .
// .
// .
func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	// .
	// .
	// .
	// .
	if mode&0o077 == 0 {
		if err := fileperm.RestrictToOwner(f); err != nil {
			f.Close()
			return fmt.Errorf("protect %s: %w", filepath.Base(path), err)
		}
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
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
	published, err := atomicfile.Replace(tmp, path)
	if err != nil {
		if published {
			// .
			// .
			// .
			// .
			// .
			// .
			return fmt.Errorf("write %s: published but not durable: %w", filepath.Base(path), err)
		}
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}
