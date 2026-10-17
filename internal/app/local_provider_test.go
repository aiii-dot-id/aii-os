package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

// The seed's local entry: an OpenAI-compatible server the operator runs,
// whose address the birth form asks for. Birth on it waits until that
// address lists its models, and nothing about its models is asked of a
// public catalogue.

func seedLocalEntry(t *testing.T) providerEntry {
	t.Helper()
	for _, e := range embeddedRegistry().Providers {
		if e.Local {
			return e
		}
	}
	t.Fatal("the shipped seed carries no local entry")
	return providerEntry{}
}

// localModels is an operator's OpenAI-compatible server, keyless, counting
// what it is asked.
func localModels(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var asked atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"org/local-model"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

// The seed offers one local server at a common loopback address, with
// nothing to fall back on — what it serves is what its address lists — and
// a fresh slot's providers.json carries it to the birth form, marked local.
func TestTheSeedOffersALocalServer(t *testing.T) {
	e := seedLocalEntry(t)
	if e.Name != "Local server" || e.APIType != "openai" || e.URL != "http://127.0.0.1:8080/v1" {
		t.Fatalf("the seed's local entry = %+v", e)
	}
	if len(e.Models) != 0 || e.DefaultModel != "" || e.CatalogueAuthor != "" || e.Credential != "" || e.Default || !chatProvider(e) {
		t.Fatalf("the seed's local entry claims what only its server can say: %+v", e)
	}

	dir := t.TempDir()
	reg, err := loadProvidersFile(filepath.Join(dir, "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	scaffolded := entryNamed(reg, "Local server")
	if scaffolded == nil || !scaffolded.Local || scaffolded.URL != e.URL {
		t.Fatalf("a fresh slot's providers.json does not carry the local entry: %+v", scaffolded)
	}

	// The directory tells the form. Only the local entry, at an address
	// nothing answers, so the listing's own probe reaches nobody.
	scaffolded.URL = "http://127.0.0.1:1/v1"
	infos := firstbootApp(t, dir).providerInfos(&providerRegistry{Providers: []providerEntry{*scaffolded}})
	if len(infos) != 1 || !infos[0].Local || !infos[0].Chat || infos[0].Endpoint != scaffolded.URL {
		t.Fatalf("the birth form is not told the entry is local: %+v", infos)
	}
}

// Discovery asks a local entry at the address the form typed, through the
// firstboot handler's own hook. Nothing there is an error — never the
// entry's own list — and the typed address is not recorded as the entry's.
// Another entry's key never travels to a typed address.
func TestALocalEntryIsAskedWhereTheFormSaysItIs(t *testing.T) {
	srv, asked := localModels(t)
	dir := t.TempDir()
	local := seedLocalEntry(t)
	local.URL, local.Models = "http://127.0.0.1:1/v1", []string{"configured"}
	hosted := providerEntry{Name: "Hosted", APIType: "openai", URL: "http://127.0.0.1:1/v1", APIKey: "hosted-secret", Models: []string{"h1"}}
	writeTestProviders(t, dir, local, hosted)
	a := firstbootApp(t, dir)
	discover := a.buildFirstbootHandler().DiscoverModels

	models, _, err := discover("Local server", srv.URL+"/v1", "")
	if err != nil || len(models) != 1 || models[0] != "org/local-model" {
		t.Fatalf("the typed address was not asked: models=%v err=%v", models, err)
	}
	if models, _, err := discover("Local server", "http://127.0.0.1:1/v1", ""); err == nil {
		t.Fatalf("an address where nothing answers listed %v", models)
	}
	if _, _, err := discover("Local server", "127.0.0.1:8080", ""); err == nil || !strings.Contains(err.Error(), "not a valid provider URL") {
		t.Fatalf("a malformed address was not refused: %v", err)
	}
	before := asked.Load()
	if _, _, err := discover("Hosted", srv.URL+"/v1", ""); err == nil || !strings.Contains(err.Error(), "not a local entry") {
		t.Fatalf("a hosted entry was asked at a typed address: %v", err)
	}
	if asked.Load() != before {
		t.Fatal("a hosted entry's key travelled to an address typed beside it")
	}
	a.providers.runtime.mu.Lock()
	_, recorded := a.providers.runtime.provStatus["Local server"]
	a.providers.runtime.mu.Unlock()
	if recorded {
		t.Fatal("a typed address was recorded as the entry's status")
	}
	reg, err := a.loadProviders()
	if err != nil || entryNamed(reg, "Local server").URL != "http://127.0.0.1:1/v1" {
		t.Fatalf("discovery moved the stored entry: %v", err)
	}
}

// The server holds the same line as the form: Birth at a local address
// where nothing answers refuses before anything is minted or written.
func TestBirthOnALocalServerThatDoesNotAnswerMintsNothing(t *testing.T) {
	dir := t.TempDir()
	a := firstbootApp(t, dir)
	a.genesisClient, a.bootstrapText = fetchVerifiedBootstrap(t, genesistest.NewRoot(t))
	writeTestProviders(t, dir, seedLocalEntry(t))
	before, err := os.ReadFile(filepath.Join(dir, "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.handleGenesis(context.Background(), &dashboard.GenesisRequest{
		Provider: "Local server", Endpoint: "http://127.0.0.1:1/v1", Model: "org/local-model",
	})
	if err == nil || !strings.Contains(err.Error(), "provider validation failed (http://127.0.0.1:1/v1)") {
		t.Fatalf("Birth at a silent local address was not refused by name: %v", err)
	}
	if fileExists(a.configSnapshot().Identity.LedgerPath) {
		t.Fatal("a refused Birth minted a ledger")
	}
	after, err := os.ReadFile(filepath.Join(dir, "providers.json"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("a refused Birth wrote the providers file: %v", err)
	}
}

// A local entry is never asked about — not under a name a catalogue would
// answer to as it stands, and not with an author the registry names. The
// same entry, not local, is asked: the silence is the rule's.
func TestALocalEntryIsNeverAskedAbout(t *testing.T) {
	baseURL, asked := modalityServing(t, 200, opusEndpoints)
	a := new(App)
	local := providerEntry{Name: "Local server", Local: true, CatalogueAuthor: "anthropic"}
	for _, model := range []string{"anthropic/claude-opus-5", "claude-opus-5"} {
		if got := a.modelModalitiesFor(context.Background(), local, model, baseURL); got.known() {
			t.Fatalf("%s: a local model was given modalities", model)
		}
	}
	if len(*asked) != 0 {
		t.Fatalf("a local model's name left the machine: %v", *asked)
	}
	local.Local = false
	if got := a.modelModalitiesFor(context.Background(), local, "claude-opus-5", baseURL); !got.known() || len(*asked) != 1 {
		t.Fatalf("control: the same entry, not local, was not asked (asked %v)", *asked)
	}
}
