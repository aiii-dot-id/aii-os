package dashboard

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The dashboard serves HTTPS and nothing else, so everything it hands a
// browser or an operator must say so. Getting this wrong is not a
// visible error — it is a blank panel, or an operator clicking a URL
// that answers 400.

// The CSP entries are SOURCE EXPRESSIONS, not links: a source written
// http:// does not match a resource served over https, so every
// section's own script and stylesheet would be blocked while the page
// around them loaded fine.
func TestSectionCSPUsesTheSchemeTheDashboardServes(t *testing.T) {
	for _, tls := range []bool{false, true} {
		scheme := SchemeFor(tls)
		csp := sectionCSP(scheme, "127.0.0.1:8443", "id.example.panel")
		// Built from SchemeFor rather than restating it: a test that
		// hardcodes the scheme passes while the CSP and the listener
		// disagree, which is the whole failure it is here to catch.
		if !strings.Contains(csp, scheme+"://127.0.0.1:8443/sections/id.example.panel/") {
			t.Errorf("tls=%v: the section's own origin is missing from its CSP: %s", tls, csp)
		}
		if !strings.Contains(csp, scheme+"://127.0.0.1:8443/section-api.js") {
			t.Errorf("tls=%v: the section API is missing from its CSP: %s", tls, csp)
		}
		if other := SchemeFor(!tls); strings.Contains(csp, other+"://") {
			t.Errorf("tls=%v: a CSP source names %q, which this dashboard does not serve — the section renders blank, with no error: %s", tls, other, csp)
		}
	}
}

// Everything that hands out a dashboard URL must agree with the scheme
// the dashboard actually serves. These drifted apart twice when there
// was only one scheme; now that it is the operator's choice, a helper
// that is right for https and wrong for http is a bug for half the
// installations — so both answers are checked, not just the current one.
func TestEveryDashboardURLAgreesOnTheSchemeServed(t *testing.T) {
	for _, tls := range []bool{false, true} {
		want, other := SchemeFor(tls), SchemeFor(!tls)

		s := New("127.0.0.1", 8443, nil)
		s.tls = tls

		if got := s.Scheme(); got != want {
			t.Fatalf("tls=%v: Scheme() = %q, want %q", tls, got, want)
		}
		if got := s.Origin(); !strings.HasPrefix(got, want+"://") {
			t.Errorf("tls=%v: Origin() = %q, does not use %q", tls, got, want)
		}
		if got := LoopbackURL(tls, 8443); !strings.HasPrefix(got, want+"://") {
			t.Errorf("tls=%v: LoopbackURL() = %q, does not use %q", tls, got, want)
		}
		if got := sectionCSP(s.Scheme(), "127.0.0.1:8443", "x.y"); !strings.Contains(got, want+"://") {
			t.Errorf("tls=%v: sectionCSP() does not use %q: %s", tls, want, got)
		}

		// The Origin check is the other half: it must accept the scheme
		// this server serves and refuse the one it does not. Accepting
		// both was never the bug — refusing the served one was, and it
		// 403'd every socket the moment TLS landed.
		s.boundAddr = "127.0.0.1:8443"
		s.allowedHosts = map[string]bool{"127.0.0.1:8443": true}
		r, _ := http.NewRequest("GET", "/ws", nil)
		r.Header.Set("Origin", want+"://127.0.0.1:8443")
		if !s.wsAuthorized(r) {
			t.Errorf("tls=%v: wsAuthorized refused an Origin on %q, the scheme this dashboard serves — every socket would 403", tls, want)
		}
		r.Header.Set("Origin", other+"://127.0.0.1:8443")
		if s.wsAuthorized(r) {
			t.Errorf("tls=%v: wsAuthorized accepted an Origin on %q, which this dashboard does not serve", tls, other)
		}
	}
}

// An empty tlsDir is the operator not asking for TLS, and it must SERVE.
// It used to be a hard error — correct while the dashboard was HTTPS
// only, and a total outage now that unchecked is the default state of a
// fresh install.
func TestNoTLSDirectoryServesPlainHTTPRatherThanFailing(t *testing.T) {
	s := New("127.0.0.1", 0, nil)
	addr, err := s.Start("")
	if err != nil {
		t.Fatalf("an unchecked HTTPS box must still serve: %v", err)
	}
	defer s.Shutdown(context.Background())

	if s.Scheme() != "http" {
		t.Errorf("served without TLS but reports scheme %q", s.Scheme())
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("plain HTTP request to a plain HTTP dashboard failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		t.Errorf("plain HTTP dashboard answered %d", resp.StatusCode)
	}
	if !strings.HasPrefix(s.Origin(), "http://") {
		t.Errorf("Origin() = %q on a plaintext dashboard", s.Origin())
	}
}

// The operator is told where the root is so they can install it. A path
// they cannot paste is a path they will not use.
func TestTheRootIsNamedByAnAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	m, err := EnsureTLS(filepath.Join(dir, "tls"), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(m.CACertPath) {
		t.Fatalf("the operator is handed a relative path: %s", m.CACertPath)
	}
	if _, err := os.Stat(m.CACertPath); err != nil {
		t.Fatalf("the path named to the operator does not exist: %v", err)
	}
}
