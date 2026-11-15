package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// EVERY VENDOR REQUEST HEADER VALUE IS CONFIGURATION (the operator,
// 2026-11-11: "ALL OAuth headers are config file only"; ruling of 2026-11-11
// §3). These tests go through the effective request builder — the identity's
// providers.json, the shipped registry, entryTransport or resolveLLM, the
// llm client — to a local server that records what arrives: no request
// leaves the host and none is billed. Each asks one thing: what config says
// reaches the wire; a changed value changes the wire; a removed value
// refuses the request, naming its key, with nothing sent.

// capture is a local server that records each request's headers and body.
type capture struct {
	srv     *httptest.Server
	mu      sync.Mutex
	headers []http.Header
	bodies  [][]byte
}

func newCapture(t *testing.T, reply string) *capture {
	t.Helper()
	c := &capture{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.headers = append(c.headers, r.Header.Clone())
		c.bodies = append(c.bodies, body)
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, reply)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.headers)
}

func (c *capture) last(t *testing.T) (http.Header, []byte) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.headers) == 0 {
		t.Fatal("no request reached the capture server")
	}
	return c.headers[len(c.headers)-1], c.bodies[len(c.bodies)-1]
}

const anthropicReply = `{"id":"m","type":"message","role":"assistant","model":"fixture-model","content":[{"type":"text","text":"alive"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`

func shippedRegistryCopy(t *testing.T) providerRegistry {
	t.Helper()
	var reg providerRegistry
	if err := json.Unmarshal(embeddedProviders, &reg); err != nil {
		t.Fatal(err)
	}
	return reg
}

// withShipped replaces the shipped providers.json for one test, so that a
// negative can remove a value from every source that may supply it, the
// shipped file included (ruling of 2026-11-11 §2c): removing only the
// identity's override and watching the shipped value appear is ruling E
// working, not a negative. Call it before the test's App is made; its
// cleanup runs after the App's.
func withShipped(t *testing.T, edit func(entries []map[string]any)) {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(embeddedProviders, &raw); err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	for _, e := range raw["providers"].([]any) {
		entries = append(entries, e.(map[string]any))
	}
	edit(entries)
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	old := embeddedProviders
	embeddedProviders = b
	embedOnce, embedReg = sync.Once{}, providerRegistry{}
	t.Cleanup(func() {
		embeddedProviders = old
		embedOnce, embedReg = sync.Once{}, providerRegistry{}
	})
}

// shippedRaw is the decoded shipped entry of that name, for withShipped.
func shippedRaw(t *testing.T, entries []map[string]any, name string) map[string]any {
	t.Helper()
	for _, e := range entries {
		if e["name"] == name {
			return e
		}
	}
	t.Fatalf("no shipped entry %q", name)
	return nil
}

// claudeApp is an identity whose providers.json holds the shipped Claude
// subscription entry, edited by mutate, adopting a borrowed credential that
// is current, and pointed at a capture server.
func claudeApp(t *testing.T, mutate func(*providerEntry)) (*App, *capture, providerEntry) {
	t.Helper()
	a := newProvidersApp(t)
	fixtureSignInContext(t, a)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	reg := shippedRegistryCopy(t)
	var entry providerEntry
	for _, candidate := range reg.Providers {
		if candidate.OAuth == "anthropic-claude" {
			entry = candidate
			break
		}
	}
	if entry.Name == "" {
		t.Fatal("the shipped Claude entry is missing")
	}
	borrowed := filepath.Join(home, "borrowed.json")
	b, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "borrowed-access", "refreshToken": "borrowed-refresh", "expiresAt": time.Now().Add(time.Hour).UnixMilli(), "scopes": []string{entry.CredentialOptions["required_scope"]}}})
	if err := os.WriteFile(borrowed, b, 0o600); err != nil {
		t.Fatal(err)
	}
	cap := newCapture(t, anthropicReply)
	// The identity's file holds what an identity writes: where its
	// credential and endpoint are. Every other option and the wire profile
	// are the shipped file's, lent at load as on a real install.
	entry.CredentialOptions = map[string]string{"default_file": borrowed, "base_url": cap.srv.URL}
	entry.ClaudeCode = nil
	entry.URL = cap.srv.URL
	entry.DefaultModel = "fixture-model"
	entry.Models = []string{entry.DefaultModel}
	entry.ThinkingMode = "off"
	if mutate != nil {
		mutate(&entry)
	}
	reg.Providers = []providerEntry{entry}
	if _, err := saveProvidersFile(a.providersPath(), &reg); err != nil {
		t.Fatal(err)
	}
	a.cfg.LLM = LLMConfig{Provider: entry.Name, Model: entry.DefaultModel, TimeoutSeconds: 5, Retries: -1}
	a.oauthGuard = func(context.Context, string) error { return nil }
	return a, cap, entry
}

func chatOnce(t *testing.T, a *App) error {
	t.Helper()
	cc, _, err := a.resolveLLM()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = llm.New(&cc).Chat(ctx, []llm.Message{{Role: "user", Content: "hi"}}, llm.ChatOptions{})
	return err
}

// shippedClaudeProfile is the shipped profile and options, as config holds
// them: the source the wire is compared with.
func shippedClaudeProfile(t *testing.T) (*llm.ClaudeCodeProfile, map[string]string) {
	t.Helper()
	for _, e := range shippedRegistryCopy(t).Providers {
		if e.OAuth == "anthropic-claude" && e.ClaudeCode != nil {
			return e.ClaudeCode, e.CredentialOptions
		}
	}
	t.Fatal("no shipped Claude profile")
	return nil, nil
}

func TestTheClaudeRequestCarriesTheConfiguredHeaders(t *testing.T) {
	a, cap, _ := claudeApp(t, nil)
	if err := chatOnce(t, a); err != nil {
		t.Fatal(err)
	}
	got, body := cap.last(t)
	profile, options := shippedClaudeProfile(t)
	version := options["client_version"]
	// Every header the profile names reaches the wire with config's value
	// (template variables resolved); none is added from code but the
	// credential and the protocol's own.
	for name, value := range profile.Headers {
		switch {
		case strings.Contains(value, "{session_id}"), strings.Contains(value, "{retry_count}"), strings.Contains(value, "{timeout_seconds}"),
			strings.Contains(value, "{os}"), strings.Contains(value, "{arch}"):
			if got.Get(name) == "" {
				t.Errorf("%s: configured with a template and absent from the wire", name)
			}
		default:
			want := strings.NewReplacer("{client_version}", version, "{entrypoint}", profile.EntryPoint).Replace(value)
			if got.Get(name) != want {
				t.Errorf("%s = %q on the wire, config says %q", name, got.Get(name), want)
			}
		}
	}
	// The shipped values are 2.1.322's own (captured from the vendor's own CLI,
	// 2026-11-11): the profile is the one the vendor accepts.
	for name, want := range map[string]string{
		"User-Agent":                  "claude-cli/2.1.322 (external, sdk-cli)",
		"X-Stainless-Package-Version": "0.128.0",
		"X-Stainless-Runtime-Version": "v26.12.1",
		"Anthropic-Version":           "2023-06-01",
	} {
		if got.Get(name) != want {
			t.Errorf("%s = %q, want 2.1.322's %q", name, got.Get(name), want)
		}
	}
	for _, gone := range []string{"X-Claude-Code-Prompt-Id", "X-Client-Request-Id", "X-Claude-Code-Request-Class"} {
		if got.Get(gone) != "" {
			t.Errorf("%s was sent; 2.1.322 sends none, and config names none", gone)
		}
	}
	var sent struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
	}
	if err := json.Unmarshal(body, &sent); err != nil || len(sent.System) == 0 {
		t.Fatalf("no system block on the wire: %v", err)
	}
	// The billing line, as 2.1.322 writes it for "hi": the fingerprint is
	// "aaf", the one its own request carried.
	if want := "x-anthropic-billing-header: cc_version=2.1.322.aaf; cc_entrypoint=sdk-cli;"; sent.System[0].Text != want {
		t.Errorf("billing line %q, want %q", sent.System[0].Text, want)
	}
}

func TestAConfiguredClaudeHeaderChangesTheWire(t *testing.T) {
	t.Run("the operator's header_ option wins over the inherited profile", func(t *testing.T) {
		a, cap, _ := claudeApp(t, func(e *providerEntry) {
			e.CredentialOptions["header_anthropic-version"] = "2031-02-03"
			e.CredentialOptions["client_version"] = "2.1.399"
		})
		if err := chatOnce(t, a); err != nil {
			t.Fatal(err)
		}
		got, _ := cap.last(t)
		if got.Get("Anthropic-Version") != "2031-02-03" {
			t.Errorf("anthropic-version = %q: the identity's value must not be overwritten by the shipped profile", got.Get("Anthropic-Version"))
		}
		if !strings.Contains(got.Get("User-Agent"), "2.1.399") {
			t.Errorf("user-agent = %q: the identity's client_version must reach it", got.Get("User-Agent"))
		}
	})
	t.Run("an explicit profile's header reaches the wire", func(t *testing.T) {
		a, cap, _ := claudeApp(t, func(e *providerEntry) {
			profile, _ := shippedClaudeProfile(t)
			own := *profile
			own.Headers = maps.Clone(profile.Headers)
			own.Headers["x-stainless-package-version"] = "9.9.9"
			e.ClaudeCode = &own
		})
		if err := chatOnce(t, a); err != nil {
			t.Fatal(err)
		}
		got, _ := cap.last(t)
		if got.Get("X-Stainless-Package-Version") != "9.9.9" {
			t.Errorf("x-stainless-package-version = %q, want the identity's 9.9.9", got.Get("X-Stainless-Package-Version"))
		}
	})
}

func TestAClaudeRequestWithoutAConfiguredVersionIsNotSent(t *testing.T) {
	// Every source that may supply it: the shipped Claude profile, the
	// shipped Claude options, and (for the dialect) the shipped Anthropic
	// entry; the identity's file is the shipped entry, so it holds none.
	withShipped(t, func(entries []map[string]any) {
		claude := shippedRaw(t, entries, "Claude (Max/Pro)")
		delete(claude["claude_code"].(map[string]any)["headers"].(map[string]any), "anthropic-version")
		delete(claude["credential_options"].(map[string]any), "header_anthropic-version")
		delete(shippedRaw(t, entries, "Anthropic"), "headers")
	})
	a, cap, _ := claudeApp(t, nil)
	err := chatOnce(t, a)
	var unset *llm.HeaderNotConfiguredError
	if !errors.As(err, &unset) || unset.Header != "anthropic-version" || !strings.Contains(err.Error(), "header_anthropic-version") {
		t.Fatalf("err = %v, want a HeaderNotConfiguredError naming header_anthropic-version", err)
	}
	if n := cap.count(); n != 0 {
		t.Fatalf("%d requests reached the server; a request without its configured version must not be sent", n)
	}
}

// AN EMPTY VALUE IS NOT A VALUE (ruling of 2026-11-11 §2b). Set empty in any
// source the identity's file holds, it is refused before a request, naming
// the key; it does not fall through to the shipped value, and an empty header
// is never sent.
func TestAnEmptyHeaderValueIsRefusedNamingItsKey(t *testing.T) {
	ownProfile := func(e *providerEntry, name, value string) {
		profile, _ := shippedClaudeProfile(t)
		own := *profile
		own.Headers = maps.Clone(profile.Headers)
		own.Headers[name] = value
		e.ClaudeCode = &own
	}
	for _, tc := range []struct {
		name   string
		mutate func(*providerEntry)
		key    string
	}{
		{"a credential option", func(e *providerEntry) { e.CredentialOptions["header_anthropic-version"] = "" }, `credential_options["header_anthropic-version"]`},
		{"a profile header", func(e *providerEntry) { ownProfile(e, "x-app", "") }, `claude_code.headers["x-app"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, cap, _ := claudeApp(t, tc.mutate)
			err := chatOnce(t, a)
			var bad *HeaderConfigError
			if !errors.As(err, &bad) || len(bad.Keys) != 1 || bad.Keys[0] != tc.key {
				t.Fatalf("err = %v, want a HeaderConfigError naming %s", err, tc.key)
			}
			if n := cap.count(); n != 0 {
				t.Fatalf("%d requests sent; a refused configuration sends none", n)
			}
		})
	}
	t.Run("an entry header", func(t *testing.T) {
		cap := newCapture(t, anthropicReply)
		err := admitOne(t, apiKeyEntry(t, cap, "", map[string]string{"anthropic-version": ""}))
		var bad *HeaderConfigError
		if !errors.As(err, &bad) || len(bad.Keys) != 1 || bad.Keys[0] != `headers["anthropic-version"]` {
			t.Fatalf("err = %v, want a HeaderConfigError naming headers[\"anthropic-version\"]", err)
		}
	})
	t.Run("the shipped file, as loaded, holds no empty value and no header twice", func(t *testing.T) {
		reg, err := decodeProviders("shipped", embeddedProviders)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range reg.Providers {
			// Lent options count here too: two shipped option keys for one
			// header name would be chosen between by map order (a
			// format-derived header_user-agent beside a header_User-Agent
			// did exactly that, caught 2026-11-11).
			if err := entryHeaderRefusal(e, reg.lent[e.Name]); err != nil {
				t.Errorf("%v", err)
			}
			seen := map[string]string{}
			for k := range e.CredentialOptions {
				if name, ok := strings.CutPrefix(k, "header_"); ok {
					if prev, dup := seen[strings.ToLower(name)]; dup {
						t.Errorf("%s: %s and %s name one header", e.Name, prev, k)
					}
					seen[strings.ToLower(name)] = k
				}
			}
		}
	})
}

// admitOne runs one entry through the admission every providers.json entry
// passes (prepareRegistry), as the identity's file would hold it.
func admitOne(t *testing.T, e providerEntry) error {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	_, err = prepareRegistry(providerRegistry{}, []json.RawMessage{raw})
	return err
}

// TWO VALUES FOR ONE HEADER NAME IN THE IDENTITY'S OWN FILE (ruling
// of 2026-11-11 §2a): names compare without case; equal values are
// accepted; differing values are refused before any request, naming both
// keys, with no precedence between them. A value the identity's file holds is
// its own whatever it equals: an explicit copy of the
// shipped default beside a different value is two own values. A value the
// loader lent is not, so a collision with an inherited value is ruling E: the
// identity's own value is the one sent. Each case goes from the identity's
// file, through load and admission, to the request.
func TestOneHeaderWithTwoOwnValuesIsRefused(t *testing.T) {
	ownProfile := func(e *providerEntry, name, value string) {
		profile, _ := shippedClaudeProfile(t)
		own := *profile
		own.Headers = maps.Clone(profile.Headers)
		for k := range own.Headers {
			if strings.EqualFold(k, name) {
				delete(own.Headers, k)
			}
		}
		own.Headers[name] = value
		e.ClaudeCode = &own
	}
	for _, tc := range []struct {
		name     string
		option   [2]string // header_ option name and value
		profile  [2]string // claude_code header name and value
		keys     []string  // refused, naming these; nil = sent
		wireName string
		wire     string
	}{
		{"option and profile differ", [2]string{"header_x-app", "mine-1"}, [2]string{"x-app", "mine-2"},
			[]string{`credential_options["header_x-app"]`, `claude_code.headers["x-app"]`}, "", ""},
		{"the other way round", [2]string{"header_x-app", "mine-2"}, [2]string{"x-app", "mine-1"},
			[]string{`credential_options["header_x-app"]`, `claude_code.headers["x-app"]`}, "", ""},
		{"names that differ only in case", [2]string{"header_X-App", "mine-1"}, [2]string{"x-app", "mine-2"},
			[]string{`credential_options["header_X-App"]`, `claude_code.headers["x-app"]`}, "", ""},
		{"equal values are accepted", [2]string{"header_X-App", "mine"}, [2]string{"x-app", "mine"},
			nil, "X-App", "mine"},
		{"an own option over an inherited profile value", [2]string{"header_x-app", "mine"}, [2]string{},
			nil, "X-App", "mine"},
		{"an explicit option equal to the default beside a different own profile value", [2]string{"header_x-app", "cli"}, [2]string{"x-app", "mine"},
			[]string{`credential_options["header_x-app"]`, `claude_code.headers["x-app"]`}, "", ""},
		{"an explicit profile value equal to the default beside a different own option", [2]string{"header_x-app", "mine"}, [2]string{"x-app", "cli"},
			[]string{`credential_options["header_x-app"]`, `claude_code.headers["x-app"]`}, "", ""},
		{"the explicit default, names that differ only in case", [2]string{"header_X-App", "cli"}, [2]string{"x-app", "mine"},
			[]string{`credential_options["header_X-App"]`, `claude_code.headers["x-app"]`}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, cap, _ := claudeApp(t, func(e *providerEntry) {
				e.CredentialOptions[tc.option[0]] = tc.option[1]
				if tc.profile[0] != "" {
					ownProfile(e, tc.profile[0], tc.profile[1])
				}
			})
			err := chatOnce(t, a)
			if tc.keys != nil {
				var two *HeaderConfigError
				if !errors.As(err, &two) || !slices.Equal(two.Keys, tc.keys) {
					t.Fatalf("err = %v, want a HeaderConfigError naming %q", err, tc.keys)
				}
				if n := cap.count(); n != 0 {
					t.Fatalf("%d requests sent; a refused configuration sends none", n)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, _ := cap.last(t)
			if vs := got.Values(tc.wireName); len(vs) != 1 || vs[0] != tc.wire {
				t.Fatalf("%s on the wire = %q, want exactly %q", tc.wireName, vs, tc.wire)
			}
		})
	}
	t.Run("an own profile value over an inherited option", func(t *testing.T) {
		a, cap, _ := claudeApp(t, func(e *providerEntry) { ownProfile(e, "anthropic-version", "2031-02-03") })
		if err := chatOnce(t, a); err != nil {
			t.Fatal(err)
		}
		if got, _ := cap.last(t); got.Get("Anthropic-Version") != "2031-02-03" {
			t.Fatalf("anthropic-version = %q, want the identity's own profile value", got.Get("Anthropic-Version"))
		}
	})
	t.Run("an entry's headers that differ only in case", func(t *testing.T) {
		cap := newCapture(t, anthropicReply)
		err := admitOne(t, apiKeyEntry(t, cap, "", map[string]string{"Anthropic-Version": "2031-02-03", "anthropic-version": "2023-06-01"}))
		var two *HeaderConfigError
		if !errors.As(err, &two) || !slices.Equal(two.Keys, []string{`headers["Anthropic-Version"]`, `headers["anthropic-version"]`}) {
			t.Fatalf("err = %v, want a HeaderConfigError naming both keys", err)
		}
	})
}

// apiKeyEntry is the shipped Anthropic API-key entry pointed at a capture
// server, renamed when name is not empty.
func apiKeyEntry(t *testing.T, cap *capture, name string, headers map[string]string) providerEntry {
	t.Helper()
	for _, e := range shippedRegistryCopy(t).Providers {
		if e.Name == "Anthropic" && e.Credential == "" {
			e.URL = cap.srv.URL
			if name != "" {
				e.Name = name
			}
			e.Headers = headers
			e.ThinkingMode = "off"
			return e
		}
	}
	t.Fatal("the shipped Anthropic entry is missing")
	return providerEntry{}
}

func TestAnAPIKeyRequestCarriesTheEntrysConfiguredVersion(t *testing.T) {
	for _, tc := range []struct {
		name, entry string
		headers     map[string]string
		unshipped   bool   // the shipped Anthropic entry holds no headers
		want        string // "" = refused, nothing sent
	}{
		{"the shipped entry's configured version", "", nil, false, "2023-06-01"},
		{"the identity's own version over the shipped one", "", map[string]string{"Anthropic-Version": "2031-02-03"}, false, "2031-02-03"},
		{"an entry the registry does not name, with its own version", "Elsewhere", map[string]string{"anthropic-version": "2030-01-01"}, false, "2030-01-01"},
		{"an entry the registry does not name is lent the dialect's", "Elsewhere", nil, false, "2023-06-01"},
		{"no source holds one", "", nil, true, ""},
		{"no source holds one, for an unnamed entry", "Elsewhere", nil, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.unshipped {
				withShipped(t, func(entries []map[string]any) { delete(shippedRaw(t, entries, "Anthropic"), "headers") })
			}
			a := newProvidersApp(t)
			cap := newCapture(t, anthropicReply)
			cc, err := a.entryTransport(apiKeyEntry(t, cap, tc.entry, tc.headers), "sk-test")
			if err != nil {
				t.Fatal(err)
			}
			cc.Model = "fixture-model"
			cc.MaxOutputTokens = 64
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err = llm.New(&cc).Chat(ctx, []llm.Message{{Role: "user", Content: "hi"}}, llm.ChatOptions{})
			if tc.want == "" {
				var unset *llm.HeaderNotConfiguredError
				if !errors.As(err, &unset) || cap.count() != 0 {
					t.Fatalf("err = %v with %d requests sent; want a refusal naming the key and nothing sent", err, cap.count())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := cap.last(t); got.Get("Anthropic-Version") != tc.want {
				t.Fatalf("anthropic-version = %q, want %q", got.Get("Anthropic-Version"), tc.want)
			}
		})
	}
}

func TestTheModelListIsAskedWithTheConfiguredVersionOrNotAtAll(t *testing.T) {
	for _, tc := range []struct {
		name, entry string
		unshipped   bool
		want        string
	}{
		{"the shipped entry asks with its configured version", "", false, "2023-06-01"},
		{"an unnamed entry asks with the dialect's", "Elsewhere", false, "2023-06-01"},
		{"with no source holding one, it does not ask", "", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.unshipped {
				withShipped(t, func(entries []map[string]any) { delete(shippedRaw(t, entries, "Anthropic"), "headers") })
			}
			a := newProvidersApp(t)
			cap := newCapture(t, `{"data":[{"id":"fixture-model"}]}`)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _, err := a.discoverMetaForEntry(ctx, apiKeyEntry(t, cap, tc.entry, nil), "sk-test")
			if tc.want == "" {
				var unset *llm.HeaderNotConfiguredError
				if !errors.As(err, &unset) || cap.count() != 0 {
					t.Fatalf("err = %v with %d requests sent; want a refusal naming the key and nothing sent", err, cap.count())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := cap.last(t); got.Get("Anthropic-Version") != tc.want {
				t.Fatalf("anthropic-version = %q, want %q", got.Get("Anthropic-Version"), tc.want)
			}
		})
	}
}

// THE CAPABILITY LOOKUP SENDS ITS OWN CONFIGURED HEADERS AND NOTHING THAT
// IDENTIFIES THE ACCOUNT (rulings of 2026-11-11, §2f and the separate key;
// the review's capture): its User-Agent is OpenRouter's
// capability_catalogue_headers from providers.json, no compiled value; an
// inference entry's own Authorization, API key and account header, set
// explicitly, do not reach it; and OpenRouter inference keeps today's wire,
// no lookup header lent to it.
func TestTheCapabilityLookupSendsItsOwnHeadersAndNoCredential(t *testing.T) {
	var shippedUA string
	for _, e := range shippedRegistryCopy(t).Providers {
		if e.Name == "OpenRouter" {
			shippedUA = e.CapabilityCatalogueHeaders["User-Agent"]
		}
	}
	if shippedUA == "" {
		t.Fatal("the shipped OpenRouter entry configures no capability_catalogue_headers User-Agent")
	}
	for _, tc := range []struct{ name, ua string }{{"the shipped value", ""}, {"a changed value", "fixture-agent/2"}} {
		t.Run(tc.name, func(t *testing.T) {
			var got http.Header
			baseURL, _ := modalityServingFunc(t, func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Clone()
				io.WriteString(w, opusEndpoints)
			})
			entry := providerEntry{Name: "OpenRouter", APIType: "openai", APIKey: "sk-must-not-travel", CatalogueAuthor: "anthropic", CapabilityCatalogue: &baseURL,
				Headers: map[string]string{"Authorization": "Bearer must-not-travel", "X-Api-Key": "must-not-travel", "ChatGPT-Account-ID": "must-not-travel"}}
			want := shippedUA
			if tc.ua != "" {
				entry.CapabilityCatalogueHeaders = map[string]string{"user-agent": tc.ua}
				want = tc.ua
			}
			if mods := new(App).modelModalitiesFor(t.Context(), entry, "claude-opus-5", baseURL); !mods.known() {
				t.Fatal("the lookup was not answered")
			}
			if got.Get("User-Agent") != want {
				t.Errorf("User-Agent = %q, want the configured %q", got.Get("User-Agent"), want)
			}
			for _, h := range []string{"Authorization", "X-Api-Key", "ChatGPT-Account-ID", "Anthropic-Version"} {
				if got.Get(h) != "" {
					t.Errorf("%s was sent to a public catalogue", h)
				}
			}
		})
	}
	t.Run("OpenRouter inference is lent no lookup header", func(t *testing.T) {
		if h := entryHeaders(providerEntry{Name: "OpenRouter", APIType: "openai"}); len(h) != 0 {
			t.Fatalf("OpenRouter inference headers = %v, want none (today's wire)", h)
		}
	})
}

// AN UNNAMED API-KEY ENTRY IS LENT ONLY WHAT ITS DIALECT'S SHIPPED ENTRIES
// AGREE ON (ruling of 2026-11-11): when they disagree, nothing is lent and the
// request goes as it does today.
func TestAnUnnamedEntryIsLentOnlyWhatItsDialectAgreesOn(t *testing.T) {
	every := func(value func(name string) string) func([]map[string]any) {
		return func(entries []map[string]any) {
			for _, e := range entries {
				if e["api_type"] == "openai" && e["credential"] == nil {
					e["headers"] = map[string]any{"X-Fixture": value(e["name"].(string))}
				}
			}
		}
	}
	t.Run("agreed", func(t *testing.T) {
		withShipped(t, every(func(string) string { return "one" }))
		if h := entryHeaders(providerEntry{Name: "Elsewhere", APIType: "openai"}); h["X-Fixture"] != "one" {
			t.Fatalf("lent %v; every shipped openai entry carries X-Fixture: one", h)
		}
	})
	t.Run("disagreed", func(t *testing.T) {
		withShipped(t, every(func(name string) string {
			if name == "DeepSeek" {
				return "two"
			}
			return "one"
		}))
		if h := entryHeaders(providerEntry{Name: "Elsewhere", APIType: "openai"}); len(h) != 0 {
			t.Fatalf("lent %v; shipped entries that disagree lend nothing", h)
		}
	})
	t.Run("the anthropic dialect's one entry", func(t *testing.T) {
		if h := entryHeaders(providerEntry{Name: "Elsewhere", APIType: "anthropic"}); h["anthropic-version"] == "" {
			t.Fatalf("lent %v, want the shipped Anthropic entry's version", h)
		}
	})
}

func TestOperatorHeadersOverTheProfile(t *testing.T) {
	_, shipped := shippedClaudeProfile(t)
	profile := map[string]string{"anthropic-version": "2023-06-01", "x-app": "cli"}
	t.Run("a shipped option changes nothing", func(t *testing.T) {
		opts := map[string]string{"header_x-app": shipped["header_x-app"], "header_anthropic-version": shipped["header_anthropic-version"]}
		if got := operatorHeadersOver(profile, "claude-code", opts); !maps.Equal(got, profile) {
			t.Fatalf("got %v, want the profile unchanged %v", got, profile)
		}
	})
	t.Run("an operator option replaces the profile's, case aside", func(t *testing.T) {
		got := operatorHeadersOver(profile, "claude-code", map[string]string{"header_Anthropic-Version": "2031-02-03"})
		if len(got) != 2 || got["Anthropic-Version"] != "2031-02-03" || got["anthropic-version"] != "" {
			t.Fatalf("got %v, want one anthropic-version, the operator's", got)
		}
	})
	t.Run("the profile it was given is not changed", func(t *testing.T) {
		operatorHeadersOver(profile, "claude-code", map[string]string{"header_x-app": "other"})
		if profile["x-app"] != "cli" {
			t.Fatal("operatorHeadersOver wrote into the profile it was given")
		}
	})
}

// chatGPTApp is an identity whose providers.json holds the shipped ChatGPT
// entry with options, adopting a borrowed Codex credential that is current,
// pointed at a capture server.
func chatGPTApp(t *testing.T, options map[string]string) (*App, *capture, providerEntry) {
	t.Helper()
	a := newProvidersApp(t)
	fixtureSignInContext(t, a)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	reg := shippedRegistryCopy(t)
	var entry providerEntry
	for _, candidate := range reg.Providers {
		if candidate.Credential == "codex" {
			entry = candidate
			break
		}
	}
	if entry.Name == "" {
		t.Fatal("the shipped ChatGPT entry is missing")
	}
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	access := enc(map[string]string{"alg": "none"}) + "." + enc(map[string]any{"exp": time.Now().Add(time.Hour).Unix()}) + ".sig"
	borrowed := filepath.Join(home, "auth.json")
	b, _ := json.Marshal(map[string]any{"tokens": map[string]string{"access_token": access, "refresh_token": "r", "account_id": "acct-1"}})
	if err := os.WriteFile(borrowed, b, 0o600); err != nil {
		t.Fatal(err)
	}
	cap := newCapture(t, `{}`)
	// What an identity writes: where its credential and endpoint are; the
	// rest is the shipped file's, lent at load.
	entry.CredentialOptions = map[string]string{"default_file": borrowed, "base_url": cap.srv.URL}
	for k, v := range options {
		entry.CredentialOptions[k] = v
	}
	entry.URL = cap.srv.URL
	entry.DefaultModel = "fixture-model"
	entry.Models = []string{entry.DefaultModel}
	reg.Providers = []providerEntry{entry}
	if _, err := saveProvidersFile(a.providersPath(), &reg); err != nil {
		t.Fatal(err)
	}
	a.cfg.LLM = LLMConfig{Provider: entry.Name, Model: entry.DefaultModel, TimeoutSeconds: 5, Retries: -1}
	a.oauthGuard = func(context.Context, string) error { return nil }
	return a, cap, entry
}

// The ChatGPT route's headers are its credential's: the account header by
// the name config gives it, and every header_<Name> option. The reply is not
// a Responses stream and the call fails after the request is sent; what is
// asked here is what was sent.
func TestTheChatGPTRequestCarriesTheConfiguredHeaders(t *testing.T) {
	for _, value := range []string{"configured-1", "configured-2"} {
		t.Run(value, func(t *testing.T) {
			a, cap, _ := chatGPTApp(t, map[string]string{"header_X-Fixture-Header": value})
			_ = chatOnce(t, a)
			got, _ := cap.last(t)
			if got.Get("X-Fixture-Header") != value {
				t.Errorf("x-fixture-header = %q, want the configured %q", got.Get("X-Fixture-Header"), value)
			}
			if got.Get("ChatGPT-Account-ID") != "acct-1" {
				t.Errorf("ChatGPT-Account-ID = %q: the account header is named by account_header and carries the credential's account", got.Get("ChatGPT-Account-ID"))
			}
		})
	}
}

// shippedOption is a shipped credential entry's option, as config holds it.
func shippedOption(t *testing.T, credential, key string) string {
	t.Helper()
	for _, e := range shippedRegistryCopy(t).Providers {
		if e.Credential == credential {
			if v := e.CredentialOptions[key]; v != "" {
				return v
			}
		}
	}
	t.Fatalf("the shipped %s entry has no %s", credential, key)
	return ""
}

// A SIGN-IN CREDENTIAL'S REQUESTS CARRY A CONFIGURED USER-AGENT (ruling
// of 2026-11-11 §2g: the value today's wire shows, as configuration). The
// ChatGPT route's inference and model list send its shipped header_User-Agent;
// the Claude route's model list sends the one its credential_option_formats
// derive from client_version (claude-cli/<version> (external, sdk-cli)), and
// its inference the profile's, which TestTheClaudeRequestCarriesTheConfiguredHeaders
// holds. With the value removed from every source, nothing is sent.
func TestASignInCredentialsRequestsCarryTheConfiguredUserAgent(t *testing.T) {
	chatGPTUA := func(t *testing.T) string { return shippedOption(t, "codex", "header_User-Agent") }
	claudeUA := func(t *testing.T) string {
		for _, e := range shippedRegistryCopy(t).Providers {
			if e.Credential == "claude-code" && e.CredentialOptionFormats["header_user-agent"] != "" {
				return strings.ReplaceAll(e.CredentialOptionFormats["header_user-agent"], "{client_version}", e.CredentialOptions["client_version"])
			}
		}
		t.Fatal("the shipped Claude entry derives no header_user-agent")
		return ""
	}
	removeChatGPT := func(entries []map[string]any) {
		delete(shippedRaw(t, entries, "ChatGPT (Plus/Pro)")["credential_options"].(map[string]any), "header_User-Agent")
	}
	removeClaude := func(entries []map[string]any) {
		delete(shippedRaw(t, entries, "Claude (Max/Pro)")["credential_option_formats"].(map[string]any), "header_user-agent")
	}
	chatGPT := func(t *testing.T) (*App, *capture, providerEntry) { return chatGPTApp(t, nil) }
	claude := func(t *testing.T) (*App, *capture, providerEntry) { return claudeApp(t, nil) }
	var unset *llm.HeaderNotConfiguredError
	t.Run("ChatGPT inference", func(t *testing.T) {
		a, cap, _ := chatGPT(t)
		_ = chatOnce(t, a)
		if got, _ := cap.last(t); got.Get("User-Agent") != chatGPTUA(t) {
			t.Fatalf("User-Agent = %q, want the configured %q", got.Get("User-Agent"), chatGPTUA(t))
		}
	})
	t.Run("ChatGPT inference with none configured", func(t *testing.T) {
		withShipped(t, removeChatGPT)
		a, cap, _ := chatGPT(t)
		if err := chatOnce(t, a); !errors.As(err, &unset) || unset.Header != "User-Agent" || cap.count() != 0 {
			t.Fatalf("err = %v with %d sent; want a HeaderNotConfiguredError for User-Agent and nothing sent", err, cap.count())
		}
	})
	for _, tc := range []struct {
		name   string
		app    func(*testing.T) (*App, *capture, providerEntry)
		want   func(*testing.T) string
		remove func([]map[string]any)
	}{
		{"ChatGPT", chatGPT, chatGPTUA, removeChatGPT},
		{"Claude", claude, claudeUA, removeClaude},
	} {
		t.Run(tc.name+" model list", func(t *testing.T) {
			a, cap, entry := tc.app(t)
			_, _, _ = a.discoverMetaForEntry(t.Context(), loadedEntry(t, a, entry.Name), "")
			if got, _ := cap.last(t); got.Get("User-Agent") != tc.want(t) {
				t.Fatalf("User-Agent = %q, want the configured %q", got.Get("User-Agent"), tc.want(t))
			}
		})
		t.Run(tc.name+" model list with none configured", func(t *testing.T) {
			withShipped(t, tc.remove)
			a, cap, entry := tc.app(t)
			_, _, err := a.discoverMetaForEntry(t.Context(), loadedEntry(t, a, entry.Name), "")
			if !errors.As(err, &unset) || unset.Header != "User-Agent" || cap.count() != 0 {
				t.Fatalf("err = %v with %d sent; want a HeaderNotConfiguredError for User-Agent and nothing sent", err, cap.count())
			}
		})
	}
}

// loadedEntry is the identity's entry as the loader admits it: lent options
// filled, its OAuth contract bound.
func loadedEntry(t *testing.T, a *App, name string) providerEntry {
	t.Helper()
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range reg.Providers {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no admitted entry %q", name)
	return providerEntry{}
}

// THE FOUR GAPS THE REVIEW OF 2026-11-11 FOUND IN THE FIRST FREEZE, each
// from the identity's file, through load and admission, to the request.
func TestAHeaderOptionHasOneValueOnTheWireInAnyCase(t *testing.T) {
	// Finding 1, as the earlier ruling has it: the Claude format's keys are
	// the shipped file's, derived from client_version, never kept from the
	// identity's file (TestAStaleClaudeClientVersionHealsOnLoadAndIsNotPersisted).
	// The identity's levers are client_version and its own
	// credential_option_formats, and each changes the wire.
	claudeUA := func(t *testing.T, a *App, cap *capture, entry providerEntry) string {
		t.Helper()
		_, _, _ = a.discoverMetaForEntry(t.Context(), loadedEntry(t, a, entry.Name), "")
		got, _ := cap.last(t)
		if len(got.Values("User-Agent")) != 1 {
			t.Fatalf("model list User-Agent = %q, want one value", got.Values("User-Agent"))
		}
		return got.Get("User-Agent")
	}
	for _, own := range []string{"operator/1", ""} {
		t.Run(fmt.Sprintf("Claude's header_user-agent %q in the file heals to the format", own), func(t *testing.T) {
			a, cap, entry := claudeApp(t, func(e *providerEntry) { e.CredentialOptions["header_user-agent"] = own })
			if got, want := claudeUA(t, a, cap, entry), "claude-cli/"+shippedOption(t, "claude-code", "client_version")+" (external, sdk-cli)"; got != want {
				t.Fatalf("User-Agent = %q, want the format's %q", got, want)
			}
		})
	}
	t.Run("Claude's own client_version and own format change the wire", func(t *testing.T) {
		a, cap, entry := claudeApp(t, func(e *providerEntry) {
			e.CredentialOptions["client_version"] = "2.1.399"
			e.CredentialOptionFormats = map[string]string{"header_user-agent": "operator/{client_version}"}
		})
		if got := claudeUA(t, a, cap, entry); got != "operator/2.1.399" {
			t.Fatalf("User-Agent = %q, want the identity's own format at its own version", got)
		}
	})
	// Finding 2: a lent header never sits beside the identity's own in another case.
	t.Run("ChatGPT's own lowercase header_user-agent, on inference and the model list", func(t *testing.T) {
		a, cap, entry := chatGPTApp(t, map[string]string{"header_user-agent": "operator/1"})
		if err := chatOnce(t, a); cap.count() == 0 {
			t.Fatalf("nothing was sent: %v", err)
		}
		if got, _ := cap.last(t); len(got.Values("User-Agent")) != 1 || got.Get("User-Agent") != "operator/1" {
			t.Fatalf("inference User-Agent = %q, want the identity's operator/1 once", got.Values("User-Agent"))
		}
		_, _, _ = a.discoverMetaForEntry(t.Context(), loadedEntry(t, a, entry.Name), "")
		if got, _ := cap.last(t); len(got.Values("User-Agent")) != 1 || got.Get("User-Agent") != "operator/1" {
			t.Fatalf("model list User-Agent = %q, want the identity's operator/1 once", got.Values("User-Agent"))
		}
	})
	t.Run("Claude's header_User-Agent in another case than the format's key", func(t *testing.T) {
		a, cap, entry := claudeApp(t, func(e *providerEntry) { e.CredentialOptions["header_User-Agent"] = "operator/2" })
		if got, want := claudeUA(t, a, cap, entry), "claude-cli/"+shippedOption(t, "claude-code", "client_version")+" (external, sdk-cli)"; got != want {
			t.Fatalf("User-Agent = %q, want the format's %q, once", got, want)
		}
	})
}

// Finding 3: a token-file credential on the Anthropic dialect, with no
// contract and no version of its own, sends the dialect's configured version,
// as it sent the compiled one before; nothing is compiled in.
func TestATokenFileCredentialOnTheAnthropicDialectSendsTheConfiguredVersion(t *testing.T) {
	a := newProvidersApp(t)
	cap := newCapture(t, anthropicReply)
	tokenFile := filepath.Join(t.TempDir(), "token.json")
	if err := os.WriteFile(tokenFile, []byte(`{"access_token":"fixture-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	e := providerEntry{Name: "Mine", APIType: "anthropic", URL: cap.srv.URL, Credential: "file:" + tokenFile, DefaultModel: "fixture-model", ThinkingMode: "off"}
	if err := admitOne(t, e); err != nil {
		t.Fatal(err)
	}
	cc, err := a.entryTransport(e, "")
	if err != nil {
		t.Fatal(err)
	}
	cc.Model, cc.MaxOutputTokens = "fixture-model", 64
	if _, err := llm.New(&cc).Chat(t.Context(), []llm.Message{{Role: "user", Content: "hi"}}, llm.ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := cap.last(t); got.Get("Anthropic-Version") != "2023-06-01" || got.Get("Authorization") != "Bearer fixture-token" {
		t.Fatalf("anthropic-version = %q, authorization %q; want the dialect's configured version and the file's token", got.Get("Anthropic-Version"), got.Get("Authorization"))
	}
	_, _, _ = a.discoverMetaForEntry(t.Context(), e, "")
	if got, _ := cap.last(t); got.Get("Anthropic-Version") != "2023-06-01" {
		t.Fatalf("model list anthropic-version = %q, want the dialect's configured version", got.Get("Anthropic-Version"))
	}
}

// Finding 4: an API-key entry's admitted headers go on its inference, on
// every dialect, not on its model list alone.
func TestAnAPIKeyEntrysHeadersReachItsInferenceOnEveryDialect(t *testing.T) {
	const openAIOK = `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
	const responsesOK = "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"
	for _, tc := range []struct{ name, apiType, reply string }{
		{"openai", "openai", openAIOK},
		{"responses", "responses", responsesOK},
		{"anthropic", "anthropic", anthropicReply},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newProvidersApp(t)
			cap := newCapture(t, tc.reply)
			e := providerEntry{Name: "Gateway", APIType: tc.apiType, URL: cap.srv.URL, DefaultModel: "fixture-model", ThinkingMode: "off",
				Headers: map[string]string{"X-Gateway": "g1", "anthropic-version": "2023-06-01"}}
			if err := admitOne(t, e); err != nil {
				t.Fatal(err)
			}
			cc, err := a.entryTransport(e, "sk-test")
			if err != nil {
				t.Fatal(err)
			}
			cc.Model, cc.MaxOutputTokens, cc.NoStream = "fixture-model", 64, tc.apiType == "openai"
			_, _ = llm.New(&cc).Chat(t.Context(), []llm.Message{{Role: "user", Content: "hi"}}, llm.ChatOptions{})
			if got, _ := cap.last(t); got.Get("X-Gateway") != "g1" {
				t.Fatalf("X-Gateway = %q on %s inference, want the entry's configured g1", got.Get("X-Gateway"), tc.apiType)
			}
		})
	}
}
