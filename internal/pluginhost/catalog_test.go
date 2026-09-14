package pluginhost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

func hostCatalog() Catalog {
	return Catalog{Version: 1, Generated: "2026-09-05T00:00:00Z", Plugins: []CatalogEntry{
		{ID: "org.example.foo", Version: "1.2.0", Tier: "T3", Summary: "a foo that foos",
			Packages: []CatalogPackage{
				{Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), URL: "https://host/foo.aiiospkg", SHA256: "sha256:aa", Size: 10},
				{Platform: "windows", Arch: "x86_64", URL: "https://host/foo-win.aiiospkg", SHA256: "sha256:bb", Size: 11},
			}},
		{ID: "org.example.bar", Version: "0.3.0", Tier: "T1", Summary: "a bar",
			Packages: []CatalogPackage{
				{Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), URL: "https://host/bar.aiiospkg", SHA256: "sha256:cc", Size: 12},
			}},
	}}
}

// .
// .
func writeSignedCatalog(t *testing.T, dir string, cat Catalog, signer *packagetest.Role) {
	t.Helper()
	block, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	md := []byte("# AII OS Plugin Catalog\n\nThe platform's signed index. Prose is ignored; the signature covers the whole file.\n\n```json\n" + string(block) + "\n```\n")
	if err := os.WriteFile(filepath.Join(dir, CatalogFile), md, 0o644); err != nil {
		t.Fatal(err)
	}
	sig, err := signer.Sign(ArtifactKindPluginCatalog, catalogSig{
		CatalogVersion: cat.Version, Generated: cat.Generated, CatalogSHA256: sigenvelope.SHA256Prefixed(md),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, CatalogSigFile), sig, 0o644); err != nil {
		t.Fatal(err)
	}
}

func platformSignedCatalog(t *testing.T, dir string, cat Catalog) *packagetest.Role {
	t.Helper()
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	writeSignedCatalog(t, dir, cat, plat)
	return plat
}

func TestCatalogLoadsVerifiesAndSelects(t *testing.T) {
	dir := t.TempDir()
	plat := platformSignedCatalog(t, dir, hostCatalog())
	cat, err := LoadCatalog(dir, plat.Env)
	if err != nil {
		t.Fatalf("a platform-signed catalog loads: %v", err)
	}
	if len(cat.Plugins) != 2 {
		t.Fatalf("both entries parse, got %d", len(cat.Plugins))
	}
	if got := cat.Search("foo"); len(got) != 1 || got[0].ID != "org.example.foo" {
		t.Fatalf("search finds foo by summary: %+v", got)
	}
	// .
	_, pkg, err := cat.Select("org.example.foo")
	if err != nil || pkg == nil || pkg.Platform != packagefmt.HostPlatform() || pkg.Arch != packagefmt.HostArch() {
		t.Fatalf("select returns the host build: %v %+v", err, pkg)
	}
	// .
	if _, p, serr := cat.SelectFor("org.example.foo", "solaris", "sparc"); serr == nil || p != nil {
		t.Fatalf("no build for solaris/sparc must select nothing, got %+v", p)
	}
}

func TestCatalogRefusesTamperedFile(t *testing.T) {
	dir := t.TempDir()
	plat := platformSignedCatalog(t, dir, hostCatalog())
	// .
	f := filepath.Join(dir, CatalogFile)
	md, _ := os.ReadFile(f)
	md[len(md)/2] ^= 0xff
	if err := os.WriteFile(f, md, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadCatalog(dir, plat.Env)
	if err == nil || !strings.Contains(err.Error(), "does not cover this file") {
		t.Fatalf("a tampered catalog is refused by the hash binding, got: %v", err)
	}
}

func TestCatalogRefusesWrongSigner(t *testing.T) {
	dir := t.TempDir()
	// .
	// .
	wrong, err := packagetest.NewRole("test-certifier", packagetest.KeyTypePublisherCertifier)
	if err != nil {
		t.Fatal(err)
	}
	writeSignedCatalog(t, dir, hostCatalog(), wrong)
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	if _, lerr := LoadCatalog(dir, plat.Env); lerr == nil || !strings.Contains(lerr.Error(), "REFUSED") {
		t.Fatalf("a catalog signed by the wrong authority is refused, got: %v", lerr)
	}
}

func TestCatalogRefusesWithoutPinnedRoot(t *testing.T) {
	dir := t.TempDir()
	platformSignedCatalog(t, dir, hostCatalog())
	if _, err := LoadCatalog(dir, nil); err == nil || !strings.Contains(err.Error(), "no platform_release root") {
		t.Fatalf("without a pinned root the catalog is unverifiable and refused, got: %v", err)
	}
}

// .
// .
func TestCatalogDetailIsOptionalBoundedAndHTTPS(t *testing.T) {
	cat := hostCatalog()
	cat.Plugins[0].Description, cat.Plugins[0].Publisher, cat.Plugins[0].Homepage, cat.Plugins[0].License = "keeps the bar", "Example Co", "https://example.test/bar", "Apache-2.0"
	dir := t.TempDir()
	plat := platformSignedCatalog(t, dir, cat)
	got, err := LoadCatalog(dir, plat.Env)
	if err != nil || got.Plugins[0].Homepage != "https://example.test/bar" || got.Plugins[0].Publisher != "Example Co" {
		t.Fatalf("detail travels: %v %+v", err, got)
	}
	cat.Plugins[0].Homepage = "http://example.test/bar"
	dir2 := t.TempDir()
	plat2 := platformSignedCatalog(t, dir2, cat)
	if _, err := LoadCatalog(dir2, plat2.Env); err == nil || !strings.Contains(err.Error(), "not https") {
		t.Fatalf("a plain-http homepage refuses the index: %v", err)
	}
}
