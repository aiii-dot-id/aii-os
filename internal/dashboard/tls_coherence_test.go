package dashboard

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	m1, err := ensureTLS(dir, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !m1.Regenerated {
		t.Fatal("first ensure should mint")
	}
	before, _ := os.ReadFile(m1.LeafCert)
	m2, err := ensureTLS(dir, "127.0.0.1")
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

func TestLeafFromForeignRootIsReminted(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	if _, err := ensureTLS(dirA, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureTLS(dirB, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	// The stored root changes out from under the leaf (a restore from
	// another machine, a half-synced backup).
	copyTLSFile(t, filepath.Join(dirB, "dashboard-ca.crt"), filepath.Join(dirA, "dashboard-ca.crt"))
	copyTLSFile(t, filepath.Join(dirB, "dashboard-ca.key"), filepath.Join(dirA, "dashboard-ca.key"))

	m, err := ensureTLS(dirA, "127.0.0.1")
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
	m, err := ensureTLS(dir, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(m.LeafKey); err != nil {
		t.Fatal(err)
	}
	m2, err := ensureTLS(dir, "127.0.0.1")
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
	if _, err := ensureTLS(dirA, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureTLS(dirB, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	oldRoot, _ := os.ReadFile(filepath.Join(dirA, "dashboard-ca.crt"))
	// dirA keeps its root CERT; its root KEY is replaced by dirB's.
	copyTLSFile(t, filepath.Join(dirB, "dashboard-ca.key"), filepath.Join(dirA, "dashboard-ca.key"))
	// Force the CA path: the leaf must be reminted too.
	if err := os.Remove(filepath.Join(dirA, "dashboard.crt")); err != nil {
		t.Fatal(err)
	}

	m, err := ensureTLS(dirA, "127.0.0.1")
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

// A leaf key from another set beside the leaf: both halves parse, and they
// do not belong together. usable() must say so, and the leaf is reminted
// against the root on disk, which is kept.
func TestALeafKeyFromAnotherSetIsReminted(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	mA, err := ensureTLS(dirA, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	mB, err := ensureTLS(dirB, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := os.ReadFile(mA.CACertPath)
	copyTLSFile(t, mB.LeafKey, mA.LeafKey)

	m, err := ensureTLS(dirA, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Regenerated {
		t.Fatal("a leaf beside another set's key was served as usable")
	}
	if _, err := tls.LoadX509KeyPair(m.LeafCert, m.LeafKey); err != nil {
		t.Fatalf("reminted pair does not load: %v", err)
	}
	if after, _ := os.ReadFile(m.CACertPath); string(after) != string(root) {
		t.Fatal("the installed root was reminted for a leaf's fault")
	}
}

// reissue rewrites the certificate at certPath with a new NotAfter, for the
// same key, signed as before: by signer's key, or by its own key for a root.
func reissue(t *testing.T, certPath string, notAfter time.Time, signer *x509.Certificate, signerKey, ownKey *ecdsa.PrivateKey) {
	t.Helper()
	tmpl := parsePEMCert(certPath)
	if tmpl == nil {
		t.Fatalf("%s does not parse", certPath)
	}
	tmpl.NotAfter = notAfter
	if signer == nil {
		signer = tmpl
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &ownKey.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readECKey(t *testing.T, path string) *ecdsa.PrivateKey {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pem.Decode(raw)
	if b == nil {
		t.Fatalf("%s holds no PEM block", path)
	}
	k, err := x509.ParseECPrivateKey(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// THE RENEWAL HEADROOM IS A MONTH, for the leaf and for the root alike:
// material that expires inside it is reminted at the next start, so expiry
// is never the operator's surprise on a morning they needed the dashboard.
// Outside it, the set is kept — a root reminted early is a root the
// operator must install again.
func TestMaterialInsideTheRenewalHeadroomIsReminted(t *testing.T) {
	for _, tc := range []struct {
		name          string
		root          bool
		left          time.Duration
		leaf, newRoot bool // what ensureTLS must remint
	}{
		{"leaf inside", false, 10 * 24 * time.Hour, true, false},
		{"leaf outside", false, 40 * 24 * time.Hour, false, false},
		{"root inside", true, 10 * 24 * time.Hour, true, true},
		{"root outside", true, 40 * 24 * time.Hour, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			m, err := ensureTLS(dir, "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			caKey := readECKey(t, filepath.Join(dir, "dashboard-ca.key"))
			notAfter := time.Now().Add(tc.left)
			if tc.root {
				reissue(t, m.CACertPath, notAfter, nil, caKey, caKey)
			} else {
				reissue(t, m.LeafCert, notAfter, parsePEMCert(m.CACertPath), caKey, readECKey(t, m.LeafKey))
			}
			root, _ := os.ReadFile(m.CACertPath)
			leaf, _ := os.ReadFile(m.LeafCert)

			m2, err := ensureTLS(dir, "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			afterLeaf, _ := os.ReadFile(m2.LeafCert)
			afterRoot, _ := os.ReadFile(m2.CACertPath)
			if m2.Regenerated != tc.leaf || (string(afterLeaf) != string(leaf)) != tc.leaf {
				t.Fatalf("%v left: leaf reminted = %v, want %v", tc.left, m2.Regenerated, tc.leaf)
			}
			if (string(afterRoot) != string(root)) != tc.newRoot {
				t.Fatalf("%v left: root reminted = %v, want %v", tc.left, string(afterRoot) != string(root), tc.newRoot)
			}
		})
	}
}
