package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/speech"
)

// heardRequest is one request as a recording server received it: the credential it
// carried ("" for none), and every header value and the query, for a key
// carried anywhere else.
type heardRequest struct{ credential, everything string }

// hostRecorder stands at every address a status probe is sent to and
// records, by the host each request named, what it carried.
type hostRecorder struct {
	mu   sync.Mutex
	sent map[string][]heardRequest
	// open makes every address list its models to a request that carries
	// no credential, as a relay that asks for none does.
	open bool
}

func (r *hostRecorder) listToAnyone(open bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.open = open
}

func (r *hostRecorder) to(host string) []heardRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.sent[host])
}

// heardBy is how many requests a host was sent, and whether any of them
// carried a credential, or the given secret anywhere in it.
func (r *hostRecorder) heardBy(host, secret string) (asks int, carried bool) {
	for _, one := range r.to(host) {
		asks++
		carried = carried || one.credential != "" || (secret != "" && strings.Contains(one.everything, secret))
	}
	return asks, carried
}

func (r *hostRecorder) forget() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = map[string][]heardRequest{}
}

// recordEveryHost answers every probe this test makes, whatever address it
// was sent to: the probe's transport dials the recording server for every
// host, so an entry can stand at a vendor's real address, or at any other,
// and nothing leaves the machine. A request with no credential is told one
// is needed, unless the test says the addresses list to anyone
// (listToAnyone); any other is given a model list.
func recordEveryHost(t *testing.T) *hostRecorder {
	t.Helper()
	rec := &hostRecorder{sent: map[string][]heardRequest{}}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		one := heardRequest{credential: r.Header.Get("Authorization") + r.Header.Get("x-api-key"), everything: r.URL.RawQuery}
		for _, values := range r.Header {
			one.everything += "\n" + strings.Join(values, "\n")
		}
		rec.mu.Lock()
		rec.sent[r.Host] = append(rec.sent[r.Host], one)
		open := rec.open
		rec.mu.Unlock()
		if one.credential == "" && !open {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"data":[{"id":"m-1"}]}`))
	}))
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	previous := httpDiscoveryClient.Transport
	everywhere := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // the recording server's own certificate, on loopback
	}
	httpDiscoveryClient.Transport = everywhere
	t.Cleanup(func() {
		httpDiscoveryClient.Transport = previous
		everywhere.CloseIdleConnections()
	})
	return rec
}

// AN ORIGIN IS A SCHEME, A HOST AND A PORT: what a key is for. A path is
// no part of it, and the port a scheme implies is the same as none.
func TestAnAddressesOriginIsItsSchemeHostAndPort(t *testing.T) {
	for raw, want := range map[string]string{
		"https://api.openai.com/v1":      "https://api.openai.com",
		"https://API.OpenAI.com:443/v2/": "https://api.openai.com",
		"http://127.0.0.1:8080/v1":       "http://127.0.0.1:8080",
		"http://localhost:80":            "http://localhost",
		"https://localhost:80":           "https://localhost:80",
		"https://[::1]:8443/v1":          "https://[::1]:8443",
		"//api.openai.com/v1":            "",
		"ftp://api.openai.com":           "",
		"https://[::1]/v1":               "https://[::1]",
		"not an address":                 "",
		"":                               "",
	} {
		if got := addressOrigin(raw); got != want {
			t.Errorf("the origin of %q is %q, want %q", raw, got, want)
		}
	}
	for name, c := range map[string]struct {
		a, b string
		want bool
	}{
		"another path":            {"https://api.openai.com/v1", "https://api.openai.com/v2", true},
		"the implied port":        {"https://api.openai.com/v1", "https://api.openai.com:443/v1", true},
		"another scheme":          {"https://api.openai.com/v1", "http://api.openai.com/v1", false},
		"another port":            {"http://127.0.0.1:8081/v1", "http://127.0.0.1:9091/v1", false},
		"another host":            {"https://api.openai.com/v1", "https://relay.example/v1", false},
		"nothing is not the same": {"", "", false},
	} {
		if got := sameOrigin(c.a, c.b); got != c.want {
			t.Errorf("%s: sameOrigin(%q, %q) = %v, want %v", name, c.a, c.b, got, c.want)
		}
	}
}

func TestAVendorsKeyVariableGoesOnlyToTheVendorsOwnAddress(t *testing.T) {
	const key = "sk-openai-the-operators-real-key"
	t.Setenv("OPENAI_API_KEY", key)
	rec := recordEveryHost(t)
	shipped := shippedEntry(t, "OpenAI")
	if shipped.VendorKeyEnv != "OPENAI_API_KEY" || addressOrigin(shipped.URL) != "https://api.openai.com" {
		t.Fatalf("the shipped OpenAI entry is not what this test stands on: %q at %q", shipped.VendorKeyEnv, shipped.URL)
	}
	const home, away = "api.openai.com", "relay.example"
	dir := t.TempDir()
	// start writes the file, starts on it as a boot does, and returns the
	// key a turn would carry and what the status probe of the entry found.
	start := func(e providerEntry) (string, providerProbe) {
		t.Helper()
		e.Name, e.APIType, e.DefaultModel, e.Default = "OpenAI", "openai", "m-1", true
		writeTestProviders(t, dir, e)
		a := resolverApp(dir, LLMConfig{Provider: "OpenAI", APIKeyEnv: "OPENAI_API_KEY"})
		t.Cleanup(a.bgCancel)
		reg, err := a.loadProviders()
		if err != nil {
			t.Fatal(err)
		}
		cc, _, err := a.resolveLLM()
		if err != nil {
			t.Fatal(err)
		}
		return cc.APIKey, a.probeOne(reg.Providers[0])
	}
	carriedTo := func(host string) (asks int, carried bool) {
		for _, r := range rec.to(host) {
			asks++
			carried = carried || r.credential != "" || strings.Contains(r.everything, key)
		}
		return asks, carried
	}

	turn, st := start(providerEntry{URL: shipped.URL})
	if asks, carried := carriedTo(home); turn != key || st.state != probeOK || asks == 0 || !carried {
		t.Fatalf("at the vendor's own address the key was not sent: turn key %q, probe %q, %d asks, carried %v", turn, st.state, asks, carried)
	}

	for _, when := range []string{"after the address was edited in the file", "after a restart on that file"} {
		rec.forget()
		turn, st = start(providerEntry{URL: "https://" + away + "/v1"})
		asks, carried := carriedTo(away)
		if asks == 0 {
			t.Fatalf("%s the other address was never asked: the test shows nothing", when)
		}
		if carried || turn != "" {
			t.Fatalf("%s THE VENDOR'S KEY WENT TO ANOTHER ADDRESS: the probe carried it = %v, a turn would carry %q", when, carried, turn)
		}
		if st.state != probeAuthRequired {
			t.Fatalf("%s the row reads %q, want %q", when, st.state, probeAuthRequired)
		}
		for _, word := range []string{"OPENAI_API_KEY", "https://" + home, "https://" + away, "API KEY", "api_key_env"} {
			if !strings.Contains(st.reason, word) {
				t.Fatalf("%s the row does not say %q: %q", when, word, st.reason)
			}
		}
	}

	rec.forget()
	turn, st = start(providerEntry{URL: "https://" + away + "/v1", APIKeyEnv: "OPENAI_API_KEY"})
	if asks, carried := carriedTo(away); turn != key || st.state != probeOK || asks == 0 || !carried {
		t.Fatalf("the operator named the variable on the entry and it was not sent there: turn key %q, probe %q, %d asks, carried %v", turn, st.state, asks, carried)
	}

	rec.forget()
	turn, st = start(providerEntry{URL: "https://" + home + "/v2"})
	if asks, carried := carriedTo(home); turn != key || st.state != probeOK || asks == 0 || !carried {
		t.Fatalf("back at the vendor's address, by another path, the key was not sent: turn key %q, probe %q, %d asks, carried %v", turn, st.state, asks, carried)
	}
	if asks, _ := carriedTo(away); asks != 0 {
		t.Fatalf("the address the entry left was asked %d more times", asks)
	}
}

func liveAppOn(t *testing.T, providersJSON string, block LLMConfig) *App {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(providersJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.LLM = block
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	normalized, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	a := New(normalized)
	t.Cleanup(a.bgCancel)
	a.live.Store(true)
	a.composer = prompt.New(ring.NewManager(), 32000)
	cc, e, err := a.resolveLLM()
	if err != nil {
		t.Fatal(err)
	}
	a.llmSwap = newSwappableLLM(a.newLLMClient(cc, 32000))
	a.adoptProvider(e)
	return a
}

// recordingProvider is a provider that lists one model and completes the
// substrate check's two requests, and records the credential each request
// it received carried ("" for none). The client writes its bearer header
// whether or not it has a token (llm/client.go), so the scheme with nothing
// after it is a request that carried none.
func recordingProvider(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		carried := r.Header.Get("Authorization") + r.Header.Get("x-api-key")
		if carried == "Bearer" {
			carried = ""
		}
		mu.Lock()
		sent = append(sent, carried)
		mu.Unlock()
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "gpt-5.2"}}})
			return
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		msg := map[string]any{"role": "assistant", "content": "OK"}
		reason := "stop"
		if raw, ok := req["tools"].([]any); ok && len(raw) > 0 {
			reason = "tool_calls"
			msg["tool_calls"] = []any{map[string]any{"id": "fixture-call", "type": "function", "function": map[string]any{"name": "report_ready", "arguments": `{"ready":true}`}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": msg, "finish_reason": reason}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12, "prompt_tokens_details": map[string]any{"cached_tokens": 0}}})
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(sent)
	}
}

// A STORED KEY IS NOT TAKEN TO ANOTHER ADDRESS BY A SAVE THAT DOES NOT ENTER
// IT (rows 4.4 and 8.1). The page never holds the key; a save with a blank
// one kept it, whatever the save did to the address, and the check that
// goes before a change of the running provider then sent it to the host
// that was typed.
//
// Through time, on a running identity, with two servers that record what
// they are sent: the address edited and saved, nothing is saved and the
// second server hears nothing; address and key entered together, the second
// hears that key and no other; the address put back with a blank key, a
// move back is a move and the first hears nothing; a path inside the same
// origin keeps the key; the key cleared, the entry moves with none.
func TestACardSaveDoesNotTakeAStoredKeyToAnotherAddress(t *testing.T) {
	first, sentFirst := recordingProvider(t)
	second, sentSecond := recordingProvider(t)
	a := liveAppOn(t, `{"providers":[{"name":"Mine","api_type":"openai","url":"`+first.URL+`","api_key":"k-first",`+
		`"default_model":"gpt-5.2","default":true,"context_length":64000,"max_output_tokens":512}]}`,
		LLMConfig{Provider: "Mine", Model: "gpt-5.2", TimeoutSeconds: 3, Retries: -1})
	save := func(address, key string, clear bool) error {
		return a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
			Name: "Mine", APIType: "openai", Endpoint: address, DefaultModel: "gpt-5.2", Default: true,
			ContextLength: 64000, MaxOutputTokens: 512}, APIKey: key, ClearKey: clear})
	}
	stored := func() providerEntry {
		t.Helper()
		reg, err := a.loadProviders()
		if err != nil || len(reg.Providers) != 1 {
			t.Fatalf("the file did not reopen: %+v %v", reg, err)
		}
		return reg.Providers[0]
	}
	var refused *keyAddressError

	err := save(second.URL, "", false)
	if !errors.As(err, &refused) {
		t.Fatalf("a save that moved the entry and entered no key was not refused: %v", err)
	}
	for _, word := range []string{addressOrigin(first.URL), addressOrigin(second.URL), "Enter the key again", "Nothing was saved"} {
		if !strings.Contains(err.Error(), word) {
			t.Fatalf("the refusal does not say %q: %v", word, err)
		}
	}
	if e := stored(); e.URL != first.URL || e.APIKey != "k-first" {
		t.Fatalf("the refused save changed the file: %q with key %q", e.URL, e.APIKey)
	}
	if got := sentSecond(); len(got) != 0 {
		t.Fatalf("THE ADDRESS THAT WAS TYPED WAS ASKED, on a save that was refused: it was sent %q", got)
	}

	if err := save(second.URL, "k-second", false); err != nil {
		t.Fatalf("address and key entered together: %v", err)
	}
	got := sentSecond()
	if len(got) == 0 {
		t.Fatal("the new address was never checked: the test shows nothing")
	}
	for _, carried := range got {
		if carried != "Bearer k-second" {
			t.Fatalf("the new address was sent %q; the key entered for it is the only one it may hear", carried)
		}
	}
	if e := stored(); e.URL != second.URL || e.APIKey != "k-second" {
		t.Fatalf("the save did not land: %q with key %q", e.URL, e.APIKey)
	}

	if err := save(first.URL, "", false); !errors.As(err, &refused) {
		t.Fatalf("the address put back with a blank key was not refused: %v", err)
	}
	if got := sentFirst(); len(got) != 0 {
		t.Fatalf("THE FIRST ADDRESS WAS SENT THE SECOND'S KEY, or asked at all, on a save that was refused: %q", got)
	}

	if err := save(second.URL+"/v1", "", false); err != nil {
		t.Fatalf("another path at the same origin: %v", err)
	}
	if e := stored(); e.URL != second.URL+"/v1" || e.APIKey != "k-second" {
		t.Fatalf("a path inside one origin is no move, and the key was not kept: %q with key %q", e.URL, e.APIKey)
	}

	if err := save(first.URL, "", true); err != nil {
		t.Fatalf("the address put back with the key cleared: %v", err)
	}
	got = sentFirst()
	if len(got) == 0 {
		t.Fatal("the first address was never checked: the test shows nothing")
	}
	for _, carried := range got {
		if carried != "" {
			t.Fatalf("the entry moved with its key cleared and the address was sent %q", carried)
		}
	}
	if e := stored(); e.URL != first.URL || e.APIKey != "" {
		t.Fatalf("the save did not land: %q with key %q", e.URL, e.APIKey)
	}
}

// A SAVE THAT MOVES A SHIPPED VENDOR'S ENTRY CHECKS THE NEW ADDRESS WITH NO
// KEY (row 8.1). The candidate a card save builds is the one that is
// checked and activated, and it carries the vendor's variable again, so
// that a save that changes nothing is no change of runtime. Carried by name
// alone it would go with the entry to whatever address the same save
// typed: here the exported OpenAI key, to a server that records what it is
// sent, on the check and on every turn after it.
func TestACardSaveThatMovesAShippedVendorsEntryChecksTheNewAddressWithNoKey(t *testing.T) {
	const key = "sk-openai-the-operators-real-key"
	t.Setenv("OPENAI_API_KEY", key)
	other, sentOther := recordingProvider(t)
	// No model catalogue: the check asks nothing but the address under test.
	a := liveAppOn(t, `{"model_catalogue_url":"","providers":[{"name":"OpenAI","api_type":"openai","url":"`+shippedEntry(t, "OpenAI").URL+`",`+
		`"default_model":"gpt-5.2","default":true,"context_length":64000,"max_output_tokens":512}]}`,
		LLMConfig{Provider: "OpenAI", Model: "gpt-5.2", TimeoutSeconds: 3, Retries: -1})
	if got := a.currentProvider().VendorKeyEnv; got != "OPENAI_API_KEY" {
		t.Fatalf("the running entry, at the vendor's address, is not lent the vendor's variable: %q", got)
	}
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: "OpenAI", APIType: "openai", Endpoint: other.URL, DefaultModel: "gpt-5.2", Default: true,
		ContextLength: 64000, MaxOutputTokens: 512}}); err != nil {
		t.Fatalf("the move, with no key of the entry's own to take along, was refused: %v", err)
	}
	checks := len(sentOther())
	if checks == 0 {
		t.Fatal("the new address was never checked: the test shows nothing")
	}
	if _, err := a.llmSwap.Chat(t.Context(), []llm.Message{{Role: "user", Content: "after the move"}}, llm.ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	got := sentOther()
	if len(got) <= checks {
		t.Fatal("the running client did not move to the new address: the turn after the save was not heard there")
	}
	for _, carried := range got {
		if carried != "" {
			t.Fatalf("THE VENDOR'S KEY WENT TO THE ADDRESS THAT WAS TYPED: it was sent %q", carried)
		}
	}
	reg, err := a.loadProviders()
	if err != nil || len(reg.Providers) != 1 || reg.Providers[0].URL != other.URL || reg.Providers[0].VendorKeyEnv != "" {
		t.Fatalf("the moved entry reopened as %+v (%v): at another address it is lent no variable", reg, err)
	}
}

// A SAVE FROM THE CARD MOVES AN ENTRY ONLY WITH WHAT THAT SAVE ENTERED. Each
// row is a stored entry, the entry a save would put in its place, and the
// word its refusal must carry ("" when the save goes ahead).
//
// The rows ask the card's whole door (cardKeyRefusal), so they also hold
// what a save may attach by name where the address does not change.
func TestACardSaveMovesAnEntryOnlyWithWhatThatSaveEntered(t *testing.T) {
	const one, two = "https://one.example/v1", "https://two.example/v1"
	own := &Config{LLM: LLMConfig{Provider: "Mine", APIKeyEnv: "AII_TEST_OWN_KEY"}}
	own.Speech.TTS.Provider, own.Speech.TTS.APIKeyEnv = "Voice", "AII_TEST_SPEECH_KEY"
	shippedBlock := &Config{LLM: LLMConfig{Provider: "Mine", APIKeyEnv: "OPENAI_API_KEY"}}
	for name, c := range map[string]struct {
		cfg          *Config
		stored, next providerEntry
		keepsKey     bool
		want         string
	}{
		"another path at the same origin, everything kept": {own,
			providerEntry{Name: "Mine", URL: one, APIKey: "k", APIKeyEnv: "MINE_KEY"},
			providerEntry{Name: "Mine", URL: "https://one.example/v2", APIKeyEnv: "MINE_KEY"}, true, ""},
		"the stored key kept": {nil,
			providerEntry{Name: "Mine", URL: one, APIKey: "k"},
			providerEntry{Name: "Mine", URL: two}, true, "the stored key was entered for the first"},
		"a key entered in this save": {nil,
			providerEntry{Name: "Mine", URL: one, APIKey: "k"},
			providerEntry{Name: "Mine", URL: two, APIKey: "new"}, false, ""},
		"the stored key cleared": {nil,
			providerEntry{Name: "Mine", URL: one, APIKey: "k"},
			providerEntry{Name: "Mine", URL: two}, false, ""},
		"the entry's own variable, unchanged": {nil,
			providerEntry{Name: "Mine", URL: one, APIKeyEnv: "MINE_KEY"},
			providerEntry{Name: "Mine", URL: two, APIKeyEnv: "MINE_KEY"}, false, "MINE_KEY, which providers.json names for it (api_key_env)"},
		"the entry's own variable, unchanged, beside a key entered now": {nil,
			providerEntry{Name: "Mine", URL: one, APIKeyEnv: "MINE_KEY"},
			providerEntry{Name: "Mine", URL: two, APIKey: "new", APIKeyEnv: "MINE_KEY"}, false, "MINE_KEY"},
		"the operator's own vendor variable": {nil,
			providerEntry{Name: "Mine", URL: one, VendorKeyEnv: "MINE_KEY"},
			providerEntry{Name: "Mine", URL: two, VendorKeyEnv: "MINE_KEY"}, false, "MINE_KEY, which providers.json names for it (vendor_key_env)"},
		"a variable named in this save": {nil,
			providerEntry{Name: "Mine", URL: one, APIKeyEnv: "MINE_KEY"},
			providerEntry{Name: "Mine", URL: two, APIKeyEnv: "OTHER_KEY"}, false, "names the variable OTHER_KEY"},
		"a variable named in this save, the address unchanged": {nil,
			providerEntry{Name: "Mine", URL: one},
			providerEntry{Name: "Mine", URL: one, APIKeyEnv: "OTHER_KEY"}, false, "names the variable OTHER_KEY"},
		"a variable named in this save, beside a key entered now": {nil,
			providerEntry{Name: "Mine", URL: one},
			providerEntry{Name: "Mine", URL: one, APIKey: "new", APIKeyEnv: "OTHER_KEY"}, false, "names the variable OTHER_KEY"},
		"the entry's own variable, cleared by the save": {nil,
			providerEntry{Name: "Mine", URL: one, APIKeyEnv: "MINE_KEY"},
			providerEntry{Name: "Mine", URL: two}, false, ""},
		"the llm block's variable, the operator's own": {own,
			providerEntry{Name: "Mine", URL: one},
			providerEntry{Name: "Mine", URL: two}, false, "AII_TEST_OWN_KEY, which config.json names for it (llm.api_key_env)"},
		"the llm block's variable, for another provider": {own,
			providerEntry{Name: "Other", URL: one},
			providerEntry{Name: "Other", URL: two}, false, ""},
		"the llm block's variable, a shipped vendor's own": {shippedBlock,
			providerEntry{Name: "Mine", URL: one},
			providerEntry{Name: "Mine", URL: two}, false, ""},
		"a speech block's variable": {own,
			providerEntry{Name: "Voice", URL: one},
			providerEntry{Name: "Voice", URL: two}, false, "AII_TEST_SPEECH_KEY, which config.json names for it (speech.tts.api_key_env)"},
		"a credential, away from its own address": {nil,
			providerEntry{Name: "Sub", URL: "https://api.anthropic.com", Credential: "claude-code"},
			providerEntry{Name: "Sub", URL: two, Credential: "claude-code"}, false, "the claude-code credential"},
		"a credential, to its own address": {nil,
			providerEntry{Name: "Mine", URL: one, APIKey: "k"},
			providerEntry{Name: "Mine", URL: "https://api.anthropic.com", Credential: "claude-code"}, false, ""},
		"a credential file of the operator's own": {nil,
			providerEntry{Name: "Sub", URL: one, Credential: "file:/token.json"},
			providerEntry{Name: "Sub", URL: two, Credential: "file:/token.json"}, false, "the file:/token.json credential"},
		"a credential attached by this save, the address unchanged": {nil,
			providerEntry{Name: "Mine", URL: one},
			providerEntry{Name: "Mine", URL: one, Credential: "claude-code"}, false, "attaches the claude-code credential"},
		"a credential attached by this save where it is for, the address unchanged": {nil,
			providerEntry{Name: "Mine", URL: "https://api.anthropic.com"},
			providerEntry{Name: "Mine", URL: "https://api.anthropic.com/v1", Credential: "claude-code"}, false, ""},
		"another credential in place of the entry's": {nil,
			providerEntry{Name: "Sub", URL: one, Credential: "codex"},
			providerEntry{Name: "Sub", URL: one, Credential: "claude-code"}, false, "attaches the claude-code credential"},
		"a credential file attached by this save": {nil,
			providerEntry{Name: "Mine", URL: one},
			providerEntry{Name: "Mine", URL: one, Credential: "file:/token.json"}, false, "ships none for this one"},
		"the entry's own credential, where the file put it": {nil,
			providerEntry{Name: "Sub", URL: one, Credential: "claude-code"},
			providerEntry{Name: "Sub", URL: "https://one.example/v2", Credential: "claude-code"}, false, ""},
	} {
		err := cardKeyRefusal(c.cfg, &c.stored, c.next, c.keepsKey)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: refused: %v", name, err)
			}
			continue
		}
		var refused *keyAddressError
		if !errors.As(err, &refused) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want a refusal that says %q, got %v", name, c.want, err)
		}
	}
}

// probeEveryEntry asks each entry the file now holds, as a read of the
// provider directory does.
func probeEveryEntry(t *testing.T, a *App) {
	t.Helper()
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range reg.Providers {
		a.probeOne(e)
	}
}

// A SAVE FROM THE DASHBOARD NAMES NO KEY VARIABLE THE FILE DOES NOT NAME
// (row 4.4: a secret is bound to its origin). The page has no field for
// the variable and only sends back the one the file names; the wire took
// any name from any client the dashboard admits. One message that added an
// entry at an address of the sender's choosing and named a variable had the
// value of that variable of the host process sent there on the next status
// probe; so did naming one on an entry of the file, where it is or moved
// by the same save.
//
// A recording server stands at every address a probe is sent to. Each of
// the three is refused, nothing is saved, and the address hears nothing of
// the variable. The variable providers.json itself names for an entry is
// kept by a save that sends it back, goes where the file put it, and is
// cleared by a save that sends none.
func TestACardSaveNamesNoKeyVariableTheFileDoesNotName(t *testing.T) {
	const variable, secret = "AII_TEST_HOST_SECRET", "v-a-secret-of-the-host-process"
	t.Setenv(variable, secret)
	rec := recordEveryHost(t)
	dir := t.TempDir()
	writeTestProviders(t, dir,
		providerEntry{Name: "Mine", APIType: "openai", URL: "https://mine.example/v1", DefaultModel: "m-1", Default: true},
		providerEntry{Name: "Named", APIType: "openai", URL: "https://named.example/v1", APIKeyEnv: variable, DefaultModel: "m-1"})
	a := resolverApp(dir, LLMConfig{Provider: "Mine"})
	t.Cleanup(a.bgCancel)
	save := func(name, address, env string) error {
		return a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
			Name: name, APIType: "openai", Endpoint: address, APIKeyEnv: env, DefaultModel: "m-1", Default: name == "Mine"}})
	}
	var refused *keyAddressError
	for name, c := range map[string]struct{ entry, address, host string }{
		"a new entry at an address the sender chose":   {"Collector", "https://collector.example/v1", "collector.example"},
		"an entry of the file, where it is":            {"Mine", "https://mine.example/v1", "mine.example"},
		"an entry of the file, moved by the same save": {"Mine", "https://moved.example/v1", "moved.example"},
	} {
		rec.forget()
		err := save(c.entry, c.address, variable)
		probeEveryEntry(t, a)
		if asks, carried := rec.heardBy(c.host, secret); carried {
			t.Fatalf("%s: A VARIABLE OF THE HOST WAS SENT TO %s, named by a save from the dashboard (%d requests)", name, c.host, asks)
		}
		if !errors.As(err, &refused) {
			t.Fatalf("%s: the save that named the variable was not refused: %v", name, err)
		}
		for _, word := range []string{variable, "api_key_env", "providers.json", "Nothing was saved"} {
			if !strings.Contains(err.Error(), word) {
				t.Fatalf("%s: the refusal does not say %q: %v", name, word, err)
			}
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("%s: the refusal carries what the variable holds", name)
		}
	}
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if mine := entryNamed(reg, "Mine"); len(reg.Providers) != 2 || mine == nil || mine.URL != "https://mine.example/v1" || mine.APIKeyEnv != "" {
		t.Fatalf("a refused save changed the file: %d entries, Mine is %+v", len(reg.Providers), mine)
	}

	// The file's own naming stands: sent back, it is kept and goes where
	// the file put it; with none sent, it is cleared.
	rec.forget()
	if err := save("Named", "https://named.example/v1", variable); err != nil {
		t.Fatalf("a save that sent back the variable the file names: %v", err)
	}
	probeEveryEntry(t, a)
	if asks, carried := rec.heardBy("named.example", secret); asks == 0 || !carried {
		t.Fatalf("the variable providers.json names for the entry was not sent to the entry's address: %d requests, carried %v", asks, carried)
	}
	if err := save("Named", "https://named.example/v1", ""); err != nil {
		t.Fatalf("a save that sent no variable: %v", err)
	}
	if reg, err = a.loadProviders(); err != nil || entryNamed(reg, "Named") == nil || entryNamed(reg, "Named").APIKeyEnv != "" {
		t.Fatalf("a save that sent no variable did not clear the one the file named: %+v %v", reg, err)
	}
}

// THE SAME ON A RUNNING IDENTITY, IN ONE MESSAGE (rows 4.4 and 8.1): a save
// that moves a shipped vendor's entry and names that vendor's variable. The
// vendor's variable is not lent at the new address, and the name on the
// wire put it back: the check that goes before a change of the running
// provider carried the exported key to the address that was typed.
func TestACardSaveThatMovesAnEntryAndNamesAVariableIsRefusedBeforeAnythingIsAsked(t *testing.T) {
	const key = "sk-openai-the-operators-real-key"
	t.Setenv("OPENAI_API_KEY", key)
	other, sentOther := recordingProvider(t)
	home := shippedEntry(t, "OpenAI").URL
	a := liveAppOn(t, `{"model_catalogue_url":"","providers":[{"name":"OpenAI","api_type":"openai","url":"`+home+`",`+
		`"default_model":"gpt-5.2","default":true,"context_length":64000,"max_output_tokens":512}]}`,
		LLMConfig{Provider: "OpenAI", Model: "gpt-5.2", TimeoutSeconds: 3, Retries: -1})
	err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
		Name: "OpenAI", APIType: "openai", Endpoint: other.URL, APIKeyEnv: "OPENAI_API_KEY", DefaultModel: "gpt-5.2", Default: true,
		ContextLength: 64000, MaxOutputTokens: 512}})
	if got := sentOther(); len(got) != 0 {
		carried := false
		for _, c := range got {
			carried = carried || c != ""
		}
		t.Fatalf("THE ADDRESS THAT WAS TYPED WAS ASKED (%d requests, a key carried = %v) on a save that named a variable for it", len(got), carried)
	}
	var refused *keyAddressError
	if !errors.As(err, &refused) {
		t.Fatalf("the save that moved the entry and named the vendor's variable was not refused: %v", err)
	}
	reg, lerr := a.loadProviders()
	if lerr != nil || len(reg.Providers) != 1 || reg.Providers[0].URL != home || reg.Providers[0].APIKeyEnv != "" {
		t.Fatalf("the refused save changed the file: %+v (%v)", reg, lerr)
	}
}

// A SAVE FROM THE DASHBOARD ATTACHES A CREDENTIAL ONLY WHERE THAT CREDENTIAL
// IS FOR (row 4.4: a lent or borrowed credential is used only at the
// vendor's own address). Only a moved entry was refused, which held
// nothing: an entry ADDED with a credential was saved at any address, and
// an entry of the file was moved in two saves, the first with no
// credential and the second with it again at the address that had by then
// not changed. The next read of the directory then sent the token there.
//
// A recording server stands at every address a probe is sent to, and the
// credential is a token file, which the shipped registry gives no address.
// Where providers.json itself pairs the credential with an address it is
// sent, as before. Added from the dashboard at another, it is refused and
// that address hears nothing; moved with it, refused; moved without it,
// saved, and asked with no token; attached again where the entry now is,
// refused, and that address still hears no token.
func TestACardSaveAttachesACredentialOnlyAtItsOwnAddress(t *testing.T) {
	const token = "tok-the-operators-own-subscription"
	rec := recordEveryHost(t)
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token.json")
	if err := os.WriteFile(tokenFile, []byte(`{"access_token":"`+token+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	credential := "file:" + tokenFile
	writeTestProviders(t, dir,
		providerEntry{Name: "Mine", APIType: "openai", URL: "https://mine.example/v1", DefaultModel: "m-1", Default: true},
		providerEntry{Name: "Sub", APIType: "openai", URL: "https://sub.example/v1", Credential: credential, DefaultModel: "m-1"})
	a := resolverApp(dir, LLMConfig{Provider: "Mine"})
	t.Cleanup(a.bgCancel)
	save := func(name, address, credential string) error {
		return a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
			Name: name, APIType: "openai", Endpoint: address, Credential: credential, DefaultModel: "m-1", Default: name == "Mine"}})
	}
	// heard asks every entry the file holds and says what one host was sent.
	heard := func(host string) (int, bool) {
		t.Helper()
		rec.forget()
		probeEveryEntry(t, a)
		return rec.heardBy(host, token)
	}
	var refused *keyAddressError

	if asks, carried := heard("sub.example"); asks == 0 || !carried {
		t.Fatalf("the credential providers.json pairs with an address was not sent there: %d requests, carried %v", asks, carried)
	}

	const collector = "https://collector.example/v1"
	err := save("Relay", collector, credential)
	if asks, carried := heard("collector.example"); asks != 0 || carried {
		t.Fatalf("A CREDENTIAL WENT TO AN ADDRESS A SAVE FROM THE DASHBOARD CHOSE, or the address was asked at all: %d requests, carried %v", asks, carried)
	}
	if !errors.As(err, &refused) {
		t.Fatalf("an entry added with a credential, at an address that is not the credential's, was not refused: %v", err)
	}
	for _, word := range []string{"attaches", "https://collector.example", "providers.json", "Nothing was saved"} {
		if !strings.Contains(err.Error(), word) {
			t.Fatalf("the refusal does not say %q: %v", word, err)
		}
	}

	if err := save("Sub", collector, credential); !errors.As(err, &refused) {
		t.Fatalf("the entry moved with its credential was not refused: %v", err)
	}
	if err := save("Sub", collector, ""); err != nil {
		t.Fatalf("the entry moved with no credential: %v", err)
	}
	if asks, carried := heard("collector.example"); asks == 0 || carried {
		t.Fatalf("the moved entry, saved with no credential, was asked %d times and carried a credential = %v", asks, carried)
	}
	err = save("Sub", collector, credential)
	if asks, carried := heard("collector.example"); carried {
		t.Fatalf("THE CREDENTIAL FOLLOWED THE ENTRY IN TWO SAVES: attached again where the entry had been moved, it was sent there (%d requests)", asks)
	}
	if !errors.As(err, &refused) {
		t.Fatalf("the credential attached again, at the address the entry was moved to, was not refused: %v", err)
	}
	reg, lerr := a.loadProviders()
	if lerr != nil || entryNamed(reg, "Relay") != nil || entryNamed(reg, "Sub") == nil || entryNamed(reg, "Sub").Credential != "" {
		t.Fatalf("a refused save changed the file: %+v (%v)", reg, lerr)
	}
}

// AN ADDRESS TYPED BESIDE AN ENTRY IS ASKED WITH ONLY WHAT WAS TYPED WITH IT
// (row 4.4). Discovery takes an address for a local entry from any dashboard
// connection, before birth and after, and asked the STORED entry there: its
// stored key, the value of the variable it names, the value of the variable
// a settings block names for it, or its credential went to whatever host
// the query named, with nothing saved.
//
// A recording server stands at every address. For an entry with each of
// the four, a query at another origin with no key reaches that origin and
// carries nothing; with a key typed, it carries that key and no other. At
// the entry's own origin, by another path, the entry is asked as the file
// holds it. An entry that is not local is still not asked at a typed
// address at all.
func TestDiscoveryAtATypedAddressCarriesOnlyWhatWasTyped(t *testing.T) {
	const stored, named, block, token = "k-stored-on-the-entry", "v-the-entrys-variable", "v-the-blocks-variable", "tok-the-entrys-credential"
	t.Setenv("AII_TEST_ENTRY_KEY", named)
	t.Setenv("AII_TEST_OWN_KEY", block)
	rec := recordEveryHost(t)
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token.json")
	if err := os.WriteFile(tokenFile, []byte(`{"access_token":"`+token+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestProviders(t, dir,
		providerEntry{Name: "Keyed", APIType: "openai", URL: "https://keyed.example/v1", APIKey: stored, Local: true, DefaultModel: "m-1"},
		providerEntry{Name: "Named", APIType: "openai", URL: "https://named.example/v1", APIKeyEnv: "AII_TEST_ENTRY_KEY", Local: true, DefaultModel: "m-1"},
		providerEntry{Name: "Chosen", APIType: "openai", URL: "https://chosen.example/v1", Local: true, DefaultModel: "m-1", Default: true},
		providerEntry{Name: "Sub", APIType: "openai", URL: "https://sub.example/v1", Credential: "file:" + tokenFile, Local: true, DefaultModel: "m-1"},
		providerEntry{Name: "Hosted", APIType: "openai", URL: "https://hosted.example/v1", APIKey: stored, DefaultModel: "m-1"})
	a := resolverApp(dir, LLMConfig{Provider: "Chosen", APIKeyEnv: "AII_TEST_OWN_KEY"})
	t.Cleanup(a.bgCancel)
	const typed, elsewhere = "https://collector.example/v1", "collector.example"

	for name, own := range map[string]struct{ host, secret string }{
		"Keyed":  {"keyed.example", stored},
		"Named":  {"named.example", named},
		"Chosen": {"chosen.example", block},
		"Sub":    {"sub.example", token},
	} {
		// The entry has what this test says it has: at its own address the
		// file's pairing is sent.
		rec.forget()
		probeEveryEntry(t, a)
		if asks, carried := rec.heardBy(own.host, own.secret); asks == 0 || !carried {
			t.Fatalf("%s: setup: at its own address the entry was asked %d times and carried its key = %v", name, asks, carried)
		}

		rec.forget()
		_, needsKey, err := a.discoverModels(name, typed, "")
		asks, carried := rec.heardBy(elsewhere, own.secret)
		if asks == 0 {
			t.Fatalf("%s: the typed address was never asked (%v): the test shows nothing", name, err)
		}
		if carried {
			t.Fatalf("%s: THE ENTRY'S OWN KEY WENT TO AN ADDRESS TYPED BESIDE IT, in %d requests", name, asks)
		}
		if !needsKey {
			t.Fatalf("%s: asked with no key and told one is needed, the page was not told to ask for it: %v", name, err)
		}

		rec.forget()
		if _, _, err := a.discoverModels(name, typed, "k-typed-with-the-address"); err != nil {
			t.Fatalf("%s: asked at the typed address with a typed key: %v", name, err)
		}
		heard := rec.to(elsewhere)
		if len(heard) == 0 {
			t.Fatalf("%s: the typed address was never asked with the typed key", name)
		}
		for _, one := range heard {
			if one.credential != "Bearer k-typed-with-the-address" || strings.Contains(one.everything, own.secret) {
				t.Fatalf("%s: the typed address was sent something other than the key typed with it", name)
			}
		}
	}

	// Another path at the entry's own origin is the entry: asked as the
	// file holds it.
	rec.forget()
	if _, _, err := a.discoverModels("Keyed", "https://keyed.example/v2", ""); err != nil {
		t.Fatalf("the entry's own origin, by another path: %v", err)
	}
	if asks, carried := rec.heardBy("keyed.example", stored); asks == 0 || !carried {
		t.Fatalf("at its own origin the entry was asked %d times and carried its stored key = %v", asks, carried)
	}

	rec.forget()
	if _, _, err := a.discoverModels("Hosted", typed, ""); err == nil {
		t.Fatal("an entry that is not local was asked at a typed address")
	}
	if asks, _ := rec.heardBy(elsewhere, stored); asks != 0 {
		t.Fatalf("an entry that is not local had %d requests sent to the typed address", asks)
	}
}

// A VENDOR'S VARIABLE STAYS AT THE VENDOR'S ADDRESS ON EVERY COPY OF ITS
// ENTRY (row 4.4). The rule was held where the variable is LENT, when the
// file is loaded, and not where it is READ. An entry is asked through
// copies whose address is put in afterwards, and a copy keeps what it was
// lent: a birth at an address typed for a named entry carried the exported
// key to that address on the model list and on the founding request, and a
// speech request to a base an operator's own mapping names carried it
// there.
func TestAVendorsVariableStaysAtTheVendorsAddressOnEveryCopyOfItsEntry(t *testing.T) {
	const key = "sk-openai-the-operators-real-key"
	t.Setenv("OPENAI_API_KEY", key)
	rec := recordEveryHost(t)
	shipped := shippedEntry(t, "OpenAI")
	const home, away = "api.openai.com", "relay.example"

	// The shipped registry itself: a speech base it ships is at its
	// entry's own origin, so no vendor's variable is withheld from the
	// vendor's own speech address.
	for _, e := range embeddedRegistry().Providers {
		for _, dir := range []speech.Direction{speech.STT, speech.TTS} {
			if svc := e.Speech.service(dir); svc != nil && svc.Base != "" && !sameOrigin(svc.Base, e.URL) {
				t.Errorf("the shipped %s entry is at %s and its %s base at %s: its own key variable would not be sent to its own speech address (speechKey)",
					e.Name, addressOrigin(e.URL), dir, addressOrigin(svc.Base))
			}
		}
	}

	// Birth, with the address the entry has and then with another.
	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{Name: "OpenAI", APIType: "openai", URL: shipped.URL, DefaultModel: "m-1", Default: true})
	a := resolverApp(dir, LLMConfig{Provider: "OpenAI", APIKeyEnv: "OPENAI_API_KEY"})
	t.Cleanup(a.bgCancel)
	born := func(endpoint string) (founding string) {
		t.Helper()
		e, eff, err := a.birthEntry("OpenAI", endpoint, "m-1")
		if err != nil {
			t.Fatal(err)
		}
		if e.VendorKeyEnv != "OPENAI_API_KEY" || e.URL != endpoint {
			t.Fatalf("the entry a birth asks is not the copy this test stands on: variable %q at %q", e.VendorKeyEnv, e.URL)
		}
		_, _, _ = a.discoverMetaForEntry(context.Background(), e, "")
		cc, err := a.clientConfigForEntry(eff, e, a.configSnapshot().LLM, "m-1", "")
		if err != nil {
			t.Fatal(err)
		}
		return cc.APIKey
	}
	founding := born(shipped.URL)
	if asks, carried := rec.heardBy(home, key); founding != key || asks == 0 || !carried {
		t.Fatalf("a birth at the vendor's own address did not carry the vendor's key: founding request carries it = %v, %d list requests, carried %v", founding == key, asks, carried)
	}
	rec.forget()
	// A birth at another address is refused before anything is asked (row
	// 9.50). The hold where the variable is read still stands for any copy
	// whose address is put in after the load, which is what a birth at a
	// typed address made before that row: such a copy is asked here.
	var refusedBirth *keyAddressError
	if _, _, err := a.birthEntry("OpenAI", "https://"+away+"/v1", "m-1"); !errors.As(err, &refusedBirth) {
		t.Fatalf("a birth that names another address for a provider's own entry was not refused: %v", err)
	}
	if asks, _ := rec.heardBy(away, ""); asks != 0 {
		t.Fatalf("a refused birth asked the typed address: %d requests", asks)
	}
	moved, eff, err := a.birthEntry("OpenAI", shipped.URL, "m-1")
	if err != nil {
		t.Fatal(err)
	}
	moved.URL = "https://" + away + "/v1"
	_, _, _ = a.discoverMetaForEntry(context.Background(), moved, "")
	cc, err := a.clientConfigForEntry(eff, moved, a.configSnapshot().LLM, "m-1", "")
	if err != nil {
		t.Fatal(err)
	}
	founding = cc.APIKey
	asks, carried := rec.heardBy(away, key)
	if asks == 0 {
		t.Fatal("the copy's address was never asked: the test shows nothing")
	}
	if carried || founding != "" {
		t.Fatalf("THE VENDOR'S KEY WENT TO AN ADDRESS PUT ON A COPY OF ITS ENTRY: the model list carried it = %v, the founding request would carry a key = %v", carried, founding != "")
	}

	// Speech, to a base of the operator's own mapping. The speech client is
	// its own, so the base is a server of this test.
	var mu sync.Mutex
	var sent []string
	base := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent = append(sent, r.Header.Get("Authorization")+r.Header.Get("x-api-key"))
		mu.Unlock()
		w.Write([]byte(`{"data":[{"id":"voice-1"}]}`))
	}))
	t.Cleanup(base.Close)
	heard := func() []string {
		mu.Lock()
		defer mu.Unlock()
		got := slices.Clone(sent)
		sent = nil
		return got
	}
	mapped := providerEntry{Name: "OpenAI", APIType: "openai", URL: shipped.URL, DefaultModel: "m-1", Default: true,
		Speech: &speechBlock{TTS: &speechOffer{Service: speech.Service{Base: base.URL + "/v1"}, ListModels: openAICompatibleModels()}}}
	writeTestProviders(t, dir, mapped)
	lists, err := a.speechLists("OpenAI", "tts", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, carried := range heard() {
		if carried != "" {
			t.Fatal("THE VENDOR'S KEY WENT TO A SPEECH BASE THAT IS NOT THE VENDOR'S: the list request carried a credential")
		}
	}
	if !lists.NeedsKey {
		t.Fatalf("with no key for that base the page was not told one is needed: %+v", lists)
	}
	// What the file itself gives the entry goes where the file's mapping
	// points, which also shows the base is heard at all.
	mapped.APIKey = "k-stored-beside-the-mapping"
	writeTestProviders(t, dir, mapped)
	if _, err := a.speechLists("OpenAI", "tts", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := heard(); len(got) == 0 || got[0] != "Bearer k-stored-beside-the-mapping" {
		t.Fatalf("the key stored on the entry was not sent to the base the file maps: %d requests", len(got))
	}
}

func TestTheRowSaysAVendorsVariableWasWithheldWhateverTheAddressAnswered(t *testing.T) {
	const key = "sk-openai-the-operators-real-key"
	t.Setenv("OPENAI_API_KEY", key)
	rec := recordEveryHost(t)
	rec.listToAnyone(true)
	const away = "relay.example"
	dir := t.TempDir()
	probed := func(e providerEntry) (providerProbe, string) {
		t.Helper()
		e.Name, e.APIType, e.URL, e.DefaultModel, e.Default = "OpenAI", "openai", "https://"+away+"/v1", "m-1", true
		writeTestProviders(t, dir, e)
		a := resolverApp(dir, LLMConfig{Provider: "OpenAI", APIKeyEnv: "OPENAI_API_KEY"})
		t.Cleanup(a.bgCancel)
		reg, err := a.loadProviders()
		if err != nil {
			t.Fatal(err)
		}
		rec.forget()
		st := a.probeOne(reg.Providers[0])
		infos := a.providerInfos(reg)
		if len(infos) != 1 {
			t.Fatalf("the directory holds %d entries", len(infos))
		}
		return st, infos[0].Status + ": " + infos[0].StatusReason
	}

	st, sent := probed(providerEntry{})
	asks, carried := rec.heardBy(away, key)
	if asks == 0 || carried {
		t.Fatalf("the relay was asked %d times and carried the vendor's key = %v", asks, carried)
	}
	if st.state != probeOK {
		t.Fatalf("the relay lists to anyone and its row reads %q", st.state)
	}
	for _, word := range []string{"no key was sent", "OPENAI_API_KEY", "https://api.openai.com", "https://" + away, "API KEY", "api_key_env"} {
		if !strings.Contains(st.reason, word) || !strings.Contains(sent, word) {
			t.Fatalf("an \"ok\" row does not say %q: the probe says %q, the page is sent %q", word, st.reason, sent)
		}
	}
	if strings.Contains(st.reason, key) {
		t.Fatal("the row carries what the variable holds")
	}

	if st, _ := probed(providerEntry{APIKey: "k-the-entrys-own"}); st.state != probeOK || st.reason != "" {
		t.Fatalf("an entry with a key of its own reads %q with the reason %q", st.state, st.reason)
	}

	os.Unsetenv("OPENAI_API_KEY")
	if st, _ := probed(providerEntry{}); st.state != probeOK || st.reason != "" {
		t.Fatalf("with the variable not set nothing was withheld, and the row reads %q with the reason %q", st.state, st.reason)
	}
}

func TestAnAdoptedCredentialGoesOnlyToItsShippedAddressOrTheOneTheFileNames(t *testing.T) {
	const token, plain = "tok-the-operators-claude-sign-in", "k-a-plain-key-in-the-codex-store"
	rec := recordEveryHost(t)
	dir := t.TempDir()
	signIn, err := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{
		"accessToken": token, "refreshToken": "ref",
		"expiresAt": time.Now().Add(2 * time.Hour).UnixMilli(),
		"scopes":    []string{"user:profile", "user:inference"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	signInFile, plainFile := filepath.Join(dir, "claude.json"), filepath.Join(dir, "codex.json")
	if err := os.WriteFile(signInFile, signIn, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plainFile, []byte(`{"OPENAI_API_KEY":"`+plain+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// A unique, absent Keychain service: the fixture file is the only
	// sign-in this test can read.
	claude := func(name, address, base string) providerEntry {
		opts := map[string]string{"file": signInFile, "keychain_service": "aii-os-test-absent-" + dir}
		if base != "" {
			opts["base_url"] = base
		}
		return providerEntry{Name: name, APIType: "anthropic", URL: address, Credential: "claude-code", CredentialOptions: opts, DefaultModel: "m-1"}
	}
	home := credentialsHome("claude-code")
	if home == "" {
		t.Fatal("the shipped registry gives the claude-code credential no address")
	}
	writeTestProviders(t, dir,
		claude("Home", home, ""),
		claude("Away", "https://away.example", ""),
		claude("Named", "https://elsewhere.example", "https://named.example"),
		providerEntry{Name: "Keyed", APIType: "openai", URL: "https://keyed.example/v1", Credential: "codex",
			CredentialOptions: map[string]string{"file": plainFile}, DefaultModel: "m-1"})
	a := resolverApp(dir, LLMConfig{Provider: "Away"})
	t.Cleanup(a.bgCancel)
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]providerProbe{}
	for _, e := range reg.Providers {
		rows[e.Name] = a.probeOne(e)
	}
	var refused *keyAddressError

	if asks, carried := rec.heardBy(strings.TrimPrefix(home, "https://"), token); asks == 0 || !carried || rows["Home"].state != probeOK {
		t.Fatalf("the sign-in was not sent to the address shipped for it: %d requests, carried %v, the row reads %q (%s)", asks, carried, rows["Home"].state, rows["Home"].reason)
	}

	if asks, _ := rec.heardBy("away.example", token); asks != 0 {
		t.Fatalf("A SIGN-IN'S ENTRY AT ANOTHER ADDRESS WAS ASKED THERE: %d requests", asks)
	}
	if rows["Away"].state != probeNoCredential {
		t.Fatalf("the row of an entry whose credential was not sent reads %q (%s)", rows["Away"].state, rows["Away"].reason)
	}
	for _, word := range []string{"the claude-code credential was not sent", home, "https://away.example", "credential_options.base_url", "providers.json"} {
		if !strings.Contains(rows["Away"].reason, word) {
			t.Fatalf("the row does not say %q: %s", word, rows["Away"].reason)
		}
	}
	if strings.Contains(rows["Away"].reason, token) {
		t.Fatal("the row carries the sign-in")
	}
	if _, err := a.entryTransport(*entryNamed(reg, "Away"), ""); !errors.As(err, &refused) {
		t.Fatalf("a client for an entry at another address than its credential's was not refused: %v", err)
	}
	if _, _, err := a.resolveLLM(); !errors.As(err, &refused) || !strings.Contains(err.Error(), "credential_options.base_url") {
		t.Fatalf("a start on that entry was not refused with the reason that names the key: %v", err)
	}

	if asks, carried := rec.heardBy("named.example", token); asks == 0 || !carried || rows["Named"].state != probeOK {
		t.Fatalf("the address providers.json names for the credential (credential_options.base_url) was not sent it: %d requests, carried %v, the row reads %q (%s)",
			asks, carried, rows["Named"].state, rows["Named"].reason)
	}
	if asks, _ := rec.heardBy("elsewhere.example", token); asks != 0 {
		t.Fatalf("the entry names an address for its credential and its own url was asked: %d requests", asks)
	}
	if cc, err := a.entryTransport(*entryNamed(reg, "Named"), ""); err != nil || cc.Endpoint != "https://named.example" || cc.Credential == nil {
		t.Fatalf("a client for the entry that names its credential's address: endpoint %q, credential attached = %v, %v", cc.Endpoint, cc.Credential != nil, err)
	}

	if asks, carried := rec.heardBy("keyed.example", plain); asks == 0 || !carried || rows["Keyed"].state != probeOK {
		t.Fatalf("a plain key in a tool's store has no shipped address and goes to its entry's, as before: %d requests, carried %v, the row reads %q (%s)",
			asks, carried, rows["Keyed"].state, rows["Keyed"].reason)
	}

	rec.forget()
	// A birth at a typed address is refused before anything is asked (row
	// 9.50); a copy whose address is put in after the load is still held
	// where the credential is attached.
	if _, _, err := a.birthEntry("Home", "https://typed.example", "m-1"); !errors.As(err, &refused) {
		t.Fatalf("a birth that names another address for an entry with a sign-in was not refused: %v", err)
	}
	born := *entryNamed(reg, "Home")
	born.URL = "https://typed.example"
	if born.Credential != "claude-code" {
		t.Fatalf("the copy is not the one this test stands on: %+v", born.Credential)
	}
	_, _, lerr := a.discoverMetaForEntry(context.Background(), born, "")
	_, terr := a.entryTransport(born, "")
	if asks, _ := rec.heardBy("typed.example", token); asks != 0 || !errors.As(lerr, &refused) || !errors.As(terr, &refused) {
		t.Fatalf("A SIGN-IN WENT WITH A COPY OF ITS ENTRY TO ANOTHER ADDRESS, or the copy was not refused: %d requests, list: %v, client: %v", asks, lerr, terr)
	}
}
