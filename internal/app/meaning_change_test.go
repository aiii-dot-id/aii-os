package app

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/memory"
)

func TestChangingTheSourceSaysWhatItWouldCostAndStoresNothing(t *testing.T) {
	const one, two = "org.example.vectors", "org.example.other"
	ctx := context.Background()
	a := liveApp(t)
	asked := providerEndpoint(t, a)
	threeMemories(t, a)
	s1 := runSource(t, a, one, working)
	checked(t, a, s1)
	setChoice(a, "plugin:"+one)
	f := memory.New(a.store)
	f.SetEmbedder(sourceEmbedder{a})
	if rep, err := f.Backfill(ctx, 10); err != nil || rep.Embedded != 3 {
		t.Fatalf("rig: the chosen plugin embeds the three memories: %+v %v", rep, err)
	}
	keep(t, a, "Elsewhere/an-older-model")
	s2 := runSource(t, a, two, working)
	checked(t, a, s2)
	providerAsked, oneAsked, twoAsked := asked.Load(), s1.asked(), s2.asked()

	state, err := a.meaningState(ctx, f, a.pickMeaningSource())
	if err != nil {
		t.Fatal(err)
	}
	byChoice := map[string]dashboard.MeaningChange{}
	for _, c := range state.Changes {
		byChoice[c.Choice] = c
	}
	if got := byChoice["provider"]; got != (dashboard.MeaningChange{Choice: "provider", Dropped: 4, ToEmbed: 3, SentTo: "Local"}) {
		t.Errorf("the provider's change drops every vector of another basis, embeds the three rows and names the entry they are sent to: %+v", got)
	}
	if got := byChoice["plugin:"+two]; got != (dashboard.MeaningChange{Choice: "plugin:" + two, Dropped: 4, ToEmbed: 3}) {
		t.Errorf("another plugin's change drops and embeds the same, and sends nowhere: %+v", got)
	}
	if _, listed := byChoice["plugin:"+one]; listed || len(state.Changes) != 2 {
		t.Errorf("the choice stored is not a change: %+v", state.Changes)
	}
	if asked.Load() != providerAsked || s1.asked() != oneAsked || s2.asked() != twoAsked {
		t.Errorf("reading what a change costs asked a source: provider %d->%d, %s %d->%d, %s %d->%d",
			providerAsked, asked.Load(), one, oneAsked, s1.asked(), two, twoAsked, s2.asked())
	}
	if got := a.configSnapshot().Memory.MeaningSource; got != "plugin:"+one {
		t.Errorf("reading what a change costs stored a choice: %q", got)
	}
	if bases, err := a.store.MemoryVectorBases(ctx); err != nil || len(bases) != 2 {
		t.Errorf("reading what a change costs dropped a vector: %v %v", bases, err)
	}

	// With the layer turned off, choosing the plugin that made the vectors
	// again drops only the stray of another basis and embeds nothing: the
	// numbers are of what that source lacks, not of the whole record.
	setChoice(a, "none")
	state, err = a.meaningState(ctx, f, a.pickMeaningSource())
	if err != nil {
		t.Fatal(err)
	}
	back := dashboard.MeaningChange{}
	for _, c := range state.Changes {
		if c.Choice == "plugin:"+one {
			back = c
		}
	}
	if back != (dashboard.MeaningChange{Choice: "plugin:" + one, Dropped: 1, ToEmbed: 0}) {
		t.Errorf("choosing again the plugin that made the vectors drops the one of another basis and embeds nothing: %+v", back)
	}

	// A provider that names no embeddings model cannot say its basis, and
	// is offered with no numbers.
	nameProviderModel(a, "")
	state, err = a.meaningState(ctx, f, a.pickMeaningSource())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range state.Changes {
		if c.Choice == "provider" {
			t.Errorf("a provider that names no model has no numbers to say: %+v", c)
		}
	}
}
