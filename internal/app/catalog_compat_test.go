package app

import (
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/version"
)

// THE STORE SAYS WHAT A RELEASE NEEDS, rather than offering an Install
// that would refuse at activation — or dropping the row and leaving the
// operator to wonder where the plugin went.
func TestTheStoreSaysWhatAReleaseNeedsInsteadOfOfferingIt(t *testing.T) {
	pkgs := []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://h/p.aiiospkg", SHA256: "sha256:aa", Size: 1}}
	cat := &plugincatalog.Index{Plugins: []plugincatalog.CatalogEntry{
		{ID: "org.example.future", Version: "3.0.0", Tier: "T3", AiiosMinVersion: "99.0.0", Packages: pkgs},
		{ID: "org.example.now", Version: "1.0.0", Tier: "T3", AiiosMinVersion: "0.0.1", Packages: pkgs},
	}}
	views := catalogViewsFor(cat, nil, nil)
	if len(views) != 2 {
		t.Fatalf("every offering is still listed: %+v", views)
	}
	byID := map[string]int{}
	for i, v := range views {
		byID[v.ID] = i
	}
	future := views[byID["org.example.future"]]
	if future.Available || !strings.Contains(future.Requires, "99.0.0") {
		t.Fatalf("a release for a newer host is offered, or does not say what it needs: %+v", future)
	}
	now := views[byID["org.example.now"]]
	if !now.Available || now.Requires != "" {
		t.Fatalf("a release this host (%s) can run must be offered plainly: %+v", version.Authored(), now)
	}
}
