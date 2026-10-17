package plugincatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// signedIndex is a one-plugin index at version, and its signature by plat.
func signedIndex(t *testing.T, plat *packagetest.Role, version string) (md, sig []byte) {
	t.Helper()
	cat := Index{Version: 1, Generated: "2026-09-09T00:00:00Z", Plugins: []CatalogEntry{
		{ID: "org.example.listed", Version: version, Tier: "T1", Summary: "listed",
			Packages: []CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/l.aiiospkg", SHA256: "sha256:aa", Size: 1}}}}}
	block, _ := json.MarshalIndent(cat, "", "  ")
	md = []byte("# Catalog\n\n```json\n" + string(block) + "\n```\n")
	sig, err := plat.Sign(ArtifactKindPluginCatalog, map[string]any{
		"catalog_version": 1, "generated": "2026-09-09T00:00:00Z", "catalog_sha256": sigenvelope.SHA256Prefixed(md),
	})
	if err != nil {
		t.Fatal(err)
	}
	return md, sig
}

func TestCatalogRefreshesFromItsURLAndKeepsTheLastGoodIndex(t *testing.T) {
	dir := t.TempDir()
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://catalog.example.test/aiios-plugins.md"
	// Opened as the host opens it with a catalog URL and no checkout: the
	// pinned root, and a kept copy that is absent on a first boot. That
	// the host passes these three (its configured URL, its pinned root,
	// <data>/plugins-catalog) is internal/app's
	// TestTheHostOpensItsKeptCatalogAndRefreshesFromTheConfiguredURL.
	cache := filepath.Join(dir, "plugins-catalog")
	var c Catalog
	c.Open("", cache, plat.Env, func() string { return url })
	if c.Current() != nil {
		t.Fatal("nothing is loaded before a fetch verifies")
	}
	md, sig := signedIndex(t, plat, "2.0.0")
	served := map[string][]byte{url: md, url + ".sig": sig}
	c.fetch = func(ctx context.Context, u string, max int64) ([]byte, error) {
		b, ok := served[u]
		if !ok {
			return nil, fmt.Errorf("no such artifact %s", u)
		}
		return b, nil
	}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.Current(); got == nil || len(got.Plugins) != 1 || got.Plugins[0].Version != "2.0.0" {
		t.Fatalf("the verified fetch is adopted: %+v", got)
	}
	if at, refusal := c.State(); at == "" || refusal != "" {
		t.Fatalf("the refresh is recorded: at=%q refusal=%q", at, refusal)
	}
	kept, err := LoadCatalog(cache, plat.Env)
	if err != nil || kept.Plugins[0].Version != "2.0.0" {
		t.Fatalf("the verified pair is kept beside the data: %v", err)
	}

	// A body the signature does not cover: refused, reported, and the
	// good index stays.
	served[url] = []byte("# Catalog!\n" + string(md[len("# Catalog\n"):]))
	if err := c.Refresh(context.Background()); err == nil {
		t.Fatal("a fetch the signature does not cover is refused")
	}
	if got := c.Current(); got == nil || got.Plugins[0].Version != "2.0.0" {
		t.Fatal("the last good index stays")
	}
	if _, refusal := c.State(); refusal == "" {
		t.Fatal("the refusal is reported to the view")
	}
	// The index missing altogether is the same story.
	delete(served, url)
	if err := c.Refresh(context.Background()); err == nil || c.Current().Plugins[0].Version != "2.0.0" {
		t.Fatalf("an index that does not arrive changes nothing: %v", err)
	}
	// And a verified newer fetch replaces it.
	served[url], served[url+".sig"] = signedIndex(t, plat, "2.1.0")
	if err := c.Refresh(context.Background()); err != nil || c.Current().Plugins[0].Version != "2.1.0" {
		t.Fatalf("a verified fetch replaces the index: %v", err)
	}
	// The URL is read at every refresh: one set later is fetched without
	// opening the catalog again.
	url = "https://mirror.example.test/aiios-plugins.md"
	served[url], served[url+".sig"] = signedIndex(t, plat, "2.2.0")
	if err := c.Refresh(context.Background()); err != nil || c.Current().Plugins[0].Version != "2.2.0" {
		t.Fatalf("a URL set later is the one fetched: %v", err)
	}
}

// THE INDEX IS FETCHED THROUGH THE ONE EGRESS GUARD. The fetch seam is a
// test's; without it, a catalog URL naming a loopback or a private address
// is refused by the policy web_fetch and a plugin's net.outbound answer
// to, before any request leaves; the refusal is typed and reported, and
// the index already held stays held.
func TestTheIndexIsFetchedThroughTheEgressGuard(t *testing.T) {
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	md, sig := signedIndex(t, plat, "2.0.0")
	if err := os.WriteFile(filepath.Join(checkout, CatalogFile), md, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, CatalogSigFile), sig, 0o600); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write(md)
	}))
	t.Cleanup(srv.Close)
	for _, url := range []string{srv.URL + "/aiios-plugins.md", "https://10.0.0.1/aiios-plugins.md"} {
		var c Catalog
		c.Open(checkout, "", plat.Env, func() string { return url })
		if got := c.Current(); got == nil || got.Plugins[0].Version != "2.0.0" {
			t.Fatalf("the signed checkout is held before any refresh: %+v", got)
		}
		err := c.Refresh(context.Background())
		if !errors.Is(err, firewall.ErrEgressBlocked) {
			t.Fatalf("%s must be refused by the egress policy, got %v", url, err)
		}
		if _, refusal := c.State(); refusal == "" {
			t.Fatalf("the refusal of %s is reported to the view", url)
		}
		if got := c.Current(); got == nil || got.Plugins[0].Version != "2.0.0" {
			t.Fatalf("the index already held stays held after %s is refused: %+v", url, got)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the refusal comes before any request; the server saw %d", n)
	}
}

func TestOverlappingRefreshesHoldTheIndexTheyKeep(t *testing.T) {
	dir := t.TempDir()
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://catalog.example.test/aiios-plugins.md"
	cache := filepath.Join(dir, "plugins-catalog")
	mdA, sigA := signedIndex(t, plat, "1.0.0")
	mdB, sigB := signedIndex(t, plat, "2.0.0")
	var c Catalog
	c.Open("", cache, plat.Env, func() string { return url })
	var serveB atomic.Bool
	c.fetch = func(ctx context.Context, u string, max int64) ([]byte, error) {
		md, sig := mdA, sigA
		if serveB.Load() {
			md, sig = mdB, sigB
		}
		if u == url {
			return md, nil
		}
		return sig, nil
	}
	const bound = 30 * time.Second // turns a stall into a failure; no run passes by waiting it out
	contended := make(chan struct{}, 1)
	secondAdopted := make(chan struct{}, 1)
	second := make(chan error, 1)
	c.contended = func() {
		select {
		case contended <- struct{}{}:
		default:
		}
	}
	var first atomic.Bool
	var order string // written only by the first refresh's goroutine, read after it returns
	c.adopted = func() {
		if !first.CompareAndSwap(false, true) {
			secondAdopted <- struct{}{}
			return
		}
		serveB.Store(true)
		go func() { second <- c.Refresh(context.Background()) }()
		select {
		case <-contended:
			order = "serialized"
		case <-secondAdopted:
			order = "overlapped"
		case <-time.After(bound):
			order = "stalled"
		}
	}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	switch order {
	case "serialized":
	case "overlapped":
		t.Fatal("a second refresh adopted its index while the first held one it had not yet kept")
	default:
		t.Fatalf("within %s the second refresh neither met the lock nor adopted: the ordering is unproven", bound)
	}
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("the second refresh: %v", err)
		}
	case <-time.After(bound):
		t.Fatalf("the second refresh did not finish within %s after the first released the lock", bound)
	}
	held := c.Current()
	if held == nil || held.Plugins[0].Version != "2.0.0" {
		t.Fatalf("the later refresh's index is held: %+v", held)
	}
	kept, err := LoadCatalog(cache, plat.Env)
	if err != nil {
		t.Fatalf("overlapping refreshes left a kept pair that does not verify: %v", err)
	}
	if kept.Plugins[0].Version != held.Plugins[0].Version {
		t.Fatalf("the kept pair (%s) is not the index held (%s)", kept.Plugins[0].Version, held.Plugins[0].Version)
	}
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != CatalogFile && e.Name() != CatalogSigFile {
			t.Errorf("left behind in the kept catalog: %s", e.Name())
		}
	}
}

// An index that verified but could not be kept is in use, and said to be
// unkept — never a silent success. A boot without a network then holds
// nothing and says why; once a later refresh keeps the index, a boot
// without a network holds it. (The cache path is made a regular file to
// fail the keep: permission bits do not stop root.)
func TestAnIndexThatCannotBeKeptIsInUseAndSaidToBe(t *testing.T) {
	dir := t.TempDir()
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://catalog.example.test/aiios-plugins.md"
	cache := filepath.Join(dir, "plugins-catalog")
	if err := os.WriteFile(cache, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	md, sig := signedIndex(t, plat, "3.0.0")
	serve := func(c *Catalog) {
		c.fetch = func(ctx context.Context, u string, max int64) ([]byte, error) {
			if u == url {
				return md, nil
			}
			return sig, nil
		}
	}
	offline := func() *Catalog {
		var c Catalog
		c.Open("", cache, plat.Env, func() string { return url })
		return &c
	}

	c := offline()
	serve(c)
	err = c.Refresh(context.Background())
	var unkept *KeepError
	if !errors.As(err, &unkept) || unkept.Dir != cache {
		t.Fatalf("a keep that failed must be reported as a KeepError for %s, got %v", cache, err)
	}
	if got := c.Current(); got == nil || got.Plugins[0].Version != "3.0.0" {
		t.Fatalf("the verified index is in use although it could not be kept: %+v", got)
	}
	if at, problem := c.State(); at == "" || problem == "" {
		t.Fatalf("the view must say the index in use is not kept: at=%q problem=%q", at, problem)
	}

	// A boot without a network: nothing is held, and the view says why.
	c = offline()
	if c.Current() != nil {
		t.Fatal("a boot without a network held an index that was never kept")
	}
	if _, problem := c.State(); problem == "" {
		t.Fatal("a boot that could not load the kept copy must say so in the view")
	}

	// The keep is possible again: a refresh keeps the index, says nothing
	// is wrong, and a boot without a network holds it.
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	serve(c)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("a refresh that keeps its index: %v", err)
	}
	if _, problem := c.State(); problem != "" {
		t.Fatalf("nothing is wrong once the index is kept, yet the view says %q", problem)
	}
	if got := offline().Current(); got == nil || got.Plugins[0].Version != "3.0.0" {
		t.Fatalf("a boot without a network must hold the kept index: %+v", got)
	}
}
