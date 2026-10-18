package packagefmt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A DEFAULT INSTALL MUST BE ABLE TO VERIFY A SIGNED PLUGIN. The three
// trust roots were operator-pinned file paths, all absent unless
// configured, and the revocation snapshots had no provisioning story at
// all — nothing ever wrote <data>/trust. Since every signed tier checks
// revocation and an absent snapshot means "tier unavailable", a fresh
// install refused T1, T2 and T3 outright. Nobody had hit it only because
// no signed plugin had been installed yet; the first one would have been
// told the tier was unavailable, with nowhere to go.
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

// The operator's copy still wins, and may be NEWER: shipped is the
// floor, not a ceiling.
func TestAnOperatorSnapshotOverridesTheShippedOne(t *testing.T) {
	dir := t.TempDir()
	roots := ShippedRoots()
	// A file that is present but garbage must be reported LOUDLY rather
	// than silently replaced by the shipped floor — present-and-broken is
	// not the same as absent.
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
