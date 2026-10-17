package broker

import (
	"encoding/json"
	"fmt"
	configdir "github.com/aiii-dot-id/aii-os/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// fakeAuthority is a token endpoint and an API on one TLS server: the
// API answers 200 only to the access token the authority issued last,
// and 401 to any other; the authority counts refreshes and can revoke.
type fakeAuthority struct {
	mu       sync.Mutex
	current  string
	issued   int
	revoked  bool
	rotate   bool // answer with a new refresh token (Microsoft) or not (Google)
	apiCalls atomic.Int32
}

func (f *fakeAuthority) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("client_id") != "cid" {
			t.Errorf("unexpected grant: %v", r.PostForm)
		}
		if r.PostForm.Get("client_secret") != "csecret" {
			t.Errorf("the client secret rides the refresh: %v", r.PostForm)
		}
		w.Header().Set("Content-Type", "application/json")
		if f.revoked {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid_grant"}`)
			return
		}
		f.issued++
		f.current = fmt.Sprintf("at-%d", f.issued)
		answer := map[string]any{"access_token": f.current, "expires_in": 3600}
		if f.rotate {
			answer["refresh_token"] = fmt.Sprintf("rt-%d", f.issued)
		}
		json.NewEncoder(w).Encode(answer)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		f.apiCalls.Add(1)
		f.mu.Lock()
		want := "Bearer " + f.current
		f.mu.Unlock()
		if r.Header.Get("Authorization") != want {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"unauthorized"}`)
			return
		}
		fmt.Fprintf(w, `{"ok":true,"saw":%q}`, r.Header.Get("Authorization"))
	})
	return mux
}

func writeTokens(t *testing.T, path, access, refresh string, expires time.Time) {
	t.Helper()
	if err := oauth.WriteTokenFile(path, &oauth.Tokens{Access: access, Refresh: refresh, Expires: expires}); err != nil {
		t.Fatal(err)
	}
}

// An oauth2 profile at the wire: the target must be one of its hosts; a
// fresh token rides as a bearer header without a round-trip; a token
// near expiry is refreshed once for many concurrent callers and the
// refresh is receipted without any value; a token the API rejects is
// refreshed once and the call retried; a revoked grant is refused by
// name as disconnected; a profile with no token file is disconnected;
// the placeholder is refused with this scheme; no reply, receipt or
// detail ever carries a token or the secret.
func TestAnOAuth2ProfileRefreshesAtTheWire(t *testing.T) {
	f := &fakeAuthority{current: "at-0"}
	ts := httptest.NewTLSServer(f.handler(t))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "profiles", "google.json")
	secretFile := filepath.Join(dir, "google.client")
	if err := os.WriteFile(secretFile, []byte("csecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTokens(t, tokenFile, "at-0", "rt-0", time.Now().Add(2*time.Hour))
	profile := AuthProfile{Scheme: SchemeOAuth2, Provider: "custom", ClientID: "cid", ClientSecretFile: secretFile,
		TokenURL: ts.URL + "/token", Hosts: []string{hostPort}, TokenFile: tokenFile}
	st := newStore(t)
	h := newHost(t, st, Config{
		Grants:       map[string]Grant{"p": {Hosts: []string{hostPort, "other.example:443"}, CredentialHandles: []string{"google", "never"}}},
		AuthProfiles: map[string]AuthProfile{"google": profile, "never": {Scheme: SchemeOAuth2, Provider: "custom", ClientID: "cid", TokenURL: ts.URL + "/token", Hosts: []string{hostPort}, TokenFile: "never.json"}},
		ProfilesDir:  filepath.Join(dir, "profiles"),
		Guard:        guardFor(ts), Transport: ts.Client().Transport,
	})
	// The plugin may reach other.example (envelope and grant), but the
	// profile's token may not: the host list is the credential's own pin.
	envelope := []string{"net.outbound:" + hostPort, "net.outbound:other.example:443"}
	b := h.Bind("p", packagefmt.TierT2, envelope)
	api := ts.URL + "/api/calendar"

	// Fresh: rides, no refresh, no auth receipt.
	m := dispatch(t, b, netParams(api, `{"auth_profile":"google"}`))
	wantResult(t, m, statusSucceeded, "")
	// The API answers only the token it issued and echoes it back: the
	// echo reaches the plugin as the placeholder, the token nowhere.
	if !strings.Contains(string(m["operation_result"]), `"saw":"Bearer `+CredentialPlaceholder+`"`) {
		t.Fatalf("the access token rides as a bearer header and its echo is redacted: %s", m["operation_result"])
	}
	noCredential(t, m, "at-0")
	if f.issued != 0 {
		t.Fatal("a fresh token needs no round-trip")
	}
	// Off the list: a scope mismatch, nothing dialled.
	before := f.apiCalls.Load()
	wantResult(t, dispatch(t, b, netParams("https://other.example:443/api/x", `{"auth_profile":"google"}`)), statusDenied, reasonAuthScopeMismatch)
	if f.apiCalls.Load() != before {
		t.Fatal("a target off the host list dials nothing")
	}
	// The placeholder does not belong to this scheme.
	wantResult(t, dispatch(t, b, netParams(ts.URL+"/api/"+CredentialPlaceholder, `{"auth_profile":"google"}`)), statusDenied, reasonAuthInvalid)

	// Near expiry: one refresh for many callers, receipted, no value anywhere.
	writeTokens(t, tokenFile, "at-old", "rt-0", time.Now().Add(-time.Minute))
	h.ReplacePolicy(h.policy.grants, h.policy.profiles) // a reconnect rewrites the file: the source rereads
	b = h.Bind("p", packagefmt.TierT2, envelope)
	var wg sync.WaitGroup
	results := make([]map[string]json.RawMessage, 6)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = dispatch(t, b, netParams(api, `{"auth_profile":"google"}`))
		}(i)
	}
	wg.Wait()
	for i, m := range results {
		wantResult(t, m, statusSucceeded, "")
		if !strings.Contains(string(m["operation_result"]), `"saw":"Bearer `+CredentialPlaceholder+`"`) {
			t.Fatalf("caller %d rides the refreshed token, its echo redacted: %s", i, m["operation_result"])
		}
		noCredential(t, m, "at-1", "at-old")
	}
	if f.issued != 1 {
		t.Fatalf("one refresh serves every waiter: %d", f.issued)
	}
	recs, err := st.PluginReceipts("p")
	if err != nil {
		t.Fatal(err)
	}
	auths := 0
	for _, r := range recs {
		if r.Operation == opAuthRefresh {
			auths++
			if !strings.Contains(string(r.ReceiptJSON), `"target":"auth_profile:google"`) || !strings.Contains(string(r.ReceiptJSON), `"detail":"refreshed"`) {
				t.Fatalf("the refresh receipt names the profile and the outcome: %s", r.ReceiptJSON)
			}
		}
		if strings.Contains(string(r.ReceiptJSON), "at-1") || strings.Contains(string(r.ReceiptJSON), "rt-0") || strings.Contains(string(r.ReceiptJSON), "csecret") {
			t.Fatalf("a receipt carries a value: %s", r.ReceiptJSON)
		}
	}
	if auths != 1 {
		t.Fatalf("exactly one refresh receipt, got %d", auths)
	}

	// Rejected by the API (the authority rotated out from under us):
	// one forced refresh, one retry, the call answers. The retry sent a
	// token the call did not hold when it began, and the answer echoes
	// it: what is scrubbed is what each request sent.
	f.mu.Lock()
	f.current = "at-rotated-elsewhere"
	f.mu.Unlock()
	m = dispatch(t, b, netParams(api, `{"auth_profile":"google"}`))
	wantResult(t, m, statusSucceeded, "")
	if !strings.Contains(string(m["operation_result"]), `"saw":"Bearer `+CredentialPlaceholder+`"`) || f.issued != 2 {
		t.Fatalf("a 401 forces one refresh and one retry, its echo redacted: %s (issued %d)", m["operation_result"], f.issued)
	}
	noCredential(t, m, "at-2", "at-1")
	whole, _ := json.Marshal(m)
	if strings.Contains(string(whole), "rt-0") || strings.Contains(string(whole), "csecret") {
		t.Fatalf("a reply carries a secret: %s", whole)
	}

	// Revoked: the refresh is refused and the profile is disconnected by name.
	f.mu.Lock()
	f.revoked = true
	f.current = "elsewhere"
	f.mu.Unlock()
	m = dispatch(t, b, netParams(api, `{"auth_profile":"google"}`))
	wantResult(t, m, statusDenied, reasonAuthDisconnected)
	if whole, _ = json.Marshal(m); !strings.Contains(string(whole), "Plugins page") {
		t.Fatalf("the denial names where the operator reconnects: %s", whole)
	}
	// No token file at all: disconnected, nothing dialled.
	before = f.apiCalls.Load()
	wantResult(t, dispatch(t, b, netParams(api, `{"auth_profile":"never"}`)), statusDenied, reasonAuthDisconnected)
	if f.apiCalls.Load() != before {
		t.Fatal("a disconnected profile dials nothing")
	}
}

// The oauth2 fields: a template fills the endpoints and hosts; a profile
// narrows the hosts; custom names its own; an incomplete profile is
// refused; the token endpoint must be https; the refresh dials only its
// authority (a token endpoint off the guard is refused, not dialled).
func TestOAuth2ProfileContractsAndTheirLimits(t *testing.T) {
	p := AuthProfile{Scheme: SchemeOAuth2, Provider: "google", ClientID: "cid", TokenFile: "/tmp/t.json", Scopes: []string{"https://www.googleapis.com/auth/calendar.readonly"}}
	params, tpl, err := p.Contract(oauthTestCatalog())
	if err != nil || params.TokenURL != "https://oauth2.googleapis.com/token" || params.AuthorizeParams["access_type"] != "offline" || len(tpl.Hosts) < 3 || !strings.Contains(params.Scope, "calendar.readonly") {
		t.Fatalf("the template fills the contract: %+v %+v %v", params, tpl, err)
	}
	p.Hosts = []string{"www.googleapis.com:443"}
	if _, tpl, _ := p.Contract(oauthTestCatalog()); len(tpl.Hosts) != 1 {
		t.Fatalf("a profile narrows the hosts: %v", tpl.Hosts)
	}
	custom := AuthProfile{Scheme: SchemeOAuth2, Provider: "custom", ClientID: "cid", TokenFile: "/tmp/t.json", TokenURL: "https://auth.example/token", Hosts: []string{"api.example:443"}}
	if params, _, err := custom.Contract(); err != nil || params.TokenURL != "https://auth.example/token" {
		t.Fatalf("custom endpoints: %+v %v", params, err)
	}
	for _, bad := range []AuthProfile{
		{Scheme: SchemeOAuth2, Provider: "acme", ClientID: "cid", TokenFile: "/tmp/t.json"},
		{Scheme: SchemeOAuth2, Provider: "google", TokenFile: "/tmp/t.json"},
		{Scheme: SchemeOAuth2, Provider: "google", ClientID: "cid"},
		{Scheme: SchemeOAuth2, Provider: "custom", ClientID: "cid", TokenFile: "/tmp/t.json"},
	} {
		if _, _, err := bad.Contract(); err == nil {
			t.Fatalf("an incomplete profile is refused: %+v", bad)
		}
	}

	// A token endpoint the guard refuses is never dialled: the refresh
	// fails as unavailable, and the API is not reached.
	f := &fakeAuthority{current: "at-0"}
	ts := httptest.NewTLSServer(f.handler(t))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "t.json")
	writeTokens(t, tokenFile, "at-old", "rt-0", time.Now().Add(-time.Minute))
	h := newHost(t, newStore(t), Config{
		Grants:       map[string]Grant{"p": {Hosts: []string{hostPort}, CredentialHandles: []string{"x"}}},
		AuthProfiles: map[string]AuthProfile{"x": {Scheme: SchemeOAuth2, Provider: "custom", ClientID: "cid", ClientSecretEnv: "AII_TEST_CS", TokenURL: "https://10.0.0.9:443/token", Hosts: []string{hostPort}, TokenFile: tokenFile}},
		ProfilesDir:  dir,
		Guard:        guardFor(ts), Transport: ts.Client().Transport,
	})
	t.Setenv("AII_TEST_CS", "csecret")
	b := h.Bind("p", packagefmt.TierT2, []string{"net.outbound:" + hostPort})
	m := dispatch(t, b, netParams(ts.URL+"/api/x", `{"auth_profile":"x"}`))
	wantResult(t, m, statusDenied, reasonAuthUnavailable)
	if f.apiCalls.Load() != 0 || f.issued != 0 {
		t.Fatal("a refresh the guard refuses reaches neither the authority nor the API")
	}
}

func oauthTestCatalog() map[string]oauth.Provider {
	var reg struct {
		OAuth map[string]oauth.Provider `json:"oauth"`
	}
	if err := json.Unmarshal(configdir.Providers, &reg); err != nil {
		panic(err)
	}
	return reg.OAuth
}

func TestOAuthContractHeadersReachRequestsAndPolicyReplacement(t *testing.T) {
	var expected atomic.Value
	expected.Store("first")
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-Configured") != expected.Load().(string) || r.Header.Get("Authorization") != "Bearer access" {
			t.Error("resolved contract headers did not reach request")
			w.WriteHeader(400)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	host, port := tsHostPort(t, server)
	hp := fmt.Sprintf("%s:%d", host, port)
	path := filepath.Join(t.TempDir(), "tokens.json")
	writeTokens(t, path, "access", "refresh", time.Now().Add(time.Hour))
	profile := AuthProfile{Scheme: SchemeOAuth2, Provider: "fixture", TokenFile: path}
	contracts := map[string]oauth.Provider{"fixture": {ClientID: "client", TokenURL: server.URL + "/token", Hosts: []string{hp}, ResourceHeaders: map[string]string{"X-Configured": "first"}}}
	grants := map[string]Grant{"p": {Hosts: []string{hp}, CredentialHandles: []string{"account"}}}
	profiles := map[string]AuthProfile{"account": profile}
	h := newHost(t, newStore(t), Config{Grants: grants, AuthProfiles: profiles, ProfilesDir: filepath.Dir(path), OAuthProviders: contracts, Guard: guardFor(server), Transport: server.Client().Transport})
	b := h.Bind("p", packagefmt.TierT2, []string{"net.outbound:" + hp})
	wantResult(t, dispatch(t, b, netParams(server.URL+"/api", `{"auth_profile":"account"}`)), statusSucceeded, "")
	next := contracts["fixture"]
	next.ResourceHeaders = map[string]string{"X-Configured": "second"}
	expected.Store("second")
	h.ReplacePolicy(grants, profiles, map[string]oauth.Provider{"fixture": next})
	wantResult(t, dispatch(t, b, netParams(server.URL+"/api", `{"auth_profile":"account"}`)), statusSucceeded, "")
	if calls.Load() != 2 {
		t.Fatalf("API calls: %d", calls.Load())
	}
}
