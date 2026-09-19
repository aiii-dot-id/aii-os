package pluginhost

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// .
// .
func signedIndex(t *testing.T, block string) ([]byte, []byte, *packagetest.Role) {
	t.Helper()
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	md := []byte("# AII OS Plugin Catalog\n\nProse the parser ignores.\n\n```json\n" + block + "\n```\n")
	sig, err := plat.Sign(ArtifactKindPluginCatalog, catalogSig{
		CatalogVersion: 1, Generated: "2026-09-17T00:00:00Z", CatalogSHA256: sigenvelope.SHA256Prefixed(md),
	})
	if err != nil {
		t.Fatal(err)
	}
	return md, sig, plat
}

func onePlugin(extraEntry string) string {
	return `{"catalog_version":1,"generated":"2026-09-17T00:00:00Z","plugins":[{` +
		`"id":"org.example.foo","version":"1.0.0","tier":"T3","summary":"a foo"` + extraEntry +
		`,"packages":[{"platform":"*","arch":"*","url":"https://h/f.aiiospkg","sha256":"sha256:aa","size":1}]}]}`
}

// .
// .
// .
// .
// .
func TestAnIndexThatGrewAFieldIsStillRead(t *testing.T) {
	block := `{"catalog_version":1,"generated":"2026-09-17T00:00:00Z","future_wisdom":{"nested":[1,2]},` +
		`"plugins":[{"id":"org.example.foo","version":"1.0.0","tier":"T3","summary":"a foo","surprise":true,` +
		`"packages":[{"platform":"*","arch":"*","url":"https://h/f.aiiospkg","sha256":"sha256:aa","size":1,"extra":"x"}]}]}`
	md, sig, plat := signedIndex(t, block)
	cat, err := ParseCatalog(md, sig, plat.Env)
	if err != nil {
		t.Fatalf("an index with fields this host does not know must still be read: %v", err)
	}
	if len(cat.Plugins) != 1 || cat.Plugins[0].ID != "org.example.foo" {
		t.Fatalf("the entries this host does understand: %+v", cat.Plugins)
	}
}

// .
// .
// .
// .
func TestAnIndexNamesWhatItsReaderMustUnderstand(t *testing.T) {
	md, sig, plat := signedIndex(t, `{"catalog_version":1,"generated":"g","must_understand":["compat"],`+
		`"plugins":[{"id":"org.example.foo","version":"1.0.0","tier":"T3","summary":"s","aiios_min_version":"0.1.0",`+
		`"packages":[{"platform":"*","arch":"*","url":"https://h/f","sha256":"sha256:aa","size":1}]}]}`)
	if _, err := ParseCatalog(md, sig, plat.Env); err != nil {
		t.Fatalf("a feature this host implements must be read: %v", err)
	}
	md, sig, plat = signedIndex(t, `{"catalog_version":1,"generated":"g","must_understand":["quantum_ranking"],`+onePlugin("")[len(`{"catalog_version":1,"generated":"2026-09-17T00:00:00Z",`):])
	_, err := ParseCatalog(md, sig, plat.Env)
	if err == nil || !strings.Contains(err.Error(), "quantum_ranking") {
		t.Fatalf("an index requiring something this host cannot do must refuse whole, naming it: %v", err)
	}
}

// .
func TestTheIndexOffersOnlyWhatThisHostCanRun(t *testing.T) {
	pkgs := []CatalogPackage{{Platform: "*", Arch: "*", URL: "https://h/p.aiiospkg", SHA256: "sha256:aa", Size: 1}}
	cat := &Catalog{Version: 1, Plugins: []CatalogEntry{
		{ID: "org.example.future", Version: "3.0.0", Tier: "T3", Summary: "s", AiiosMinVersion: "0.9.0", Packages: pkgs},
		{ID: "org.example.past", Version: "0.1.0", Tier: "T3", Summary: "s", AiiosMaxExclusiveVersion: "0.1.0", Packages: pkgs},
		{ID: "org.example.now", Version: "1.0.0", Tier: "T3", Summary: "s", AiiosMinVersion: "0.1.0", AiiosMaxExclusiveVersion: "9.0.0", Packages: pkgs},
		{ID: "org.example.any", Version: "1.0.0", Tier: "T3", Summary: "s", Packages: pkgs},
	}}
	const host = "0.1.6"
	plat, arch := packagefmt.HostPlatform(), packagefmt.HostArch()
	for _, tc := range []struct{ id, needs string }{
		{"org.example.future", "needs AII OS 0.9.0 or newer"},
		{"org.example.past", "needs AII OS below 0.1.0"},
	} {
		_, pkg, err := cat.SelectForHost(tc.id, plat, arch, host)
		if err == nil || pkg != nil || !strings.Contains(err.Error(), tc.needs) {
			t.Fatalf("%s: a release this host cannot run must refuse, naming what it needs: %v", tc.id, err)
		}
		if !strings.Contains(err.Error(), host) {
			t.Fatalf("%s: the refusal names this host: %v", tc.id, err)
		}
	}
	for _, id := range []string{"org.example.now", "org.example.any"} {
		if _, pkg, err := cat.SelectForHost(id, plat, arch, host); err != nil || pkg == nil {
			t.Fatalf("%s: a release this host can run must be offered: %v", id, err)
		}
	}
	// .
	// .
	if _, pkg, err := cat.SelectForHost("org.example.future", plat, arch, "not-a-version"); err == nil || pkg != nil {
		t.Fatalf("an undecidable window must not be offered: %v", err)
	}
	// .
	if _, pkg, err := cat.SelectForHost("org.example.any", plat, arch, "not-a-version"); err != nil || pkg == nil {
		t.Fatalf("a release with no window is offered to any host: %v", err)
	}
	if ok, why := (CatalogEntry{}).SupportedBy(host); !ok || why != "" {
		t.Fatalf("an entry with no window is supported everywhere: %v %q", ok, why)
	}
}

// .
func TestAnIndexWindowIsAVersionRangeOrTheIndexRefuses(t *testing.T) {
	for _, tc := range []struct{ name, entry string }{
		{"a minimum that is not a version", `,"aiios_min_version":"one.two"`},
		{"a maximum that is not a version", `,"aiios_max_exclusive_version":"0.2"`},
		{"a window with no versions in it", `,"aiios_min_version":"0.3.0","aiios_max_exclusive_version":"0.3.0"`},
		// .
		// .
		{"a null minimum", `,"aiios_min_version":null`},
		{"an empty minimum", `,"aiios_min_version":""`},
		{"a numeric maximum", `,"aiios_max_exclusive_version":123`},
		{"a prerelease bound", `,"aiios_min_version":"1.0.0-beta.1"`},
		{"build metadata", `,"aiios_min_version":"1.0.0+build.5"`},
	} {
		md, sig, plat := signedIndex(t, onePlugin(tc.entry))
		if _, err := ParseCatalog(md, sig, plat.Env); err == nil {
			t.Fatalf("%s was accepted", tc.name)
		}
	}
	// .
	md, sig, plat := signedIndex(t, onePlugin(`,"aiios_min_version":"00.01.006","aiios_max_exclusive_version":"00.02.000"`))
	cat, err := ParseCatalog(md, sig, plat.Env)
	if err != nil {
		t.Fatalf("a window written with leading zeros is in the grammar: %v", err)
	}
	if ok, _ := cat.Plugins[0].SupportedBy("0.1.7"); !ok {
		t.Fatal("0.1.7 is inside 00.01.006 .. 00.02.000")
	}
	if ok, why := cat.Plugins[0].SupportedBy("0.2.0"); ok || why == "" {
		t.Fatalf("0.2.0 is at the exclusive maximum: ok=%v why=%q", ok, why)
	}
}
