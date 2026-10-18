package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestAPluginConnectedFromItsCardRidesTheIssuedToken(t *testing.T) {
	const (
		plugin   = "org.example.notes"
		provider = "example-notes"
		handle   = "notes-work"
	)
	var mu sync.Mutex
	var rode []string
	authority := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f := r.PostForm
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" && f.Get("grant_type") == "authorization_code" && f.Get("code") == "the-code" &&
			f.Get("code_verifier") != "" && f.Get("client_id") == "cid" && f.Get("client_secret") == "csecret" {
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at-notes", "refresh_token": "rt-notes", "expires_in": 3600})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_request"}`)
	}))
	defer authority.Close()
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		rode = append(rode, r.Header.Get("Authorization"))
		mu.Unlock()
		fmt.Fprint(w, `{"notes":["remember the milk"]}`)
	}))
	defer api.Close()
	apiHost := strings.TrimPrefix(api.URL, "https://")
	admit := func(servers ...*httptest.Server) func(context.Context, string) error {
		return func(ctx context.Context, raw string) error {
			for _, s := range servers {
				if strings.HasPrefix(raw, s.URL) {
					return nil
				}
			}
			return firewall.FetchGuard(ctx, raw)
		}
	}
	riders := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), rode...)
	}

	dir := t.TempDir()
	genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "JourneyTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}).Ledger.Close()
	s, err := packagetest.NewSigner()
	if err != nil {
		t.Fatal(err)
	}
	pin := func(name string, env *sigenvelope.PublicKeyEnvelope) string {
		raw, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	certifierRoot, reviewerRoot := pin("certifier-root.json", s.Certifier.Env), pin("reviewer-root.json", s.Reviewer.Env)
	if err := os.MkdirAll(filepath.Join(dir, "trust"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := s.MintEmptyStatus(filepath.Join(dir, "trust")); err != nil {
		t.Fatal(err)
	}
	guest := wasmgen.CannedCaller([]byte(`{"operation":"settings.get"}`), []byte(fmt.Sprintf(
		`{"operation":"http.get","target":{"url":%q},"arguments":{"auth_profile":%q}}`, api.URL+"/v1/notes", handle)))
	spec := settingsPluginSpec(plugin, []string{apiHost}, `[{"key":"account","type":"secret","title":"Notes account","oauth":{"provider":"`+provider+`","services":["notes"]}}]`, guest)
	if err := s.SignT2(&spec, plugin, "0.1.0", []string{"net.outbound:" + apiHost}); err != nil {
		t.Fatal(err)
	}
	installSpec(t, dir, plugin, spec)
	t.Chdir(dir)

	// The slot's own contract for the service: its endpoints, its one API
	// host, and a notes service to read or modify.
	llm := withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x")
	var file map[string]json.RawMessage
	if err := json.Unmarshal(readFileT(t, filepath.Join(dir, "providers.json")), &file); err != nil {
		t.Fatal(err)
	}
	file["oauth"], _ = json.Marshal(map[string]any{provider: map[string]any{
		"name": "Example Notes", "authorize_url": authority.URL + "/authorize", "token_url": authority.URL + "/token",
		"hosts":    []string{apiHost},
		"services": map[string]any{"notes": map[string]any{"read": []string{"notes.read"}, "modify": []string{"notes.write"}}},
	}})
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")}
	cfg.LLM, cfg.SourcePath, cfg.Dashboard.Port, cfg.Tools.CWD = llm, filepath.Join(dir, "config.json"), 0, dir
	cfg.Plugins.Autoload, cfg.Plugins.CertifierRoot, cfg.Plugins.ReviewerRoot = "T2", certifierRoot, reviewerRoot
	app := New(cfg)
	app.oauthTransport, app.oauthGuard = authority.Client().Transport, admit(authority)
	app.brokerTransport, app.brokerGuard = api.Client().Transport, admit(api, authority)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)
	if len(app.plugins) != 1 || app.plugins[0].Tier != packagefmt.TierT2 {
		t.Fatalf("the reviewed plugin must be active at T2: %+v", app.plugins)
	}
	tool := app.plugins[0].ToolNames[0]
	// call runs the plugin's operation once and returns what its two host
	// calls answered: the settings it read, and the notes request.
	call := func() (settings, notes string) {
		t.Helper()
		res, err := app.toolReg.Execute(context.Background(), tool, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.ReasonCode == tools.ReasonSafeSuspended {
			return "", "SAFE: " + res.Error
		}
		var out struct {
			Relayed []json.RawMessage `json:"relayed"`
		}
		if err := json.Unmarshal([]byte(res.Output), &out); err != nil || len(out.Relayed) != 2 {
			t.Fatalf("the plugin answers with both host calls: %q %q %v", res.Output, res.Error, err)
		}
		return string(out.Relayed[0]), string(out.Relayed[1])
	}

	// The account, connected on the Plugins page against the authority.
	if err := app.SetAuthProfile(dashboard.AuthProfileEdit{Name: handle, Provider: provider, ClientID: "cid", ClientSecret: "csecret",
		Services: map[string]string{"notes": "read"}, RedirectURI: "https://dashboard.example/oauth/callback"}); err != nil {
		t.Fatal(err)
	}
	u, err := app.SignInProfile(handle)
	if err != nil {
		t.Fatal(err)
	}
	au, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.OAuthCallback("the-code", au.Query().Get("state"), ""); err != nil {
		t.Fatalf("the consent completes: %v", err)
	}

	// The card offers it, connected, for the package's one host.
	v := cardSetting(t, app, plugin, "account")
	if v.Account == nil || len(v.Account.Hosts) != 1 || v.Account.Hosts[0] != apiHost || len(v.Account.Offers) != 1 || v.Account.Offers[0].Name != handle ||
		v.Account.Offers[0].State != "connected" || v.Account.Offers[0].Not != "" || v.Account.Granted {
		t.Fatalf("the card offers the connected account for the package's host: %+v", v.Account)
	}

	// Before the save: nothing chosen, and the request refused as not granted.
	if settings, notes := call(); strings.Contains(settings, handle) || !strings.Contains(notes, `"reasonCode":"POLICY_DENY"`) {
		t.Fatalf("before the save the plugin has no account and is not granted the host: %s %s", settings, notes)
	}
	if got := riders(); len(got) != 0 {
		t.Fatalf("nothing reached the service before the save: %q", got)
	}

	// The card's save, through the config door: the plugin's next call
	// reads the account and rides its issued token to the service.
	if _, err := app.applyConfigChange(map[string]any{"plugins.settings." + plugin + ".account": handle}); err != nil {
		t.Fatal(err)
	}
	settings, notes := call()
	if !strings.Contains(settings, `"account":"`+handle+`"`) || !strings.Contains(notes, "remember the milk") {
		t.Fatalf("after the save the plugin reads its account and the call goes through: %s %s", settings, notes)
	}
	if strings.Contains(notes, "at-notes") {
		t.Fatal("the access token reached the plugin")
	}
	if got := riders(); len(got) != 1 || got[0] != "Bearer at-notes" {
		t.Fatalf("the issued token rides to the service host: %q", got)
	}
	if v := cardSetting(t, app, plugin, "account"); !v.Account.Granted {
		t.Fatalf("the card says the account is granted: %+v", v.Account)
	}

	// While SAFE holds, the grant lets the plugin do nothing.
	app.enterSafe("test: the record is frozen")
	if _, notes := call(); !strings.HasPrefix(notes, "SAFE: ") {
		t.Fatalf("under SAFE the plugin's call is suspended: %s", notes)
	}
	app.resetModeForTest()
	if got := riders(); len(got) != 1 {
		t.Fatalf("nothing rode under SAFE: %q", got)
	}

	// Remove: the handle goes back, so the call is refused by name; the
	// host grant stays, so it is not the host that refuses it.
	if _, err := app.applyConfigChange(map[string]any{"plugins.settings." + plugin + ".account": nil}); err != nil {
		t.Fatal(err)
	}
	if settings, notes := call(); strings.Contains(settings, handle) || !strings.Contains(notes, "NET_AUTH_PROFILE_NOT_ADMITTED") {
		t.Fatalf("after Remove the account is not the plugin's to cite: %s %s", settings, notes)
	}
	if got := riders(); len(got) != 1 {
		t.Fatalf("nothing rode after Remove: %q", got)
	}
}
