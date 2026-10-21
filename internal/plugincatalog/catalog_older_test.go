package plugincatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// signedIndexAt is signedIndex with the time the index says it was
// generated, in the file and in the signed payload alike.
func signedIndexAt(t *testing.T, plat *packagetest.Role, version, generated string) (md, sig []byte) {
	t.Helper()
	cat := Index{Version: 1, Generated: generated, Plugins: []CatalogEntry{
		{ID: "org.example.listed", Version: version, Tier: "T1", Summary: "listed",
			Packages: []CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/l.aiiospkg", SHA256: "sha256:aa", Size: 1}}}}}
	block, _ := json.MarshalIndent(cat, "", "  ")
	md = []byte("# Catalog\n\n```json\n" + string(block) + "\n```\n")
	sig, err := plat.Sign(ArtifactKindPluginCatalog, map[string]any{
		"catalog_version": 1, "generated": generated, "catalog_sha256": sigenvelope.SHA256Prefixed(md),
	})
	if err != nil {
		t.Fatal(err)
	}
	return md, sig
}

// A SIGNATURE SAYS WHO MADE AN INDEX, NOT THAT IT IS THE LATEST. Every
// index the platform ever signed verifies for ever. A fetch that returns
// an older one (a stale mirror, a cache, someone on the path) is refused,
// typed, the held index stays in use and stays the kept copy, and the
// refusal says how an operator who meant it gets past it. The same holds
// after a restart, because the kept copy is the index held at boot.
func TestAVerifiedIndexIsNeverReplacedByAnOlderOne(t *testing.T) {
	dir := t.TempDir()
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://catalog.example.test/aiios-plugins.md"
	cache := filepath.Join(dir, "plugins-catalog")
	served := map[string][]byte{}
	fetch := func(ctx context.Context, u string, max int64) ([]byte, error) {
		b, ok := served[u]
		if !ok {
			return nil, fmt.Errorf("no such artifact %s", u)
		}
		return b, nil
	}
	serve := func(version, generated string) {
		served[url], served[url+".sig"] = signedIndexAt(t, plat, version, generated)
	}
	open := func() *Catalog {
		c := new(Catalog)
		c.Open("", cache, plat.Env, func() string { return url })
		c.fetch = fetch
		return c
	}
	const older, held, newer = "2026-10-01T00:00:00Z", "2026-10-09T12:00:00Z", "2026-10-11T00:00:00Z"

	c := open()
	serve("2.0.0", held)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	refusedOlder := func(c *Catalog, when string) {
		t.Helper()
		serve("1.0.0", older)
		err := c.Refresh(context.Background())
		var oe *OlderIndexError
		if !errors.As(err, &oe) || oe.Held != held || oe.Fetched != older || oe.Kept != cache {
			t.Fatalf("%s: an older verified index must be refused as an OlderIndexError naming both times and the kept copy; got %v", when, err)
		}
		if !strings.Contains(err.Error(), cache) {
			t.Fatalf("%s: the refusal must say where the kept index is, for an operator who meant it: %v", when, err)
		}
		if got := c.Current(); got == nil || got.Plugins[0].Version != "2.0.0" {
			t.Fatalf("%s: the held index must stay in use: %+v", when, got)
		}
		if _, refusal := c.State(); refusal == "" {
			t.Fatalf("%s: the refusal is reported to the view", when)
		}
		if kept, err := LoadCatalog(cache, plat.Env); err != nil || kept.Plugins[0].Version != "2.0.0" {
			t.Fatalf("%s: the kept copy must still be the held index: %v", when, err)
		}
	}
	refusedOlder(c, "in one run")
	refusedOlder(open(), "after a restart")

	// An index that does not say when it was generated cannot show it is
	// not older than one that does.
	serve("1.5.0", "sometime")
	var oe *OlderIndexError
	if err := c.Refresh(context.Background()); !errors.As(err, &oe) || c.Current().Plugins[0].Version != "2.0.0" {
		t.Fatalf("an index with no readable time must not replace one that has it: %v", err)
	}

	// The same moment is the same publication; a later one replaces it.
	serve("2.0.1", held)
	if err := c.Refresh(context.Background()); err != nil || c.Current().Plugins[0].Version != "2.0.1" {
		t.Fatalf("an index generated at the same moment is adopted as before: %v", err)
	}
	serve("3.0.0", newer)
	if err := c.Refresh(context.Background()); err != nil || c.Current().Plugins[0].Version != "3.0.0" {
		t.Fatalf("a newer verified index replaces the held one: %v", err)
	}
	if _, refusal := c.State(); refusal != "" {
		t.Fatalf("a verified refresh clears the refusal: %q", refusal)
	}

	// The operator's way past it, as the refusal says: with the kept copy
	// removed, a host that starts holds nothing and adopts what verifies.
	for _, name := range []string{CatalogFile, CatalogSigFile} {
		if err := os.Remove(filepath.Join(cache, name)); err != nil {
			t.Fatal(err)
		}
	}
	fresh := open()
	serve("1.0.0", older)
	if err := fresh.Refresh(context.Background()); err != nil || fresh.Current().Plugins[0].Version != "1.0.0" {
		t.Fatalf("with the kept index removed, the older copy the operator pointed at is adopted: %v", err)
	}
}
