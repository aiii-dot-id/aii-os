package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func TestAnOpenAIKeyMaySelectTheResponsesShape(t *testing.T) {
	dir := t.TempDir()
	entry := liveProvider(t, "OpenAI", "gpt-5.6-sol")
	entry.APIKey = "fixture-key"
	entry.APIType = "responses"
	entry.ContextLength, entry.Models = 0, nil // as a file states them: lent, not written
	writeTestProviders(t, dir, entry)
	a := resolverApp(dir, LLMConfig{Provider: "OpenAI"})
	t.Cleanup(a.bgCancel)

	cc, _, err := a.resolveLLM()
	if err != nil {
		t.Fatalf("an OpenAI entry on the Responses shape does not resolve: %v", err)
	}
	if cc.Provider != "responses" || llm.DialectFor(cc.Provider) != llm.DialectResponses {
		t.Fatalf("the entry resolved to provider %q (%s), want the Responses dialect on a key", cc.Provider, llm.DialectFor(cc.Provider))
	}
	if cc.Endpoint != "https://api.openai.com/v1" || cc.APIKey != "fixture-key" || cc.Credential != nil {
		t.Fatalf("the client goes to %q with key %q (credential %v), want the entry's own address and key", cc.Endpoint, cc.APIKey, cc.Credential)
	}
	if cc.ExplicitCache || cc.MaxCompletionTokens {
		t.Fatalf("the Responses shape was given the chat dialect's cache breakpoints (%t) or output field (%t)", cc.ExplicitCache, cc.MaxCompletionTokens)
	}
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	got := entryNamed(reg, "OpenAI")
	if got == nil || got.ContextLength == 0 || len(got.Models) == 0 {
		t.Fatalf("the entry moved to the Responses shape at the vendor's address lost what the vendor says of that address: %+v", got)
	}
}

// A card save of the Responses shape is the operator's word and is written
// as stated; any other api_type is still refused by name, with every value
// there is.
func TestTheResponsesShapeIsSavedAsStatedAndAnUnknownShapeIsRefusedByName(t *testing.T) {
	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{Name: "alpha", APIType: "openai", URL: "https://alpha.example/v1", APIKey: "ka", DefaultModel: "am", Default: true})
	a := resolverApp(dir, LLMConfig{Provider: "alpha"})
	t.Cleanup(a.bgCancel)

	if err := a.setProvider(providerEntry{Name: "alpha", APIType: "responses", URL: "https://alpha.example/v1", DefaultModel: "am", Default: true}, true); err != nil {
		t.Fatalf("the card could not save the Responses shape: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"api_type": "responses"`) {
		t.Fatalf("the file does not state the shape the card saved:\n%s", data)
	}
	err = a.setProvider(providerEntry{Name: "alpha", APIType: "responsess", URL: "https://alpha.example/v1"}, true)
	if err == nil || !strings.Contains(err.Error(), `unknown api_type "responsess" (want openai, anthropic, gemini or responses)`) {
		t.Fatalf("an unknown api_type was not refused by name with the values there are: %v", err)
	}
}
