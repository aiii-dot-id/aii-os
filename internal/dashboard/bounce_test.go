package dashboard

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"testing"
)

func noFollow(c *http.Client) *http.Client {
	nc := *c
	nc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &nc
}

func bounceServer(t *testing.T, named bool) (*Server, string) {
	t.Helper()
	s := New("127.0.0.1", 0, &WSHandler{})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	if named {
		s.SetPublicCertificate("ui.x.example.test", func() *tls.Certificate { return nil })
		s.SetOrigin("https://ui.x.example.test:8180")
		s.AllowHost("ui.x.example.test:8180")
	}
	return s, addr
}

// .
// .
// .
// .
func TestCleartextOnTheTLSPortUpgradesOnTheSameAddress(t *testing.T) {
	for _, named := range []bool{true, false} {
		_, addr := bounceServer(t, named)
		req, _ := http.NewRequest("GET", "http://"+addr+"/settings?x=1", nil)
		req.Host = "evil.example:80"
		res, err := noFollow(http.DefaultClient).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusPermanentRedirect || res.Header.Get("Location") != "https://"+addr+"/settings?x=1" {
			t.Fatalf("named=%v: cleartext must bounce to TLS on its own address: %d %q", named, res.StatusCode, res.Header.Get("Location"))
		}
	}
}

// .
// .
// .
func TestAByAddressNavigationIsServedNotBounced(t *testing.T) {
	s, addr := bounceServer(t, true)
	_, port, _ := net.SplitHostPort(addr)
	s.AllowHost("10.0.0.5:" + port)
	get := func(host, path, accept string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", "https://"+addr+path, nil)
		req.Host = host
		req.Header.Set("Accept", accept)
		res, err := noFollow(testClient).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	for _, host := range []string{"10.0.0.5:" + port, "ui.x.example.test:8180", "127.0.0.1:" + port} {
		if res := get(host, "/?a=b", "text/html,*/*"); res.StatusCode != http.StatusOK {
			t.Fatalf("a navigation at %s must be served, got %d (Location %q)", host, res.StatusCode, res.Header.Get("Location"))
		}
	}
	if res := get("evil.example:"+port, "/", "text/html"); res.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign Host is still refused: %d", res.StatusCode)
	}
}
