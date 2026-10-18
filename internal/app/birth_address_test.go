package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// keyHearingServer is an OpenAI-compatible server that lists one model to
// anyone and records the credential each request carried ("" for none).
func keyHearingServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var heard []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		heard = append(heard, r.Header.Get("Authorization")+r.Header.Get("x-api-key"))
		mu.Unlock()
		fmt.Fprint(w, `{"data":[{"id":"org/local-model"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		got := heard
		heard = nil
		return got
	}
}

func TestABirthNamingAnotherAddressForAShippedEntryIsRefused(t *testing.T) {
	srv, heard := keyHearingServer(t)
	dir := t.TempDir()
	a := firstbootApp(t, dir)
	a.genesisClient, a.bootstrapText = fetchVerifiedBootstrap(t, genesistest.NewRoot(t))
	writeTestProviders(t, dir, providerEntry{Name: "Hosted", APIType: "openai", URL: "https://hosted.example/v1", APIKey: "hosted-secret", DefaultModel: "org/local-model", Default: true})
	before, err := os.ReadFile(filepath.Join(dir, "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.handleGenesis(context.Background(), &dashboard.GenesisRequest{Provider: "Hosted", Endpoint: srv.URL + "/v1", Model: "org/local-model"})
	var refused *keyAddressError
	if !errors.As(err, &refused) {
		t.Fatalf("a birth that names another address for a provider's own entry was not refused as one: %v", err)
	}
	for _, want := range []string{`provider "Hosted" is at https://hosted.example`, "this birth names " + addressOrigin(srv.URL), "nothing was asked", "change the address in providers.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if got := heard(); len(got) != 0 {
		t.Fatalf("THE TYPED ADDRESS WAS ASKED by a refused birth: %d requests, carrying %q", len(got), got)
	}
	if fileExists(a.configSnapshot().Identity.LedgerPath) {
		t.Fatal("a refused birth minted a ledger")
	}
	if after, err := os.ReadFile(filepath.Join(dir, "providers.json")); err != nil || string(after) != string(before) {
		t.Fatalf("a refused birth wrote the providers file: %v", err)
	}
	if _, err := a.upsertBirthProvider(&dashboard.GenesisRequest{Provider: "Hosted", Endpoint: srv.URL + "/v1", Model: "org/local-model"}); !errors.As(err, &refused) {
		t.Fatalf("the birth's own write of its entry took the entry to another address: %v", err)
	}
	if e, _, err := a.birthEntry("Hosted", "https://hosted.example/v2", "org/local-model"); err != nil || e.APIKey != "hosted-secret" || e.URL != "https://hosted.example/v2" {
		t.Fatalf("control: another path at the entry's own origin is the same address: %+v, %v", e.URL, err)
	}
}

// A LOCAL BIRTH IS ASKED ONLY WITH WHAT WAS TYPED (row 9.50). The
// operator's own local server is the one entry whose address the birth form
// lets them type. At another address than the file holds, the entry's
// stored key, the variable it names and the variable a settings block names
// for it are not sent: the list and the founding request carry the key
// typed beside the address, or none, and the entry the birth writes holds
// the new address with only that key. At the address the file holds the
// entry is asked as it stands.
func TestALocalBirthIsAskedOnlyWithWhatWasTyped(t *testing.T) {
	srv, heard := keyHearingServer(t)
	t.Setenv("AII_TEST_LOCAL_ENTRY_KEY", "entry-variable-secret")
	t.Setenv("AII_TEST_LOCAL_BLOCK_KEY", "block-variable-secret")
	dir := t.TempDir()
	a := firstbootApp(t, dir)
	a.cfg.LLM.Provider, a.cfg.LLM.APIKeyEnv = "Local server", "AII_TEST_LOCAL_BLOCK_KEY"
	local := seedLocalEntry(t)
	local.URL, local.APIKey, local.APIKeyEnv = "http://127.0.0.1:1/v1", "stored-local-secret", "AII_TEST_LOCAL_ENTRY_KEY"
	writeTestProviders(t, dir, local)
	typed := srv.URL + "/v1"

	e, eff, err := a.birthEntry("Local server", typed, "org/local-model")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.discoverMetaForEntry(context.Background(), e, ""); err != nil {
		t.Fatal(err)
	}
	cc, err := a.clientConfigForEntry(eff, e, a.configSnapshot().LLM, "org/local-model", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := heard(); len(got) != 1 || got[0] != "" || cc.APIKey != "" || cc.Credential != nil {
		t.Fatalf("A KEY THE OPERATOR DID NOT TYPE WENT TO THE ADDRESS TYPED AT BIRTH: the list carried %q, the founding request would carry %q", got, cc.APIKey)
	}
	if _, _, err := a.discoverMetaForEntry(context.Background(), e, "typed-secret"); err != nil {
		t.Fatal(err)
	}
	cc, err = a.clientConfigForEntry(eff, e, a.configSnapshot().LLM, "org/local-model", "typed-secret")
	if err != nil {
		t.Fatal(err)
	}
	if got := heard(); len(got) != 1 || got[0] != "Bearer typed-secret" || cc.APIKey != "typed-secret" {
		t.Fatalf("the key typed beside the address was not the one sent: the list carried %q, the founding request %q", got, cc.APIKey)
	}

	if _, err := a.upsertBirthProvider(&dashboard.GenesisRequest{Provider: "Local server", Endpoint: typed, Model: "org/local-model", APIKey: "typed-secret"}); err != nil {
		t.Fatal(err)
	}
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	written := entryNamed(reg, "Local server")
	if written == nil || written.URL != typed || written.APIKey != "typed-secret" || written.APIKeyEnv != "" {
		t.Fatalf("the entry a local birth wrote is not the typed address with only the typed key: %+v", written)
	}

	// Control: at the address the file holds, the entry is asked as it stands.
	written.APIKey = "stored-local-secret"
	writeTestProviders(t, dir, *written)
	e, _, err = a.birthEntry("Local server", typed, "org/local-model")
	if err != nil || e.APIKey != "stored-local-secret" {
		t.Fatalf("control: a local birth at the entry's own address did not keep its stored key: %+v, %v", e.APIKey, err)
	}
}

// A MOVED SPEECH SERVER DOES NOT TAKE ITS STORED KEY (row 4.4's own words).
// Settings → Speech repoints the operator's own speech server; a key stored
// for its first address is not sent to the next. A move that enters no key
// is refused, as a save from the Providers card is, and nothing is
// written; a move with a key entered takes that key; another path at the
// same origin keeps the stored key.
func TestAMovedSpeechServerDoesNotTakeItsStoredKey(t *testing.T) {
	a := newVoiceApp(t)
	if err := a.setOwnSpeechServer("my-speech", "first-secret", "http://127.0.0.1:9/v1"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(a.providersPath())
	if err != nil {
		t.Fatal(err)
	}
	err = a.setOwnSpeechServer("my-speech", "", "http://127.0.0.1:10/v1")
	var refused *keyAddressError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "the stored key was entered for the first") {
		t.Fatalf("A MOVED SPEECH SERVER TOOK ITS STORED KEY, or was not refused as a key's move: %v", err)
	}
	if after, err := os.ReadFile(a.providersPath()); err != nil || string(after) != string(before) {
		t.Fatalf("a refused move wrote the providers file: %v", err)
	}
	if err := a.setOwnSpeechServer("my-speech", "second-secret", "http://127.0.0.1:10/v1"); err != nil {
		t.Fatalf("a move with its key entered was refused: %v", err)
	}
	if err := a.setOwnSpeechServer("my-speech", "", "http://127.0.0.1:10/v2"); err != nil {
		t.Fatalf("another path at the same origin was refused: %v", err)
	}
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if e := entryNamed(reg, "my-speech"); e == nil || e.URL != "http://127.0.0.1:10/v2" || e.APIKey != "second-secret" {
		t.Fatalf("the speech server is not at its new address with the key entered for it: %+v", e)
	}
}

func TestABirthWithNoStoredEntryIsAskedOnlyWithWhatWasTyped(t *testing.T) {
	for name, selected := range map[string]string{"": "", "Relay": "Relay"} {
		srv, heard := keyHearingServer(t)
		t.Setenv("AII_TEST_BLOCK_KEY", "block-variable-secret")
		dir := t.TempDir()
		a := firstbootApp(t, dir)
		a.cfg.LLM.Provider, a.cfg.LLM.APIKeyEnv = selected, "AII_TEST_BLOCK_KEY"
		writeTestProviders(t, dir, seedLocalEntry(t))
		typed := srv.URL + "/v1"
		e, eff, err := a.birthEntry(name, typed, "org/local-model")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.discoverMetaForEntry(context.Background(), e, ""); err != nil {
			t.Fatal(err)
		}
		cc, err := a.clientConfigForEntry(eff, e, a.configSnapshot().LLM, "org/local-model", "")
		if err != nil {
			t.Fatal(err)
		}
		if got := heard(); len(got) != 1 || got[0] != "" || cc.APIKey != "" {
			t.Errorf("provider %q: A VARIABLE THE OPERATOR DID NOT TYPE WENT TO THE ADDRESS TYPED AT BIRTH: the list carried %q, the founding request would carry %q", name, got, cc.APIKey)
		}
		if _, _, err := a.discoverMetaForEntry(context.Background(), e, "typed-secret"); err != nil {
			t.Fatal(err)
		}
		if got := heard(); len(got) != 1 || got[0] != "Bearer typed-secret" {
			t.Errorf("provider %q: the key typed beside the address was not the one sent: %q", name, got)
		}
	}
}

func TestWhatABirthWritesIsTheFilesEntry(t *testing.T) {
	asked := providerEntry{Name: "Local server", URL: "http://127.0.0.1:9/v1", typedOnly: true}
	w := birthWritten(asked, "Local server", "typed-secret")
	if w.typedOnly || w.APIKey != "typed-secret" || !w.Default || w.Chat == nil || !*w.Chat {
		t.Fatalf("the written entry is not the file's: typedOnly=%v key=%q default=%v", w.typedOnly, w.APIKey, w.Default)
	}
	if got := providerAPIKey(w, "", ""); got != "typed-secret" {
		t.Fatalf("a client built from the written entry would send %q, not the key stored on it", got)
	}
}

func TestALocalBirthAtAMovedAddressSaysWhichVariableTheNextStartSends(t *testing.T) {
	srv, _ := keyHearingServer(t)
	typed := srv.URL + "/v1"
	for name, c := range map[string]struct {
		env, endpoint, key string
		said               bool
	}{
		"moved, the block names the operator's own": {"AII_TEST_LOCAL_BLOCK_KEY", typed, "", true},
		"moved, a key typed":                        {"AII_TEST_LOCAL_BLOCK_KEY", typed, "typed-secret", false},
		"not moved":                                 {"AII_TEST_LOCAL_BLOCK_KEY", "http://127.0.0.1:1/v1", "", false},
		"moved, the block names a vendor's own":     {"OPENAI_API_KEY", typed, "", false},
	} {
		logs := logsink.CaptureForTest(t)
		dir := t.TempDir()
		a := firstbootApp(t, dir)
		a.cfg.LLM.Provider, a.cfg.LLM.APIKeyEnv = "Local server", c.env
		local := seedLocalEntry(t)
		local.URL = "http://127.0.0.1:1/v1"
		writeTestProviders(t, dir, local)
		if _, err := a.upsertBirthProvider(&dashboard.GenesisRequest{Provider: "Local server", Endpoint: c.endpoint, Model: "org/local-model", APIKey: c.key}); err != nil {
			t.Fatal(err)
		}
		said := strings.Contains(logs.String(), "config.json names the variable "+c.env+" for the identity's key (llm.api_key_env)")
		if said != c.said {
			t.Errorf("%s: said=%v, want %v\n%s", name, said, c.said, logs.String())
		}
		if said && !strings.Contains(logs.String(), addressOrigin(typed)) || said && !strings.Contains(logs.String(), "Settings → Providers") {
			t.Errorf("%s: the line does not name the address and where to change it:\n%s", name, logs.String())
		}
	}
}
