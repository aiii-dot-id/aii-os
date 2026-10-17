package app

import (
	"sync"
	"testing"
)

// A SAFE BOOT'S FACILITY IS BUILT BY A PAGE WHILE ANOTHER POKES IT (T23 F2).
// A live boot builds the plugin facility before the dashboard serves (its
// first scan); a SAFE boot scans nothing, so the first request that renders
// the plugins builds it — pluginFacility, through facilityOnce, on that
// request's goroutine — while another request's change ends in a poke
// (pokePluginSweep: a config set of plugins.*, an install, an uninstall).
// The poke read the field bare, beside the once's write, and enterSafeFor
// read it under pluginMu, which the write did not take: two requests at
// once were a data race, and -race reported it through this boot. The
// facility is published under pluginMu now, and each reader that must not
// build it reads it there. The page's render takes pluginMu on its way to
// the build, which can order a locked poke before it; the build alone,
// beside the poke, holds the write side to the lock as well.
func TestASafeBootsFacilityIsBuiltByOnePageWhileAnotherPokesIt(t *testing.T) {
	for _, build := range []struct {
		name string
		run  func(*testing.T, *App)
	}{
		{"a page renders the plugins", func(t *testing.T, a *App) {
			if a.buildLiveHandler().GetConfig() == nil {
				t.Error("the page's configuration read answered nothing")
			}
		}},
		{"the build itself", func(_ *testing.T, a *App) { a.pluginFacility() }},
	} {
		t.Run(build.name, func(t *testing.T) {
			dir := t.TempDir()
			keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Facility")
			cfg := safebootConfig(t, dir, "Facility", keyPath, ledgerPath, dbPath)
			a := New(cfg)
			t.Cleanup(func() { stopApp(t, a) })
			if err := a.startSafeBoot(safeRecord, "test: a page builds the facility"); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { // the facility is built here
				defer wg.Done()
				<-start
				build.run(t, a)
			}()
			go func() { // another page's change ends in a poke
				defer wg.Done()
				<-start
				a.pokePluginSweep()
			}()
			close(start)
			wg.Wait()
			if a.pluginFacility() == nil {
				t.Fatal("no facility after it was built")
			}
		})
	}
}
