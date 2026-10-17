package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
)

// The Acquirer's word reaches the sweep: a pass is asked for now — so
// a plugin whose models arrived activates without a click, a re-install
// or a restart.
func TestMaterialReadyAsksForAPass(t *testing.T) {
	a := &App{sweepPoke: make(chan struct{}, 1)}
	a.materialReady("org.example.x")
	select {
	case <-a.sweepPoke:
	default:
		t.Fatal("no pass was asked for")
	}
}

// A wanted package's attempt is named while it runs and when it refuses,
// and the catalog row says so instead of offering Install.
func TestAnAttemptIsNamedStartingThenRefused(t *testing.T) {
	a := &App{sweepPoke: make(chan struct{}, 1)}
	a.markPlugin("org.example.slow", "1.2.0", lifeStarting, "")
	views := a.pluginPendingViews()
	if len(views) != 1 || views[0].Phase != "starting" || views[0].Version != "1.2.0" || views[0].Since == "" || views[0].Summary == "" {
		t.Fatalf("starting: %+v", views)
	}
	a.markPlugin("org.example.slow", "1.2.0", lifeRefused, "the child did not report readiness within 30s")
	views = a.pluginPendingViews()
	if len(views) != 1 || views[0].Phase != "refused" || views[0].Summary != "the child did not report readiness within 30s" {
		t.Fatalf("refused: %+v", views)
	}
	marks := a.pendingSummaries()
	cat := &plugincatalog.Index{Plugins: []plugincatalog.CatalogEntry{{ID: "org.example.slow", Version: "1.2.0", Tier: "T3"}}}
	if out := catalogViewsFor(cat, nil, marks); len(out) != 1 || out[0].Pending != "refused" || !strings.Contains(out[0].PendingText, "readiness") {
		t.Fatalf("catalog: %+v", out)
	}

	if err := a.RetryPlugin("org.example.slow"); err == nil {
		t.Fatal("Try again reported success for an id no facility knows")
	}
	if a.pluginPendingViews() == nil {
		t.Fatal("a Try again that reached nothing erased the refusal from the page")
	}
	if err := a.RetryPlugin("../x"); err == nil {
		t.Fatal("a path was accepted as a plugin id")
	}
}
