package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestALocalServerSaysTheWindowItRunsWith(t *testing.T) {
	for _, tc := range []struct {
		name   string
		model  string
		routes map[string]string // path -> body; any other path is a 404
		local  bool
		want   int
		asked  []string // the window routes that must not have been asked
	}{
		{"llama.cpp", "qwen3-8b-q4.gguf", map[string]string{
			"/v1/models": `{"object":"list","data":[{"id":"qwen3-8b-q4.gguf","object":"model","owned_by":"llamacpp","meta":{"vocab_type":2,"n_ctx_train":131072}}]}`,
			"/props":     `{"default_generation_settings":{"id":0,"n_ctx":8192,"is_processing":false,"params":{}},"total_slots":1,"model_path":"/models/qwen3-8b-q4.gguf"}`,
		}, true, 8192, nil},
		{"LM Studio", "qwen3-8b", map[string]string{
			"/v1/models":     `{"object":"list","data":[{"id":"qwen3-8b","object":"model","owned_by":"organization_owner"}]}`,
			"/api/v0/models": `{"object":"list","data":[{"id":"qwen3-8b","object":"model","type":"llm","state":"loaded","max_context_length":32768,"loaded_context_length":16384}]}`,
		}, true, 16384, nil},
		{"Ollama", "llama3.2:latest", map[string]string{
			"/v1/models": `{"object":"list","data":[{"id":"llama3.2:latest","object":"model","created":1729000000,"owned_by":"library"}]}`,
			"/api/ps":    `{"models":[{"name":"llama3.2:latest","model":"llama3.2:latest","size":3200000000,"context_length":4096}]}`,
			"/api/show":  `{"model_info":{"llama.context_length":131072}}`,
		}, true, 4096, nil},
		{"Ollama, the model not loaded", "llama3.2:latest", map[string]string{
			"/v1/models": `{"object":"list","data":[{"id":"llama3.2:latest","object":"model","created":1729000000,"owned_by":"library"}]}`,
			"/api/ps":    `{"models":[]}`,
			"/api/show":  `{"model_info":{"llama.context_length":131072}}`,
		}, true, 0, nil},
		{"vLLM", "qwen3-8b", map[string]string{
			"/v1/models": `{"object":"list","data":[{"id":"qwen3-8b","object":"model","owned_by":"vllm","max_model_len":65536}]}`,
			"/props":     `{"default_generation_settings":{"n_ctx":1}}`,
		}, true, 65536, []string{"/props", "/api/v0/models", "/api/ps"}},
		{"mlx_lm", "mlx-community/MiMo-7B-RL-4bit", map[string]string{
			"/v1/models": `{"object": "list", "data": [{"id": "mlx-community/MiMo-7B-RL-4bit", "object": "model", "created": 1792722783}]}`,
		}, true, 0, nil},
		{"a hosted entry", "qwen3-8b-q4.gguf", map[string]string{
			"/v1/models": `{"object":"list","data":[{"id":"qwen3-8b-q4.gguf","object":"model"}]}`,
			"/props":     `{"default_generation_settings":{"n_ctx":8192}}`,
		}, false, 0, []string{"/props", "/api/v0/models", "/api/ps"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			hits := map[string]int{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				hits[r.URL.Path]++
				mu.Unlock()
				if got := r.Header.Get("Authorization"); got != "Bearer local-key" {
					t.Errorf("%s was asked with %q, not the entry's own key", r.URL.Path, got)
				}
				body, ok := tc.routes[r.URL.Path]
				if !ok {
					http.NotFound(w, r)
					return
				}
				io.WriteString(w, body)
			}))
			defer srv.Close()
			dir := t.TempDir()
			writeTestProviders(t, dir, providerEntry{Name: "Mine", APIType: "openai", URL: srv.URL + "/v1", APIKey: "local-key",
				DefaultModel: tc.model, MaxOutputTokens: 1024, Local: tc.local, Default: true})
			block := LLMConfig{Provider: "Mine", Model: tc.model}
			a := resolverApp(dir, block)
			t.Cleanup(a.bgCancel)
			reg, err := a.loadProviders()
			if err != nil {
				t.Fatal(err)
			}
			a.probeProviders(reg)
			_, entry, err := a.resolveLLMConfig(Config{LLM: block}, reg)
			if err != nil {
				t.Fatalf("the entry did not resolve: %v", err)
			}
			if entry.ContextLength != tc.want {
				t.Fatalf("%s: the window resolved is %d; want %d, the one the server runs the model with (0: none known, the fallback)", tc.name, entry.ContextLength, tc.want)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, path := range tc.asked {
				if hits[path] != 0 {
					t.Errorf("%s: %s was asked although %s", tc.name, path, strings.TrimSpace(map[bool]string{true: "the list named the window", false: "the entry is not local"}[tc.local]))
				}
			}
		})
	}
}
