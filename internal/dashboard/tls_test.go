package dashboard

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The dashboard served plaintext: every word between an identity and its
// operator crossed the network in the clear, and the browser refused the
// microphone because an http:// LAN address is not a secure context.
//
// This is the file that verifies PROPERLY. Every other dashboard test
// skips certificate verification, because a test about content types
// should fail for a wrong content type and not for a certificate. Here
// the chain is pooled and required.

func TestAMissingCertificateIsMintedOnStartup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls") // deliberately absent

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
	// The key is the whole trust decision the operator makes when they
	// install the root. It does not get to be world-readable.
	for _, p := range []string{m.LeafKey, filepath.Join(dir, "dashboard-ca.key")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s is readable beyond its owner: %v", filepath.Base(p), info.Mode().Perm())
		}
	}
}

// Minting again would silently invalidate the root the operator already
// installed — turning a solved problem back into a warning.
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

// A certificate that stops covering its address is a warning nobody
// asked for, so a changed host re-mints.
func TestAChangedHostGetsACertificateThatCoversIt(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureTLS(dir, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	m, err := EnsureTLS(dir, "10.200.200.2")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Regenerated {
		t.Fatal("the host changed and the certificate did not")
	}
	leaf := parseLeaf(t, m.LeafCert)
	if err := leaf.VerifyHostname("10.200.200.2"); err != nil {
		t.Fatalf("the new certificate does not cover the new host: %v", err)
	}
	// And still covers loopback: the operator may reach the same
	// dashboard through a tunnel, and that must not warn either.
	if err := leaf.VerifyHostname("127.0.0.1"); err != nil {
		t.Fatalf("loopback stopped working: %v", err)
	}
}

// THE REAL PROPERTY: a client that trusts the minted root connects with
// full verification. This is what the operator gets after installing it
// once — and what makes the page a secure context, which is what lets
// the browser hand over a microphone at all.
func TestAClientTrustingTheRootConnectsWithFullVerification(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{IdentityName: "X"})
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

// There is no plaintext listener to fall back to.
func TestThereIsNoPlaintextDashboard(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{IdentityName: "X"})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	// Go answers a plaintext request to a TLS listener with a 400 and
	// the words "client sent an HTTP request to an HTTPS server" rather
	// than dropping the connection — so the assertion is that no
	// plaintext request SUCCEEDS, not that it fails to connect.
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + addr + "/")
	if err != nil {
		return // refused at the transport: also fine
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
