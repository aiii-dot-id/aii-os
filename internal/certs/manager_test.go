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

// A CHANGED DIRECTORY KEEPS THE HELD CERTIFICATE SERVING UNTIL THE NEW CA
// ISSUES. The state kept the directory beside the account's URL and nothing
// compared it with the configured one: after certificate.acme_directory
// changed, every request to the new CA was signed under the old CA's
// account and refused. What the old CA issued is valid until it expires
// whatever directory is configured, so it is served throughout: Ensure
// and the next renewal pass each order at the new CA under an
// account made there, a failed order leaves the old certificate in
// service, and a directory set back finds its own account and certificate
// and orders nothing. The order is owed at once (OrderOwed): a start, or
// the card's Retry, places it through Ensure, and so does the daily pass.
func TestAChangedDirectoryKeepsTheHeldCertificateServingUntilTheNewCAIssues(t *testing.T) {
	pub := newStubPublisher()
	first, second, down := newFakeCA(t, pub), newFakeCA(t, pub), newFakeCA(t, pub)
	down.srv.Close() // a CA that cannot be reached
	dir := t.TempDir()
	const name = "ui.abcdefghijklmnopqrstuvwxyz.example"
	m := managerFor(t, first, pub, dir, name)
	if _, err := m.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	held := m.Certificate().Leaf.SerialNumber
	if got := m.State().AccountURL; !strings.HasPrefix(got, first.url("/acct/")) {
		t.Fatalf("the account was not made at the first CA: %q", got)
	}
	// servesTheFirst holds that what the manager serves is still the first
	// CA's certificate, and that its state still names the first CA.
	servesTheFirst := func(m *Manager, when string) {
		t.Helper()
		cert := m.Certificate()
		if cert == nil {
			t.Fatalf("%s: THE NAME HAS NO CERTIFICATE — the first CA's is valid and was taken out of service", when)
		}
		if cert.Leaf.SerialNumber.Cmp(held) != 0 || cert.Leaf.CheckSignatureFrom(first.caCert) != nil {
			t.Fatalf("%s: the certificate served is not the one the first CA issued", when)
		}
		if st := m.State(); st.DirectoryURL != first.url("/dir") || !strings.HasPrefix(st.AccountURL, first.url("/acct/")) {
			t.Fatalf("%s: the state no longer names the CA of the certificate held: directory %q account %q", when, st.DirectoryURL, st.AccountURL)
		}
	}
	ordersAt := func(ca *fakeCA) int {
		ca.mu.Lock()
		defer ca.mu.Unlock()
		return len(ca.orders)
	}

	// The directory changes to a CA that is down. An order is owed at
	// once; the start's Ensure and the pass each try it and fail, and the
	// first CA's certificate serves.
	unreachable := managerFor(t, down, pub, dir, name)
	servesTheFirst(unreachable, "after the change")
	if !unreachable.OrderOwed() {
		t.Fatal("no order is owed under the new directory")
	}
	if issued, err := unreachable.Ensure(context.Background()); err == nil || issued {
		t.Fatalf("a start under the new directory did not try it at once: issued=%v err=%v", issued, err)
	}
	servesTheFirst(unreachable, "after a start that could not reach the new CA")
	if at := unreachable.State().RenewAt; at.After(time.Now()) {
		t.Fatalf("renewal is not due under the new directory: renew_at %s", at)
	}
	if renewed, err := unreachable.Renew(context.Background()); err == nil || renewed {
		t.Fatalf("the renewal pass did not try the new directory: renewed=%v err=%v", renewed, err)
	}
	servesTheFirst(unreachable, "after a CA that cannot be reached")
	if unreachable.State().LastError == "" {
		t.Fatal("the failed attempt at the new directory was not recorded")
	}

	// The directory is set back. Nothing is ordered, and the first CA's
	// account is still the one in use.
	back := managerFor(t, first, pub, dir, name)
	servesTheFirst(back, "after the directory was set back")
	if at := back.State().RenewAt; !at.After(time.Now()) {
		t.Fatalf("a failed attempt elsewhere left renewal due at the first CA: renew_at %s", at)
	}
	if renewed, err := back.Renew(context.Background()); err != nil || renewed {
		t.Fatalf("setting the directory back placed an order: renewed=%v err=%v", renewed, err)
	}
	if issued, err := back.Ensure(context.Background()); err != nil || issued || back.OrderOwed() {
		t.Fatalf("setting the directory back owes an order: issued=%v err=%v", issued, err)
	}
	if n := ordersAt(first); n != 1 || first.issued != 1 {
		t.Fatalf("the first CA saw %d order(s) and issued %d; want 1 and 1", n, first.issued)
	}

	// The directory changes to a CA that answers, and its first order fails.
	moved := managerFor(t, second, pub, dir, name)
	servesTheFirst(moved, "after the second change")
	moved.cfg.Publisher = failingPublisher{}
	if renewed, err := moved.Renew(context.Background()); err == nil || renewed {
		t.Fatalf("a failed publish renewed: renewed=%v err=%v", renewed, err)
	}
	servesTheFirst(moved, "after a failed order at the new CA")
	second.mu.Lock()
	for id, o := range second.orders {
		if !strings.HasPrefix(o.account, second.url("/acct/")) {
			t.Errorf("order %s at the second CA was signed under an account it never made: %q", id, o.account)
		}
	}
	second.mu.Unlock()
	if n := ordersAt(second); n != 1 || second.issued != 0 {
		t.Fatalf("the second CA saw %d order(s) and issued %d; want 1 and 0", n, second.issued)
	}

	// The card's Retry (Ensure) succeeds: only now is the pair replaced,
	// and nothing more is owed.
	moved.cfg.Publisher = pub
	if issued, err := moved.Ensure(context.Background()); err != nil || !issued {
		t.Fatalf("no certificate from the second CA: issued=%v err=%v", issued, err)
	}
	if moved.OrderOwed() {
		t.Fatal("an order is still owed once the second CA has issued")
	}
	if err := moved.Certificate().Leaf.CheckSignatureFrom(second.caCert); err != nil {
		t.Fatalf("the certificate served is not the second CA's: %v", err)
	}
	if st := moved.State(); st.DirectoryURL != second.url("/dir") || !strings.HasPrefix(st.AccountURL, second.url("/acct/")) {
		t.Fatalf("the state does not name the second CA: directory %q account %q", st.DirectoryURL, st.AccountURL)
	}
	// A restart under the same directory is a restart: nothing is ordered.
	again := managerFor(t, second, pub, dir, name)
	if again.Certificate() == nil || again.Certificate().Leaf.CheckSignatureFrom(second.caCert) != nil {
		t.Fatal("the second CA's certificate did not load on restart")
	}
	if renewed, err := again.Renew(context.Background()); err != nil || renewed {
		t.Fatalf("a restart under the same directory placed an order: renewed=%v err=%v", renewed, err)
	}
	if issued, err := again.Ensure(context.Background()); err != nil || issued {
		t.Fatalf("a restart's Ensure under the same directory placed an order: issued=%v err=%v", issued, err)
	}
	if n := ordersAt(second); n != 2 || first.issued != 1 || second.issued != 1 {
		t.Fatalf("orders at the second CA=%d, issued first=%d second=%d; want 2, 1, 1", n, first.issued, second.issued)
	}
}

func TestAnOrderAtAChangedDirectoryIsSignedUnderAnAccountMadeThere(t *testing.T) {
	pub := newStubPublisher()
	first, second := newFakeCA(t, pub), newFakeCA(t, pub)
	dir := t.TempDir()
	const name = "ui.abcdefghijklmnopqrstuvwxyz.example"
	if _, err := managerFor(t, first, pub, dir, name).Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"public.crt", "public.key"} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	m := managerFor(t, second, pub, dir, name)
	_, err := m.Ensure(context.Background())
	second.mu.Lock()
	defer second.mu.Unlock()
	if len(second.orders) == 0 {
		t.Fatalf("no order reached the second CA: %v", err)
	}
	for id, o := range second.orders {
		if !strings.HasPrefix(o.account, second.url("/acct/")) {
			t.Errorf("order %s at the second CA was signed under an account it never made: %q (ensure: %v)", id, o.account, err)
		}
	}
	if err != nil {
		t.Fatalf("the order at the second CA failed: %v", err)
	}
	if st := m.State(); st.DirectoryURL != second.url("/dir") || !strings.HasPrefix(st.AccountURL, second.url("/acct/")) {
		t.Fatalf("the state does not name the second CA: directory %q account %q", st.DirectoryURL, st.AccountURL)
	}
}

// A TRAILING SLASH IS THE SAME DIRECTORY. The directory was compared as a
// string, so the operator's same URL written with a slash at its end was
// another CA: an order owed, its account registered again, one duplicate
// certificate spent. The certificate held stays the configured CA's, and
// nothing is ordered.
func TestATrailingSlashIsTheSameDirectory(t *testing.T) {
	pub := newStubPublisher()
	ca := newFakeCA(t, pub)
	dir := t.TempDir()
	const name = "ui.abcdefghijklmnopqrstuvwxyz.example"
	if _, err := managerFor(t, ca, pub, dir, name).Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err := New(Config{Dir: dir, Name: name, DirectoryURL: ca.url("/dir") + "/", Publisher: pub, HTTPClient: ca.srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if m.OrderOwed() {
		t.Fatal("the same directory with a trailing slash owes an order")
	}
	if at := m.State().RenewAt; !at.After(time.Now()) {
		t.Fatalf("renewal is due under the same directory with a trailing slash: renew_at %s", at)
	}
	if issued, err := m.Ensure(context.Background()); err != nil || issued {
		t.Fatalf("the same directory with a trailing slash placed an order: issued=%v err=%v", issued, err)
	}
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if len(ca.orders) != 1 || ca.issued != 1 {
		t.Fatalf("the CA saw %d order(s) and issued %d; want 1 and 1", len(ca.orders), ca.issued)
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

// A CERTIFICATE THAT CANNOT BE READ IS NOT AN ABSENT ONE. A start on a disk
// that does not answer took the stored pair for absent and ordered a new
// certificate, and a start that is tried again by itself on that disk did
// so at each try: every public name shares one registered domain's weekly
// count of new certificates. Nothing is ordered or renewed over a pair that
// could not be read; once it reads (here, once nothing stands in its way and
// it is absent), the order is owed as before.
func TestACertificateThatCannotBeReadOwesNoOrder(t *testing.T) {
	pub := newStubPublisher()
	ca := newFakeCA(t, pub)
	dir := t.TempDir()
	const name = "ui.unreadable.example"
	// A directory where the certificate's file belongs: its read fails, and
	// not as a file that does not exist.
	if err := os.MkdirAll(filepath.Join(dir, "public.crt"), 0o700); err != nil {
		t.Fatal(err)
	}
	m := managerFor(t, ca, pub, dir, name)
	if m.OrderOwed() {
		t.Fatal("A CERTIFICATE THAT COULD NOT BE READ OWES AN ORDER: a failing disk would order one at every start")
	}
	for _, step := range []struct {
		what string
		run  func(context.Context) (bool, error)
	}{{"Ensure", m.Ensure}, {"Renew", m.Renew}} {
		if did, err := step.run(context.Background()); err != nil || did {
			t.Fatalf("%s over an unreadable certificate: did=%v err=%v; nothing is ordered", step.what, did, err)
		}
	}
	if len(ca.orders) != 0 {
		t.Fatalf("%d order(s) were placed over a certificate that could not be read", len(ca.orders))
	}
	// The disk answers again, and what it holds is no certificate: the order
	// is owed and placed.
	if err := os.Remove(filepath.Join(dir, "public.crt")); err != nil {
		t.Fatal(err)
	}
	if issued, err := m.Ensure(context.Background()); err != nil || !issued {
		t.Fatalf("once the certificate's place reads as empty, the order is owed: issued=%v err=%v", issued, err)
	}
}
