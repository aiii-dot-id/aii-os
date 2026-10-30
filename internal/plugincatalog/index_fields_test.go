package plugincatalog

import (
	"strings"
	"testing"
)

// The store's browsing surface is bounded like every other field the
// store shows: a title, a category, a keyword list and a release date
// pass; an unparseable date, an overlong title or category, too many
// keywords, or an empty keyword are refused with the index.
func TestCatalogEntryStoreFieldsAreBounded(t *testing.T) {
	pkg := []CatalogPackage{{Platform: "*", Arch: "*", URL: "https://example.test/a.aiiospkg", SHA256: "sha256:00"}}
	good := Index{Plugins: []CatalogEntry{{ID: "a.b", Version: "1.0.0", Title: "A", Category: "memory",
		Keywords: []string{"notes", "recall"}, Updated: "2026-09-10", Packages: pkg}}}
	if err := good.validate(); err != nil {
		t.Fatalf("a well-formed entry was refused: %v", err)
	}
	rfc := Index{Plugins: []CatalogEntry{{ID: "a.b", Version: "1.0.0", Updated: "2026-09-10T10:00:00Z", Packages: pkg}}}
	if err := rfc.validate(); err != nil {
		t.Fatalf("an RFC 3339 date was refused: %v", err)
	}
	bad := map[string]CatalogEntry{
		"unparseable date":  {ID: "a.b", Version: "1.0.0", Updated: "yesterday", Packages: pkg},
		"overlong title":    {ID: "a.b", Version: "1.0.0", Title: strings.Repeat("t", 121), Packages: pkg},
		"overlong category": {ID: "a.b", Version: "1.0.0", Category: strings.Repeat("c", 41), Packages: pkg},
		"too many keywords": {ID: "a.b", Version: "1.0.0", Keywords: strings.Split(strings.Repeat("k,", 17), ","), Packages: pkg},
		"empty keyword":     {ID: "a.b", Version: "1.0.0", Keywords: []string{""}, Packages: pkg},
	}
	for name, e := range bad {
		if err := (&Index{Plugins: []CatalogEntry{e}}).validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if hits := good.Search("recall"); len(hits) != 1 {
		t.Fatalf("search by keyword found %d entries, want 1", len(hits))
	}
	if hits := good.Search("a"); len(hits) != 1 {
		t.Fatalf("search by title found %d entries, want 1", len(hits))
	}
}

// A build is portable only when the index lists it for every system AND
// every processor: that is the one build selection falls back to, and the
// one the store names as portable before Install. A build that names
// either is a build for that system or processor.
func TestOnlyABuildForEverySystemAndProcessorIsPortable(t *testing.T) {
	for _, c := range []struct {
		platform, arch string
		want           bool
	}{{"*", "*", true}, {"linux", "x86_64", false}, {"*", "arm64", false}, {"macos", "*", false}} {
		if got := (CatalogPackage{Platform: c.platform, Arch: c.arch}).Portable(); got != c.want {
			t.Errorf("%s/%s: portable=%v, want %v", c.platform, c.arch, got, c.want)
		}
	}
}
