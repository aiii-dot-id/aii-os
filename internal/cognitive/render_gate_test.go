package cognitive

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestAnUnchangedRenderIsNotAskedForAgain(t *testing.T) {
	setup := func() (*ConsolidateFacility, *mockStore, *mockRingWriter, *mockLLM) {
		st := &mockStore{unprocessedCnt: 0, beliefs: []store.Belief{{ID: "b1", Statement: "standing belief", Ring: 3}}}
		rw := &mockRingWriter{}
		llm := &mockLLM{override: "You believe the standing belief."}
		return NewConsolidate(st, llm, &mockLedger{st: st}, rw, ConsolidateConfig{Threshold: 3}), st, rw, llm
	}
	pass := func(c *ConsolidateFacility) {
		t.Helper()
		if err := c.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("unchanged input, unchanged section", func(t *testing.T) {
		c, _, rw, llm := setup()
		pass(c)
		pass(c)
		pass(c)
		if llm.calls != 1 {
			t.Fatalf("%d model calls for three identical passes, want 1", llm.calls)
		}
		if got := rw.section(ring.Ring3, "working_truth"); got != "You believe the standing belief." {
			t.Fatalf("the render does not stand: %q", got)
		}
	})
	t.Run("changed input", func(t *testing.T) {
		c, st, _, llm := setup()
		pass(c)
		st.beliefs = append(st.beliefs, store.Belief{ID: "b2", Statement: "a new belief", Ring: 3})
		pass(c)
		if llm.calls != 2 {
			t.Fatalf("%d model calls, want 2: changed input must render", llm.calls)
		}
	})
	t.Run("a lost section", func(t *testing.T) {
		c, _, rw, llm := setup()
		pass(c)
		rw.SetRingSection(ring.Ring3, "working_truth", "")
		pass(c)
		if llm.calls != 2 {
			t.Fatalf("%d model calls, want 2: a lost section must render", llm.calls)
		}
	})
	t.Run("a section another writer rewrote", func(t *testing.T) {
		c, _, rw, llm := setup()
		pass(c)
		rw.SetRingSection(ring.Ring3, "working_truth", "rendered from the store")
		pass(c)
		if llm.calls != 2 {
			t.Fatalf("%d model calls, want 2: a rewritten section must render", llm.calls)
		}
	})
}
