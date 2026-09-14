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

// .
// .
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

// .
// .
// .
// .
func TestLeafFromForeignRootIsReminted(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	if _, err := EnsureTLS(dirA, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureTLS(dirB, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	// .
	// .
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

// .
// .
// .
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

// .
// .
// .
func TestMismatchedRootPairIsRemintedWithAside(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	if _, err := EnsureTLS(dirA, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureTLS(dirB, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	oldRoot, _ := os.ReadFile(filepath.Join(dirA, "dashboard-ca.crt"))
	// .
	copyTLSFile(t, filepath.Join(dirB, "dashboard-ca.key"), filepath.Join(dirA, "dashboard-ca.key"))
	// .
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
