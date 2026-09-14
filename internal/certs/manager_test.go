package certs

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/fileperm"
)

func managerFor(t *testing.T, ca *fakeCA, pub *stubPublisher, dir, name string) *Manager {
	t.Helper()
	m, err := New(Config{Dir: dir, Name: name, DirectoryURL: ca.url("/dir"), Publisher: pub, HTTPClient: ca.srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// .
// .
// .
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
	// .
	for _, f := range []string{"account.key", "public.key"} {
		if ok, err := fileperm.IsRestrictedToOwner(filepath.Join(dir, f)); err != nil || !ok {
			t.Fatalf("%s is not owner-only (ok=%v err=%v)", f, ok, err)
		}
	}
	// .
	// .
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

// .
// .
func TestRenewsInsideTheSuggestedWindowAndNotOutside(t *testing.T) {
	pub := newStubPublisher()
	ca := newFakeCA(t, pub)
	far := true
	ca.window = func(leaf *x509.Certificate) (time.Time, time.Time) {
		if far {
			return time.Now().Add(30 * 24 * time.Hour), time.Now().Add(31 * 24 * time.Hour)
		}
		// .
		// .
		// .
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
	// .
	// .
	// .
	// .
}

// .
func TestFallsBackToTheLastThirdWithoutARI(t *testing.T) {
	pub := newStubPublisher()
	ca := newFakeCA(t, pub)
	ca.lifetime = 90 * 24 * time.Hour
	now := time.Now()
	// .
	// .
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

// .
// .
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

// .
// .
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

// .
// .
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

// .
// .
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
