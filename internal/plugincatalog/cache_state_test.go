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
	for _, missing := range []string{CatalogFileFor(OwnLine()), CatalogFileFor(OwnLine()) + ".sig"} {
		t.Run(missing, func(t *testing.T) {
			cache := t.TempDir()
			if err := writeCache(cache, OwnLine(), md, sig); err != nil {
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
	type outcome struct {
		problem string
		err     error
	}
	changed := make(chan outcome, 4)
	go func() {
		defer close(done)
		c.Run(ctx, func(err error) { _, problem := c.State(); changed <- outcome{problem, err} }, func() time.Duration { return time.Hour })
	}()
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
		case got := <-changed:
			if got.problem == "" {
				t.Fatal("failed fetch was broadcast as healthy")
			}
			// The observer is handed the attempt's own outcome: a failed
			// fetch is never taken for one this host completed.
			if !errors.Is(got.err, fault) {
				t.Fatalf("the observer was not handed the failed fetch: %v", got.err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("refresh failure did not notify the existing observer")
		}
	}
}

func TestTheRefreshesAreSpacedByTheOperatorsSetting(t *testing.T) {
	plat, err := packagetest.NewRole("loop-spacing-test", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	var c Catalog
	c.Open("", "", plat.Env, func() string { return "https://loop.example.test/catalog" })
	c.fetch = func(context.Context, string, int64) ([]byte, error) {
		return nil, errors.New("fixture fetch unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	attempts := make(chan struct{}, 8)
	go func() {
		defer close(done)
		c.Run(ctx, func(error) { attempts <- struct{}{} }, func() time.Duration { return 20 * time.Millisecond })
	}()
	t.Cleanup(func() { cancel(); <-done })
	c.Poke()
	for i := range 3 {
		select {
		case <-attempts:
		case <-time.After(10 * time.Second):
			t.Fatalf("refresh %d did not come at the spacing the setting gives", i+1)
		}
	}
	if got := HeldRefreshMinutes(0); got != DefaultRefreshMinutes {
		t.Fatalf("an unset spacing is %d minutes", got)
	}
	if HeldRefreshMinutes(1) != MinRefreshMinutes || HeldRefreshMinutes(1<<20) != MaxRefreshMinutes || HeldRefreshMinutes(90) != 90 {
		t.Fatal("a spacing is not held to its bounds")
	}
}
