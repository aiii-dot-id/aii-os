package plugincatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// signedAnnouncing is a one-plugin index generated at indexAt, its
// signature made at signedAt carrying extra payload members.
func signedAnnouncing(t *testing.T, plat *packagetest.Role, indexAt, signedAt string, extra map[string]any) (md, sig []byte) {
	t.Helper()
	cat := Index{Version: 1, Generated: indexAt, Plugins: []CatalogEntry{
		{ID: "org.example.listed", Version: "1.0.0", Tier: "T1", Summary: "listed",
			Packages: []CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/l.aiiospkg", SHA256: "sha256:aa", Size: 1}}}}}
	block, _ := json.MarshalIndent(cat, "", "  ")
	md = []byte("# Catalog\n\n```json\n" + string(block) + "\n```\n")
	payload := map[string]any{"catalog_version": 1, "generated": signedAt, "catalog_sha256": sigenvelope.SHA256Prefixed(md), "host_line": OwnLine()}
	for k, v := range extra {
		payload[k] = v
	}
	sig, err := plat.Sign(ArtifactKindPluginCatalog, payload)
	if err != nil {
		t.Fatal(err)
	}
	return md, sig
}

func TestTheCatalogSignatureCarriesTheAnnouncement(t *testing.T) {
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	const at = "2026-10-14T09:00:00Z"
	md, sig := signedAnnouncing(t, plat, "2026-10-01T00:00:00Z", at, map[string]any{
		"host_release": "0.2.0", "status_epochs": map[string]int64{"platform_release": 2, "plugin_reviewer": 1},
	})
	cat, err := ParseCatalog(md, sig, plat.Env)
	if err != nil {
		t.Fatal(err)
	}
	a := cat.Announced
	if a.Release != "0.2.0" || a.At != at || a.StatusEpochs["platform_release"] != 2 || a.StatusEpochs["plugin_reviewer"] != 1 || len(a.StatusEpochs) != 2 {
		t.Fatalf("the announcement read is %+v", a)
	}

	md, sig = signedAnnouncing(t, plat, "2026-10-01T00:00:00Z", at, nil)
	if cat, err := ParseCatalog(md, sig, plat.Env); err != nil || cat.Announced.Release != "" || cat.Announced.StatusEpochs != nil {
		t.Fatalf("an index signed before the announcement existed: %+v, %v", cat, err)
	}

	for name, extra := range map[string]map[string]any{
		"a release that is not a version": {"host_release": "latest"},
		"an epoch below 1":                {"host_release": "0.2.0", "status_epochs": map[string]int64{"platform_release": 0}},
	} {
		md, sig := signedAnnouncing(t, plat, "2026-10-01T00:00:00Z", at, extra)
		if _, err := ParseCatalog(md, sig, plat.Env); err == nil {
			t.Errorf("%s was read as an announcement", name)
		}
	}
	md, sig = signedAnnouncing(t, plat, "2026-10-01T00:00:00Z", "when it was ready", map[string]any{"host_release": "0.2.0"})
	if _, err := ParseCatalog(md, sig, plat.Env); err == nil || !strings.Contains(err.Error(), "RFC 3339") {
		t.Errorf("an announcement with no time it was made was read: %v", err)
	}
}

// A RE-SIGNED INDEX IS HELD TO ITS SIGNATURE'S TIME. Announcing a release
// re-signs the catalog and leaves its file as it was, so the file's own
// generated cannot tell an older announcement from a newer one: an older
// signature over the same file is refused as an older index is, and the
// announcement held stays.
func TestAnOlderAnnouncementDoesNotReplaceANewerOne(t *testing.T) {
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://catalog.example.test/aiios-plugins.md"
	served := map[string][]byte{}
	var asked []string
	c := new(Catalog)
	c.Open("", "", plat.Env, func() string { return url })
	c.fetch = func(ctx context.Context, u string, max int64) ([]byte, error) {
		asked = append(asked, fmt.Sprintf("%s %d", u, max))
		if b, ok := served[u]; ok {
			return b, nil
		}
		return nil, fmt.Errorf("no such artifact %s", u)
	}
	const indexAt, first, second = "2026-10-01T00:00:00Z", "2026-10-14T00:00:00Z", "2026-10-20T00:00:00Z"
	serve := func(signedAt, release string) {
		served[url], served[url+".sig"] = signedAnnouncing(t, plat, indexAt, signedAt, map[string]any{"host_release": release})
	}
	serve(second, "0.2.1")
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	serve(first, "0.2.0")
	var oe *OlderIndexError
	if err := c.Refresh(context.Background()); !errors.As(err, &oe) || oe.Held != second || oe.Fetched != first {
		t.Fatalf("an older announcement over the same file was not refused as older: %v", err)
	}
	if got := c.Current().Announced; got.Release != "0.2.1" || got.At != second {
		t.Fatalf("the newer announcement was replaced: %+v", got)
	}

	// What the announcement points at is fetched beside the catalog,
	// through the catalog's own fetch and with the caller's bound.
	served[url+".aiii_platform_release_status.json"] = []byte("list")
	asked = nil
	if b, err := c.FetchBeside(context.Background(), "aiii_platform_release_status.json", 1<<20); err != nil || string(b) != "list" {
		t.Fatalf("fetch beside the catalog: %q, %v", b, err)
	}
	if len(asked) != 1 || asked[0] != url+".aiii_platform_release_status.json 1048576" {
		t.Fatalf("fetched %v", asked)
	}
	if _, err := new(Catalog).FetchBeside(context.Background(), "x", 1); err == nil {
		t.Fatal("a host with no catalog fetched something beside it")
	}
}
