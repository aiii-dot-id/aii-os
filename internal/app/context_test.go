package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestFacilityRequestAdmitsRealAuthorityContext(t *testing.T) {
	projection, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = projection.Close() })
	rings := ring.NewManager()
	rings.Set(ring.Ring0, &ring.RingContent{Level: ring.Ring0, Content: strings.Repeat("constitution ", 40)})
	rings.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "boundary"})
	authority, err := (ringAuthority{prompt.NewGate(appRingSource{rings}, 4096), projection}).AuthorityPreamble()
	if err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	t.Cleanup(server.Close)
	a := &App{}
	facilities := newSwappableLLM(a.newLLMClient(llm.ClientConfig{Endpoint: server.URL, Model: "test"}, 20))
	_, _, err = facilities.ChatSimple(t.Context(), authority+"\n\nDREAM", "new experience")
	var limitErr *llm.ContextLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("facility authority context must meet the resolved model limit, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("oversize facility request reached the provider")
	}
}

func TestPromptsReadCurrentCharterFromProjection(t *testing.T) {
	projection, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = projection.Close() })

	mint := func(seq uint64, approval, charter string) {
		t.Helper()
		if err := projection.AddConversationTurn("operator", approval); err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(map[string]interface{}{
			"id":                        "rel_operator",
			"counterpart_name":          "Operator",
			"counterpart_role":          "operator",
			"relationship_type":         "founding_operator",
			"charter_text":              charter,
			"operator_approval_excerpt": approval,
			"operator_approval_turn":    seq,
			"approval_basis":            "conversation_turn",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := projection.Materialize(&ledger.Event{
			Seq: seq, Type: ledger.EventRelationshipUpsert, Ring: 1, Payload: payload,
		}); err != nil {
			t.Fatal(err)
		}
	}

	rings := ring.NewManager()
	rings.Set(ring.Ring0, &ring.RingContent{Level: ring.Ring0, Content: "constitution"})
	rings.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "boundary"})
	// A stale Ring 1 value must be inert; the projection is its sole prompt owner.
	rings.Set(ring.Ring1, &ring.RingContent{Level: ring.Ring1, Content: "STALE CHARTER"})
	gate := prompt.NewGate(appRingSource{rings}, 4096)
	composer := prompt.New(rings, 4096)
	composer.SetIdentitySource(projection)

	mint(1, "approve first", "FIRST CHARTER")
	first, err := composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := gate.SystemForPrompt(first); !strings.Contains(got, "FIRST CHARTER") || strings.Contains(got, "STALE CHARTER") {
		t.Fatalf("resident prompt did not use the projected charter: %q", got)
	}

	mint(2, "approve second", "SECOND CHARTER")
	second, err := composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	got := gate.SystemForPrompt(second)
	if !strings.Contains(got, "SECOND CHARTER") || strings.Contains(got, "FIRST CHARTER") || strings.Contains(got, "STALE CHARTER") {
		t.Fatalf("resident prompt retained stale charter content: %q", got)
	}

	authority, err := (ringAuthority{gate: gate, st: projection}).AuthorityPreamble()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(authority, "SECOND CHARTER") || strings.Contains(authority, "FIRST CHARTER") || strings.Contains(authority, "STALE CHARTER") {
		t.Fatalf("facility prompt retained stale charter content: %q", authority)
	}
}

func TestBuildWorkStateReturnsStoreFailure(t *testing.T) {
	projection, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	if err := projection.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = (&App{store: projection}).buildWorkState()
	if err == nil {
		t.Fatal("closed projection was treated as empty working state")
	}
}
