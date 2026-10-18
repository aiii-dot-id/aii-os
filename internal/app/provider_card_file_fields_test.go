package app

import (
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestProviderCardSaveKeepsFileOnlyFieldsAndHonorsEditableClears(t *testing.T) {
	a := newProvidersApp(t)
	path := writeProvidersFor(t, a, `{"providers":[{"name":"metal","api_type":"openai","url":"http://127.0.0.1:9/v1",`+
		`"models":["m","m2"],"default_model":"m","api_key":"fixture-key","api_key_env":"OLD_KEY",`+
		`"reasoning_effort":"medium","embeddings_model":"bge-m3","local":true,"catalogue_author":"operator-catalog",`+
		`"replay_reasoning":true}]}`)
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: "metal", APIType: "openai", Endpoint: "http://127.0.0.1:9/v1",
		DefaultModel: "m2", ConfiguredModels: []string{"m", "m2"},
		APIKeyEnv: "", ReasoningEffort: "",
	}}); err != nil {
		t.Fatal(err)
	}
	reg, err := a.loadProviders()
	if err != nil || len(reg.Providers) != 1 {
		t.Fatalf("saved registry did not reopen: %+v %v", reg, err)
	}
	e := reg.Providers[0]
	if e.EmbeddingsModel != "bge-m3" || !e.Local || e.CatalogueAuthor != "operator-catalog" || !e.ReplayReasoning {
		t.Fatalf("card erased file-only provider settings: %+v", e)
	}
	if e.APIKey != "fixture-key" {
		t.Fatal("card lost the key it was told to keep")
	}
	if e.DefaultModel != "m2" || e.APIKeyEnv != "" || e.ReasoningEffort != "" {
		t.Fatalf("card edit or explicit clears did not land: %+v", e)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), `"embeddings_model": "bge-m3"`) ||
		!strings.Contains(string(raw), `"local": true`) || !strings.Contains(string(raw), `"catalogue_author": "operator-catalog"`) ||
		!strings.Contains(string(raw), `"replay_reasoning": true`) {
		t.Fatalf("file-only values were not persisted: %s %v", raw, err)
	}
}

func TestFullProviderReplacementCanStillClearFileOnlyFields(t *testing.T) {
	a := newProvidersApp(t)
	writeProvidersFor(t, a, `{"providers":[{"name":"metal","url":"http://127.0.0.1:9/v1",`+
		`"embeddings_model":"bge-m3","local":true,"catalogue_author":"operator-catalog","replay_reasoning":true}]}`)
	if err := a.setProvider(providerEntry{Name: "metal", URL: "http://127.0.0.1:9/v1"}, false); err != nil {
		t.Fatal(err)
	}
	reg, err := a.loadProviders()
	if err != nil || len(reg.Providers) != 1 {
		t.Fatalf("replacement registry did not reopen: %+v %v", reg, err)
	}
	e := reg.Providers[0]
	if e.EmbeddingsModel != "" || e.Local || e.CatalogueAuthor != "" || e.ReplayReasoning {
		t.Fatalf("full replacement could not clear fields it owns: %+v", e)
	}
}

// THE CARD DOES NOT ERASE THE OPERATOR'S OWN vendor_key_env. The card's save
// builds an entry with no vendor's key variable, and the fields carried
// over from the stored entry did not include it: one number changed on the
// card, and the variable the file named was no longer read.
func TestProviderCardSaveKeepsTheOperatorsVendorKeyVariable(t *testing.T) {
	a := newProvidersApp(t)
	path := writeProvidersFor(t, a, `{"providers":[{"name":"metal","api_type":"openai","url":"http://127.0.0.1:9/v1",`+
		`"models":["m","m2"],"default_model":"m","vendor_key_env":"MY_METAL_KEY"}]}`)
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: "metal", APIType: "openai", Endpoint: "http://127.0.0.1:9/v1",
		DefaultModel: "m2", ConfiguredModels: []string{"m", "m2"},
	}}); err != nil {
		t.Fatal(err)
	}
	reg, err := a.loadProviders()
	if err != nil || len(reg.Providers) != 1 {
		t.Fatalf("saved registry did not reopen: %+v %v", reg, err)
	}
	if got := reg.Providers[0].VendorKeyEnv; got != "MY_METAL_KEY" {
		t.Fatalf("A CARD SAVE ERASED THE OPERATOR'S vendor_key_env: it is %q after one change on the card", got)
	}
	if reg.Providers[0].DefaultModel != "m2" {
		t.Fatalf("the card's own edit did not land: %+v", reg.Providers[0])
	}
	if raw, err := os.ReadFile(path); err != nil || !strings.Contains(string(raw), `"vendor_key_env": "MY_METAL_KEY"`) {
		t.Fatalf("the operator's vendor_key_env is not in the file: %s %v", raw, err)
	}
}

// A CARD SAVE THAT CHANGES NOTHING OF A SHIPPED VENDOR'S ENTRY CHANGES
// NOTHING. The entry the card built had no vendor's key variable and the
// loaded one it replaced had the lent one, so the two never compared
// equal: the save was written, and on a running identity it read as a
// change of runtime and was checked with no key. The file here is written
// on one line, so any save at all would rewrite it.
func TestACardSaveOfAShippedVendorsEntryThatChangesNothingWritesNothing(t *testing.T) {
	a := newProvidersApp(t)
	const file = `{"providers":[{"name":"OpenAI","api_type":"openai","url":"https://api.openai.com/v1","models":["m"],"default_model":"m","default":true}]}`
	path := writeProvidersFor(t, a, file)
	reg, err := a.loadProviders()
	if err != nil || len(reg.Providers) != 1 || reg.Providers[0].VendorKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("the vendor's entry at the vendor's address is not lent its variable: %+v %v", reg, err)
	}
	e := reg.Providers[0]
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: e.Name, APIType: e.APIType, Endpoint: e.URL, DefaultModel: e.DefaultModel, Default: e.Default,
		ConfiguredModels: []string{"m"}, ThinkingDisplay: displayOnCard(e),
	}}); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != file {
		t.Fatalf("A CARD SAVE THAT CHANGED NOTHING WAS WRITTEN: the entry the card built is not the entry it replaced.\n%s %v", raw, err)
	}
}
