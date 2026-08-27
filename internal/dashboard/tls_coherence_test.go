package dashboard

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
)

func copyTLSFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A healthy set is reused untouched — reminting would silently
// invalidate the root the operator installed.
func TestHealthyTLSSetIsReused(t *testing.T) {
	dir := t.TempDir()
	m1, err := EnsureTLS(dir, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !m1.Regenerated {
		t.Fatal("first ensure should mint")
	}
	before, _ := os.ReadFile(m1.LeafCert)
	m2, err := EnsureTLS(dir, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if m2.Regenerated {
		t.Fatal("healthy set was reminted")
	}
	after, _ := os.ReadFile(m2.LeafCert)
	if string(before) != string(after) {
		t.Fatal("healthy leaf was rewritten")
	}
}

// A leaf signed by a root that is no longer the stored root serves
// certificate warnings the operator cannot diagnose — Start would even
// report success (D46). The set check must remint against the root
// actually on disk.
func TestLeafFromForeignRootIsReminted(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	if _, err := EnsureTLS(dirA, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureTLS(dirB, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	// The stored root changes out from under the leaf (a restore from
	// another machine, a half-synced backup).
	copyTLSFile(t, filepath.Join(dirB, "dashboard-ca.crt"), filepath.Join(dirA, "dashboard-ca.crt"))
	copyTLSFile(t, filepath.Join(dirB, "dashboard-ca.key"), filepath.Join(dirA, "dashboard-ca.key"))

	m, err := EnsureTLS(dirA, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Regenerated {
		t.Fatal("a leaf from a foreign root was served as usable")
	}
	leaf := parsePEMCert(m.LeafCert)
	ca := parsePEMCert(m.CACertPath)
	if leaf == nil || ca == nil {
		t.Fatal("reminted material unreadable")
	}
	if err := leaf.CheckSignatureFrom(ca); err != nil {
		t.Fatalf("reminted leaf does not chain to the stored root: %v", err)
	}
}

// A missing or torn leaf key is detected and healed by remint — the
// old check looked only at the cert and left Start to fail on a pair
// the operator could not repair without deleting files by hand.
func TestMissingLeafKeyIsReminted(t *testing.T) {
	dir := t.TempDir()
	m, err := EnsureTLS(dir, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(m.LeafKey); err != nil {
		t.Fatal(err)
	}
	m2, err := EnsureTLS(dir, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !m2.Regenerated {
		t.Fatal("a set with no leaf key was served as usable")
	}
	if _, err := tls.LoadX509KeyPair(m2.LeafCert, m2.LeafKey); err != nil {
		t.Fatalf("healed pair does not load: %v", err)
	}
}

// A root cert beside a key from another generation signs nothing the
// installed root validates: the pair must be reminted, never reused,
// and the old root preserved aside as evidence.
func TestMismatchedRootPairIsRemintedWithAside(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	if _, err := EnsureTLS(dirA, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureTLS(dirB, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	oldRoot, _ := os.ReadFile(filepath.Join(dirA, "dashboard-ca.crt"))
	// dirA keeps its root CERT; its root KEY is replaced by dirB's.
	copyTLSFile(t, filepath.Join(dirB, "dashboard-ca.key"), filepath.Join(dirA, "dashboard-ca.key"))
	// Force the CA path: the leaf must be reminted too.
	if err := os.Remove(filepath.Join(dirA, "dashboard.crt")); err != nil {
		t.Fatal(err)
	}

	m, err := EnsureTLS(dirA, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Regenerated {
		t.Fatal("mismatched root pair was reused")
	}
	leaf := parsePEMCert(m.LeafCert)
	ca := parsePEMCert(m.CACertPath)
	if leaf == nil || ca == nil {
		t.Fatal("reminted material unreadable")
	}
	if err := leaf.CheckSignatureFrom(ca); err != nil {
		t.Fatalf("leaf does not chain to the freshly minted root: %v", err)
	}
	newRoot, _ := os.ReadFile(m.CACertPath)
	if string(newRoot) == string(oldRoot) {
		t.Fatal("root was not reminted")
	}
	entries, _ := os.ReadDir(dirA)
	aside := false
	for _, e := range entries {
		if len(e.Name()) > len("dashboard-ca.crt.replaced-") && e.Name()[:len("dashboard-ca.crt.replaced-")] == "dashboard-ca.crt.replaced-" {
			aside = true
		}
	}
	if !aside {
		t.Fatal("the replaced root was not preserved aside")
	}
}
