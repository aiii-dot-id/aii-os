package dashboard

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// gateAdmits runs the REAL hostGate handler — the page half of the pair.
func gateAdmits(s *Server, hostPort string) bool {
	admitted := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { admitted = true })
	s.hostGate(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://"+hostPort+"/", nil))
	return admitted
}

// originAdmits is the socket half: the handshake policy, on one Origin,
// sent as a browser sends it from its own page: to the host the page came
// from.
func originAdmits(s *Server, origin string) bool {
	host := ""
	if u, err := url.Parse(origin); err == nil {
		host = u.Host
	}
	return originAdmitsAt(s, origin, host)
}

// originAdmitsAt is the same handshake with the request's Host given apart
// from its Origin, as a page on one name sends it to another.
func originAdmitsAt(s *Server, origin, host string) bool {
	r := httptest.NewRequest("GET", "/ws", nil)
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if s.tls {
		r.TLS = &tls.ConnectionState{} // the request arrived on the TLS bind
	}
	return s.wsAuthorized(r)
}

func TestWildcardBindAdmitsTheSameHostOnThePageAndTheSocket(t *testing.T) {
	// EVERY WILDCARD BIND, and the KERNEL says which those are. A literal
	// list only ever proves the spellings someone remembered to type: the
	// resolver net.Listen goes through accepts inet_aton shorthand, so
	// "0", "0.0" and "0x0" each bind every address while net.ParseIP —
	// which the gate used to consult — calls them nothing. So the
	// expectation below is taken from ln.Addr(), the one answer that
	// cannot disagree with what was actually bound.
	for _, host := range []string{"0.0.0.0", "::", "::0", "0:0:0:0:0:0:0:0", "0", "0.0", "0x0", "127.0.0.1", "127.1"} {
		t.Run(host, func(t *testing.T) {
			s := New(host, 0, nil)
			addr, err := s.Start(t.TempDir())
			if err != nil {
				// The inet_aton SHORTHAND spellings ("0", "0.0", "0x0",
				// "127.1") bind only where the cgo resolver answers —
				// the pure-Go resolver (Go's default where nsswitch
				// permits, e.g. GitHub runners) refuses them as lookup
				// failures before any gate runs. A host that cannot
				// perform the bind has no page-gate/socket-gate
				// agreement to disagree about: the startup refusal IS
				// the honest product answer there. The canonical
				// spellings above stay mandatory on every host.
				var dnsErr *net.DNSError
				if host != "0.0.0.0" && host != "::" && host != "127.0.0.1" && errors.As(err, &dnsErr) {
					t.Skipf("this host's resolver rejects the inet_aton shorthand %q (pure-Go resolver): %v", host, err)
				}
				t.Fatalf("start: %v", err)
			}
			defer s.Shutdown(context.Background())
			// GROUND TRUTH: is this bind actually wide? Not "did the
			// operator type a spelling we recognise" — what did the kernel
			// hand back.
			boundHost, port, err := net.SplitHostPort(addr)
			if err != nil {
				t.Fatalf("split bound addr %q: %v", addr, err)
			}
			ip := net.ParseIP(boundHost)
			if ip == nil {
				t.Fatalf("the kernel returned a bound host we cannot parse: %q", boundHost)
			}
			wide := ip.IsUnspecified()

			// A documentation address (TEST-NET-1): reachable by no test,
			// and in neither allowedHosts nor any loopback alias — exactly
			// the shape of the LAN address the operator's browser carries.
			lan := net.JoinHostPort("192.0.2.10", port)
			if got := gateAdmits(s, lan); got != wide {
				t.Fatalf("bind %q resolved to %q (wildcard=%v) but hostGate admits(%s)=%v — the page gate and the kernel disagree",
					host, boundHost, wide, lan, got)
			}
			// The two gates must never drift apart: whatever the page
			// admits, the socket admits, or the dashboard loads and then
			// reconnect-loops forever showing offline.
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

// The loopback default must not have widened with it: anyHostPort is
// empty there, so a foreign Host dies at BOTH gates. This is the
// DNS-rebinding kill switch (H2) — a page on evil.tld that rebinds to
// 127.0.0.1 arrives with the attacker's Host and its socket with the
// attacker's Origin.
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

// A PAGE ON ANOTHER NAME CANNOT OPEN THIS DASHBOARD'S SOCKET. On a wide bind
// the Origin gate matches the port, so the Origin of a page served under a
// sibling name on the same port passed, and with the host-only, Lax cookie
// the browser sends within one zone, that page could open the operator's
// socket. The handshake now also asks that the Origin be the host the
// request was sent to, which a browser's own page always is.
func TestASiblingNamesPageCannotOpenTheSocketOnAWideBind(t *testing.T) {
	s := New("0.0.0.0", 0, nil)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	own := net.JoinHostPort("quinn.example.test", port)
	sibling := net.JoinHostPort("mallory.example.test", port)
	if !gateAdmits(s, own) || !gateAdmits(s, sibling) {
		t.Fatal("the fixture is not a wide bind: the page gate must admit any name on the bound port")
	}
	if !originAdmitsAt(s, s.Scheme()+"://"+own, own) {
		t.Fatal("the dashboard's own page, on the name it was fetched under, was refused its socket")
	}
	if originAdmitsAt(s, s.Scheme()+"://"+sibling, own) {
		t.Fatal("a page served under another name on the same port opened this dashboard's socket")
	}
	if originAdmitsAt(s, s.Scheme()+"://"+own, sibling) {
		t.Fatal("an Origin that is not the host the request was sent to was admitted")
	}
}
