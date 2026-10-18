package memory

import (
	"context"
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/memory/vec"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestPluginActivationFiltersEveryCandidatePoolBeforeLimit(t *testing.T) {
	s := newStore(t)
	a, b := store.PluginScope{PluginID: "p", Activation: "a"}, store.PluginScope{PluginID: "p", Activation: "b"}
	add := func(scope store.PluginScope, id, old string, temp bool) {
		t.Helper()
		if err := s.PluginMemoryAdd(scope, store.PluginMemory{ID: id, Text: "lighthouse harbor", Temp: temp}, 0, old); err != nil {
			t.Fatal(err)
		}
	}
	// Foreign rows arrive first and saturate each one-row candidate pool
	// unless visibility is applied in SQL before LIMIT.
	for i := 0; i < 8; i++ {
		add(b, fmt.Sprintf("foreign%d", i), "", true)
	}
	add(a, "root", "", false)
	add(a, "local", "root", true)
	packed, scale := vec.Quantize([]float32{1, 0})
	d, _ := Lookup("plugin_memories")
	var vectors []store.MemoryVector
	for _, id := range []string{"foreign0", "foreign1", "foreign2", "foreign3", "root", "local"} {
		vectors = append(vectors, store.MemoryVector{Store: "plugin_memories", ID: id, Basis: "test", ContentSHA: shaOfRow(t, s, d, id), Q: packed, Scale: scale, Dims: 2})
	}
	if err := s.PutMemoryVectors(vectors); err != nil {
		t.Fatal(err)
	}
	q := Query{PluginID: "p", Activation: "a"}
	check := func(want string) {
		t.Helper()
		exact, _, err := layerExact(context.Background(), s.DB(), d, q, `"lighthouse"`, 1, true)
		if err != nil {
			t.Fatal(err)
		}
		fuzzy, err := layerFuzzy(context.Background(), s.DB(), d, q, "lighhouse", []string{"lighhouse"}, 1, true)
		if err != nil {
			t.Fatal(err)
		}
		meaning, _, err := layerMeaning(context.Background(), s.DB(), d, q, &queryVector{basis: "test", q: packed, scale: scale}, 1)
		if err != nil {
			t.Fatal(err)
		}
		for name, hits := range map[string][]Hit{"exact": exact, "fuzzy": fuzzy, "meaning": meaning} {
			if len(hits) != 1 || hits[0].ID != want {
				t.Fatalf("%s: %+v, want %s", name, hits, want)
			}
			m, found, err := s.PluginMemoryGet(a, hits[0].ID)
			if err != nil || !found || m.SupersededBy != "" {
				t.Fatalf("by-ID disagreement: %+v %v %v", m, found, err)
			}
		}
	}
	check("local")
	if err := s.PluginMemoryClearTemp(a); err != nil {
		t.Fatal(err)
	}
	check("root")
}
