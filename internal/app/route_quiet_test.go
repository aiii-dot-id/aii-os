package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// .
// .
// .
// .
// .
func TestAnUnchangedRouteRenewalIsNotNews(t *testing.T) {
	dir := t.TempDir()
	pub := &stubNamePublisher{zone: "example.test"}
	var managers []*stubManager
	app := publicNameApp(t, dir, pub, &managers)
	defer app.Stop()
	if _, err := app.claimPublicName(); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, app, publicNameIssued)

	app.cfgMu.Lock()
	app.cfg.Dashboard.Host = "10.0.0.5"
	app.cfgMu.Unlock()

	buf := logsink.CaptureForTest(t)

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
	name := app.publicNameState().Name
	lines := func() int { return strings.Count(buf.String(), name+" is ") }
	writes := func() int {
		pub.mu.Lock()
		defer pub.mu.Unlock()
		return len(pub.replaced)
	}
	// .
	// .
	// .
	ageTheLease := func() {
		name := app.publicNameState().Name
		pub.mu.Lock()
		set := pub.records[name]
		for i := range set.Records {
			set.Records[i].ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
		}
		pub.records[name] = set
		pub.mu.Unlock()
	}

	if _, err := app.reconcileRoute(context.Background()); err != nil {
		t.Fatalf("the first publish: %v", err)
	}
	if lines() != 1 {
		t.Fatalf("the first publish after a boot must be told exactly once, got %d:\n%s", lines(), buf.String())
	}
	wroteOnce := writes()

	for i := 0; i < 3; i++ {
		ageTheLease()
		if _, err := app.reconcileRoute(context.Background()); err != nil {
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
	if _, err := app.reconcileRoute(context.Background()); err != nil {
		t.Fatalf("the changed publish: %v", err)
	}
	if lines() != 2 {
		t.Fatalf("a changed address must be told: %d line(s)\n%s", lines(), buf.String())
	}
}
