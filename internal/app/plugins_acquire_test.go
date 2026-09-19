package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// .
// .
// .
func TestMaterialReadyAsksForAPass(t *testing.T) {
	a := &App{sweepPoke: make(chan struct{}, 1)}
	a.materialReady("org.example.x")
	select {
	case <-a.sweepPoke:
	default:
		t.Fatal("no pass was asked for")
	}
}

// .
// .
func TestPendingViewsAndTheCatalogSayPreparing(t *testing.T) {
	dir := t.TempDir()
	decl := pluginhost.ModelDecl{Name: "stt.bin", URL: "https://example.invalid/stt.bin", SHA256: strings.Repeat("a", 64), Size: 1000}
	if err := os.WriteFile(filepath.Join(dir, "stt.bin.partial"), make([]byte, 400), 0o600); err != nil {
		t.Fatal(err)
	}
	q := pluginhost.NewAcquirer(pluginhost.AcquirerConfig{Backoff: func(int) time.Duration { return time.Hour }, Logf: func(string, ...interface{}) {}})
	q.Attach(context.Background())
	q.Want(pluginhost.Material{PluginID: "org.example.big", Version: "2.0.0", Models: []pluginhost.ModelDecl{decl}, ModelsDir: dir})
	deadline := time.Now().Add(15 * time.Second)
	for {
		if st, ok := q.Status("org.example.big"); ok && st.Phase == pluginhost.PhaseWaiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the job never reached waiting")
		}
		time.Sleep(10 * time.Millisecond)
	}
	a := &App{pluginOpts: &pluginhost.Options{Acquirer: q}}
	views := a.pluginPendingViews()
	if len(views) != 1 {
		t.Fatalf("views: %+v", views)
	}
	v := views[0]
	if v.ID != "org.example.big" || v.Version != "2.0.0" || v.Phase != "waiting" || v.BytesPresent != 400 || v.BytesTotal != 1000 ||
		v.FilesTotal != 1 || v.FilesPresent != 0 || v.RetryAt == "" || !strings.Contains(v.LastError, "place the files") || !strings.Contains(v.Summary, "400 B of 1000 B") {
		t.Fatalf("view: %+v", v)
	}
	if len(v.Models) != 1 || v.Models[0].Partial != 400 || v.Models[0].Present {
		t.Fatalf("models: %+v", v.Models)
	}
	cat := &pluginhost.Catalog{Plugins: []pluginhost.CatalogEntry{{ID: "org.example.big", Version: "2.0.0", Tier: "T3"}}}
	out := catalogViewsFor(cat, nil, a.pendingSummaries())
	if len(out) != 1 || out[0].Pending != "waiting" || !strings.Contains(out[0].PendingText, "400 B of 1000 B") || out[0].Installed {
		t.Fatalf("catalog: %+v", out)
	}
	// .
	q.Forget("org.example.big")
	if a.pluginPendingViews() != nil || len(a.pendingSummaries()) != 0 {
		t.Fatal("a forgotten acquisition still shows")
	}
}

// .
// .
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
	cat := &pluginhost.Catalog{Plugins: []pluginhost.CatalogEntry{{ID: "org.example.slow", Version: "1.2.0", Tier: "T3"}}}
	if out := catalogViewsFor(cat, nil, marks); len(out) != 1 || out[0].Pending != "refused" || !strings.Contains(out[0].PendingText, "readiness") {
		t.Fatalf("catalog: %+v", out)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
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
