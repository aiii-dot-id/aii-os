package plugincatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

func TestAnIncompleteCacheIsReportedAndRefreshRepairsIt(t *testing.T) {
	plat, err := packagetest.NewRole("cache-state-test", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	md, sig := signedIndex(t, plat, "4.0.0")
	for _, missing := range []string{CatalogFile, CatalogSigFile} {
		t.Run(missing, func(t *testing.T) {
			cache := t.TempDir()
			if err := writeCache(cache, md, sig); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(cache, missing)); err != nil {
				t.Fatal(err)
			}
			var c Catalog
			c.Open("", cache, plat.Env, func() string { return "https://cache.example.test/catalog" })
			if c.Current() != nil {
				t.Fatal("incomplete pair was trusted")
			}
			if _, problem := c.State(); problem == "" {
				t.Fatal("a partial cache was silently treated as absent")
			}
			c.fetch = func(_ context.Context, url string, _ int64) ([]byte, error) {
				if url == "https://cache.example.test/catalog" {
					return md, nil
				}
				return sig, nil
			}
			if err := c.Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, problem := c.State(); problem != "" {
				t.Fatalf("repair retained stale warning: %s", problem)
			}
			var restarted Catalog
			restarted.Open("", cache, plat.Env, func() string { return "https://unused.invalid" })
			if got := restarted.Current(); got == nil || got.Plugins[0].Version != "4.0.0" {
				t.Fatal("repaired cache did not survive offline restart")
			}
		})
	}
	var first Catalog
	first.Open("", filepath.Join(t.TempDir(), "absent"), plat.Env, func() string { return "https://unused.invalid" })
	if _, problem := first.State(); problem != "" {
		t.Fatalf("ordinary first boot must not acquire a false error: %s", problem)
	}
	var checkout Catalog
	checkout.Open(filepath.Join(t.TempDir(), "configured-but-missing"), "", plat.Env, nil)
	if _, problem := checkout.State(); problem == "" {
		t.Fatal("an explicitly configured missing checkout was not reported")
	}
}

func TestRefreshLoopPublishesFailuresAndKeepsRetrying(t *testing.T) {
	plat, err := packagetest.NewRole("loop-state-test", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	var c Catalog
	c.Open("", "", plat.Env, func() string { return "https://loop.example.test/catalog" })
	fault := errors.New("fixture fetch unavailable")
	c.fetch = func(context.Context, string, int64) ([]byte, error) { return nil, fault }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	changed := make(chan string, 4)
	go func() { defer close(done); c.Run(ctx, func() { _, problem := c.State(); changed <- problem }) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("catalog loop did not stop")
		}
	})
	for range 2 {
		c.Poke()
		select {
		case problem := <-changed:
			if problem == "" {
				t.Fatal("failed fetch was broadcast as healthy")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("refresh failure did not notify the existing observer")
		}
	}
}
