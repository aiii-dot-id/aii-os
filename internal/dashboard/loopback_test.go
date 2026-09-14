package dashboard

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"testing"
)

// .
func nonLoopbackIPv4(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skip("no interface addresses")
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			return ipn.IP.String()
		}
	}
	t.Skip("this host has no non-loopback IPv4 address for a network bind")
	return ""
}

// .
// .
// .
// .
func TestLoopbackIsServedInTheClearBesideANetworkBind(t *testing.T) {
	ip := nonLoopbackIPv4(t)
	s := New(ip, 0, &WSHandler{})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	_, port, _ := net.SplitHostPort(addr)
	if got := s.LocalURL(); got != "http://127.0.0.1:"+port {
		t.Fatalf("the local URL is plain loopback beside the bind: %q", got)
	}
	if got := s.AddressURL(); got != "https://"+addr {
		t.Fatalf("the address URL is the TLS bind: %q", got)
	}
	res, err := http.Get("http://127.0.0.1:" + port + "/")
	if err != nil {
		t.Fatalf("loopback in the clear: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("loopback must be served: %d", res.StatusCode)
	}
	res, err = testClient.Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("the network bind still serves TLS: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the TLS bind must be served: %d", res.StatusCode)
	}
	// .
	plain, _ := http.NewRequest("GET", "http://127.0.0.1:"+port+"/ws", nil)
	plain.Header.Set("Origin", "http://127.0.0.1:"+port)
	if u := plain.Header.Get("Origin"); !s.hostAllowed(strings.TrimPrefix(u, "http://")) || requestScheme(plain) != "http" {
		t.Fatal("a plain loopback origin is this server's own")
	}
	secure, _ := http.NewRequest("GET", "https://"+addr+"/ws", nil)
	secure.TLS = &tls.ConnectionState{}
	if requestScheme(secure) != "https" {
		t.Fatal("a TLS request is https")
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestTheNetworkBindServesHTTP2BesideLoopback(t *testing.T) {
	ip := nonLoopbackIPv4(t)
	s := New(ip, 0, &WSHandler{})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	c := &http.Client{Transport: &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2", "http/1.1"}},
	}}
	res, err := c.Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("an HTTP/2 GET to the bind failed: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the bind must serve HTTP/2: %d", res.StatusCode)
	}
	if res.ProtoMajor != 2 {
		t.Fatalf("the bind must negotiate HTTP/2, got HTTP/%d.%d", res.ProtoMajor, res.ProtoMinor)
	}
	// .
	_, port, _ := net.SplitHostPort(addr)
	lres, err := http.Get("http://127.0.0.1:" + port + "/")
	if err != nil {
		t.Fatalf("loopback: %v", err)
	}
	lres.Body.Close()
	if lres.StatusCode != http.StatusOK {
		t.Fatalf("loopback must answer: %d", lres.StatusCode)
	}
}
