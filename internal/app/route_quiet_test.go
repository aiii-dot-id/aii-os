package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/publicname"
	"github.com/aiii-dot-id/aii-os/internal/publicname/publicnametest"
)

// A ROUTE RENEWAL THAT CHANGED NOTHING IS NOT NEWS. reconcileRoute runs
// on a lease cadence, and on two live identities it answered with the
// same address 85 times out of 85 in a day — 15% of an idle log spent
// telling the reader what they already knew. The publisher must keep
// renewing; only the telling stops.
func TestAnUnchangedRouteRenewalIsNotNews(t *testing.T) {
	dir := t.TempDir()
	pub := &publicnametest.Publisher{Zone: "example.test"}
	var managers []*publicnametest.Manager
	app := publicNameApp(t, dir, pub, &managers)
	defer stopApp(t, app)
	if _, err := app.pn.Claim(); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, app, publicname.StatusIssued)

	app.cfgMu.Lock()
	app.cfg.Dashboard.Host = "10.0.0.5"
	app.cfgMu.Unlock()

	buf := logsink.CaptureForTest(t)

	name := app.pn.State().Name
	lines := func() int { return strings.Count(buf.String(), name+" is ") }
	writes := func() int {
		pub.Mu.Lock()
		defer pub.Mu.Unlock()
		return len(pub.Replaced)
	}
	// The reconciler republishes when the lease is near its end, not on
	// every call — so a renewal is driven by ageing the lease the
	// service handed back, which is what the cadence waits for.
	ageTheLease := func() {
		name := app.pn.State().Name
		pub.Mu.Lock()
		set := pub.Records[name]
		for i := range set.Records {
			set.Records[i].ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
		}
		pub.Records[name] = set
		pub.Mu.Unlock()
	}

	if _, err := app.pn.ReconcileForTest(context.Background()); err != nil {
		t.Fatalf("the first publish: %v", err)
	}
	if lines() != 1 {
		t.Fatalf("the first publish after a boot must be told exactly once, got %d:\n%s", lines(), buf.String())
	}
	wroteOnce := writes()

	for i := 0; i < 3; i++ {
		ageTheLease()
		if _, err := app.pn.ReconcileForTest(context.Background()); err != nil {
			t.Fatalf("renewal %d: %v", i, err)
		}
	}
	if writes() <= wroteOnce {
		t.Fatalf("fixture: the renewals did not reach the publisher (%d writes), so silence proves nothing", writes())
	}
	if lines() != 1 {
		t.Fatalf("renewals that changed nothing were told anyway: %d line(s)\n%s", lines(), buf.String())
	}

	app.cfgMu.Lock()
	app.cfg.Dashboard.Host = "10.0.0.9"
	app.cfgMu.Unlock()
	ageTheLease()
	if _, err := app.pn.ReconcileForTest(context.Background()); err != nil {
		t.Fatalf("the changed publish: %v", err)
	}
	if lines() != 2 {
		t.Fatalf("a changed address must be told: %d line(s)\n%s", lines(), buf.String())
	}
}
