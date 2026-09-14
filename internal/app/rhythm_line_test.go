package app

import (
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
func TestRhythmLineRenders(t *testing.T) {
	projection, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer projection.Close()

	// .
	ws, err := (&App{store: projection}).buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ws, "### Rhythm") {
		t.Fatalf("empty store rendered a rhythm line: %q", ws)
	}

	// .
	if err := projection.InsertTurnMetric(store.TurnMetric{TsMs: time.Now().UnixMilli(), Calls: 13, ReadOnly: 10, Spawned: 1, Harvested: 2}); err != nil {
		t.Fatal(err)
	}
	ws, err = (&App{store: projection}).buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ws, "### Rhythm — last 48h: 1 turns, 13 calls, 76% read-only, 1 spawns, 2 harvests") {
		t.Fatalf("rhythm line missing or wrong:\n%s", ws)
	}
}
