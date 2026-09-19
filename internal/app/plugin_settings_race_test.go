package app

import (
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestClearingASettingDoesNotMutateWhatReadersHold(t *testing.T) {
	const id = "com.example.settings"
	a := &App{cfg: &Config{Plugins: PluginsConfig{Settings: map[string]map[string]interface{}{
		id:             {"alpha": "a", "beta": "b", "gamma": "c"},
		"other.plugin": {"kept": "k"},
	}}}}
	a.plugins = []*pluginhost.ActivePlugin{{ID: id}}

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
			// .
			// .
			// .
			// .
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

	// .
	if got := a.configSnapshot().Plugins.Settings; got[id] != nil {
		t.Fatalf("the emptied plugin must be gone, not left behind: %v", got[id])
	} else if got["other.plugin"]["kept"] != "k" {
		t.Fatalf("another plugin's values were disturbed: %v", got)
	}
}
