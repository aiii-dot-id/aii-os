package dashboard

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .
// .
// .

func TestAMissingCertificateIsMintedOnStartup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")

	m, err := EnsureTLS(dir, "127.0.0.1")
	if err != nil {
		t.Fatalf("startup could not mint a certificate: %v", err)
	}
	if !m.Regenerated {
		t.Fatal("nothing existed and nothing was minted")
	}
	for _, p := range []string{m.CACertPath, m.LeafCert, m.LeafKey} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing after mint: %s", p)
		}
	}
	// .
	// .
	for _, p := range []string{m.LeafKey, filepath.Join(dir, "dashboard-ca.key")} {
		restricted, err := fileperm.IsRestrictedToOwner(p)
		if err != nil {
			t.Fatal(err)
		}
		if !restricted {
			t.Fatalf("%s is a private key and is readable beyond its owner", filepath.Base(p))
		}
	}
}

// .
// .
func TestAnExistingCertificateIsReused(t *testing.T) {
	dir := t.TempDir()
	first, err := EnsureTLS(dir, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(first.CACertPath)
	if err != nil {
		t.Fatal(err)
	}

	second, err := EnsureTLS(dir, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if second.Regenerated {
		t.Fatal("a usable certificate was replaced — the installed root would stop matching")
	}
	after, _ := os.ReadFile(second.CACertPath)
	if string(before) != string(after) {
		t.Fatal("the root changed under an operator who had installed it")
	}
}

// .
// .
func TestAChangedHostGetsACertificateThatCoversIt(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureTLS(dir, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	m, err := EnsureTLS(dir, "192.0.2.2")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Regenerated {
		t.Fatal("the host changed and the certificate did not")
	}
	leaf := parseLeaf(t, m.LeafCert)
	if err := leaf.VerifyHostname("192.0.2.2"); err != nil {
		t.Fatalf("the new certificate does not cover the new host: %v", err)
	}
	// .
	// .
	if err := leaf.VerifyHostname("127.0.0.1"); err != nil {
		t.Fatalf("loopback stopped working: %v", err)
	}
}

// .
// .
// .
// .
func TestAClientTrustingTheRootConnectsWithFullVerification(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	dir := t.TempDir()
	addr, err := s.Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	caPEM, err := os.ReadFile(filepath.Join(dir, "dashboard-ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("the minted root is not a usable CA certificate")
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}

	resp, err := client.Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("a client trusting the root could not connect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if resp.TLS == nil {
		t.Fatal("the connection was not TLS")
	}
}

// .
func TestThereIsNoPlaintextDashboard(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	// .
	// .
	// .
	// .
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + addr + "/")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Fatalf("the dashboard served plaintext http with status %d", resp.StatusCode)
	}
	if resp.TLS != nil {
		t.Fatal("a plaintext request somehow negotiated TLS")
	}
}

func parseLeaf(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatal("leaf is not PEM")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return c
}
