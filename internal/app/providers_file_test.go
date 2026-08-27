package app

import (
	"context"
	"encoding/json"
	"fmt"
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

// providers.json is the operator's file, created alongside config.json
// (operator, 2026-08-20): scaffolded from the embedded registry on
// first use, 0600 (entries carry API keys), user-editable, and the UI's
// add/update/remove write it through one act each.
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
	st, err := os.Stat("providers.json")
	if err != nil {
		t.Fatal("providers.json must be created alongside config.json:", err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("providers.json carries API keys — want 0600, got %o", st.Mode().Perm())
	}
}

func TestEmbeddedClaudeOAuthContractIsProviderOwned(t *testing.T) {
	const (
		billing = "x-anthropic-billing-header: cc_version=2.1.111; cc_entrypoint=cli; cch=00000;"
		beta    = "oauth-2025-04-20,interleaved-thinking-2025-05-14,claude-code-20250219,context-management-2025-06-27,prompt-caching-scope-2026-01-05,effort-2025-11-24"
	)
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
		want := map[string]string{
			"billing_text":          billing,
			"header_anthropic-beta": beta,
			"header_user-agent":     "claude-cli/2.1.111 (external, cli)",
			"header_x-app":          "cli",
		}
		for name, value := range want {
			if got := entry.CredentialOptions[name]; got != value {
				t.Fatalf("Claude OAuth %s = %q, want working C contract %q", name, got, value)
			}
		}
		return
	}
	t.Fatal("embedded Claude OAuth provider is absent")
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
			if len(entry.CredentialOptions) != 0 {
				t.Fatalf("prior credential options survived credential change: %+v", entry.CredentialOptions)
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
		`{"providers":[{"name":"bad","api_type":"anthropicc","url":"https://example.com"}]}`,
		`{"providers":[{"name":""}]}`,
		`{"providers":[{"name":"same"},{"name":"same"}]}`,
		`{"providers":[{"name":"one","default":true},{"name":"two","default":true}]}`,
	} {
		path := filepath.Join(t.TempDir(), "providers.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadProvidersFile(path); err == nil {
			t.Fatalf("invalid provider data was accepted: %s", body)
		}
	}
}

func TestProviderCRUDAndKeyPersistence(t *testing.T) {
	a := newProvidersApp(t)

	// Add.
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

	// The key is not round-tripped; has_key explicitly keeps it.
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

	// Non-secret fields are replacements: the editor round-trips values
	// it keeps, and blank/zero clears them.
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
	info, err := os.Stat("providers.json")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("providers.json mode = %o, want 600", info.Mode().Perm())
	}
}

// Vendor request facts come from the embedded registry; the operator
// overrides them, and never has to supply them.
//
// A live identity could not switch to Claude OAuth: "credential
// claude-code requires provider options billing_text,
// header_anthropic-beta, header_user-agent, header_x-app". Its
// providers.json predated those options, and load scaffolds the
// embedded file only when none exists — it merged nothing afterwards,
// so the file could never gain a fact added later. The four names meant
// nothing to the operator, and their correct values live in a file they
// have no reason to know about.
func TestEmbeddedCredentialOptionsFillWithoutOverriding(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json")

	// An operator file shaped like the one that broke: the Claude entry
	// carries no credential_options at all, plus one entry that pins a
	// single option deliberately.
	body := `{"providers":[
	  {"name":"Claude (Max/Pro)","api_type":"anthropic","url":"https://api.anthropic.com","credential":"claude-code","default_model":"claude-opus-5"},
	  {"name":"Pinned","api_type":"anthropic","url":"https://api.anthropic.com","credential":"claude-code",
	   "credential_options":{"header_user-agent":"operator/1.0"}}
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

	// Not overridden: an operator value survives, and the rest still fill.
	pinned := byName["Pinned"]
	if got := pinned.CredentialOptions["header_user-agent"]; got != "operator/1.0" {
		t.Fatalf("operator value overwritten: header_user-agent = %q, want operator/1.0", got)
	}
	if pinned.CredentialOptions["header_x-app"] == "" {
		t.Fatal("pinning one option must not suppress the others")
	}
}

// The vendor contract is checked against DATA, and refusal still names
// every option it wanted.
//
// internal/oauth used to hold these four names as a Go slice behind a
// kind == KindClaudeCode special case, contradicting its own comment:
// "Data, not special cases — a vendor that changes what it wants is a
// table edit." Adding a fifth required header meant a code change and a
// rebuild. It is now a line of config/providers.json.
//
// This carries over the invariant from the retired oauth test: a
// credential adopted without its request contract is refused, and the
// refusal names what is missing.
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

// THE LIVE REPORT, 2026-08-23: the dashboard said "valid to 17:52" and
// the identity died at 17:37 with "expired or too close to expiry". The
// runtime refuses a skew EARLY; the dashboard was publishing the raw
// vendor expiry. Two components, one fact, different answers — and the
// operator, told the credential was fine, had no way to read the error.
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
	for _, o := range requiredCredentialOptions("claude-code") {
		if _, ok := opts[o]; !ok {
			opts[o] = "x"
		}
	}
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

// The runtime may never refresh an adopted credential, so when one lapses
// the identity stops thinking and cannot say why — announcing anything
// needs the credential that just failed. The warning is the only thing
// standing between the operator and a silent outage, so its boundaries
// are pinned here.
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
		name     string
		status   int
		body     string
		wantWarn string // substring, "" = no warning
	}{
		{
			name:   "substrate calls the tool",
			status: 200,
			body:   `{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`,
		},
		{
			name:     "substrate answers in words instead",
			status:   200,
			body:     `{"content":[{"type":"text","text":"I am ready."}],"stop_reason":"end_turn"}`,
			wantWarn: "WITHOUT calling the tool",
		},
		{
			name:     "substrate rejects a tool-bearing request",
			status:   400,
			body:     `{"error":{"message":"tools not supported"}}`,
			wantWarn: "rejected a tool-bearing request",
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
			got := app.probeToolSelection(context.Background(), client,
				providerEntry{Name: "p"}, "m")
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
