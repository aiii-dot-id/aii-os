package identity

import (
	"context"
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// The operator's number for a provider's window on the dialogue reaches the
// facility the engine recalls, backfills and counts coverage with.
func TestTheEnginesFacilityIsGivenTheProvidersWindow(t *testing.T) {
	base, st, lg, kp, _ := setupEngine(t)
	e := NewEngine(st, &testEventWriter{ledger: lg, store: st, key: kp}, ring.NewManager(), base.toolDisc,
		Ports{Embedder: currencyEmbedder{}, ConversationWindow: 5})
	for i := 0; i < 8; i++ {
		if err := st.AddConversationTurn("operator", fmt.Sprintf("turn %d about the ferry", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Instruments().Backfill(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	_, cov, unavailable, err := e.Instruments().VectorCoverage(context.Background())
	if err != nil || unavailable != "" {
		t.Fatalf("coverage: %v %q", err, unavailable)
	}
	for _, c := range cov {
		if c.Store == "conversations" {
			if c.Rows != 5 || c.Embedded != 5 || c.Beyond != 3 {
				t.Fatalf("with a window of 5 over 8 turns the engine's facility counts %+v", c)
			}
			return
		}
	}
	t.Fatal("the coverage names no conversations")
}
