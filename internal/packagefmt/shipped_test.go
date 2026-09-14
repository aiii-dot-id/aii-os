package packagefmt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestADefaultInstallCarriesItsPluginTrust(t *testing.T) {
	roots := ShippedRoots()
	if roots.PlatformRelease == nil || roots.Reviewer == nil || roots.PublisherCertifier == nil {
		t.Fatal("a default install carries no plugin trust roots — every signed tier refuses")
	}
	for keyType, got := range map[string]string{
		KeyTypePlatformRelease:    roots.PlatformRelease.KeyType,
		KeyTypeReviewer:           roots.Reviewer.KeyType,
		KeyTypePublisherCertifier: roots.PublisherCertifier.KeyType,
	} {
		if got != keyType {
			t.Fatalf("shipped root for %s declares key_type %q — trust domains are separate keys", keyType, got)
		}
	}

	// .
	// .
	set := LoadRevocationStatus(t.TempDir(), roots, nil)
	for _, d := range RevocationDomains() {
		if _, ok := set.Epoch(d.RootKeyType); !ok {
			t.Fatalf("%s tier is UNAVAILABLE on a default install — the first signed plugin is refused with nowhere to go", d.RootKeyType)
		}
	}
	for _, line := range set.Describe() {
		if strings.Contains(line, "missing") {
			t.Fatalf("a default install reports a missing snapshot: %s", line)
		}
	}
}

// .
// .
func TestAnOperatorSnapshotOverridesTheShippedOne(t *testing.T) {
	dir := t.TempDir()
	roots := ShippedRoots()
	// .
	// .
	// .
	for _, d := range RevocationDomains() {
		if err := writeFileForTest(dir, d.FileName, "{ not a snapshot"); err != nil {
			t.Fatal(err)
		}
	}
	set := LoadRevocationStatus(dir, roots, nil)
	for _, d := range RevocationDomains() {
		if _, ok := set.Epoch(d.RootKeyType); ok {
			t.Fatalf("%s: a corrupt operator snapshot was silently replaced by the shipped one", d.RootKeyType)
		}
	}
}

func writeFileForTest(dir, name, body string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600)
}
