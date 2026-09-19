package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// .
// .
// .
// .
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
	// .
	// .
	// .
	restricted, err := fileperm.IsRestrictedToOwner("providers.json")
	if err != nil {
		t.Fatal(err)
	}
	if !restricted {
		t.Fatal("providers.json carries API keys and is readable beyond its owner")
	}
}

func TestEmbeddedClaudeOAuthContractIsProviderOwned(t *testing.T) {
	const beta = "oauth-2025-04-20,interleaved-thinking-2025-05-14,claude-code-20250219,context-management-2025-06-27,prompt-caching-scope-2026-01-05,effort-2025-11-24"
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
		// .
		// .
		// .
		if _, has := entry.CredentialOptions["billing_text"]; has {
			t.Fatal("the embed still carries a literal billing_text — the version must live in client_version alone")
		}
		if _, has := entry.CredentialOptions["header_user-agent"]; has {
			t.Fatal("the embed still carries a literal header_user-agent — the version must live in client_version alone")
		}
		version := entry.CredentialOptions["client_version"]
		var major, minor, patch int
		if n, err := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); err != nil || n != 3 {
			t.Fatalf("client_version is not a parseable version: %q", version)
		}
		if major < 2 || major == 2 && (minor < 1 || minor == 1 && patch < 251) {
			t.Fatalf("Claude OAuth advertises unsupported Claude Code %s; need 2.1.251 or newer", version)
		}
	}
}

// .
// .
// .
// .
// .
// .
func TestAStaleClaudeClientVersionHealsOnLoadAndIsNotPersisted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json")
	// .
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
	if ver == "" {
		t.Fatal("client_version was not supplied to a stale file")
	}
	if co["billing_text"] != "x-anthropic-billing-header: cc_version="+ver+"; cc_entrypoint=cli; cch=00000;" {
		t.Fatalf("billing_text did not heal: %q (version %q)", co["billing_text"], ver)
	}
	if co["header_user-agent"] != "claude-cli/"+ver+" (external, cli)" {
		t.Fatalf("user-agent did not heal: %q (version %q)", co["header_user-agent"], ver)
	}
	if strings.Contains(co["billing_text"], "2.1.111") || strings.Contains(co["header_user-agent"], "2.1.251") {
		t.Fatal("a stale version survived the load")
	}
	// .
	// .
	// .
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

// .
// .
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
	if err := a.setProviderInfo(dashboard.ProviderInfo{
		Name: "subscription", APIType: "anthropic", Endpoint: "https://example.test/v1",
		Credential: "file:/credential.json",
	}); err != nil {
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
	if err := a.setProvider(providerEntry{
		Name: "subscription", APIType: "anthropic", URL: "https://example.test",
		Credential: "claude-code", CredentialOptions: map[string]string{"billing_text": "claude-only"},
	}, false); err != nil {
		t.Fatal(err)
	}
	if err := a.setProviderInfo(dashboard.ProviderInfo{
		Name: "subscription", APIType: "openai", Endpoint: "https://example.test",
		Credential: "codex",
	}); err != nil {
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

// .
// .
// .
// .
// .
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

	// .
	if err := a.setProviderInfo(dashboard.ProviderInfo{
		Name: "Local", Endpoint: "http://127.0.0.1:8081/v1",
		APIKey: "k-1", DefaultModel: "glm-5.2", Default: true,
	}); err != nil {
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

	// .
	if err := a.setProviderInfo(dashboard.ProviderInfo{
		Name: "Local", APIType: "openai", Endpoint: "http://127.0.0.1:9091/v1",
		HasKey: true, DefaultModel: "glm-5.3", Default: true,
	}); err != nil {
		t.Fatal(err)
	}
	reg, _ = a.loadProviders()
	for i := range reg.Providers {
		if reg.Providers[i].Name == "Local" {
			if reg.Providers[i].APIKey != "k-1" {
				t.Fatal("blank key must KEEP the stored key")
			}
			if reg.Providers[i].URL != "http://127.0.0.1:9091/v1" || reg.Providers[i].DefaultModel != "glm-5.3" {
				t.Fatalf("update did not land: %+v", reg.Providers[i])
			}
		}
	}

	// .
	// .
	temp, topp := 0.0, 0.9
	if err := a.setProviderInfo(dashboard.ProviderInfo{
		Name: "Local", Endpoint: "http://127.0.0.1:9091/v1",
		APIType: "anthropic", APIKeyEnv: "LOCAL_KEY", HasKey: true,
		DefaultModel: "glm-5.3", Default: true,
		ConfiguredModels: []string{"glm-5.3"}, ContextLength: 4096, MaxOutputTokens: 512,
		ReasoningEffort: "high", ThinkingBudget: 128,
		Temperature: &temp, TopP: &topp,
		Extra: map[string]any{"repetition_penalty": 1.05},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.setProviderInfo(dashboard.ProviderInfo{
		Name: "Local", APIType: "anthropic", Endpoint: "http://127.0.0.1:9091/v1",
		HasKey: true, DefaultModel: "glm-5.4", Default: true,
		ConfiguredModels: []string{"glm-5.3"}, ContextLength: 4096, MaxOutputTokens: 512,
		ReasoningEffort: "high", ThinkingBudget: 128,
		Temperature: &temp, TopP: &topp, Extra: map[string]any{"repetition_penalty": 1.05},
	}); err != nil {
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
	if err := a.setProviderInfo(dashboard.ProviderInfo{
		Name: "Local", APIType: "anthropic", Endpoint: "http://127.0.0.1:9091/v1",
		DefaultModel: "glm-5.4", Credential: "none", Default: true,
	}); err != nil {
		t.Fatal(err)
	}
	reg, _ = a.loadProviders()
	for _, e := range reg.Providers {
		if e.Name == "Local" && (e.APIKey != "" || len(e.Models) != 0 || e.ContextLength != 0 || e.MaxOutputTokens != 0 ||
			e.ReasoningEffort != "" || e.ThinkingBudget != 0 || e.Temperature != nil || e.TopP != nil || e.Extra != nil) {
			t.Fatalf("explicit clears did not replace provider fields: %+v", e)
		}
	}
	if err := a.setProviderInfo(dashboard.ProviderInfo{
		Name: "bad-env", Endpoint: "https://provider.example/v1",
		APIKeyEnv: "this.looks.like.a.pasted.key", DefaultModel: "m",
	}); err == nil || !strings.Contains(err.Error(), "environment variable name") {
		t.Fatalf("a pasted key in api_key_env must be refused with guidance, got %v", err)
	}

	// .
	// .
	// .
	// .
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

	// .
	if err := a.deleteProvider("Local"); err != nil {
		t.Fatal(err)
	}
	reg, _ = a.loadProviders()
	for _, e := range reg.Providers {
		if e.Name == "Local" {
			t.Fatal("deleted entry survived")
		}
	}

	// .
	raw, _ := os.ReadFile("providers.json")
	var chk providerRegistry
	if err := json.Unmarshal(raw, &chk); err != nil {
		t.Fatalf("providers.json must remain clean editable JSON: %v", err)
	}
}

// .
// .
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
			errs <- a.setProviderInfo(dashboard.ProviderInfo{
				Name: fmt.Sprintf("concurrent-%02d", i), Endpoint: "https://provider.example/v1", DefaultModel: "m",
			})
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestEmbeddedCredentialOptionsFillWithoutOverriding(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json")

	// .
	// .
	// .
	// .
	// .
	// .
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

	// .
	claude := byName["Claude (Max/Pro)"]
	for _, want := range []string{"billing_text", "header_anthropic-beta", "header_user-agent", "header_x-app"} {
		if v := claude.CredentialOptions[want]; v == "" {
			t.Errorf("credential option %q not supplied from the embedded registry — the operator would be asked for a vendor fact they cannot know", want)
		}
	}

	// .
	// .
	pinned := byName["Pinned"]
	if got := pinned.CredentialOptions["header_x-app"]; got != "operator-app" {
		t.Fatalf("operator value on a non-version option overwritten: header_x-app = %q, want operator-app", got)
	}
	if pinned.CredentialOptions["header_anthropic-beta"] == "" {
		t.Fatal("pinning one option must not suppress the others")
	}
	// .
	// .
	if !strings.Contains(pinned.CredentialOptions["header_user-agent"], pinned.CredentialOptions["client_version"]) {
		t.Fatalf("version fields are not embed-derived on the pinned entry: %v", pinned.CredentialOptions)
	}

	// .
	// .
	if got := byName["Legacy ChatGPT"].CredentialOptions["query_client_version"]; got != "1.0.0" {
		t.Fatalf("Codex discovery version was not supplied from the embedded registry: %q", got)
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestCredentialRefusedWhenRegistryOptionsMissing(t *testing.T) {
	required := requiredCredentialOptions("claude-code")
	if len(required) == 0 {
		t.Fatal("the shipped registry declares no options for claude-code — the requirement is no longer data")
	}

	// .
	missing := missingCredentialOptions("claude-code", nil)
	if len(missing) != len(required) {
		t.Fatalf("missing = %v, want all of %v", missing, required)
	}

	// .
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

	// .
	// .
	opts := map[string]string{}
	for _, o := range required {
		opts[o] = "x"
	}
	opts[required[0]] = "   "
	if got := missingCredentialOptions("claude-code", opts); len(got) != 1 || got[0] != required[0] {
		t.Fatalf("a blanked option must still be missing; got %v", got)
	}

	// .
	if got := missingCredentialOptions("no-such-credential", nil); len(got) != 0 {
		t.Fatalf("an undeclared credential must require nothing; got %v", got)
	}
}

// .
// .
// .
// .
// .
func TestPublishedCredentialBoundaryMatchesTheRuntime(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "creds.json")
	body, err := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{
		"accessToken": "acc", "refreshToken": "ref",
		"expiresAt": time.Now().Add(7 * time.Minute).UnixMilli(),
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
	// .
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

// .
// .
// .
// .
// .
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
	// .
	// .
	// .
	// .
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

// .
// .
// .
// .
// .
// .
// .
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

// .
// .
// .
// .
// .
func TestCredentialWarningArrivesBeforeTheLightsGoOut(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name string
		left time.Duration
		want string
	}{
		{"a whole day of life", 24 * time.Hour, ""},
		{"just outside the window", credentialWarnWindow + time.Minute, ""},
		{"inside the window", credentialWarnWindow - time.Minute, "soon"},
		{"minutes left", 5 * time.Minute, "soon"},
		{"already unusable", -time.Minute, "gone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := credentialWarningFor("claude-code", now.Add(tc.left), now)
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

// .
// .
// .
// .
func TestOutputAllocationIsMaterialized(t *testing.T) {
	got := resolveOutputAllocation(providerEntry{Name: "p"})
	if got.MaxOutputTokens != defaultOutputReserve {
		t.Fatalf("MaxOutputTokens = %d, want the reserve %d written back", got.MaxOutputTokens, defaultOutputReserve)
	}
	// .
	got = resolveOutputAllocation(providerEntry{Name: "p", MaxOutputTokens: 300})
	if got.MaxOutputTokens != 300 {
		t.Fatalf("an explicit allocation was overwritten: %d", got.MaxOutputTokens)
	}
	// .
	if defaultOutputReserve != llm.DefaultMaxOutputTokens {
		t.Fatalf("the budget reserve and the wire default have diverged: %d vs %d",
			defaultOutputReserve, llm.DefaultMaxOutputTokens)
	}
}

// .
// .
// .
// .
// .
func TestToolSelectionProbe(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantWarn  string
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
			// .
			// .
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
			// .
			if !strings.Contains(got, "\"p\"") {
				t.Fatalf("warning does not name the provider: %q", got)
			}
		})
	}
}
