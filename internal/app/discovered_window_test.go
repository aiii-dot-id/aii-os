package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// A LIVE IDENTITY DID NOT BOOT. Its local server publishes, for the model
// it runs, a context window of 262,144 tokens and max_tokens of 262,144 —
// the same number, because for that server max_tokens IS the window. Read
// as an output cap it reserved the whole window, the window check found
// nothing left for a prompt, and the substrate was refused: "context_length
// 262144 must exceed output reserve 262144 plus safety margin 2048". The
// identity had been running on the 32k fallback and only met this the day
// its window became discoverable under the name it was configured with.
//
// A cap that leaves no room for a prompt is not a cap on output.
func TestAPublishedWindowIsNotAnOutputCap(t *testing.T) {
	const window = 262144
	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{
		Name: "Local (oMLX)", APIType: "openai", URL: "http://127.0.0.1:1081/v1",
		APIKey: "k", DefaultModel: "Example-Model-30B-Q4-mtp", Default: true,
	})
	a := resolverApp(dir, LLMConfig{Provider: "Local (oMLX)", Model: "Example-Model-30B-Q4-mtp"})
	// The server's own words for this model, as it publishes them: one
	// window under three names, max_tokens among them.
	seedDiscovered(a, "Local (oMLX)", "Example-Model-30B-Q4-mtp", modelMeta{Context: window, MaxOut: window})

	_, entry, err := a.resolveLLM()
	if err != nil {
		t.Fatalf("the identity must boot on a model whose server names its window twice: %v", err)
	}
	if entry.ContextLength != window {
		t.Fatalf("the published window is still the window: %d", entry.ContextLength)
	}
	if entry.MaxOutputTokens != defaultOutputReserve {
		t.Fatalf("output reserve = %d, want the default %d — the window is not a cap", entry.MaxOutputTokens, defaultOutputReserve)
	}
	if budget, _ := promptBudgetFor(entry, 0); budget != window-defaultOutputReserve-promptSafetyTokens {
		t.Fatalf("prompt budget = %d, want %d", budget, window-defaultOutputReserve-promptSafetyTokens)
	}
}

// AND A REAL CAP IS STILL A REAL CAP: a server that publishes an output
// maximum smaller than its window is describing output, and it is used.
func TestAPublishedOutputCapSmallerThanTheWindowIsUsed(t *testing.T) {
	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{
		Name: "svc", APIType: "openai", URL: "https://svc.example/v1", APIKey: "k", DefaultModel: "m", Default: true,
	})
	a := resolverApp(dir, LLMConfig{Provider: "svc", Model: "m"})
	seedDiscovered(a, "svc", "m", modelMeta{Context: 200000, MaxOut: 32000})
	_, entry, err := a.resolveLLM()
	if err != nil {
		t.Fatal(err)
	}
	if entry.ContextLength != 200000 || entry.MaxOutputTokens != 32000 {
		t.Fatalf("a published output cap under the window is the cap: window=%d reserve=%d", entry.ContextLength, entry.MaxOutputTokens)
	}
}

func TestAPublishedSmallWindowRequiresFittingAllocation(t *testing.T) {
	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{
		Name: "tiny", APIType: "openai", URL: "https://tiny.example/v1", APIKey: "k", DefaultModel: "m", Default: true,
	})
	a := resolverApp(dir, LLMConfig{Provider: "tiny", Model: "m"})
	seedDiscovered(a, "tiny", "m", modelMeta{Context: 4096})
	if _, _, err := a.resolveLLM(); err == nil || !strings.Contains(err.Error(), "must exceed output reserve") {
		t.Fatalf("an allocation that cannot fit a known window must refuse before use, got: %v", err)
	}
	// The same window with an allocation that fits is usable.
	writeTestProviders(t, dir, providerEntry{
		Name: "tiny", APIType: "openai", URL: "https://tiny.example/v1", APIKey: "k", DefaultModel: "m", Default: true, MaxOutputTokens: 1024,
	})
	_, entry, err := a.resolveLLM()
	if err != nil || entry.ContextLength != 4096 || entry.MaxOutputTokens != 1024 {
		t.Fatalf("a fitting allocation under a small published window must resolve: %+v %v", entry, err)
	}
}

func TestADiscoveredWindowIsNeverErasedToAdmitAnOversizedRequest(t *testing.T) {
	for _, tc := range []struct {
		name                                    string
		window, publishedOutput, explicitOutput int
		mustAdmit                               bool
	}{
		{"4096, default output", 4096, 0, 0, false},
		{"8192, max_tokens is the window", 8192, 8192, 0, false},
		{"8192, published output near the window", 8192, 7000, 0, false},
		{"32768, explicit output larger than the window", 32768, 0, 65536, false},
		{"4096, explicit output that fits", 4096, 0, 1024, true},
		{"8192, published output that fits", 8192, 1024, 0, true},
		{"262144, max_tokens is the window (the live case)", 262144, 262144, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type observed struct{ max, status int }
			requests := make(chan observed, 4)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
					fmt.Fprintf(w, `{"data":[{"id":"synthetic-model","context_length":%d,"max_tokens":%d}]}`, tc.window, tc.publishedOutput)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
					w.WriteHeader(404)
					return
				}
				var body struct {
					MaxTokens           int `json:"max_tokens"`
					MaxCompletionTokens int `json:"max_completion_tokens"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					w.WriteHeader(400)
					return
				}
				max := body.MaxTokens
				if body.MaxCompletionTokens > 0 {
					max = body.MaxCompletionTokens
				}
				status := 200
				if max >= tc.window {
					status = 400
				}
				requests <- observed{max, status}
				w.WriteHeader(status)
				if status == 400 {
					fmt.Fprint(w, `{"error":{"message":"synthetic context window exceeded","type":"invalid_request_error"}}`)
					return
				}
				fmt.Fprint(w, `{"id":"synthetic","object":"chat.completion","model":"synthetic-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
			}))
			defer srv.Close()
			// BIRTH IS HELD TO THE SAME RULE ON THE SAME SERVER: what boot
			// admits below a birth admits, and what boot refuses a birth
			// refuses before the founding request.
			born := birthApp(t, providerEntry{Name: "synthetic", APIType: "openai", URL: srv.URL + "/v1", APIKey: "synthetic-test-key", DefaultModel: "synthetic-model", Default: true, MaxOutputTokens: tc.explicitOutput})
			_, birthErr := born.handleGenesis(t.Context(), &dashboard.GenesisRequest{Provider: "synthetic", Endpoint: srv.URL + "/v1", Model: "synthetic-model"})
			stopApp(t, born)
			if (birthErr == nil) != tc.mustAdmit {
				t.Fatalf("birth and boot read this server by two rules: birth admitted=%t, want %t: %v", birthErr == nil, tc.mustAdmit, birthErr)
			}
			if birthErr == nil {
				if got := <-requests; got.max >= tc.window || got.status != 200 {
					t.Fatalf("the founding request exceeds the %d-token bound: wire=%d HTTP=%d", tc.window, got.max, got.status)
				}
			} else if !strings.Contains(birthErr.Error(), "must exceed output reserve") || !strings.Contains(birthErr.Error(), "max_output_tokens on the entry") {
				t.Fatalf("a birth refused for its window must say what to set: %v", birthErr)
			}
			dir := t.TempDir()
			writeTestProviders(t, dir, providerEntry{Name: "synthetic", APIType: "openai", URL: srv.URL + "/v1", APIKey: "synthetic-test-key", DefaultModel: "synthetic-model", Default: true, MaxOutputTokens: tc.explicitOutput})
			a := resolverApp(dir, LLMConfig{Provider: "synthetic", Model: "synthetic-model"})
			reg, err := a.loadProviders()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err = a.discoverForProvider(ctx, reg, "synthetic", "", ""); err != nil {
				t.Fatal(err)
			}
			cc, entry, err := a.resolveLLM()
			if err != nil {
				if tc.mustAdmit {
					t.Fatalf("a fitting allocation was refused: %v", err)
				}
				return // refused before the first request: the safe answer
			}
			if !tc.mustAdmit {
				t.Fatalf("an allocation the server will refuse was admitted: context=%d output=%d", entry.ContextLength, entry.MaxOutputTokens)
			}
			budget, _ := promptBudgetFor(entry, 0)
			_, chatErr := llm.New(&cc).Chat(ctx, []llm.Message{{Role: "user", Content: "hi"}}, llm.ChatOptions{})
			var got observed
			select {
			case got = <-requests:
			case <-ctx.Done():
				t.Fatalf("the wire was not reached: %v", chatErr)
			}
			if entry.ContextLength != tc.window || budget+entry.MaxOutputTokens+promptSafetyTokens > tc.window || got.max >= tc.window || chatErr != nil {
				t.Fatalf("the admitted configuration exceeds the %d-token bound: context=%d prompt=%d output=%d wire=%d HTTP=%d err=%v", tc.window, entry.ContextLength, budget, entry.MaxOutputTokens, got.max, got.status, chatErr)
			}
		})
	}
}

func TestBirthReadsAPublishedWindowAsBootDoes(t *testing.T) {
	const window = 32768
	wire := make(chan int, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			fmt.Fprintf(w, `{"data":[{"id":"m","context_length":%d,"max_tokens":%d}]}`, window, window)
			return
		}
		var body struct {
			MaxTokens int `json:"max_tokens"`
		}
		if r.URL.Path != "/v1/chat/completions" || json.NewDecoder(r.Body).Decode(&body) != nil || body.MaxTokens >= window {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"synthetic context window exceeded","type":"invalid_request_error"}}`)
			return
		}
		wire <- body.MaxTokens
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	a := birthApp(t, providerEntry{Name: "Local", APIType: "openai", URL: srv.URL + "/v1", DefaultModel: "m", Default: true})
	defer stopApp(t, a)
	if _, err := a.handleGenesis(t.Context(), &dashboard.GenesisRequest{Provider: "Local", Endpoint: srv.URL + "/v1", Model: "m"}); err != nil {
		t.Fatalf("a model whose server names its window twice must be born on: %v", err)
	}
	if got := <-wire; got != 8192 {
		t.Fatalf("the founding request asked for max_tokens %d, want the default reserve 8192", got)
	}
	if budget, source := a.currentPromptBudget(); budget != 22528 || source != budgetDerived {
		t.Fatalf("the born identity's prompt budget is %d (%s), want 22528 derived from the window", budget, source)
	}
}

// WHAT THE OPERATOR TYPED IS STILL THEIRS TO GET RIGHT: an explicit pair
// that leaves no room for a prompt is refused, loudly, as before.
func TestAnOperatorsOwnImpossiblePairIsStillRefused(t *testing.T) {
	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{
		Name: "typed", APIType: "openai", URL: "https://typed.example/v1", APIKey: "k", DefaultModel: "m",
		Default: true, ContextLength: 8192, MaxOutputTokens: 8192,
	})
	a := resolverApp(dir, LLMConfig{Provider: "typed", Model: "m"})
	if _, _, err := a.resolveLLM(); err == nil || !strings.Contains(err.Error(), "must exceed output reserve") {
		t.Fatalf("an explicit pair that cannot work must be refused: %v", err)
	}
}

// seedDiscovered puts one model's published numbers where a finished probe
// would have left them.
func seedDiscovered(a *App, provider, model string, m modelMeta) {
	a.providers.runtime.provStatus = map[string]providerProbe{provider: {
		state: "ok", models: []string{model}, meta: map[string]modelMeta{model: m},
		checkedAt: time.Now(),
	}}
}
