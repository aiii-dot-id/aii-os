package app

import (
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// A SNAPSHOT IS A SHALLOW COPY, so every holder of one is reading the
// SAME maps the live configuration holds — the plugins page, the speech
// engines card, a webhook resolving a credential handle. Storing a
// value has always known that and copied on write. CLEARING one deleted
// in place, right under the comment explaining why it must not.
//
// The race detector found this on a page that happened to render while
// a setting was being cleared, which is rare enough to have survived
// every gate so far. This provokes it deliberately.
func TestClearingASettingDoesNotMutateWhatReadersHold(t *testing.T) {
	const id = "com.example.settings"
	a := &App{cfg: &Config{Plugins: PluginsConfig{Settings: map[string]map[string]any{
		id:             {"alpha": "a", "beta": "b", "gamma": "c"},
		"other.plugin": {"kept": "k"},
	}}}}
	setPlugins(a, []*pluginhost.ActivePlugin{{ID: id}})

	held := a.configSnapshot()
	cfg := a.configSnapshot()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// A READER THAT ALREADY HAS ITS SNAPSHOT. That is the
			// real shape: a page took one and is rendering from it
			// while the operator clears a setting. Taking a fresh one
			// each time would never meet the mutation.
			for _, vals := range held.Plugins.Settings {
				for k, v := range vals {
					_, _ = k, v
				}
			}
		}
	}()

	for _, key := range []string{"alpha", "beta", "gamma"} {
		if err := a.applyPluginSetting(&cfg, "plugins.settings."+id+"."+key, ""); err != nil {
			t.Fatalf("clearing %s: %v", key, err)
		}
		a.cfgMu.Lock()
		a.cfg = &cfg
		a.cfgMu.Unlock()
	}
	close(stop)
	wg.Wait()

	// And it did the clearing it was asked to do.
	if got := a.configSnapshot().Plugins.Settings; got[id] != nil {
		t.Fatalf("the emptied plugin must be gone, not left behind: %v", got[id])
	} else if got["other.plugin"]["kept"] != "k" {
		t.Fatalf("another plugin's values were disturbed: %v", got)
	}
}
