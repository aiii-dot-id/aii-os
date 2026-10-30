//go:build (linux && !android) || (darwin && !ios) || windows

package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func TestAnOperatorLimitChangeRejudgesWithoutARestart(t *testing.T) {
	roots, pkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	models := newTestStore(t)
	fetch := newSwapFetch()
	s := bootSwap(t, roots, pkg, models, roomy, fetch, func(e *swapEngines) {
		e.a.cfg.Plugins.Runtime.AdmissionMemoryBudgetBytes = 4 << 30 // below the full set's 8 GiB
	})
	s.until(t, "the small set serving, the full one passed over by the budget", func() bool { return s.serving() == "small" })
	var card *pluginfacility.Selection
	for _, act := range s.view().Activations {
		if act.Role == pluginfacility.RoleActive {
			card = act.Selection
		}
	}
	if card == nil || len(card.Sets) != 2 || len(card.Sets[0].Reasons) == 0 || !strings.Contains(card.Sets[0].Reasons[0].Remedy, "admission_memory_budget_bytes") {
		t.Fatalf("the full set is not passed over by the budget: %+v", card)
	}
	time.Sleep(200 * time.Millisecond)
	if starts, _, _, _ := s.eng.record(); len(starts) != 1 {
		t.Fatalf("something moved before the limit did: %v", starts)
	}
	s.a.cfgMu.Lock()
	s.a.cfg.Plugins.Runtime.AdmissionMemoryBudgetBytes = 16 << 30
	s.a.cfgMu.Unlock()
	if err := s.a.applyPluginLimits(); err != nil {
		t.Fatal(err)
	}
	s.until(t, "the full set serving and the small one drained", func() bool {
		_, stops, live, _ := s.eng.record()
		return s.serving() == "full" && live == 1 && len(stops) == 1 && stops[0] == "small"
	})
	if got := fetch.urls(); len(got) != 2 || got[1] != fullURL+"@0" {
		t.Fatalf("fetched %v", got)
	}
}

// A DOWNLOAD THE RULE ENDED IS FINAL ON THE PAGE, WITH ITS REMEDY AND TRY
// AGAIN, and Try again asks again (G1): through the product's facility and
// Acquirer, from the operator's button (RetryPlugin).
func TestAnEndedDownloadIsFinalOnThePageUntilTryAgain(t *testing.T) {
	roots, pkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	models := newTestStore(t)
	fetch := newSwapFetch()
	fetch.fail[fullURL] = &pluginhost.FetchStatusError{Status: 404}
	s := bootSwap(t, roots, pkg, models, roomy, fetch)
	s.until(t, "the refusal on the page", func() bool {
		r := s.view().Refusal
		return r != nil && r.Class == pluginfacility.ClassPermanent
	})
	v := s.view()
	if r := v.Refusal; r.Stage != pluginfacility.StageMaterial || !strings.Contains(r.Remedy, "Nothing is at the address") || !v.RetryAt.IsZero() {
		t.Fatalf("the end of the download on the card: %+v, retry at %v", r, v.RetryAt)
	}
	s.a.applyFacilitySnapshot()
	var row bool
	for _, p := range s.a.pluginPendingViews() {
		if p.ID == s.id {
			row = p.Phase == lifeRefused && p.Refusal != nil && strings.Contains(p.Refusal.Remedy, "Nothing is at the address") && p.RetryAt == ""
		}
	}
	if !row {
		t.Fatalf("the page's row: %+v", s.a.pluginPendingViews())
	}
	time.Sleep(200 * time.Millisecond)
	if got := fetch.urls(); len(got) != 1 {
		t.Fatalf("an ended download was asked again on its own: %v", got)
	}
	fetch.mu.Lock()
	delete(fetch.fail, fullURL)
	fetch.mu.Unlock()
	if err := s.a.RetryPlugin(s.id); err != nil {
		t.Fatal(err)
	}
	s.until(t, "Try again to bring the full set up", func() bool { return s.serving() == "full" })
}

func TestADownloadTheNetworkEndedStartsAgainWhenTheCatalogIsNextFetched(t *testing.T) {
	roots, pkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	models := newTestStore(t)
	fetch := newSwapFetch()
	fetch.pass = true
	fetch.fail[fullURL] = &pluginhost.NetworkError{Cause: errors.New("dial tcp: connect: network is unreachable")}
	s := bootSwap(t, roots, pkg, models, roomy, fetch)
	s.until(t, "the refusal on the page", func() bool {
		r := s.view().Refusal
		return r != nil && r.Class == pluginfacility.ClassPermanent
	})
	if r := s.view().Refusal; r.Stage != pluginfacility.StageMaterial || !r.WaitsOn(pluginfacility.InputNetwork) || !strings.Contains(r.Remedy, "tried again by itself when the plugin catalog is next fetched") {
		t.Fatalf("the network's end on the card: %+v", r)
	}
	s.a.applyFacilitySnapshot()
	var row bool
	for _, p := range s.a.pluginPendingViews() {
		if p.ID == s.id {
			row = p.Phase == lifeRefused && p.Resumes
		}
	}
	if !row {
		t.Fatalf("the page's row does not say the host asks again: %+v", s.a.pluginPendingViews())
	}
	if got := fetch.urls(); len(got) != pluginhost.MaxDownloadAttempts {
		t.Fatalf("five attempts the network failed: fetched %v", got)
	}
	fetch.mu.Lock()
	delete(fetch.fail, fullURL)
	fetch.mu.Unlock()
	s.a.catalogRefreshed(errors.New("fetching the index: no route to host"))
	time.Sleep(200 * time.Millisecond)
	if got := fetch.urls(); len(got) != pluginhost.MaxDownloadAttempts {
		t.Fatalf("a refresh of the catalog that failed asked again: %v", got)
	}
	s.a.catalogRefreshed(nil)
	s.until(t, "the catalog's fetch to bring the full set up", func() bool { return s.serving() == "full" })
}

func TestTheCatalogsSpacingIsSetAtTheDoor(t *testing.T) {
	a := routeApp(t, true, serviceOffersRelays)
	st, err := a.applyConfigChange(map[string]any{"plugins.catalog_refresh_minutes": float64(20)})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.RestartRequired) != 0 {
		t.Fatalf("held for a restart: %v", st.RestartRequired)
	}
	if got := a.catalogRefreshEvery(); got != 20*time.Minute {
		t.Fatalf("the loop is spaced by %s", got)
	}
	if onDisk, err := LoadConfig(a.configSnapshot().SourcePath); err != nil || onDisk.Plugins.CatalogRefreshMinutes != 20 {
		t.Fatalf("config.json does not hold it: %v", err)
	}
	for _, bad := range []any{float64(-1), float64(14), float64(1441), "often"} {
		if _, err := a.applyConfigChange(map[string]any{"plugins.catalog_refresh_minutes": bad}); err == nil || !strings.Contains(err.Error(), "plugins.catalog_refresh_minutes") {
			t.Fatalf("%v was taken, or refused without naming the key: %v", bad, err)
		}
	}
	if _, err := a.applyConfigChange(map[string]any{"plugins.catalog_refresh_minutes": float64(0)}); err != nil || a.catalogRefreshEvery() != time.Hour {
		t.Fatalf("zero takes the hour: %v, %s", err, a.catalogRefreshEvery())
	}
}
