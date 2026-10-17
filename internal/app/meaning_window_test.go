package app

import (
	"context"
	"fmt"
	"testing"
)

// The operator's number for a provider's window on the conversation goes
// the whole way: from the configuration, through the start, to the facility
// the engine recalls and backfills with, and from that facility's count to
// what the Memory card is given. Each joint was a place the number could
// be dropped without any other test noticing.
func TestAProvidersWindowReachesTheEngineAndTheCardFromTheConfiguration(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Window")
	cfg := safebootConfig(t, dir, "Window", keyPath, ledgerPath, dbPath)
	cfg.Dashboard.Port = 0
	cfg.Memory.ConversationVectorWindow = 5
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })

	// A provider's source, stood in on the engine's own facility.
	source := cardEmbedder{basis: "Local/bge-m3", floor: 0.3}
	f := a.engine.Instruments()
	f.SetEmbedder(source)
	for i := 0; i < 8; i++ {
		if err := a.store.AddConversationTurn("operator", fmt.Sprintf("turn %d about the harbor", i)); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if _, err := f.Backfill(ctx, 100); err != nil {
		t.Fatal(err)
	}
	state, err := a.meaningState(ctx, f, meaningPick{source: source, kind: "provider"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range state.Coverage {
		if c.Store != "conversations" {
			continue
		}
		if c.Rows != 5 || c.Embedded < 5 || c.Beyond < 3 {
			t.Fatalf("with memory.conversation_vector_window 5 and eight turns said, the card is given %+v: want 5 in scope and the rest counted as beyond", c)
		}
		return
	}
	t.Fatalf("the card's state names no conversations: %+v", state.Coverage)
}
