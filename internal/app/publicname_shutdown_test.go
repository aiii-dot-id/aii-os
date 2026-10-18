package app

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/publicname"
	"github.com/aiii-dot-id/aii-os/internal/publicname/publicnametest"
)

func assertStopWaitsForHeldIssue(t *testing.T, app *App, held *publicnametest.HeldIssueManager) {
	t.Helper()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(held.Release) }) }
	stopped := make(chan struct{}) // the stop's, once it has begun
	defer func() {
		release()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
		}
	}()

	select {
	case <-held.Started:
	case <-time.After(5 * time.Second):
		t.Fatal("certificate issuance never began")
	}
	stop := stopAppAside(app)
	stopped = stop.done
	select {
	case <-held.Cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not cancel certificate issuance")
	}
	select {
	case <-stopped:
		t.Fatal("Stop returned while certificate issuance was still running")
	case <-time.After(100 * time.Millisecond):
	}

	release()
	select {
	case <-stopped:
		stop.check(t)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after certificate issuance exited")
	}
}

// THE STORE OUTLIVES PUBLIC-NAME ISSUANCE. A restart finds the name in the
// record and starts certificate issuance in the background. Stop cancels that
// request, then waits for the goroutine to return before closeLiveResources
// can close and clear a.store. The old raw go statement let Stop return while
// this held call was still live; its subsequent BroadcastStatus read raced the
// store teardown.
func TestStopWaitsForRestartedPublicNameIssuanceBeforeClosingTheStore(t *testing.T) {
	dir := t.TempDir()
	pub := &publicnametest.Publisher{Zone: "example.test"}
	var managers []*publicnametest.Manager
	cfg := birthNamed(t, dir)
	first := bootNamed(t, cfg, pub, &managers)
	if _, err := first.pn.Claim(); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, first, publicname.StatusIssued)
	stopApp(t, first)

	held := publicnametest.NewHeldIssueManager()
	second := New(cfg)
	second.pn.NewPublisher = func(publicname.Settings) (publicname.NamePublisher, error) { return pub, nil }
	second.pn.NewCertManager = publicnametest.Held[publicname.CertificateManager](held)
	if err := startLiveForTest(second); err != nil {
		t.Fatalf("restart: %v", err)
	}
	assertStopWaitsForHeldIssue(t, second, held)
}

// A manual retry uses the same lifecycle owner as boot issuance. This is a
// separate launch site: keeping only the restart covered would allow the
// Settings retry to reintroduce the same store-teardown race.
func TestStopWaitsForRetriedPublicNameIssuanceBeforeClosingTheStore(t *testing.T) {
	held := publicnametest.NewHeldIssueManager()
	app := New(&Config{})
	app.pn.ManageForTest(held)
	if _, err := app.pn.Retry(); err != nil {
		t.Fatalf("retry: %v", err)
	}
	assertStopWaitsForHeldIssue(t, app, held)
	if _, err := app.pn.Retry(); !errors.Is(err, errStopping) || err.Error() != "public-name issuance is unavailable: the identity is stopping" {
		t.Fatalf("retry after Stop: %v, want the public name's refusal around %v", err, errStopping)
	}
}
