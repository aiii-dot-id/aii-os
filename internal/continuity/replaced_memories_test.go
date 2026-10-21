package continuity

import (
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// THE DAILY PASS PRUNES A PLUGIN'S REPLACED MEMORIES BY THE OPERATOR'S
// WINDOW, read from the settings it runs under
// (plugins.runtime.replaced_memory_days reaches it as
// Settings.ReplacedPluginMemories). Under a window of 30 days a memory
// replaced 40 days ago goes and one replaced 20 days ago stays; under the
// default of 90 both would stay.
func TestThePassPrunesReplacedPluginMemoriesByTheWindowItRunsUnder(t *testing.T) {
	a, _, _ := maintApp(t)
	day := 24 * time.Hour
	a.cfg.ReplacedPluginMemories = 30 * day
	now := time.Now()
	scope := store.PluginScope{PluginID: "id.a"}
	for _, m := range []struct {
		id, replaces string
		at           time.Time
	}{
		{"forty", "", now.Add(-200 * day)},
		{"forty-successor", "forty", now.Add(-40 * day)},
		{"twenty", "", now.Add(-200 * day)},
		{"twenty-successor", "twenty", now.Add(-20 * day)},
	} {
		if err := a.store.PluginMemoryAdd(scope, store.PluginMemory{ID: m.id, Text: "memory " + m.id, CreatedAt: m.at}, 0, m.replaces); err != nil {
			t.Fatalf("remember %s: %v", m.id, err)
		}
	}
	a.continuity.runMaintenance(t.Context())
	for id, want := range map[string]bool{"forty": false, "forty-successor": true, "twenty": true, "twenty-successor": true} {
		if _, found, err := a.store.PluginMemoryGet(scope, id); err != nil || found != want {
			t.Errorf("after a pass under a 30-day window, %s held: %v (%v), want %v", id, found, err, want)
		}
	}
}
