//go:build (linux && !android) || (darwin && !ios) || windows

package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// The plugins page names an acquisition in flight with what is on disk,
// and the catalog card says preparing instead of offering Install again.
func TestPendingViewsAndTheCatalogSayPreparing(t *testing.T) {
	store := newTestStore(t)
	decl := pluginhost.ModelDecl{Name: "stt.bin", URL: "https://example.invalid/stt.bin", SHA256: strings.Repeat("a", 64), Size: 1000}
	blob, _ := store.w.Blob(decl.SHA256)
	if err := os.WriteFile(blob+".partial", make([]byte, 400), 0o600); err != nil {
		t.Fatal(err)
	}
	// The store's writer with no download path.
	q := store.acquirer(nil, pluginhost.AcquirerConfig{After: func(time.Duration) <-chan time.Time { return nil }, Logf: func(string, ...any) {}})
	q.Attach(context.Background())
	q.Want(pluginhost.ModelMaterialForTest(store.w.Dir, "org.example.big", "2.0.0", decl))
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
		v.FilesTotal != 1 || v.FilesPresent != 0 || v.RetryAt != "" || !strings.Contains(v.Remedy, "place the files") || !strings.Contains(v.LastError, "place the files") || !strings.Contains(v.Summary, "400 B of 1000 B") {
		t.Fatalf("view: %+v", v)
	}
	if len(v.Models) != 1 || v.Models[0].Partial != 400 || v.Models[0].Present {
		t.Fatalf("models: %+v", v.Models)
	}
	cat := &plugincatalog.Index{Plugins: []plugincatalog.CatalogEntry{{ID: "org.example.big", Version: "2.0.0", Tier: "T3"}}}
	out := catalogViewsFor(cat, nil, a.pendingSummaries())
	if len(out) != 1 || out[0].Pending != "waiting" || !strings.Contains(out[0].PendingText, "400 B of 1000 B") || out[0].Installed {
		t.Fatalf("catalog: %+v", out)
	}
	// Nothing in flight: nothing is said.
	q.Forget("org.example.big")
	if a.pluginPendingViews() != nil || len(a.pendingSummaries()) != 0 {
		t.Fatal("a forgotten acquisition still shows")
	}
}
