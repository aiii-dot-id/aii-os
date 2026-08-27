package dashboard

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

// tls.go — the dashboard serves HTTPS, always, and mints what it needs
// to do that.
//
// It served plain HTTP, so an identity's entire conversation with its
// operator crossed the network in the clear — every word, every tool
// result, every secret either of them typed. That is the reason for
// this file; the microphone is only what made someone look.
//
// A LOCAL CA, AND ITS KEY NEVER LEAVES THIS MACHINE. The root is
// generated here, on first run, and the operator installs it once to get
// a browser with no warnings. It is emphatically NOT an AIII-operated
// CA: a root that AIII held and operators installed could mint a
// certificate for any name those machines trust — their bank, their
// mail — so a compromise of one key would be silent interception of
// every operator's whole web. That is a far larger risk than the one
// being fixed.
//
// Name constraints would be the elegant fence and do not work: RFC 5280
// treats an imported root as a trust anchor where only Subject and SPKI
// are read, extensions ignored, and enforcement on user-added roots is
// inconsistent across browsers and absent on some platforms. They are
// set below anyway — free, and honoured by the clients that do read them
// — but nothing here relies on them. The safety comes from the key being
// local, which is also why mkcert generates per-machine roots rather
// than shipping one.
//
// PURE GO, SO IT HOLDS ON ALL FIVE PLATFORMS. Generating is
// crypto/x509 and branches on nothing. INSTALLING the root into a trust
// store is per-platform and is the operator's one action, documented
// rather than automated — a mature tool already does that job well, and
// it is operator tooling rather than runtime code.

const (
	// caLifetime is long because renewal is friction and this root is
	// scoped to one machine that the operator already controls.
	caLifetime   = 10 * 365 * 24 * time.Hour
	leafLifetime = 2 * 365 * 24 * time.Hour
)

// TLSMaterial is where the dashboard's certificate lives.
type TLSMaterial struct {
	CACertPath  string // the root the operator installs, once
	LeafCert    string
	LeafKey     string
	Regenerated bool
}

// EnsureTLS returns certificate material for host, minting it if absent.
//
// Idempotent and cheap: existing material is reused unless it has expired
// or no longer covers the host, because a certificate that stops matching
// its address is a click-through the operator did not ask for.
func EnsureTLS(dir, host string) (*TLSMaterial, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("tls dir: %w", err)
	}
	// ABSOLUTE, because the operator is told this path so they can
	// install the root, and a relative one is not something they can
	// paste into a command from wherever they happen to be standing.
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

// usable reports whether the existing on-disk SET still serves this
// host: the leaf parses and covers the host, the leaf KEY exists and
// matches it, and the leaf chains to the STORED root, itself within
// headroom. Anything less remints (D46, Sev 2026-08-26): the old
// leaf-only check served every torn state a crash or partial restore
// can leave — a new cert beside an old key, a leaf from a root the
// operator no longer has installed — and Start could report success
// while the browser refused the chain.
func usable(certPath, keyPath, caPath, host string) bool {
	c := parsePEMCert(certPath)
	if c == nil {
		return false
	}
	// A month of headroom: expiry should never be the operator's
	// surprise on a morning they needed the dashboard.
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
						// A cert from one generation beside a key from
						// another (partial restore, torn copy) signs
						// NOTHING the installed root validates: reused,
						// it mints leaves every browser rejects while
						// Start reports success (D46, Sev 2026-08-26).
						log.Printf("dashboard: TLS root cert and key do not correspond — reminting the root; install the new %s once", certPath)
					case time.Now().Add(30 * 24 * time.Hour).After(c.NotAfter):
						log.Printf("dashboard: TLS root is expiring — reminting; install the new %s once", certPath)
					default:
						// REUSED, deliberately: minting a new root would
						// silently invalidate the one the operator installed.
						return c, k, nil
					}
				}
			}
		}
	}

	// The old root file is the operator's installed trust: set it aside
	// rather than overwrite — evidence, and the way back from a mistaken
	// remint. Best-effort; the atomic write below is the correctness.
	if _, serr := os.Stat(certPath); serr == nil {
		aside := certPath + ".replaced-" + time.Now().UTC().Format("20060102T150405Z")
		if rerr := os.Rename(certPath, aside); rerr == nil {
			log.Printf("dashboard: previous TLS root set aside as %s", filepath.Base(aside))
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
			// Named for what it is and where it came from, because the
			// operator will read this in a trust-store list years from
			// now and must be able to tell what it is for.
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
		// Defence in depth only — see the file comment on why nothing
		// relies on these holding.
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
	// KEY FIRST, CERT LAST: the cert is the commit point the
	// correspondence checks validate the pair by, so a crash between
	// the two writes is detected and reminted, never served. 0600, and
	// it stays here — this key is the whole trust decision the operator
	// made when they installed the root.
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
	// EVERY WAY IN, on one certificate. The operator may reach the same
	// dashboard as a LAN address, as localhost through a tunnel, or by
	// name — and a certificate that covers only one of those is a
	// warning the first time they use another.
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
	// KEY FIRST, CERT LAST — usable() validates the set by the cert, so
	// a torn write is detected (key mismatch) and reminted, not served.
	if err := writePEM(keyPath, "EC PRIVATE KEY", kder, 0o600); err != nil {
		return err
	}
	return writePEM(certPath, "CERTIFICATE", der, 0o644)
}

// writePEM writes one PEM file ATOMICALLY: unique temp in the same
// directory, chmod, sync, then rename over the target through the
// atomicfile seam. O_TRUNC in place could leave a half-written or
// mixed-generation file behind a crash (D46, Sev 2026-08-26); the
// set-validation in usable()/loadOrMintCA detects what a crash
// BETWEEN two writes leaves, and this removes the torn state WITHIN
// one.
func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
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
	_, err = atomicfile.Replace(tmp, path)
	return err
}
