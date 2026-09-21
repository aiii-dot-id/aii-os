package app

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/certs"
)

// .
// .
// .
// .
type heldIssueManager struct {
	base      *stubManager
	started   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
}

func newHeldIssueManager() *heldIssueManager {
	return &heldIssueManager{
		base:      &stubManager{},
		started:   make(chan struct{}),
		cancelled: make(chan struct{}),
		release:   make(chan struct{}),
	}
}

func (m *heldIssueManager) Certificate() *tls.Certificate { return m.base.Certificate() }
func (m *heldIssueManager) State() certs.State            { return m.base.State() }
func (m *heldIssueManager) DaysLeft() float64             { return m.base.DaysLeft() }
func (m *heldIssueManager) Renew(ctx context.Context) (bool, error) {
	return m.base.Renew(ctx)
}
func (m *heldIssueManager) Ensure(ctx context.Context) (bool, error) {
	close(m.started)
	<-ctx.Done()
	close(m.cancelled)
	<-m.release
	return false, ctx.Err()
}

func assertStopWaitsForHeldIssue(t *testing.T, app *App, held *heldIssueManager) {
	t.Helper()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(held.release) }) }
	stopped := make(chan struct{})
	defer func() {
		release()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
		}
	}()

	select {
	case <-held.started:
	case <-time.After(5 * time.Second):
		t.Fatal("certificate issuance never began")
	}
	go func() {
		app.Stop()
		close(stopped)
	}()
	select {
	case <-held.cancelled:
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
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after certificate issuance exited")
	}
}

// .
// .
// .
// .
// .
// .
func TestStopWaitsForRestartedPublicNameIssuanceBeforeClosingTheStore(t *testing.T) {
	dir := t.TempDir()
	pub := &stubNamePublisher{zone: "example.test"}
	var managers []*stubManager
	cfg := birthNamed(t, dir)
	first := bootNamed(t, cfg, pub, &managers)
	if _, err := first.claimPublicName(); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, first, publicNameIssued)
	first.Stop()

	held := newHeldIssueManager()
	second := New(cfg)
	second.newPublisher = func(Config) (namePublisher, error) { return pub, nil }
	second.newCertManager = func(c certs.Config) (certificateManager, error) {
		held.base.cfg = c
		return held, nil
	}
	if err := startLiveForTest(second); err != nil {
		t.Fatalf("restart: %v", err)
	}
	assertStopWaitsForHeldIssue(t, second, held)
}

// .
// .
// .
func TestStopWaitsForRetriedPublicNameIssuanceBeforeClosingTheStore(t *testing.T) {
	held := newHeldIssueManager()
	app := New(&Config{})
	app.pn.manager = held
	if _, err := app.retryPublicCertificate(); err != nil {
		t.Fatalf("retry: %v", err)
	}
	assertStopWaitsForHeldIssue(t, app, held)
	if _, err := app.retryPublicCertificate(); !errors.Is(err, errPublicNameStopping) {
		t.Fatalf("retry after Stop: %v, want %v", err, errPublicNameStopping)
	}
}
