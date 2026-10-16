package tlsfaulttest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func Certificate(t testing.TB, names []string, notBefore, notAfter time.Time, edits ...func(*x509.Certificate)) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "tlsfaulttest"},
		NotBefore: notBefore, NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	for _, edit := range edits {
		edit(tmpl)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

func Server(t testing.TB, names []string, notBefore, notAfter time.Time) (srv *httptest.Server, pool *x509.CertPool, handshakes *atomic.Int32) {
	t.Helper()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	return Serve(t, ok, Certificate(t, names, notBefore, notAfter))
}

func Serve(t testing.TB, h http.Handler, certs ...tls.Certificate) (srv *httptest.Server, pool *x509.CertPool, handshakes *atomic.Int32) {
	t.Helper()
	if len(certs) == 0 {
		t.Fatal("tlsfaulttest: Serve needs a certificate")
	}
	pool = x509.NewCertPool()
	for _, c := range certs {
		pool.AddCert(c.Leaf)
	}
	handshakes = &atomic.Int32{}
	srv = httptest.NewUnstartedServer(h)

	srv.TLS = &tls.Config{Certificates: certs[:1], GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		n := int(handshakes.Add(1))
		cfg := &tls.Config{Certificates: []tls.Certificate{certs[min(n, len(certs))-1]}}

		cfg.NextProtos = []string{"http/1.1"}
		return cfg, nil
	}}
	srv.StartTLS()
	srv.Config.SetKeepAlivesEnabled(false)
	t.Cleanup(srv.Close)
	return srv, pool, handshakes
}
