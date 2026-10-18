package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func newProvidersApp(t *testing.T) *App {
	dir := t.TempDir()
	t.Chdir(dir)
	a := New(&Config{})
	a.cfg.SourcePath = "config.json"
	return a
}

func TestProvidersFileScaffoldedBesideConfig(t *testing.T) {
	a := newProvidersApp(t)
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Providers) == 0 {
		t.Fatal("scaffold must carry the embedded registry")
	}
	if _, err := os.Stat("providers.json"); err != nil {
		t.Fatal("providers.json must be created alongside config.json:", err)
	}
	// A PROPERTY, not a mode integer: Windows has no POSIX bits, so
	// "!= 0600" is a statement about nothing there. This file carries
	// the operator's API keys on every platform.
	restricted, err := fileperm.IsRestrictedToOwner("providers.json")
	if err != nil {
		t.Fatal(err)
	}
	if !restricted {
		t.Fatal("providers.json carries API keys and is readable beyond its owner")
	}
}

func TestEmbeddedClaudeOAuthContractIsProviderOwned(t *testing.T) {
	const beta = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,per-turn-control-2026-07-01,mid-conversation-tool-changes-2026-07-01,mid-conversation-system-clear-at-2026-08-21,effort-2025-11-24,thinking-binding-controls-2026-08-01,extended-cache-ttl-2025-04-11,cache-diagnosis-2026-04-07"
	var reg providerRegistry
	if err := json.Unmarshal(embeddedProviders, &reg); err != nil {
		t.Fatal(err)
	}
	for _, entry := range reg.Providers {
		if entry.Name != "Claude (Max/Pro)" {
			continue
		}
		if entry.APIType != "anthropic" || entry.Credential != "claude-code" {
			t.Fatalf("Claude OAuth route = %q/%q", entry.APIType, entry.Credential)
		}
		want := map[string]string{"header_anthropic-beta": beta, "header_x-app": "cli"}
		for name, value := range want {
			if got := entry.CredentialOptions[name]; got != value {
				t.Fatalf("Claude OAuth %s = %q, want working C contract %q", name, got, value)
			}
		}
		// ONE scalar owns the version now. The two literal request fields
		// are gone from the embed — they derive at load — so the embed
		// cannot carry a two-field mismatch.
		if _, has := entry.CredentialOptions["billing_text"]; has {
			t.Fatal("the embed still carries a literal billing_text — the version must live in client_version alone")
		}
		if _, has := entry.CredentialOptions["header_user-agent"]; has {
			t.Fatal("the embed still carries a literal header_user-agent — the version must live in client_version alone")
		}
		version := entry.CredentialOptions["client_version"]
		if version != "2.1.283" {
			t.Fatalf("Claude OAuth shipped client version = %q, want 2.1.283", version)
		}
		if thinkingShapeFor(reg.effective(), entry, "claude-opus-5-5") != "adaptive" {
			t.Fatal("Opus 5.5 lacks its captured adaptive-thinking request shape")
		}
		if !slices.Contains(entry.Models, "claude-opus-5-5") {
			t.Fatal("Claude Opus 5.5 is absent from the shipped model fallback")
		}
		var major, minor, patch int
		if n, err := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); err != nil || n != 3 {
			t.Fatalf("client_version is not a parseable version: %q", version)
		}
		if major < 2 || major == 2 && (minor < 1 || minor == 1 && patch < 251) {
			t.Fatalf("Claude OAuth advertises unsupported Claude Code %s; need 2.1.251 or newer", version)
		}
	}
}

func TestAStaleClaudeClientVersionHealsOnLoadAndIsNotPersisted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json")

	raw := `{"providers":[{"name":"Claude (Max/Pro)","api_type":"anthropic","url":"https://api.anthropic.com","credential":"claude-code","credential_options":{` +
		`"billing_text":"x-anthropic-billing-header: cc_version=2.1.111; cc_entrypoint=cli; cch=00000;",` +
		`"header_user-agent":"claude-cli/2.1.251 (external, cli)"}}]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	co := reg.Providers[0].CredentialOptions
	ver := co["client_version"]
	if ver != "2.1.283" {
		t.Fatalf("missing client_version did not inherit shipped default: %q", ver)
	}
	if co["billing_text"] != "x-anthropic-billing-header: cc_version="+ver+"; cc_entrypoint=cli; cch=00000;" {
		t.Fatalf("billing_text did not heal: %q (version %q)", co["billing_text"], ver)
	}
	if co["header_user-agent"] != "claude-cli/"+ver+" (external, sdk-cli)" {
		t.Fatalf("user-agent did not heal: %q (version %q)", co["header_user-agent"], ver)
	}
	if strings.Contains(co["billing_text"], "2.1.111") || strings.Contains(co["header_user-agent"], "2.1.251") {
		t.Fatal("a stale version survived the load")
	}
	// Embed-owned: saving must NOT persist the derived fields as operator
	// choices, or a future shipped correction could never replace them —
	// the very trap that created this bug.
	if _, err := saveProvidersFile(path, reg); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(path)
	for _, k := range []string{"client_version", "billing_text", "header_user-agent"} {
		if strings.Contains(string(back), k) {
			t.Fatalf("%q was written back as an operator choice: %s", k, back)
		}
	}
}

// A former shipped value may also be an intentional operator pin. The file
// cannot distinguish those cases, so an explicit value must survive a vendor
// default update and an unrelated save.
func TestExplicitFormerClaudeVersionRemainsAnOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	raw := `{"providers":[{"name":"Claude (Max/Pro)","api_type":"anthropic",` +
		`"url":"https://api.anthropic.com","credential":"claude-code",` +
		`"credential_options":{"client_version":"2.1.257"}}]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	co := reg.Providers[0].CredentialOptions
	if got := co["client_version"]; got != "2.1.257" {
		t.Fatalf("explicit client version was replaced: %q", got)
	}
	for key, want := range map[string]string{
		"billing_text":      "x-anthropic-billing-header: cc_version=2.1.257; cc_entrypoint=cli; cch=00000;",
		"header_user-agent": "claude-cli/2.1.257 (external, sdk-cli)",
	} {
		if got := co[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if _, err := saveProvidersFile(path, reg); err != nil {
		t.Fatal(err)
	}
	back, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(back), `"client_version": "2.1.257"`) {
		t.Fatal("explicit client version was lost on save")
	}
	for _, key := range []string{`"billing_text"`, `"header_user-agent"`} {
		if strings.Contains(string(back), key) {
			t.Fatalf("derived vendor fact %s became a persisted override", key)
		}
	}
	again, err := loadProvidersFile(path)
	if err != nil || again.Providers[0].CredentialOptions["client_version"] != "2.1.257" {
		t.Fatalf("explicit version did not survive save/reload: %v", err)
	}
}

// A vendor protocol revision must be selectable through the operator's JSON.
// The loader must neither replace it nor strip it on an unrelated save.
func TestClaudeClientVersionAndFormatsAreJSONOverrides(t *testing.T) {
	a := newProvidersApp(t)
	raw := `{"providers":[{"name":"Configured Claude","api_type":"anthropic","url":"https://example.test","credential":"claude-code","credential_options":{"client_version":"99.8.7"},"credential_option_formats":{"billing_text":"revision={client_version}","header_user-agent":"configured/{client_version}"}}]}`
	if err := os.WriteFile(a.providersPath(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		reg, err := a.loadProviders()
		if err != nil {
			t.Fatal(err)
		}
		opts := reg.Providers[0].CredentialOptions
		if opts["client_version"] != "99.8.7" || opts["billing_text"] != "revision=99.8.7" || opts["header_user-agent"] != "configured/99.8.7" {
			t.Fatal("configured revision/formats were replaced")
		}
		if _, err := saveProvidersFile(a.providersPath(), reg); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEmbeddedAnthropicUsesNativeMessagesAPI(t *testing.T) {
	var reg providerRegistry
	if err := json.Unmarshal(embeddedProviders, &reg); err != nil {
		t.Fatal(err)
	}
	for _, entry := range reg.Providers {
		if entry.Name == "Anthropic" {
			if entry.APIType != "anthropic" || entry.URL != "https://api.anthropic.com" || entry.Credential != "" {
				t.Fatalf("Anthropic API-key route = dialect %q, endpoint %q, credential %q", entry.APIType, entry.URL, entry.Credential)
			}
			return
		}
	}
	t.Fatal("embedded Anthropic API-key provider is absent")
}

func TestProviderEditPreservesFileOnlyCredentialOptions(t *testing.T) {
	a := newProvidersApp(t)
	options := map[string]string{
		"billing_text": "billing", "header_anthropic-beta": "oauth-test",
	}
	if err := a.setProvider(providerEntry{
		Name: "subscription", APIType: "anthropic", URL: "https://example.test",
		Credential: "file:/credential.json", CredentialOptions: options,
	}, false); err != nil {
		t.Fatal(err)
	}
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: "subscription", APIType: "anthropic", Endpoint: "https://example.test/v1",
		Credential: "file:/credential.json",
	}}); err != nil {
		t.Fatal(err)
	}
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range reg.Providers {
		if entry.Name == "subscription" {
			if entry.URL != "https://example.test/v1" {
				t.Fatalf("visible edit did not land: %q", entry.URL)
			}
			for name, want := range options {
				if got := entry.CredentialOptions[name]; got != want {
					t.Fatalf("credential option %s = %q, want %q", name, got, want)
				}
			}
			return
		}
	}
	t.Fatal("provider was not saved")
}

func TestProviderCredentialChangeDropsPriorOptions(t *testing.T) {
	a := newProvidersApp(t)
	// At the address the new credential is for: the card attaches a
	// credential nowhere else (row 4.4).
	home := shippedEntry(t, "ChatGPT (Plus/Pro)").URL
	if err := a.setProvider(providerEntry{
		Name: "subscription", APIType: "anthropic", URL: home,
		Credential: "claude-code", CredentialOptions: map[string]string{"billing_text": "claude-only"},
	}, false); err != nil {
		t.Fatal(err)
	}
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: "subscription", APIType: "openai", Endpoint: home,
		Credential: "codex",
	}}); err != nil {
		t.Fatal(err)
	}
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range reg.Providers {
		if entry.Name == "subscription" {
			if entry.CredentialOptions["billing_text"] != "" {
				t.Fatalf("prior credential options survived credential change: %+v", entry.CredentialOptions)
			}
			if entry.CredentialOptions["query_client_version"] == "" {
				t.Fatalf("new credential's shipped options were not supplied: %+v", entry.CredentialOptions)
			}
			return
		}
	}
	t.Fatal("provider was not saved")
}

func TestProvidersFileRejectsUnknownAndTrailingData(t *testing.T) {
	for _, body := range []string{
		`{"providers":[],"typo":true}`,
		`{"providers":[]} {}`,
	} {
		path := filepath.Join(t.TempDir(), "providers.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadProvidersFile(path); err == nil {
			t.Fatalf("a file that is not a providers file was accepted: %s", body)
		}
	}
	good := `{"name":"ok","url":"https://ok.test","models":["m"]}`
	for _, c := range []struct{ bad, reason string }{
		{`{"name":"bad","api_type":"anthropicc","url":"https://example.com"}`, `unknown api_type "anthropicc"`},
		{`{"name":""}`, "provider name is empty"},
		{`{"name":"ok","url":"https://again.test"}`, `duplicate provider "ok"`},
		{`{"name":"two","url":"https://two.test","default":true}`, `are both default`},
	} {
		first := strings.Replace(good, `"models"`, `"default":true,"models"`, 1)
		body := `{"providers":[` + first + `,` + c.bad + `]}`
		path := filepath.Join(t.TempDir(), "providers.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		reg, err := loadProvidersFile(path)
		if err != nil {
			t.Fatalf("one bad entry refused the whole file: %v\n%s", err, body)
		}
		if len(reg.Providers) != 1 || reg.Providers[0].Name != "ok" {
			t.Fatalf("the good entry is not in use: %+v", reg.Providers)
		}
		if len(reg.broken) != 1 || reg.broken[0].position != 1 || !strings.Contains(reg.broken[0].reason, c.reason) {
			t.Fatalf("the bad entry was not set aside with its reason %q: %+v", c.reason, reg.broken)
		}
	}
}

func TestProviderCRUDAndKeyPersistence(t *testing.T) {
	a := newProvidersApp(t)

	// Add.
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: "Local", Endpoint: "http://127.0.0.1:8081/v1",
		DefaultModel: "glm-5.2", Default: true,
	}, APIKey: "k-1"}); err != nil {
		t.Fatal(err)
	}
	reg, _ := a.loadProviders()
	var got *providerEntry
	for i := range reg.Providers {
		if reg.Providers[i].Name == "Local" {
			got = &reg.Providers[i]
		} else if reg.Providers[i].Default {
			t.Fatalf("one default only: %s still default", reg.Providers[i].Name)
		}
	}
	if got == nil || got.APIKey != "k-1" || got.DefaultModel != "glm-5.2" || !got.Default {
		t.Fatalf("added entry wrong: %+v", got)
	}

	// The key is not round-tripped; an edit that does not clear it keeps it.
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: "Local", APIType: "openai", Endpoint: "http://127.0.0.1:8081/v1",
		DefaultModel: "glm-5.3", Default: true,
	}}); err != nil {
		t.Fatal(err)
	}

	var refused *keyAddressError
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: "Local", APIType: "openai", Endpoint: "http://127.0.0.1:9091/v1",
		DefaultModel: "glm-5.3", Default: true,
	}}); !errors.As(err, &refused) {
		t.Fatalf("a blank key was taken to another address: %v", err)
	}
	reg, _ = a.loadProviders()
	for i := range reg.Providers {
		if reg.Providers[i].Name == "Local" {
			if reg.Providers[i].APIKey != "k-1" {
				t.Fatal("blank key must KEEP the stored key")
			}
			if reg.Providers[i].URL != "http://127.0.0.1:8081/v1" || reg.Providers[i].DefaultModel != "glm-5.3" {
				t.Fatalf("update did not land, or a refused move did: %+v", reg.Providers[i])
			}
		}
	}
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: "Local", APIType: "openai", Endpoint: "http://127.0.0.1:9091/v1",
		DefaultModel: "glm-5.3", Default: true,
	}, APIKey: "k-1"}); err != nil {
		t.Fatal(err)
	}
	reg, _ = a.loadProviders()
	for i := range reg.Providers {
		if reg.Providers[i].Name == "Local" && (reg.Providers[i].URL != "http://127.0.0.1:9091/v1" || reg.Providers[i].APIKey != "k-1") {
			t.Fatalf("the move with its key entered did not land: %+v", reg.Providers[i])
		}
	}

	// A key variable is not the card's to name (row 4.4): a save that
	// names one providers.json does not is refused. It is named in the
	// file, here by the door that writes a whole entry.
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: "Local", APIType: "openai", Endpoint: "http://127.0.0.1:9091/v1",
		APIKeyEnv: "LOCAL_KEY", DefaultModel: "glm-5.3", Default: true,
	}}); !errors.As(err, &refused) {
		t.Fatalf("a save from the card named a key variable the file does not name: %v", err)
	}
	reg, _ = a.loadProviders()
	named := *entryNamed(reg, "Local")
	named.APIKeyEnv = "LOCAL_KEY"
	if err := a.setProvider(named, true); err != nil {
		t.Fatal(err)
	}

	// Non-secret fields are replacements: the editor round-trips values
	// it keeps, and blank/zero clears them.
	temp, topp := 0.0, 0.9
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: "Local", Endpoint: "http://127.0.0.1:9091/v1",
		APIType: "anthropic", APIKeyEnv: "LOCAL_KEY",
		DefaultModel: "glm-5.3", Default: true,
		ConfiguredModels: []string{"glm-5.3"}, ContextLength: 4096, MaxOutputTokens: 512,
		ReasoningEffort: "high", ThinkingBudget: 128,
		Temperature: &temp, TopP: &topp,
		Extra: map[string]any{"repetition_penalty": 1.05},
	}}); err != nil {
		t.Fatal(err)
	}
	if reg, _ = a.loadProviders(); entryNamed(reg, "Local").APIKeyEnv != "LOCAL_KEY" {
		t.Fatalf("the variable the file names, sent back by the save, was not kept: %+v", entryNamed(reg, "Local"))
	}
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: "Local", APIType: "anthropic", Endpoint: "http://127.0.0.1:9091/v1",
		DefaultModel: "glm-5.4", Default: true,
		ConfiguredModels: []string{"glm-5.3"}, ContextLength: 4096, MaxOutputTokens: 512,
		ReasoningEffort: "high", ThinkingBudget: 128,
		Temperature: &temp, TopP: &topp, Extra: map[string]any{"repetition_penalty": 1.05},
	}}); err != nil {
		t.Fatal(err)
	}
	reg, _ = a.loadProviders()
	for i := range reg.Providers {
		if reg.Providers[i].Name == "Local" {
			e := reg.Providers[i]
			if e.APIType != "anthropic" || e.APIKeyEnv != "" {
				t.Fatalf("api_type must survive and blank api_key_env must unset: %+v", e)
			}
			if e.Temperature == nil || *e.Temperature != 0 || e.TopP == nil || *e.TopP != 0.9 {
				t.Fatalf("sampling pointers must survive (0 stays a SET zero): %+v", e)
			}
			if e.Extra == nil || e.Extra["repetition_penalty"] != 1.05 {
				t.Fatalf("extra must survive: %+v", e.Extra)
			}
			if e.DefaultModel != "glm-5.4" {
				t.Fatalf("the edit itself must land: %+v", e)
			}
		}
	}
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: "Local", APIType: "anthropic", Endpoint: "http://127.0.0.1:9091/v1",
		DefaultModel: "glm-5.4", Credential: "none", Default: true,
	}, ClearKey: true}); err != nil {
		t.Fatal(err)
	}
	reg, _ = a.loadProviders()
	for _, e := range reg.Providers {
		if e.Name == "Local" && (e.APIKey != "" || len(e.Models) != 0 || e.ContextLength != 0 || e.MaxOutputTokens != 0 ||
			e.ReasoningEffort != "" || e.ThinkingBudget != 0 || e.Temperature != nil || e.TopP != nil || e.Extra != nil) {
			t.Fatalf("explicit clears did not replace provider fields: %+v", e)
		}
	}
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: "bad-env", Endpoint: "https://provider.example/v1",
		APIKeyEnv: "this.looks.like.a.pasted.key", DefaultModel: "m",
	}}); err == nil || !strings.Contains(err.Error(), "environment variable name") {
		t.Fatalf("a pasted key in api_key_env must be refused with guidance, got %v", err)
	}

	// The birth path writes the data where it lives: the genesis
	// request upserts the entry — key, chosen model as its default,
	// default flag — and returns the pointer name; with no name on the
	// wire it adopts the existing entry for the endpoint.
	name, uerr := a.upsertBirthProvider(&dashboard.GenesisRequest{
		Endpoint: "http://127.0.0.1:9091/v1", APIKey: "k-2", Model: "glm-6",
	})
	if uerr != nil {
		t.Fatalf("the birth provider write is a refusal point, not best-effort: %v", uerr)
	}
	if name != "Local" {
		t.Fatalf("birth must adopt the existing entry for its endpoint, got %q", name)
	}
	reg, _ = a.loadProviders()
	for i := range reg.Providers {
		if reg.Providers[i].Name == "Local" {
			if reg.Providers[i].APIKey != "k-2" || reg.Providers[i].DefaultModel != "glm-6" || !reg.Providers[i].Default {
				t.Fatalf("birth upsert must persist key+model+default: %+v", reg.Providers[i])
			}
		}
	}

	// Remove.
	if err := a.deleteProvider("Local"); err != nil {
		t.Fatal(err)
	}
	reg, _ = a.loadProviders()
	for _, e := range reg.Providers {
		if e.Name == "Local" {
			t.Fatal("deleted entry survived")
		}
	}

	// The file is user-editable JSON end to end.
	raw, _ := os.ReadFile("providers.json")
	var chk providerRegistry
	if err := json.Unmarshal(raw, &chk); err != nil {
		t.Fatalf("providers.json must remain clean editable JSON: %v", err)
	}
}

// A birth on an endpoint the registry has never seen invents an entry
// named after the endpoint host — the pointer must land on something.
func TestBirthProviderNamedFromHostWhenUnknown(t *testing.T) {
	a := newProvidersApp(t)
	name, uerr := a.upsertBirthProvider(&dashboard.GenesisRequest{
		Endpoint: "http://10.0.0.5:8081/v1", APIKey: "k", Model: "m",
	})
	if uerr != nil {
		t.Fatalf("the birth provider write is a refusal point, not best-effort: %v", uerr)
	}
	if name != "10.0.0.5:8081" {
		t.Fatalf("unknown endpoint must yield a host-named entry, got %q", name)
	}
	reg, _ := a.loadProviders()
	found := false
	for _, e := range reg.Providers {
		if e.Name == name {
			found = true
			if e.URL != "http://10.0.0.5:8081/v1" || e.DefaultModel != "m" || e.APIKey != "k" || !e.Default {
				t.Fatalf("birth entry incomplete: %+v", e)
			}
		}
	}
	if !found {
		t.Fatal("birth entry missing from the registry")
	}
}

func TestProviderUpdatesAreSerialized(t *testing.T) {
	a := newProvidersApp(t)
	const updates = 16
	start := make(chan struct{})
	errs := make(chan error, updates)
	for i := 0; i < updates; i++ {
		i := i
		go func() {
			<-start
			errs <- a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
				Name: fmt.Sprintf("concurrent-%02d", i), Endpoint: "https://provider.example/v1", DefaultModel: "m",
			}})
		}()
	}
	close(start)
	for i := 0; i < updates; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent provider update: %v", err)
		}
	}

	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	found := make(map[string]bool, updates)
	for _, entry := range reg.Providers {
		found[entry.Name] = true
	}
	for i := 0; i < updates; i++ {
		name := fmt.Sprintf("concurrent-%02d", i)
		if !found[name] {
			t.Fatalf("serialized provider update lost %s", name)
		}
	}
}

func TestProviderWriteRestoresPrivateMode(t *testing.T) {
	a := newProvidersApp(t)
	if _, err := a.loadProviders(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod("providers.json", 0644); err != nil {
		t.Fatal(err)
	}
	if err := a.setProvider(providerEntry{Name: "private", URL: "https://provider.example/v1", DefaultModel: "m"}, false); err != nil {
		t.Fatal(err)
	}
	restricted, err := fileperm.IsRestrictedToOwner("providers.json")
	if err != nil {
		t.Fatal(err)
	}
	if !restricted {
		t.Fatal("providers.json is readable beyond its owner")
	}
}

func TestEmbeddedCredentialOptionsFillWithoutOverriding(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json")

	// An operator file shaped like the one that broke: the Claude entry
	// carries no credential_options at all, plus one entry that pins a
	// NON-version option deliberately. (The version-bearing fields —
	// billing_text, header_user-agent, client_version — are embed-owned
	// and are covered by TestAStaleClaudeClientVersionHealsOnLoad...;
	// header_x-app is an ordinary fill-without-override option.)
	body := `{"providers":[
	  {"name":"Claude (Max/Pro)","api_type":"anthropic","url":"https://api.anthropic.com","credential":"claude-code","default_model":"claude-opus-5"},
	  {"name":"Pinned","api_type":"anthropic","url":"https://api.anthropic.com","credential":"claude-code",
	   "credential_options":{"header_x-app":"operator-app"}},
	  {"name":"Legacy ChatGPT","api_type":"openai","url":"https://chatgpt.com/backend-api/codex","credential":"codex"}
	]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	byName := map[string]providerEntry{}
	for _, e := range reg.Providers {
		byName[e.Name] = e
	}

	// Filled: every option the credential requires is present.
	claude := byName["Claude (Max/Pro)"]
	for _, want := range []string{"billing_text", "header_anthropic-beta", "header_user-agent", "header_x-app"} {
		if v := claude.CredentialOptions[want]; v == "" {
			t.Errorf("credential option %q not supplied from the embedded registry — the operator would be asked for a vendor fact they cannot know", want)
		}
	}

	// Not overridden: an operator value on a NON-version option survives,
	// and the rest still fill.
	pinned := byName["Pinned"]
	if got := pinned.CredentialOptions["header_x-app"]; got != "operator-app" {
		t.Fatalf("operator value on a non-version option overwritten: header_x-app = %q, want operator-app", got)
	}
	if pinned.CredentialOptions["header_anthropic-beta"] == "" {
		t.Fatal("pinning one option must not suppress the others")
	}
	// But the version FIELDS are embed-owned even on the pinned entry:
	// an operator cannot pin a version the API will refuse.
	if !strings.Contains(pinned.CredentialOptions["header_user-agent"], pinned.CredentialOptions["client_version"]) {
		t.Fatalf("version fields are not embed-derived on the pinned entry: %v", pinned.CredentialOptions)
	}

	// A providers.json created before Codex acquired a discovery-version
	// option heals on load just like Claude's vendor request facts.
	if got := byName["Legacy ChatGPT"].CredentialOptions["query_client_version"]; got != "1.0.0" {
		t.Fatalf("Codex discovery version was not supplied from the embedded registry: %q", got)
	}
}

func TestCredentialRefusedWhenRegistryOptionsMissing(t *testing.T) {
	required := requiredCredentialOptions("claude-code")
	if len(required) == 0 {
		t.Fatal("the shipped registry declares no options for claude-code — the requirement is no longer data")
	}

	// Nothing supplied: every declared option is reported missing.
	missing := missingCredentialOptions("claude-code", nil)
	if len(missing) != len(required) {
		t.Fatalf("missing = %v, want all of %v", missing, required)
	}

	// The refusal an operator actually sees names them.
	app := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	_, err := app.credentialSource("claude-code", nil)
	if err == nil {
		t.Fatal("a credential adopted without its request contract must be refused")
	}
	for _, opt := range required {
		if !strings.Contains(err.Error(), opt) {
			t.Fatalf("refusal did not name the missing option %q: %v", opt, err)
		}
	}

	// A blank value is an operator clearing one deliberately, and is
	// still refused — filling only supplies what is ABSENT.
	opts := map[string]string{}
	for _, o := range required {
		opts[o] = "x"
	}
	opts[required[0]] = "   "
	if got := missingCredentialOptions("claude-code", opts); len(got) != 1 || got[0] != required[0] {
		t.Fatalf("a blanked option must still be missing; got %v", got)
	}

	// A credential the registry says nothing about requires nothing.
	if got := missingCredentialOptions("no-such-credential", nil); len(got) != 0 {
		t.Fatalf("an undeclared credential must require nothing; got %v", got)
	}
}

func TestPublishedCredentialBoundaryMatchesTheRuntime(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "creds.json")
	body, err := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{
		"accessToken": "acc", "refreshToken": "ref",
		"expiresAt": time.Now().Add(7 * time.Minute).UnixMilli(), // the reported case
		"scopes":    []string{"user:profile", "user:inference"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credPath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	opts := map[string]string{"file": credPath}
	for _, entry := range embeddedRegistry().Providers {
		if entry.Credential == "claude-code" {
			for k, v := range entry.CredentialOptions {
				opts[k] = v
			}
		}
	}
	// Runtime-derived version fields are filled by the same registry path.
	reg := providerRegistry{Providers: []providerEntry{{Credential: "claude-code", CredentialOptions: opts}}}
	fillEmbeddedCredentialOptions(&reg)
	opts = reg.Providers[0].CredentialOptions
	app := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	info := app.credentialInfo(providerEntry{Credential: "claude-code", CredentialOptions: opts})
	if info == nil {
		t.Fatal("no credential info")
	}
	if info.Error != "" {
		t.Fatalf("source refused for an unrelated reason: %s", info.Error)
	}
	if !info.Expired {
		t.Fatalf("a credential 7 minutes from expiry is inside the %v skew and the runtime WILL refuse it, "+
			"but the dashboard was told it is usable until %s", oauth.ExpirySkew, info.ExpiresAt)
	}
}

func TestCredentialInfoNoticesAFileRemovedAfterItWasRead(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "creds.json")
	body, err := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{
		"accessToken": "acc", "refreshToken": "ref",
		"expiresAt": time.Now().Add(2 * time.Hour).UnixMilli(),
		"scopes":    []string{"user:profile", "user:inference"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := map[string]string{"file": credPath}
	for _, entry := range embeddedRegistry().Providers {
		if entry.Credential == "claude-code" {
			for k, v := range entry.CredentialOptions {
				opts[k] = v
			}
		}
	}
	reg := providerRegistry{Providers: []providerEntry{{Credential: "claude-code", CredentialOptions: opts}}}
	fillEmbeddedCredentialOptions(&reg)
	opts = reg.Providers[0].CredentialOptions
	app := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	// This proof removes a FILE, not the operator's Keychain item. The
	// shipped macOS fallback must not read a real credential after that
	// removal or turn a fixture absence into an unrelated usable account.
	// A service is required; name this unique fixture, not a real account.
	opts["keychain_service"] = "aii-os-test-absent-" + dir
	entry := providerEntry{Credential: "claude-code", CredentialOptions: opts}
	if info := app.credentialInfo(entry); info == nil || info.Error != "" || info.Expired {
		t.Fatalf("a readable, unexpired credential is not described as one: %+v", info)
	}
	if err := os.Remove(credPath); err != nil {
		t.Fatal(err)
	}
	info := app.credentialInfo(entry)
	if info == nil || !strings.Contains(info.Error, credPath) {
		t.Fatalf("the credential's file is gone and its description does not say so: %+v", info)
	}
}

// A REJECTED CREDENTIAL IS A CREDENTIAL STATE, NOT AN OUTAGE. The probe
// dials a credential-backed entry with its token; when the provider refuses
// it, the turn path asks the source whether the owner has advanced the file
// and otherwise refuses with the typed reason — and the probe reports that
// same state, so a revoked token reads as credential_expired with its
// remedy, never as "unreachable", and Settings offers the sign-in. An owned
// credential the authority no longer honours classifies the same way.
func TestARejectedCredentialIsNotReportedAsAnOutage(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "creds.json")
	body, err := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{
		"accessToken": "acc", "refreshToken": "ref",
		"expiresAt": time.Now().Add(2 * time.Hour).UnixMilli(),
		"scopes":    []string{"user:profile", "user:inference"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"type":"error","error":{"type":"authentication_error"}}`, http.StatusUnauthorized)
	}))
	t.Cleanup(refused.Close)
	opts := map[string]string{"file": credPath, "base_url": refused.URL}
	for _, entry := range embeddedRegistry().Providers {
		if entry.Credential == "claude-code" {
			for k, v := range entry.CredentialOptions {
				if _, mine := opts[k]; !mine {
					opts[k] = v
				}
			}
		}
	}
	reg := providerRegistry{Providers: []providerEntry{{Credential: "claude-code", CredentialOptions: opts}}}
	fillEmbeddedCredentialOptions(&reg)
	opts = reg.Providers[0].CredentialOptions
	app := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	probe := app.probeOne(providerEntry{Name: "Claude", URL: refused.URL, APIType: "anthropic", Credential: "claude-code", CredentialOptions: opts})
	if probe.state != "credential_expired" || !strings.Contains(probe.reason, "rejected") {
		t.Fatalf("a rejected credential probed as %q (%s), want credential_expired naming the rejection", probe.state, probe.reason)
	}
	if got := classifyCredentialErr(wrapUnavailable(oauth.ErrGrantInvalid)); got != "credential_expired" {
		t.Fatalf("an owned credential the authority no longer honours classified as %q, want credential_expired", got)
	}
}

func TestCredentialWarningArrivesBeforeTheLightsGoOut(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name string
		left time.Duration
		want string // "" = silent, "soon" = countdown, "gone" = already unusable
	}{
		{"a whole day of life", 24 * time.Hour, ""},
		{"just outside the window", credentialWarnWindow + time.Minute, ""},
		{"inside the window", credentialWarnWindow - time.Minute, "soon"},
		{"minutes left", 5 * time.Minute, "soon"},
		{"already unusable", -time.Minute, "gone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := credentialWarningFor("claude-code", false, now.Add(tc.left), now)
			switch tc.want {
			case "":
				if got != "" {
					t.Fatalf("warned with %v of life left: %q", tc.left, got)
				}
			case "soon":
				if !strings.Contains(got, "becomes unusable in") {
					t.Fatalf("no countdown with %v left: %q", tc.left, got)
				}
				if !strings.Contains(got, "claude-code") || !strings.Contains(got, "its own tool") {
					t.Fatalf("warning must name the credential and the remedy: %q", got)
				}
			case "gone":
				if !strings.Contains(got, "no longer usable") {
					t.Fatalf("an expired credential must say so plainly: %q", got)
				}
				if !strings.Contains(got, "cannot think until you do") {
					t.Fatalf("the operator must be told the consequence: %q", got)
				}
			}
		})
	}
}

// The identity's own sign-in has no other tool. When it cannot be renewed
// the remedy named is the one its credential source names, to sign in
// again; "refresh it with its own tool" sent the operator looking for a
// tool that does not exist.
func TestTheWarningForTheIdentitysOwnSignInSaysToSignInAgain(t *testing.T) {
	now := time.Now()
	for _, left := range []time.Duration{5 * time.Minute, -time.Minute} {
		got := credentialWarningFor("claude-code", true, now.Add(left), now)
		if !strings.Contains(got, "claude-code") || !strings.Contains(got, "sign in again") || strings.Contains(got, "its own tool") {
			t.Fatalf("with %v left, an owned credential that cannot be renewed was warned about as %q", left, got)
		}
	}
	if got := credentialWarningFor("claude-code", true, now.Add(credentialWarnWindow+time.Minute), now); got != "" {
		t.Fatalf("warned outside the window: %q", got)
	}
}

// The fallback used to be applied locally wherever a number was needed,
// leaving the entry itself at zero — so the budget reserved 8192 and the
// wire sent whatever each dialect decided. Materializing it into the
// entry is what makes one number govern both halves.
func TestOutputAllocationIsMaterialized(t *testing.T) {
	got := resolveOutputAllocation(providerEntry{Name: "p"})
	if got.MaxOutputTokens != defaultOutputReserve {
		t.Fatalf("MaxOutputTokens = %d, want the reserve %d written back", got.MaxOutputTokens, defaultOutputReserve)
	}
	// An operator's explicit value is never overwritten.
	got = resolveOutputAllocation(providerEntry{Name: "p", MaxOutputTokens: 300})
	if got.MaxOutputTokens != 300 {
		t.Fatalf("an explicit allocation was overwritten: %d", got.MaxOutputTokens)
	}
	// And the budget reserves exactly what the wire will carry.
	if defaultOutputReserve != llm.DefaultMaxOutputTokens {
		t.Fatalf("the budget reserve and the wire default have diverged: %d vs %d",
			defaultOutputReserve, llm.DefaultMaxOutputTokens)
	}
}

// The identity reaches the world through native tool calls — 64.5% of
// 1,322 measured calls are bash alone. A substrate that answers text
// beautifully and never calls a tool hosts a resident who can think and
// cannot act, and that was discovered at the first turn needing a tool,
// silently, as work simply not happening.
func TestToolSelectionProbe(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantWarn  string // substring, "" = no warning
		wantState capState
	}{
		{
			name:      "substrate calls the tool",
			status:    200,
			body:      `{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`,
			wantState: capYes,
		},
		{
			name:      "substrate answers in words instead",
			status:    200,
			body:      `{"content":[{"type":"text","text":"I am ready."}],"stop_reason":"end_turn"}`,
			wantWarn:  "WITHOUT calling the tool",
			wantState: capNo,
		},
		{
			// A REJECTION IS NOT A VERDICT: the same 400 arrives from a
			// rate limit, and both legs now ask at once. Unknown, not no.
			name:      "substrate rejects a tool-bearing request",
			status:    400,
			body:      `{"error":{"message":"tools not supported"}}`,
			wantWarn:  "rejected a tool-bearing request",
			wantState: capUnknown,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			app := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
			client := llm.New(&llm.ClientConfig{
				Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic",
			})
			state, got := app.probeToolSelection(context.Background(), client,
				providerEntry{Name: "p"}, "m")
			if state != tc.wantState {
				t.Fatalf("state = %v, want %v", state, tc.wantState)
			}
			if tc.wantWarn == "" {
				if got != "" {
					t.Fatalf("a working substrate was warned about: %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantWarn) {
				t.Fatalf("warning = %q, want it to contain %q", got, tc.wantWarn)
			}
			// The operator has to know which provider and what it costs them.
			if !strings.Contains(got, "\"p\"") {
				t.Fatalf("warning does not name the provider: %q", got)
			}
		})
	}
}

func TestClaudeWireProfileJSONSurvivesSaveAndReachesTransport(t *testing.T) {
	a := newProvidersApp(t)

	credentialPath := filepath.Join(t.TempDir(), "claude-fixture.json")
	credential, err := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{
		"accessToken": "fixture-access", "refreshToken": "fixture-refresh",
		"expiresAt": time.Now().Add(time.Hour).UnixMilli(),
		"scopes":    []string{"user:profile", "user:inference"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialPath, credential, 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	var entry providerEntry
	for _, e := range reg.Providers {
		if e.Credential == "claude-code" {
			entry = e
			break
		}
	}
	if entry.ClaudeCode == nil {
		t.Fatal("shipped Claude wire profile missing")
	}
	entry.ClaudeCode.RequestPath = "/configured/messages?beta=custom"
	entry.ClaudeCode.Stream = false
	entry.ClaudeCode.Headers = map[string]string{"user-agent": "configured/{client_version}"}
	entry.ClaudeCode.ContextManagement = json.RawMessage(`{"edits":[]}`)
	entry.CredentialOptions["client_version"] = "99.8.7"
	entry.CredentialOptions["file"] = credentialPath
	reg.Providers = []providerEntry{entry}
	if _, err := saveProvidersFile(a.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadProvidersFile(a.providersPath())
	if err != nil {
		t.Fatal(err)
	}
	e := loaded.Providers[0]
	if e.ClaudeCode == nil || e.ClaudeCode.RequestPath != entry.ClaudeCode.RequestPath || e.ClaudeCode.Stream || e.ClaudeCode.Headers["user-agent"] != "configured/{client_version}" {
		t.Fatal("explicit profile replaced or lost on save/load")
	}
	cc, err := a.entryTransport(e, "")
	if !oauth.Available() {
		// JSON round-trip applies everywhere; adopting a desktop CLI does
		// not. Preserve the credential owner's typed refusal on mobile.
		_, adoptionErr := oauth.New(e.Credential, e.CredentialOptions)
		if adoptionErr == nil || !errors.Is(err, adoptionErr) {
			t.Fatalf("desktop adoption on mobile: transport=%v owner=%v", err, adoptionErr)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if cc.ClaudeCode == nil || cc.ClaudeCode.Version != "99.8.7" || cc.ClaudeCode.Stream || cc.ClaudeCode.RequestPath != entry.ClaudeCode.RequestPath {
		t.Fatal("profile override did not reach transport")
	}
	e.ClaudeCode = &llm.ClaudeCodeProfile{Enabled: false}
	cc, err = a.entryTransport(e, "")
	if err != nil {
		t.Fatal(err)
	}
	if cc.ClaudeCode != nil {
		t.Fatal("explicit disabled profile inherited defaults")
	}
	e.ClaudeCode = nil
	cc, err = a.entryTransport(e, "")
	if err != nil {
		t.Fatal(err)
	}
	if cc.ClaudeCode == nil || !cc.ClaudeCode.Stream || cc.ClaudeCode.Version != "99.8.7" {
		t.Fatal("absent profile did not inherit shipped defaults and configured version")
	}
}
