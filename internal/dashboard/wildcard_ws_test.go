package dashboard

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
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
func gateAdmits(s *Server, hostPort string) bool {
	admitted := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { admitted = true })
	s.hostGate(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://"+hostPort+"/", nil))
	return admitted
}

// .
func originAdmits(s *Server, origin string) bool {
	r := httptest.NewRequest("GET", "/ws", nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if s.tls {
		r.TLS = &tls.ConnectionState{}
	}
	return s.wsAuthorized(r)
}

func TestWildcardBindAdmitsTheSameHostOnThePageAndTheSocket(t *testing.T) {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	for _, host := range []string{"0.0.0.0", "::", "::0", "0:0:0:0:0:0:0:0", "0", "0.0", "0x0", "127.0.0.1", "127.1"} {
		t.Run(host, func(t *testing.T) {
			s := New(host, 0, nil)
			addr, err := s.Start(t.TempDir())
			if err != nil {
				// .
				// .
				// .
				// .
				// .
				// .
				// .
				// .
				// .
				var dnsErr *net.DNSError
				if host != "0.0.0.0" && host != "::" && host != "127.0.0.1" && errors.As(err, &dnsErr) {
					t.Skipf("this host's resolver rejects the inet_aton shorthand %q (pure-Go resolver): %v", host, err)
				}
				t.Fatalf("start: %v", err)
			}
			defer s.Shutdown(context.Background())
			// .
			// .
			// .
			boundHost, port, err := net.SplitHostPort(addr)
			if err != nil {
				t.Fatalf("split bound addr %q: %v", addr, err)
			}
			ip := net.ParseIP(boundHost)
			if ip == nil {
				t.Fatalf("the kernel returned a bound host we cannot parse: %q", boundHost)
			}
			wide := ip.IsUnspecified()

			// .
			// .
			// .
			lan := net.JoinHostPort("192.0.2.10", port)
			if got := gateAdmits(s, lan); got != wide {
				t.Fatalf("bind %q resolved to %q (wildcard=%v) but hostGate admits(%s)=%v — the page gate and the kernel disagree",
					host, boundHost, wide, lan, got)
			}
			// .
			// .
			// .
			if got := originAdmits(s, s.Scheme()+"://"+lan); got != wide {
				t.Errorf("bind %q (wildcard=%v): the page gate and the socket gate disagree on %s://%s — socket admits=%v",
					host, wide, s.Scheme(), lan, got)
			}

			if wrong := net.JoinHostPort("192.0.2.10", "1"); originAdmits(s, s.Scheme()+"://"+wrong) {
				t.Errorf("Origin %q was authorised — the wildcard gate matches the bound PORT, not every port", wrong)
			}
			if other := SchemeFor(!s.tls); originAdmits(s, other+"://"+lan) {
				t.Errorf("an Origin on %q was authorised; this dashboard serves %q only", other, s.Scheme())
			}
			if originAdmits(s, "") {
				t.Error("a header-less client was authorised — an absent Origin is refused, wide bind or not")
			}
		})
	}
}

// .
// .
// .
// .
// .
func TestLoopbackBindStillRefusesAForeignHostOnBothGates(t *testing.T) {
	s := New("127.0.0.1", 0, nil)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split bound addr %q: %v", addr, err)
	}

	foreign := net.JoinHostPort("192.0.2.10", port)
	if gateAdmits(s, foreign) {
		t.Errorf("hostGate admitted Host %q on a loopback bind — the rebinding kill switch is off", foreign)
	}
	if originAdmits(s, s.Scheme()+"://"+foreign) {
		t.Errorf("wsAuthorized admitted Origin %q on a loopback bind — the port-only rule leaked past the wildcard bind it belongs to", foreign)
	}
	if !originAdmits(s, s.Scheme()+"://"+addr) {
		t.Errorf("wsAuthorized refused the server's own address %q — every socket would 403", addr)
	}
}
