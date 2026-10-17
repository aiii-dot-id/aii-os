package witness

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/tlsfault"
	"github.com/aiii-dot-id/aii-os/internal/tlsfault/tlsfaulttest"
)

// A WITNESS WHOSE CERTIFICATE IS REFUSED IS NOT A WITNESS THAT IS DARK. The
// probe's error says which, by type, so the notices that follow can.
func TestAWitnessProbeRefusedForItsCertificateSaysSo(t *testing.T) {
	now := time.Now()
	srv, pool, _ := tlsfaulttest.Server(t, []string{"mail.example"}, now.Add(-90*24*time.Hour), now.Add(-3*time.Hour))
	_, err := NewWithRoots(srv.URL, "", pool).Status()
	var fault *tlsfault.Fault
	if !errors.As(err, &fault) || fault.Reason != tlsfault.Expired || fault.Host != "127.0.0.1" {
		t.Fatalf("a probe of a witness whose certificate has expired: %v (%+v)", err, fault)
	}
	// Verified again against the client's own authorities, which trust it:
	// it is not said to be from an authority this machine does not trust.
	if fault.AlsoUntrusted {
		t.Fatalf("a certificate the client's authorities trust is said to be untrusted: %v", err)
	}
}

// A WITNESS THAT REDIRECTS THE PROBE TO A HOST WHOSE CERTIFICATE IS REFUSED
// is said with that host: the one whose certificate was refused, not the
// configured one, which answered.
func TestAWitnessProbeRedirectedToARefusedCertificateNamesTheHostRedirectedTo(t *testing.T) {
	now := time.Now()
	bad, pool, _ := tlsfaulttest.Server(t, []string{"mail.example"}, now.Add(-90*24*time.Hour), now.Add(-3*time.Hour))
	_, port, err := net.SplitHostPort(strings.TrimPrefix(bad.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	configured := httptest.NewServer(http.RedirectHandler("https://localhost:"+port+"/status", http.StatusFound))
	defer configured.Close()
	_, err = NewWithRoots(configured.URL, "", pool).Status()
	var fault *tlsfault.Fault
	if !errors.As(err, &fault) || fault.Host != "localhost" || !strings.HasPrefix(fault.Error(), "the server localhost presented a certificate that") {
		t.Fatalf("a probe redirected to a refused certificate: %v (%+v)", err, fault)
	}
}

// A PINNED WITNESS THAT PRESENTS ANOTHER KEY IS NOT A WITNESS THAT IS DARK.
// Its certificate verifies; the key is not the pinned one, and the probe's
// error says so by type, so the notices that follow do too. The pinned key
// is a control: it is answered.
func TestAPinnedWitnessThatPresentsAnotherKeyIsSaidAsThat(t *testing.T) {
	now := time.Now()
	srv, pool, _ := tlsfaulttest.Server(t, []string{"127.0.0.1"}, now.Add(-time.Hour), now.Add(time.Hour))
	other := tlsfaulttest.Certificate(t, []string{"127.0.0.1"}, now.Add(-time.Hour), now.Add(time.Hour))
	pinOf := func(leaf []byte) string { sum := sha256.Sum256(leaf); return "sha256:" + hex.EncodeToString(sum[:]) }
	_, err := NewWithRoots(srv.URL, pinOf(other.Leaf.RawSubjectPublicKeyInfo), pool).Status()
	var fault *tlsfault.Fault
	if !errors.As(err, &fault) || fault.Reason != tlsfault.KeyNotPinned || !strings.HasPrefix(fault.Error(), "the server 127.0.0.1 presented a key that is not the pinned one") {
		t.Fatalf("a witness whose key is not the pinned one: %v (%+v)", err, fault)
	}
	served := srv.TLS.Certificates[0].Leaf
	if _, err := NewWithRoots(srv.URL, pinOf(served.RawSubjectPublicKeyInfo), pool).Status(); errors.As(err, &fault) {
		t.Fatalf("the pinned key was refused: %v", err)
	}
}

// A WITNESS'S REFUSAL IS SAID IN ITS OWN WORDS, BOUNDED: a page with line
// breaks reaches the probe's log line printable and cut, so it forges no
// line there.
func TestAWitnessRefusalBodyIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html>\n2026/10/14 00:00:00 forged line\r\n" + strings.Repeat("x", 5000)))
	}))
	defer srv.Close()
	_, err := New(srv.URL, "").Status()
	if err == nil || strings.ContainsAny(err.Error(), "\n\r") || !strings.Contains(err.Error(), "returned 403: <html> 2026/10/14 00:00:00 forged line") || len(err.Error()) > tlsfault.MaxSaid+64 {
		t.Fatalf("a witness's refusal: %q", err)
	}
	conflict := newConflictError([]byte(`{"error":"line one\nforged line"}`))
	if strings.ContainsAny(conflict.Error(), "\n\r") {
		t.Fatalf("a conflict's words: %q", conflict.Error())
	}
}
