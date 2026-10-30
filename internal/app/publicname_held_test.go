package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/publicname"
	"github.com/aiii-dot-id/aii-os/internal/publicname/publicnametest"
)

// storeHeldCertificate writes a certificate and key for name where the
// identity's certificate manager keeps them (tls/public beside the
// record), as an earlier issuance leaves them.
func storeHeldCertificate(t *testing.T, identityDir, name string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(30 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(identityDir, "tls", "public")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "public.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "public.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A BOOT INTO SAFE BECAUSE THE KEY DID NOT LOAD STILL SERVES THE CERTIFICATE
// THE IDENTITY HOLDS. Nothing can be signed to the certificate service, so
// nothing is asked of it: no order, no renewal, no route; Retry and Look
// refuse saying why, and the card says the held certificate is served and
// why it cannot be renewed. It used to serve the local certificate and say
// no name was claimed.
func TestABootIntoSafeWithNoKeyServesTheHeldCertificate(t *testing.T) {
	dir := t.TempDir()
	pub := &publicnametest.Publisher{Zone: "example.test"}
	var managers []*publicnametest.Manager
	cfg := birthNamed(t, dir)
	live := bootNamed(t, cfg, pub, &managers)
	claimed, err := live.pn.Claim()
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, live, publicname.StatusIssued)
	stopApp(t, live)
	storeHeldCertificate(t, filepath.Dir(cfg.Identity.LedgerPath), claimed.Name)
	if err := os.Remove(cfg.Identity.KeyPath); err != nil {
		t.Fatal(err)
	}
	statuses, reads, writes := pub.Asked()
	claims := len(pub.Claims)

	cfg2 := *cfg
	app := New(&cfg2)
	app.pn.NewPublisher = func(publicname.Settings) (publicname.NamePublisher, error) { return pub, nil }
	// The manager is the real one, reading what is stored.
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	defer stopApp(t, app)
	if _, safe := app.SafeMode(); !safe {
		t.Fatal("a missing key did not boot SAFE")
	}
	st := app.pn.State()
	if st.Name != claimed.Name || st.Status != publicname.StatusHeld || !strings.Contains(st.LastError, "key is not loaded") || st.NotAfter == "" || st.DaysLeft <= 0 {
		t.Fatalf("the card does not say the held certificate is served and why it cannot be renewed: %+v", st)
	}
	if got := servedCert(t, app.dashboard.BoundAddr(), claimed.Name); got.VerifyHostname(claimed.Name) != nil {
		t.Fatal("SAFE with no key does not serve the certificate held for the name")
	}
	if _, err := app.pn.Retry(); err == nil || !strings.Contains(err.Error(), "key is not loaded") {
		t.Fatalf("Retry with no key must refuse saying why: %v", err)
	}
	if _, err := app.pn.Look(); err == nil || !strings.Contains(err.Error(), "key is not loaded") {
		t.Fatalf("Look with no key must refuse saying why: %v", err)
	}
	app.pn.Renew()
	time.Sleep(100 * time.Millisecond)
	if s2, r2, w2 := pub.Asked(); s2 != statuses || r2 != reads || w2 != writes || len(pub.Claims) != claims {
		t.Fatalf("the certificate service was asked something with no key to sign it: statuses %d->%d, reads %d->%d, writes %d->%d", statuses, s2, reads, r2, writes, w2)
	}
	if st := app.pn.State(); st.Status != publicname.StatusHeld {
		t.Fatalf("the daily pass moved a held certificate's status: %+v", st)
	}
}
