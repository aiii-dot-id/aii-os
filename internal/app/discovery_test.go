package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDiscoverModelsRejectsOversizeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte{'x'}, maxModelListBytes+1))
	}))
	t.Cleanup(server.Close)

	_, _, err := discoverModelsWith(context.Background(), "", server.URL, "", false, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize model list returned %v", err)
	}
}

func TestProviderDirectoryAddsDiscoveredModels(t *testing.T) {
	dir := t.TempDir()
	entry := providerEntry{
		Name: "provider", APIType: "openai", URL: "https://provider.example/v1",
		DefaultModel: "configured", Models: []string{"configured", "fallback"},
	}
	writeTestProviders(t, dir, entry)
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	a.provStatus = map[string]providerProbe{entry.Name: {
		state: "ok", models: []string{"live-b", "live-a"},
		checkedAt: time.Now(), key: probeKey(entry),
	}}

	providers := a.providerDirectoryLive()
	if len(providers) != 1 {
		t.Fatalf("got %d providers, want 1", len(providers))
	}
	if want := []string{"configured", "fallback", "live-b", "live-a"}; !reflect.DeepEqual(providers[0].Models, want) {
		t.Fatalf("picker models = %v, want %v", providers[0].Models, want)
	}
	if want := entry.Models; !reflect.DeepEqual(providers[0].ConfiguredModels, want) {
		t.Fatalf("editor fallback = %v, want %v", providers[0].ConfiguredModels, want)
	}
}

func TestProbeKeyCoversProviderEdits(t *testing.T) {
	entry := providerEntry{Name: "same", URL: "https://example.com", APIType: "openai", APIKey: "first"}
	before := probeKey(entry)
	entry.APIKey = "second"
	if after := probeKey(entry); after == before {
		t.Fatal("credential edit retained a stale provider-probe cache key")
	}
	if strings.Contains(before, "first") {
		t.Fatal("provider-probe cache key copied the credential")
	}
}

func TestDiscoverForProviderAddsConfiguredModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"shared"},{"id":"live"}]}`))
	}))
	t.Cleanup(server.Close)

	entry := providerEntry{Name: "provider", URL: server.URL, Models: []string{"configured", "shared"}}
	a := New(&Config{})
	models, err := a.discoverForProvider(context.Background(), &providerRegistry{Providers: []providerEntry{entry}}, entry.Name, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"configured", "shared", "live"}; !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %v, want %v", models, want)
	}
}

// Where a model list lives is a property of the URL, not of a dialect
// label. This is the rule that makes an Anthropic base work whether or
// not the operator typed the /v1.
func TestAPIVersionInPath(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"https://api.anthropic.com", false},
		{"https://api.anthropic.com/v1", true},
		{"https://api.openai.com/v1", true},
		{"https://generativelanguage.googleapis.com/v1beta/openai", true},
		{"https://chatgpt.com/backend-api/codex", false},
		{"https://api.z.ai/api/paas/v4", true},
		{"https://example.com/version/api", false}, // "version" is not a version
	} {
		if got := apiVersionInPath(tc.url); got != tc.want {
			t.Errorf("%s -> %v, want %v", tc.url, got, tc.want)
		}
	}
}

// Three model-list shapes exist in the wild, and the window each provider
// publishes is the truth we should budget prompts against.
func TestParseModelListShapesAndWindows(t *testing.T) {
	anthropic := `{"data":[{"id":"claude-opus-5","max_input_tokens":1000000,"max_tokens":128000}]}`
	models, meta, err := parseModelList([]byte(anthropic))
	if err != nil || len(models) != 1 || models[0] != "claude-opus-5" {
		t.Fatalf("anthropic shape: %v %v", models, err)
	}
	if meta["claude-opus-5"].Context != 1000000 || meta["claude-opus-5"].MaxOut != 128000 {
		t.Fatalf("the published window must be carried, got %+v", meta["claude-opus-5"])
	}

	chatgpt := `{"models":[{"slug":"gpt-5.6-sol","context_window":400000}]}`
	models, meta, err = parseModelList([]byte(chatgpt))
	if err != nil || len(models) != 1 || models[0] != "gpt-5.6-sol" {
		t.Fatalf("chatgpt shape: %v %v", models, err)
	}
	if meta["gpt-5.6-sol"].Context != 400000 {
		t.Fatalf("context_window must be carried, got %+v", meta["gpt-5.6-sol"])
	}

	local := `{"models":[{"name":"llama3.3"}]}`
	if models, _, err = parseModelList([]byte(local)); err != nil || len(models) != 1 || models[0] != "llama3.3" {
		t.Fatalf("local-runner shape: %v %v", models, err)
	}

	if _, _, err = parseModelList([]byte("not json")); err == nil {
		t.Fatal("a non-JSON body must be an error, not an empty list")
	}
}
