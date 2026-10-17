package certs

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func managerFor(t *testing.T, ca *fakeCA, pub *stubPublisher, dir, name string) *Manager {
	t.Helper()
	m, err := New(Config{Dir: dir, Name: name, DirectoryURL: ca.url("/dir"), Publisher: pub, HTTPClient: ca.srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// THE CERTIFICATE, END TO END: account, order, DNS-01 through the
// publisher, finalize, store — and the second Manager over the same
// directory places no order at all.
func TestObtainsOnceAndNeverOnStart(t *testing.T) {
	pub := newStubPublisher()
	ca := newFakeCA(t, pub)
	dir := t.TempDir()
	const name = "ui.abcdefghijklmnopqrstuvwxyz.example"
	m := managerFor(t, ca, pub, dir, name)
	if m.Certificate() != nil {
		t.Fatal("a fresh directory has no certificate")
	}
	issued, err := m.Ensure(context.Background())
	if err != nil || !issued {
		t.Fatalf("ensure: issued=%v err=%v", issued, err)
	}
	cert := m.Certificate()
	if cert == nil || cert.Leaf == nil || cert.Leaf.VerifyHostname(name) != nil {
		t.Fatal("no usable certificate for the name after Ensure")
	}
	if pub.published != 1 || pub.removed != 1 || pub.standing() != 0 {
		t.Fatalf("publisher saw published=%d removed=%d standing=%d; want 1, 1, 0", pub.published, pub.removed, pub.standing())
	}
	if m.State().Orders != 1 || ca.issued != 1 {
		t.Fatalf("orders=%d issued=%d, want 1 and 1", m.State().Orders, ca.issued)
	}
	// Keys are the identity's, owner-only.
	for _, f := range []string{"account.key", "public.key"} {
		if ok, err := fileperm.IsRestrictedToOwner(filepath.Join(dir, f)); err != nil || !ok {
			t.Fatalf("%s is not owner-only (ok=%v err=%v)", f, ok, err)
		}
	}
	// A second manager over the same directory — a restart — loads the
	// pair and orders nothing.
	m2 := managerFor(t, ca, pub, dir, name)
	if m2.Certificate() == nil {
		t.Fatal("the stored certificate did not load on restart")
	}
	issued, err = m2.Ensure(context.Background())
	if err != nil || issued {
		t.Fatalf("a restart placed an order: issued=%v err=%v", issued, err)
	}
	if ca.issued != 1 {
		t.Fatalf("THE CA ISSUED %d CERTIFICATES FOR ONE NAME", ca.issued)
	}
}

// RENEWAL FOLLOWS THE CA'S WINDOW: outside it nothing happens, inside it
// one order is placed and the certificate changes.
func TestRenewsInsideTheSuggestedWindowAndNotOutside(t *testing.T) {
	pub := newStubPublisher()
	ca := newFakeCA(t, pub)
	far := true
	ca.window = func(leaf *x509.Certificate) (time.Time, time.Time) {
		if far {
			return time.Now().Add(30 * 24 * time.Hour), time.Now().Add(31 * 24 * time.Hour)
		}
		// A window already past: overdue, so renewal is immediate. A window
		// spanning now would let the manager pick a later point inside it,
		// which is correct and not what this test measures.
		return time.Now().Add(-2 * time.Hour), time.Now().Add(-time.Hour)
	}
	m := managerFor(t, ca, pub, t.TempDir(), "ui.renew.example")
	if _, err := m.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := m.Certificate().Leaf.SerialNumber
	if renewed, err := m.Renew(context.Background()); err != nil || renewed {
		t.Fatalf("renewed outside the window: renewed=%v err=%v", renewed, err)
	}
	if w := m.State().Window; w == nil || w.Start.Before(time.Now().Add(29*24*time.Hour)) {
		t.Fatalf("the window was not taken from the CA: %+v", w)
	}
	far = false
	renewed, err := m.Renew(context.Background())
	if err != nil || !renewed {
		t.Fatalf("did not renew inside the window: renewed=%v err=%v", renewed, err)
	}
	if m.Certificate().Leaf.SerialNumber.Cmp(first) == 0 {
		t.Fatal("renewal kept the old certificate")
	}
	if ca.issued != 2 {
		t.Fatalf("issued %d, want 2", ca.issued)
	}
	// Inside the window but just renewed: the new window is ahead again
	// only if the CA says so; here it still says now, so a second call
	// would renew again — which is why the runtime calls Renew daily,
	// not in a loop. The point held here: one call, one order.
}

// WITHOUT ARI the manager renews in the last third of the lifetime.
func TestFallsBackToTheLastThirdWithoutARI(t *testing.T) {
	pub := newStubPublisher()
	ca := newFakeCA(t, pub)
	ca.lifetime = 90 * 24 * time.Hour
	now := time.Now()
	// An all-zero random source puts the chosen point at the window's
	// start, so the start itself is what is asserted.
	m, err := New(Config{Dir: t.TempDir(), Name: "ui.fallback.example", DirectoryURL: ca.url("/dir"), Publisher: pub, HTTPClient: ca.srv.Client(),
		Now: func() time.Time { return now }, Rand: zeroReader{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if renewed, err := m.Renew(context.Background()); err != nil || renewed {
		t.Fatalf("renewed at day 0: %v %v", renewed, err)
	}
	at := m.State().RenewAt
	leaf := m.Certificate().Leaf
	wantStart := leaf.NotBefore.Add(leaf.NotAfter.Sub(leaf.NotBefore) * 2 / 3)
	if d := at.Sub(wantStart); d < -time.Minute || d > time.Minute {
		t.Fatalf("renew_at %s is not the start of the last third %s", at, wantStart)
	}
	now = at.Add(time.Minute)
	if renewed, err := m.Renew(context.Background()); err != nil || !renewed {
		t.Fatalf("did not renew at renew_at: %v %v", renewed, err)
	}
}

// A CHANGED NAME MEANS THE STORED CERTIFICATE IS NOT USED, and a torn
// pair is refused rather than served.
func TestStoredMaterialIsRefusedWhenItDoesNotFit(t *testing.T) {
	pub := newStubPublisher()
	ca := newFakeCA(t, pub)
	dir := t.TempDir()
	m := managerFor(t, ca, pub, dir, "ui.one.example")
	if _, err := m.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	other := managerFor(t, ca, pub, dir, "ui.two.example")
	if other.Certificate() != nil {
		t.Fatal("a certificate for another name was served")
	}
	if issued, err := other.Ensure(context.Background()); err != nil || !issued {
		t.Fatalf("no certificate for the new name: %v %v", issued, err)
	}
	if err := os.Remove(filepath.Join(dir, "public.key")); err != nil {
		t.Fatal(err)
	}
	torn := managerFor(t, ca, pub, dir, "ui.two.example")
	if torn.Certificate() != nil {
		t.Fatal("a certificate without its key was served")
	}
}

// RFC 9773 §4.1's example: the identifier is the AKI and the serial's
// INTEGER value bytes, base64url, joined by a dot.
func TestCertIDMatchesTheRFCExample(t *testing.T) {
	aki, _ := base64.RawURLEncoding.DecodeString("aYhba4dGQEHhs3uEe6CuLN4ByNQ")
	serial := new(big.Int).SetBytes([]byte{0x00, 0x87, 0x65, 0x43, 0x21})
	leaf := &x509.Certificate{AuthorityKeyId: aki, SerialNumber: serial}
	id, err := CertID(leaf)
	if err != nil {
		t.Fatal(err)
	}
	if id != "aYhba4dGQEHhs3uEe6CuLN4ByNQ.AIdlQyE" {
		t.Fatalf("CertID = %q", id)
	}
}

// THE PUBLISHER IS REQUIRED and its failure is the manager's failure,
// reported in the state, with no certificate and the old one kept.
func TestAPublisherFailureLeavesTheOldCertificate(t *testing.T) {
	pub := newStubPublisher()
	ca := newFakeCA(t, pub)
	m := managerFor(t, ca, pub, t.TempDir(), "ui.keep.example")
	if _, err := m.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := m.Certificate()
	m.cfg.Publisher = failingPublisher{}
	ca.window = func(*x509.Certificate) (time.Time, time.Time) {
		return time.Now().Add(-2 * time.Hour), time.Now().Add(-time.Hour)
	}
	if renewed, err := m.Renew(context.Background()); err == nil || renewed {
		t.Fatalf("a failed publish renewed: %v %v", renewed, err)
	}
	if m.Certificate() != before {
		t.Fatal("the old certificate was dropped on a failed renewal")
	}
	if m.State().LastError == "" {
		t.Fatal("the failure was not recorded")
	}
	if _, err := New(Config{Dir: t.TempDir(), Name: "x", DirectoryURL: ca.url("/dir")}); err != nil {
		t.Fatal(err)
	}
}

type failingPublisher struct{}

func (failingPublisher) Publish(context.Context, string, string) error {
	return context.DeadlineExceeded
}
func (failingPublisher) Unpublish(context.Context, string, string) error { return nil }

// zeroReader is a random source of zeros: every choice lands at the
// start of its range.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// heldPublisher holds a challenge's publication until released: an order
// stopped in the middle, as a slow DNS service or CA holds one.
type heldPublisher struct {
	*stubPublisher
	entered chan struct{}
	release chan struct{}
}

func (p *heldPublisher) Publish(ctx context.Context, name, txt string) error {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return p.stubPublisher.Publish(ctx, name, txt)
}

// AN ORDER IN FLIGHT DOES NOT STOP THE CERTIFICATE BEING READ. Ensure and
// Renew held the manager's lock across the whole ACME order, and every TLS
// handshake for the public name reads the certificate under that lock: for
// as long as an order took (tens of seconds, up to its ten-minute budget)
// the name took no connection, and Settings, which reads the state, hung
// behind it. Orders wait on their own slot now, one at a time, and the
// lock covers only what the manager knows.
func TestAnOrderInFlightDoesNotHoldTheCertificate(t *testing.T) {
	pub := newStubPublisher()
	ca := newFakeCA(t, pub)
	held := &heldPublisher{stubPublisher: pub, entered: make(chan struct{}, 1), release: make(chan struct{})}
	const name = "ui.abcdefghijklmnopqrstuvwxyz.example"
	m, err := New(Config{Dir: t.TempDir(), Name: name, DirectoryURL: ca.url("/dir"), Publisher: held, HTTPClient: ca.srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	first := make(chan error, 1)
	go func() {
		_, err := m.Ensure(context.Background())
		first <- err
	}()
	select {
	case <-held.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the order never reached its challenge")
	}

	read := make(chan struct{})
	go func() {
		_, _, _ = m.Certificate(), m.State(), m.DaysLeft()
		close(read)
	}()
	select {
	case <-read:
	case <-time.After(2 * time.Second):
		t.Fatal("reading the certificate waited on the order in flight")
	}

	// A second order waits its turn, and stops waiting when its caller does.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := m.Ensure(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a second order did not wait its turn: %v", err)
	}
	second := make(chan error, 1)
	go func() {
		issued, err := m.Ensure(context.Background())
		if issued {
			err = errors.New("a second certificate was ordered")
		}
		second <- err
	}()

	close(held.release)
	if err := <-first; err != nil {
		t.Fatalf("the order: %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("the order that waited: %v", err)
	}
	if ca.issued != 1 || m.Certificate() == nil {
		t.Fatalf("the CA issued %d certificate(s); held %v", ca.issued, m.Certificate() != nil)
	}
}

// refusingPublisher is a DNS service that refuses every challenge.
type refusingPublisher struct{ *stubPublisher }

func (refusingPublisher) Publish(context.Context, string, string) error {
	return errors.New("the DNS service refused the challenge")
}

// WHAT THE MANAGER KNOWS IS SAVED, OR IT SAYS SO. Every save of its state
// was discarded, so a manager that could not write it carried on silently
// and a restart read an older state (a renewal point already past orders
// again at once). A failed order now says its attempt was not saved
// either; a certificate that was obtained is not called a failure, and
// that its state was not saved is said with the cause.
func TestAStateThatCannotBeSavedIsNotSilent(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	pub := newStubPublisher()
	ca := newFakeCA(t, pub)
	dir := t.TempDir()
	// A directory where the state file goes: no write replaces it, root's included.
	if err := os.MkdirAll(filepath.Join(dir, stateFile, "held"), 0o700); err != nil {
		t.Fatal(err)
	}
	const name = "ui.abcdefghijklmnopqrstuvwxyz.example"
	refused, err := New(Config{Dir: dir, Name: name, DirectoryURL: ca.url("/dir"), Publisher: refusingPublisher{pub}, HTTPClient: ca.srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := refused.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "refused the challenge") || !strings.Contains(err.Error(), "not saved") {
		t.Fatalf("a failed order whose attempt was not saved said: %v", err)
	}
	m := managerFor(t, ca, pub, dir, name)
	if issued, err := m.Ensure(context.Background()); err != nil || !issued {
		t.Fatalf("a certificate that was obtained was reported as: issued=%v err=%v", issued, err)
	}
	if !logs.Contains("was not saved") {
		t.Fatal("a state that could not be saved went unsaid")
	}
}
