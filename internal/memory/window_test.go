package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// machineSource is a source on this machine for these tests: the fake's
// vectors, with a pass that is given in time.
type machineSource struct{ *fakeEmbedder }

func (machineSource) PassWork() time.Duration { return time.Hour }

func seedTurns(t *testing.T, s *store.Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		role := "operator"
		if i%2 == 1 {
			role = "resident"
		}
		if err := s.AddConversationTurn(role, fmt.Sprintf("turn %d about the harbor", i)); err != nil {
			t.Fatal(err)
		}
	}
}

func dialogueCoverage(t *testing.T, f *Facility) Coverage {
	t.Helper()
	for pass := 0; pass < 40; pass++ {
		rep, err := f.Backfill(context.Background(), 100000)
		if err != nil {
			t.Fatal(err)
		}
		var left int64
		for _, bs := range rep.Stores {
			left += bs.Remaining
		}
		if left == 0 {
			break
		}
	}
	_, cov, unavailable, err := f.VectorCoverage(context.Background())
	if err != nil || unavailable != "" {
		t.Fatalf("coverage: %v %q", err, unavailable)
	}
	for _, c := range cov {
		if c.Store == "conversations" {
			return c
		}
	}
	t.Fatal("the coverage names no conversations")
	return Coverage{}
}

func TestASourceOnThisMachineReadsEveryTurnAndAProviderItsWindow(t *testing.T) {
	const turns = ConversationVectorWindow + 10

	// On this machine: every searchable turn holds a vector, and nothing
	// is left to words alone.
	s := newStore(t)
	seedTurns(t, s, turns)
	local := New(s)
	local.SetEmbedder(machineSource{&fakeEmbedder{basis: "machine/v1"}})
	if c := dialogueCoverage(t, local); c.Rows != turns || c.Embedded != turns || c.Beyond != 0 {
		t.Fatalf("a source on this machine: %+v, want all %d turns in scope and embedded, none beyond", c, turns)
	}

	// A provider: the newest window, and the count of what it leaves out.
	s = newStore(t)
	seedTurns(t, s, turns)
	provider := New(s)
	provider.SetEmbedder(&fakeEmbedder{basis: "provider/v1"})
	if c := dialogueCoverage(t, provider); c.Rows != ConversationVectorWindow || c.Embedded != ConversationVectorWindow || c.Beyond != 10 {
		t.Fatalf("a provider: %+v, want the newest %d in scope and embedded, 10 beyond", c, ConversationVectorWindow)
	}
}

func TestAProvidersWindowIsTheOperatorsNumber(t *testing.T) {
	s := newStore(t)
	seedTurns(t, s, 60)
	f := New(s)
	f.SetEmbedder(&fakeEmbedder{basis: "provider/v1"})
	f.SetConversationWindow(50)
	if c := dialogueCoverage(t, f); c.Rows != 50 || c.Embedded != 50 || c.Beyond != 10 || c.BeyondEmbedded != 0 {
		t.Fatalf("a provider with a window of 50 over 60 turns: %+v", c)
	}

	seedTurns(t, s, 10)
	_, cov, _, err := f.VectorCoverage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var before Coverage
	for _, c := range cov {
		if c.Store == "conversations" {
			before = c
		}
	}
	if before.Rows != 50 || before.Embedded != 40 || before.Beyond != 20 || before.BeyondEmbedded != 10 {
		t.Fatalf("ten new turns, before a pass: %+v, want 40 of 50 in the window and 10 of the 20 older holding a vector", before)
	}
	if c := dialogueCoverage(t, f); c.Rows != 50 || c.Embedded != 50 || c.Beyond != 20 || c.BeyondEmbedded != 10 {
		t.Fatalf("after the pass: %+v, want 50 of 50 in the window and the 10 older vectors kept", c)
	}
	// The same number bounds nothing for a source on this machine.
	s = newStore(t)
	seedTurns(t, s, 60)
	f = New(s)
	f.SetEmbedder(machineSource{&fakeEmbedder{basis: "machine/v1"}})
	f.SetConversationWindow(50)
	if c := dialogueCoverage(t, f); c.Rows != 60 || c.Embedded != 60 || c.Beyond != 0 {
		t.Fatalf("a source on this machine under a provider's number: %+v, want all 60", c)
	}
}

func TestAnOlderRowKeepsItsVectorAndIsStillReadByMeaning(t *testing.T) {
	s := newStore(t)
	seedTurns(t, s, 60)
	f := New(s)
	f.SetEmbedder(&fakeEmbedder{basis: "provider/v1"})
	f.SetConversationWindow(50)
	dialogueCoverage(t, f)
	seedTurns(t, s, 10)
	c := dialogueCoverage(t, f)
	ctx := context.Background()
	qv, _ := f.embedQuery(ctx, "port")
	if qv == nil {
		t.Fatal("rig: no query vector")
	}
	d, _ := Lookup("conversations")
	var hits []Hit
	if err := f.st.ReadWith(ctx, func(db store.Reader) error {
		var err error
		hits, _, err = layerMeaning(ctx, db, d, Query{}, qv, 64)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if int64(len(hits)) != c.Embedded+c.BeyondEmbedded || c.BeyondEmbedded != 10 {
		t.Fatalf("the meaning layer answers %d rows; coverage %+v says %d in the window and %d older hold a vector", len(hits), c, c.Embedded, c.BeyondEmbedded)
	}
}
