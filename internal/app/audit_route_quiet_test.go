package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/store"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// .
// .
func auditQuietRoute(t *testing.T) (*App, *stubNamePublisher, *logsink.Capture) {
	t.Helper()
	cfg := routeCfg("192.0.2.6", "", routeDirect)
	a := &App{cfg: &cfg}
	p := &stubNamePublisher{zone: "example.test", records: map[string]certs.RecordSet{}}
	a.pn.publisher = p
	a.pn.name = &store.PublicName{Name: "audit.example.test"}
	b := logsink.CaptureForTest(t)
	return a, p, b
}
func auditQuietSet(p *stubNamePublisher, value string, delivered bool, expiry time.Time) {
	p.records["audit.example.test"] = certs.RecordSet{Generation: 4, Managed: true, Delivered: delivered,
		Records: []certs.LeasedRecord{{Type: certs.RecordA, Value: value, ExpiresAt: expiry.UTC().Format(time.RFC3339)}}}
}
func auditQuietReconcile(t *testing.T, a *App) {
	t.Helper()
	if _, err := a.reconcileRoute(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestAuditFirstSameAddressRenewalIsReported(t *testing.T) {
	a, p, b := auditQuietRoute(t)
	auditQuietSet(p, "192.0.2.6", true, time.Now().Add(-time.Minute))
	auditQuietReconcile(t, a)
	if len(p.replaced) != 1 {
		t.Fatal("fixture: no renewal reached publisher")
	}
	if strings.Count(b.String(), "route.") != 1 {
		t.Fatalf("first renewal after boot committed but emitted no first-state record: %q", b.String())
	}
}
func TestAuditSuccessfulRenewalAfterOutageIsReported(t *testing.T) {
	a, p, b := auditQuietRoute(t)
	auditQuietReconcile(t, a)
	b.Reset()
	p.readFail = errors.New("synthetic certd outage")
	if _, err := a.reconcileRoute(context.Background()); err == nil {
		t.Fatal("fixture: outage did not fail")
	}
	if a.pn.route.LastError == "" {
		t.Fatal("fixture: outage not observed")
	}
	p.readFail = nil
	auditQuietSet(p, "192.0.2.6", true, time.Now().Add(-time.Minute))
	auditQuietReconcile(t, a)
	if len(p.replaced) != 2 {
		t.Fatal("fixture: recovery did not renew")
	}
	if strings.Count(b.String(), "route.") != 1 {
		t.Fatalf("outage cleared and renewal committed but recovery was silent: %q", b.String())
	}
}
func TestAuditQuietRenewalsKeepWritesAndChangedAddressNews(t *testing.T) {
	a, p, b := auditQuietRoute(t)
	auditQuietReconcile(t, a)
	for i := 0; i < 3; i++ {
		auditQuietSet(p, "192.0.2.6", true, time.Now().Add(-time.Minute))
		auditQuietReconcile(t, a)
	}
	if len(p.replaced) != 4 || strings.Count(b.String(), "route.") != 1 {
		t.Fatalf("writes=%d log=%q", len(p.replaced), b.String())
	}
	a.cfg.Dashboard.Host = "192.0.2.9"
	auditQuietReconcile(t, a)
	if len(p.replaced) != 5 || strings.Count(b.String(), "route.") != 2 {
		t.Fatalf("changed address writes=%d log=%q", len(p.replaced), b.String())
	}
}
