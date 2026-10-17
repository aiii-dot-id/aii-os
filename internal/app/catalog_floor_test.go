package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
)

// onSystem stands the test on a system of the given version.
func onSystem(t *testing.T, v string) {
	t.Helper()
	was := systemVersion
	systemVersion = func() string { return v }
	t.Cleanup(func() { systemVersion = was })
}

func floorCatalog(floor string) *plugincatalog.Index {
	return &plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{{ID: "org.example.engine", Version: "1.0.0", Tier: "T3", Summary: "an engine",
		Packages: []plugincatalog.CatalogPackage{{Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			URL: "https://example.org/engine.aiiospkg", SHA256: strings.Repeat("a", 64), Size: 7, OSMinVersion: floor}}}}}
}

// THE STORE SAYS WHAT A BUILD NEEDS WHERE IT WOULD OFFER INSTALL. On a system
// older than the build's floor the entry is shown, not offered, and says
// what it needs; on one that meets the floor it is offered as before.
func TestTheStoreNamesABuildsSystemFloorInPlaceOfInstall(t *testing.T) {
	onSystem(t, "15.6")
	views := catalogViewsFor(floorCatalog("26.5"), nil, nil)
	if len(views) != 1 || views[0].Available || !strings.Contains(views[0].Requires, "26.5 or newer (this is 15.6)") {
		t.Fatalf("on an older system the entry must be shown, not offered, and say what it needs: %+v", views)
	}
	onSystem(t, "26.5")
	views = catalogViewsFor(floorCatalog("26.5"), nil, nil)
	if len(views) != 1 || !views[0].Available || views[0].Requires != "" {
		t.Fatalf("on a system that meets the floor the entry is offered: %+v", views)
	}
	onSystem(t, "")
	if views = catalogViewsFor(floorCatalog(""), nil, nil); !views[0].Available {
		t.Fatalf("a build that states no floor is offered where the system's version cannot be read: %+v", views)
	}
}

// AND AN INSTALL ASKED FOR ANYWAY DOWNLOADS NOTHING. The page does not offer
// it; a request that arrives all the same is refused before the fetch,
// with the same sentence, and the package's address is never asked.
func TestAnInstallBelowTheSystemFloorIsRefusedBeforeAnythingIsFetched(t *testing.T) {
	onSystem(t, "15.6")
	var asked atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { asked.Add(1) }))
	t.Cleanup(srv.Close)
	cat := floorCatalog("26.5")
	cat.Plugins[0].Packages[0].URL = srv.URL
	dir := catalogInstallDir(t)
	t.Chdir(dir)
	app := startCatalogApp(t, dir, *cat)
	app.pkgFetch = loopbackFetcher()

	err := app.InstallFromCatalog(context.Background(), "org.example.engine")
	var refusal *CatalogInstallRefusal
	if !errors.As(err, &refusal) || refusal.Reason != "not_offered" || !strings.Contains(err.Error(), "26.5 or newer (this is 15.6)") {
		t.Fatalf("an install below the floor must be refused as not offered, saying what the build needs: %v", err)
	}
	if n := asked.Load(); n != 0 {
		t.Fatalf("the package's address was asked %d times for a build this system cannot run", n)
	}
}

// THIS SYSTEM SAYS ITS VERSION IN NUMBERS WHERE IT HAS ONE. On macOS and on
// Windows the reading is what a floor is held against, so it must be one a
// floor can be compared with; elsewhere there is none, and a build that
// states a floor is not offered.
func TestThisSystemsVersionIsNumbersWhereItHasOne(t *testing.T) {
	v := readSystemVersion()
	oldest := plugincatalog.CatalogPackage{Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), OSMinVersion: "1"}
	ok, why := oldest.RunsOn(v)
	switch runtime.GOOS {
	case "darwin", "windows":
		if !ok {
			t.Fatalf("this system reads its version as %q, which a floor of 1 cannot be held against: %s", v, why)
		}
	default:
		if v != "" || ok {
			t.Fatalf("this system has no one version to read, and reads %q (offered %v)", v, ok)
		}
	}
}
