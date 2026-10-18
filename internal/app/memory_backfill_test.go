package app

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/memory"
)

// The facility's seam names its basis from the provider entry and
// refuses honestly when no embeddings model is named.
func TestMemoryEmbedderBasisIsTheProviderAndItsModel(t *testing.T) {
	a := &App{}
	em := memoryEmbedder{a}
	a.adoptProvider(providerEntry{Name: "zAI"})
	if _, err := em.Basis(); err == nil || !strings.Contains(err.Error(), "embeddings_model") {
		t.Fatalf("no model must refuse with the remedy: %v", err)
	}
	if _, _, err := em.Embed(t.Context(), memory.KindCue, []string{"x"}); err == nil {
		t.Fatal("embed without a model must refuse")
	}
	a.adoptProvider(providerEntry{Name: "zAI", EmbeddingsModel: "embedding-3"})
	basis, err := em.Basis()
	if err != nil || basis != "zAI/embedding-3" {
		t.Fatalf("basis = %q %v", basis, err)
	}
	if _, _, err := em.Embed(t.Context(), memory.KindCue, []string{"x"}); err == nil || !strings.Contains(err.Error(), "no model client") {
		t.Fatalf("embed without a client must say so: %v", err)
	}
}

// basisOf is the basis a provider entry's vectors are made under.
func basisOf(t *testing.T, name, model string) string {
	t.Helper()
	b, err := providerBasis(providerEntry{Name: name, EmbeddingsModel: model})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAProviderBasisNamesItsProviderAndItsModelUnambiguously(t *testing.T) {
	for _, c := range []struct{ name, model, want string }{
		{"zAI", "embedding-3", "zAI/embedding-3"},
		{"My Local", "text-embedding-3-small", "My Local/text-embedding-3-small"},
		{"OpenRouter", "openai/text-embedding-3-small", "OpenRouter/openai/text-embedding-3-small"},
		{"Local/team", "embed", "Local%2Fteam/embed"},
		{"Local", "team/embed", "Local/team/embed"},
		{"Local%2Fteam", "embed", "Local%252Fteam/embed"},
	} {
		if got := basisOf(t, c.name, c.model); got != c.want {
			t.Errorf("provider %q, model %q: basis %q, want %q", c.name, c.model, got, c.want)
		}
	}
}

// A CHANGE BETWEEN TWO PROVIDERS THAT SHARED A BASIS REBUILDS THE VECTORS.
// The first's vectors are of another basis now: the second's cue is
// compared with none of them, and the next pass drops them and asks the
// second for its own model's.
func TestAChangeBetweenProvidersThatSharedABasisRebuildsTheVectors(t *testing.T) {
	a := liveApp(t)
	threeMemories(t, a)
	first, _ := embedEndpoint(t, []float32{1, 0})
	second, secondAsked := embedEndpoint(t, []float32{1, 0})
	a.adoptRuntime(providerEntry{Name: "Local/team", EmbeddingsModel: "embed"}, first)
	f := memory.New(a.store)
	f.SetEmbedder(memoryEmbedder{a})
	ctx := context.Background()
	if rep, err := f.Backfill(ctx, memory.BackfillBudget); err != nil || rep.Embedded != 3 {
		t.Fatalf("rig: the first provider's pass: %+v %v", rep, err)
	}

	a.adoptRuntime(providerEntry{Name: "Local", EmbeddingsModel: "team/embed"}, second)
	res, err := f.Recall(ctx, memory.Query{Text: "zeppelin"})
	if err != nil || len(res.Hits) != 0 {
		t.Fatalf("the second provider's cue was compared with the first's vectors: %d hits %+v %v", len(res.Hits), res.Meaning, err)
	}
	rep, err := f.Backfill(ctx, memory.BackfillBudget)
	if err != nil || rep.Dropped != 3 || rep.Embedded != 3 || rep.Basis != basisOf(t, "Local", "team/embed") {
		t.Fatalf("the pass after the change must drop the first model's three vectors and make the second's: %+v %v", rep, err)
	}
	if got := secondAsked(); !slices.Contains(got, "team/embed") {
		t.Fatalf("the second provider was asked for %q; want its own model", got)
	}
}

// What a provider made is told from what another made by the same
// spelling: with no choice stored, a provider whose name begins another's
// does not take that one's vectors for its own, and keeps its own.
func TestAProviderKnowsItsOwnVectorsByTheSameSpelling(t *testing.T) {
	const id = "org.example.vectors"
	for _, c := range []struct {
		entry, kept, kind string
	}{
		{entry: "Local", kept: "Local/team", kind: "plugin"},
		{entry: "Local/team", kept: "Local/team", kind: "provider"},
	} {
		a := liveApp(t)
		nameProvider(a, c.entry, "bge-m3")
		keep(t, a, basisOf(t, c.kept, "bge-m3"))
		checked(t, a, runSource(t, a, id, working))
		setChoice(a, "")
		if pick := a.pickMeaningSource(); pick.kind != c.kind {
			t.Errorf("provider %q with %q's vectors kept and a plugin running: picked %q, want %q", c.entry, c.kept, pick.kind, c.kind)
		}
	}
}
