package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// nextLine is the line after this program's: the line an update across
// lines offers, whatever VERSION says today.
func nextLine(t *testing.T) string {
	t.Helper()
	own := plugincatalog.OwnLine()
	minor, err := strconv.Atoi(own[strings.IndexByte(own, '.')+1:])
	if err != nil {
		t.Fatal(err)
	}
	return own[:strings.IndexByte(own, '.')+1] + strconv.Itoa(minor+1)
}

func TestAnUpdateToTheNextLineNamesThePluginsItWouldStrand(t *testing.T) {
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	next := nextLine(t)
	offered := next + ".0"
	catDir := t.TempDir()
	cat := plugincatalog.Index{Version: 1, Generated: "2026-12-01T00:00:00Z", MustUnderstand: []string{"compat", "host_line"}, Plugins: []plugincatalog.CatalogEntry{
		{ID: "org.example.carried", Version: "2.0.0", Tier: "T1", Summary: "s", Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/c", SHA256: "sha256:aa", Size: 1}}},
		{ID: "org.example.later", Version: "2.0.0", Tier: "T1", Summary: "s", AiiosMinVersion: next + ".1", Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/l", SHA256: "sha256:aa", Size: 1}}},
	}}
	block, _ := json.MarshalIndent(cat, "", "  ")
	md := []byte("# Catalog\n\n```json\n" + string(block) + "\n```\n")
	sig, err := plat.Sign(plugincatalog.ArtifactKindPluginCatalog, map[string]any{"catalog_version": 1, "generated": cat.Generated,
		"catalog_sha256": sigenvelope.SHA256Prefixed(md), "host_line": next})
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(catDir, plugincatalog.CatalogFileFor(next))
	if err := os.WriteFile(name, md, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name+".sig", sig, 0o600); err != nil {
		t.Fatal(err)
	}

	a := New(&Config{Plugins: PluginsConfig{CatalogDir: catDir}})
	a.catalog.Open(catDir, "", plat.Env, a.effectiveCatalogURL)
	if rows, note := a.strandedBy(context.Background(), offered); rows != nil || note != "" {
		t.Fatalf("an identity with no plugin is told %v %q", rows, note)
	}
	for _, p := range []*pluginhost.ActivePlugin{{ID: "org.example.carried", Version: "1.0.0"}, {ID: "org.example.later", Version: "1.0.0"}, {ID: "org.example.gone", Version: "1.4.0"}} {
		addPlugin(a, p)
	}
	rows, note := a.strandedBy(context.Background(), offered)
	want := fmt.Sprint([]updates.StrandedPlugin{
		{ID: "org.example.gone", Version: "1.4.0", Reason: "the AII OS " + next + " catalog does not carry it"},
		{ID: "org.example.later", Version: "1.0.0", Reason: "the AII OS " + next + " catalog's 2.0.0 needs AII OS " + next + ".1 or newer"},
	})
	if fmt.Sprint(rows) != want || note != "" {
		t.Fatalf("an update to %s is said to strand %v %q, want %s", offered, rows, note, want)
	}
	if rows, note := a.strandedBy(context.Background(), plugincatalog.OwnLine()+".99"); rows != nil || note != "" {
		t.Fatalf("an update within this line is said to strand %v %q", rows, note)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if rows, note := a.strandedBy(context.Background(), offered); rows != nil || note != fmt.Sprintf(strandedUnpublished, next, offered, 3) {
		t.Fatalf("with no %s catalog the card is told %v %q", next, rows, note)
	}

	// The card is told beside an offer, and nothing once nothing is offered.
	snap := updates.StateSnapshot{AvailableVersion: offered, Stranded: []updates.StrandedPlugin{{ID: "a", Version: "1", Reason: "r"}}, StrandedNote: "n"}
	if got, said := strandedView(snap); len(got) != 1 || got[0].ID != "a" || got[0].Version != "1" || got[0].Reason != "r" || said != "n" {
		t.Fatalf("the card is told %+v %q", got, said)
	}
	snap.AvailableVersion = ""
	if got, said := strandedView(snap); got != nil || said != "" {
		t.Fatalf("with nothing offered the card is told %+v %q", got, said)
	}
}
