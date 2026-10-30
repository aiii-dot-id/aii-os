package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

func TestTheWorkCardCarriesTheMatchingBeliefWithoutReinforcing(t *testing.T) {
	b := newCognitionBench(t)
	engine := identity.NewEngine(b.st, b.door, ring.NewManager(), nil, identity.Ports{})
	commit := func(args map[string]any) {
		t.Helper()
		if _, err := engine.ExecuteAction(context.Background(), "verb", "commit", args); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.door.Append(ledger.EventExperienceCreate, 3, map[string]any{"id": "exp_origin", "content": "an origin", "provenance": "self"}, ""); err != nil {
		t.Fatal(err)
	}
	commit(map[string]any{"variant": "belief.upsert", "id": "b_preview_log", "statement": "The preview deployment prints its address in the build log.", "evidence_refs": "exp_origin"})
	for i, other := range []string{"Ferries sail at noon on Sundays.", "Rain falls hardest in April.", "Mira prefers tea to coffee.",
		"Bread rises faster in warm kitchens.", "Owls hunt mostly at dusk.", "Copper turns green outdoors.", "Chess clocks count down.",
		"Glaciers carve wide valleys.", "Violins need regular tuning.", "Bees dance to share directions."} {
		commit(map[string]any{"variant": "belief.upsert", "id": fmt.Sprintf("b_other_%d", i), "statement": other, "evidence_refs": "exp_origin"})
	}
	if err := b.st.StartWorkSession(context.Background(), "ws_preview", "redeploy the preview deployment"); err != nil {
		t.Fatal(err)
	}
	a := &App{store: b.st, cfg: defaultConfig()}
	accesses := func() int {
		var n int
		if err := b.st.QueryRowForTest(`SELECT COUNT(*) FROM memory_access`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := accesses()
	var card string
	for i := 0; i < 10; i++ {
		var err error
		if card, err = a.buildWorkState(); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(card, "b_preview_log: The preview deployment prints its address in the build log.") {
		t.Fatalf("the composed work card lacks the matching belief:\n%s", card)
	}
	if strings.Contains(card, "b_other_") {
		t.Fatalf("the card names a belief that shares no word with the work:\n%s", card)
	}
	if after := accesses(); after != before {
		t.Fatalf("COMPOSING THE CARD REINFORCED WHAT IT SHOWED: memory_access %d -> %d", before, after)
	}
}
