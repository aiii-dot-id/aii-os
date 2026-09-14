package dashboard

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// .
// .
// .
// .

func selfSigned(t *testing.T, name string) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// .
// .
func peekCert(t *testing.T, addr, serverName string) *x509.Certificate {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: serverName, InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("dial as %q: %v", serverName, err)
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0]
}

func TestThePublicCertificateIsServedForItsNameOnlyAndLive(t *testing.T) {
	s := New("127.0.0.1", 0, nil)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	const name = "ui.abcdefghijklmnopqrstuvwxyz.example"
	local := peekCert(t, addr, "127.0.0.1")
	if local.VerifyHostname(name) == nil {
		t.Fatal("fixture: the local leaf must not already name the public name")
	}
	// .
	if got := peekCert(t, addr, name); got.SerialNumber.Cmp(local.SerialNumber) != 0 {
		t.Fatal("with no public certificate installed, the local pair must serve every name")
	}
	first := selfSigned(t, name)
	current := first
	s.SetPublicCertificate(name, func() *tls.Certificate { return current })
	if got := peekCert(t, addr, name); got.SerialNumber.Cmp(first.Leaf.SerialNumber) != 0 {
		t.Fatal("the public certificate was not served for the public name")
	}
	if got := peekCert(t, addr, "127.0.0.1"); got.SerialNumber.Cmp(local.SerialNumber) != 0 {
		t.Fatal("a client not asking for the public name must get the local pair")
	}
	// .
	// .
	// .
	if got := peekCert(t, addr, "some-other-host.example"); got.SerialNumber.Cmp(local.SerialNumber) != 0 {
		t.Fatal("a client naming another host must get the local pair, not the public certificate")
	}
	if got := peekCert(t, addr, strings.ToUpper(name)); got.SerialNumber.Cmp(first.Leaf.SerialNumber) != 0 {
		t.Fatal("the server name comparison must be case-insensitive")
	}
	// .
	// .
	renewed := selfSigned(t, name)
	current = renewed
	if got := peekCert(t, addr, name); got.SerialNumber.Cmp(renewed.Leaf.SerialNumber) != 0 {
		t.Fatal("a renewed certificate was not served on the next handshake")
	}
	// .
	current = nil
	if got := peekCert(t, addr, name); got.SerialNumber.Cmp(local.SerialNumber) != 0 {
		t.Fatal("a nil from the source must fall back to the local pair")
	}
}

func TestTheHostGateAdmitsThePublicNameLive(t *testing.T) {
	s := New("127.0.0.1", 0, nil)
	addr, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	_, port, _ := net.SplitHostPort(addr)
	name := "ui.abcdefghijklmnopqrstuvwxyz.example:" + port
	if s.hostAllowed(name) {
		t.Fatal("the public name must not be admitted before it is claimed")
	}
	s.AllowHost(name)
	if !s.hostAllowed(name) {
		t.Fatal("AllowHost did not admit the public name")
	}
	if !s.hostAllowed("127.0.0.1:" + port) {
		t.Fatal("admitting the public name must not disturb the loopback host")
	}
	if s.hostAllowed("evil.example:" + port) {
		t.Fatal("a loopback bind must still refuse a foreign host")
	}
}

func TestTheAdvertisedOriginWinsWhenSet(t *testing.T) {
	s := New("127.0.0.1", 0, nil)
	addr, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	_, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	if s.Origin() != "http://127.0.0.1:"+strconv.Itoa(p) {
		t.Fatalf("socket-derived origin: %s", s.Origin())
	}
	s.SetOrigin("https://ui.abcdefghijklmnopqrstuvwxyz.example:8180/")
	if s.Origin() != "https://ui.abcdefghijklmnopqrstuvwxyz.example:8180" {
		t.Fatalf("advertised origin not returned as set: %s", s.Origin())
	}
	s.SetOrigin("")
	if s.Origin() != "http://127.0.0.1:"+strconv.Itoa(p) {
		t.Fatalf("clearing the origin did not return to the socket: %s", s.Origin())
	}
}
