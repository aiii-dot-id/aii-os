package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/memory"
)

// embedEndpoint is a provider that answers embeddings with one vector a
// text, always the same vector, so a vector says which endpoint made it;
// asked returns the model each request named.
func embedEndpoint(t *testing.T, vector []float32) (client *llm.Client, asked func() []string) {
	t.Helper()
	var mu sync.Mutex
	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &req)
		mu.Lock()
		models = append(models, req.Model)
		mu.Unlock()
		var out struct {
			Data []map[string]any `json:"data"`
		}
		for i := range req.Input {
			out.Data = append(out.Data, map[string]any{"index": i, "embedding": vector})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "m"}), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(models)
	}
}

// VECTORS ARE NAMED BY THE PROVIDER WHOSE CLIENT MADE THEM. A change of
// provider records the new entry at its start and replaces the client the
// turns use at its end. The embedding seams read the entry in one place
// and that client in another, so in between they called the provider
// before with the name of the new provider's model, and named what came
// back by the new provider: vectors of one model kept as another's, under
// the basis in use, which no later pass drops.
//
// Here the activation is stopped at that point: the second provider is
// recorded and the turns' client is still the first's. A memory embedded
// now must be made by the second provider's endpoint, with its model's
// name, and named by it; the first endpoint must hear nothing. The same
// holds for the vectors a plugin asks for, which it keeps beside the
// model's name.
func TestVectorsAreNamedByTheProviderWhoseClientMadeThem(t *testing.T) {
	a := &App{cfg: defaultConfig()}
	a.cfg.Memory.MeaningSource = meaningChoiceProvider
	first, firstAsked := embedEndpoint(t, []float32{1, 0})
	second, secondAsked := embedEndpoint(t, []float32{0, 1})
	a.llmSwap = newSwappableLLM(first)
	a.adoptRuntime(providerEntry{Name: "First", EmbeddingsModel: "first-embed"}, first)

	a.adoptRuntime(providerEntry{Name: "Second", EmbeddingsModel: "second-embed"}, second)
	if a.llmSwap.Current() != first {
		t.Fatal("rig: the turns' client is still the first provider's, as in the middle of an activation")
	}

	basis, vectors, err := memoryEmbedder{a}.Embed(t.Context(), memory.KindMemory, []string{"a memory"})
	if err != nil {
		t.Fatalf("a memory embedded in the middle of a change of provider: %v", err)
	}
	if basis != "Second/second-embed" || len(vectors) != 1 || !slices.Equal(vectors[0], []float32{0, 1}) {
		t.Fatalf("the memory's vector is %v under %q; the provider recorded is the second, and its endpoint answers [0 1]", vectors, basis)
	}
	model, vectors, err := providerEmbedder{a}.Embed(t.Context(), []string{"a plugin's text"})
	if err != nil || model != "second-embed" || len(vectors) != 1 || !slices.Equal(vectors[0], []float32{0, 1}) {
		t.Fatalf("a plugin's vector is %v by model %q (%v); the provider recorded is the second", vectors, model, err)
	}
	if got := firstAsked(); len(got) != 0 {
		t.Fatalf("the provider before was asked for %q after the second was recorded", got)
	}
	if got := secondAsked(); !slices.Equal(got, []string{"second-embed", "second-embed"}) {
		t.Fatalf("the second provider was asked for %q; want its own model, once for each text", got)
	}
}

// AN ACTIVATION PUBLISHES THE ENTRY AND ITS CLIENT TOGETHER, and no reader
// ever sees one provider's entry with another's client. The real
// activation runs back and forth between two providers while readers take
// the pair as the embedding seams do.
func TestAnActivationPublishesTheProviderAndItsClientAsOne(t *testing.T) {
	a := &App{cfg: defaultConfig()}
	clients := map[string]*llm.Client{
		"First":  llm.New(&llm.ClientConfig{Endpoint: "https://first.invalid", Model: "m"}),
		"Second": llm.New(&llm.ClientConfig{Endpoint: "https://second.invalid", Model: "m"}),
	}
	a.llmSwap = newSwappableLLM(clients["First"])
	a.activateLLMRuntime(clients["First"], providerEntry{Name: "First", EmbeddingsModel: "first-embed"}, 0)
	if entry, client := a.activeRuntime(); entry.Name != "First" || client != clients["First"] || a.llmSwap.Current() != clients["First"] {
		t.Fatalf("after an activation the provider recorded is %q with its own client: %v", entry.Name, client == clients["First"])
	}

	stop := make(chan struct{})
	torn := make(chan string, 1)
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if entry, client := a.activeRuntime(); client != clients[entry.Name] {
					select {
					case torn <- entry.Name:
					default:
					}
					return
				}
			}
		}()
	}
	for i := 0; i < 20000; i++ {
		name := "Second"
		if i%2 == 1 {
			name = "First"
		}
		a.activateLLMRuntime(clients[name], providerEntry{Name: name, EmbeddingsModel: name + "-embed"}, 0)
	}
	close(stop)
	readers.Wait()
	select {
	case name := <-torn:
		t.Fatalf("a reader saw provider %q recorded with another provider's client", name)
	default:
	}
}
