package publicname

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/publicname/publicnametest"
	"github.com/aiii-dot-id/aii-os/internal/store"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// Exercise the production reconciler directly with the existing in-memory
// publisher. No identity birth, cache, credentials, network, or workers.
func auditQuietRoute(t *testing.T) (*Runtime, *settingsBox, *publicnametest.Publisher, *logsink.Capture) {
	t.Helper()
	a, box := fixtureRuntime(routeCfg("192.0.2.6", "", RouteDirect))
	p := &publicnametest.Publisher{Zone: "example.test", Records: map[string]certs.RecordSet{}}
	a.publisher = p
	a.name = &store.PublicName{Name: "audit.example.test"}
	b := logsink.CaptureForTest(t)
	return a, box, p, b
}
func auditQuietSet(p *publicnametest.Publisher, value string, delivered bool, expiry time.Time) {
	p.Records["audit.example.test"] = certs.RecordSet{Generation: 4, Managed: true, Delivered: delivered,
		Records: []certs.LeasedRecord{{Type: certs.RecordA, Value: value, ExpiresAt: expiry.UTC().Format(time.RFC3339)}}}
}
func auditQuietReconcile(t *testing.T, a *Runtime) {
	t.Helper()
	if _, err := a.reconcileRoute(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestAuditFirstSameAddressRenewalIsReported(t *testing.T) {
	a, _, p, b := auditQuietRoute(t)
	auditQuietSet(p, "192.0.2.6", true, time.Now().Add(-time.Minute))
	auditQuietReconcile(t, a)
	if len(p.Replaced) != 1 {
		t.Fatal("fixture: no renewal reached publisher")
	}
	if strings.Count(b.String(), "route.") != 1 {
		t.Fatalf("first renewal after boot committed but emitted no first-state record: %q", b.String())
	}
}
func TestAuditSuccessfulRenewalAfterOutageIsReported(t *testing.T) {
	a, _, p, b := auditQuietRoute(t)
	auditQuietReconcile(t, a)
	b.Reset()
	p.ReadFail = errors.New("synthetic certd outage")
	if _, err := a.reconcileRoute(context.Background()); err == nil {
		t.Fatal("fixture: outage did not fail")
	}
	if a.route.LastError == "" {
		t.Fatal("fixture: outage not observed")
	}
	p.ReadFail = nil
	auditQuietSet(p, "192.0.2.6", true, time.Now().Add(-time.Minute))
	auditQuietReconcile(t, a)
	if len(p.Replaced) != 2 {
		t.Fatal("fixture: recovery did not renew")
	}
	if strings.Count(b.String(), "route.") != 1 {
		t.Fatalf("outage cleared and renewal committed but recovery was silent: %q", b.String())
	}
}
func TestAuditQuietRenewalsKeepWritesAndChangedAddressNews(t *testing.T) {
	a, box, p, b := auditQuietRoute(t)
	auditQuietReconcile(t, a)
	for i := 0; i < 3; i++ {
		auditQuietSet(p, "192.0.2.6", true, time.Now().Add(-time.Minute))
		auditQuietReconcile(t, a)
	}
	if len(p.Replaced) != 4 || strings.Count(b.String(), "route.") != 1 {
		t.Fatalf("writes=%d log=%q", len(p.Replaced), b.String())
	}
	box.set(func(s *Settings) { s.Host = "192.0.2.9" })
	auditQuietReconcile(t, a)
	if len(p.Replaced) != 5 || strings.Count(b.String(), "route.") != 2 {
		t.Fatalf("changed address writes=%d log=%q", len(p.Replaced), b.String())
	}
}

// A ROUTE'S FAILURE IS THE ROUTE'S TO REPORT, AND THE NEXT LOOK THAT
// SUCCEEDS CLEARS IT. The route owner wrote its refusals into the one error
// field issuance and renewal own, which the card prints as "the certificate
// could not be obtained": a route failure replaced a certificate's failure
// under that label, a recovered route left it standing, and with a
// certificate issued it was shown nowhere. The state carries the route's
// error as its own.
func TestARouteFailureIsTheRoutesToReportAndIsClearedWhenTheRouteRecovers(t *testing.T) {
	a, box, p, _ := auditQuietRoute(t)
	const certificate = "order: the CA refused the account"
	a.mu.Lock()
	a.status, a.lastError = StatusFailing, certificate
	a.mu.Unlock()

	p.ReadFail = errors.New("synthetic certd outage")
	if _, err := a.reconcileRoute(context.Background()); err == nil {
		t.Fatal("fixture: the outage did not fail the pass")
	}
	st := a.State()
	if !strings.Contains(st.RouteError, "synthetic certd outage") {
		t.Fatalf("A ROUTE FAILURE IS NOT IN THE STATE THE CARD READS: route_error %q", st.RouteError)
	}
	if st.LastError != certificate {
		t.Fatalf("A ROUTE FAILURE REPLACED THE CERTIFICATE'S OWN ERROR: last_error %q", st.LastError)
	}

	p.ReadFail = nil
	auditQuietReconcile(t, a)
	st = a.State()
	if st.RouteError != "" {
		t.Fatalf("the route recovered and its error still stands: %q", st.RouteError)
	}
	if st.Address != "192.0.2.6" {
		t.Fatalf("the state does not say what the service answered the route is: %q", st.Address)
	}
	if st.LastError != certificate {
		t.Fatalf("a recovered route cleared the certificate's error: %q", st.LastError)
	}

	// A route this host refuses to work out is reported the same way,
	// with a certificate issued.
	a.mu.Lock()
	a.status, a.lastError = StatusIssued, ""
	a.mu.Unlock()
	box.set(func(s *Settings) { s.Certificate.RouteMode = "rely" })
	if _, err := a.reconcileRoute(context.Background()); err == nil {
		t.Fatal("fixture: an unknown route mode was not refused")
	}
	if st := a.State(); st.Status != StatusIssued || !strings.Contains(st.RouteError, "rely") || st.LastError != "" {
		t.Fatalf("a refused route under an issued certificate: status %q route_error %q last_error %q", st.Status, st.RouteError, st.LastError)
	}
}
