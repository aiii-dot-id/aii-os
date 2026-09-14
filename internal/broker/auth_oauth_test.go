package broker

import (
	"encoding/json"
	"fmt"
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

// .
// .
// .
type fakeAuthority struct {
	mu       sync.Mutex
	current  string
	issued   int
	revoked  bool
	rotate   bool
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

// .
// .
// .
// .
// .
// .
// .
// .
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
		AuthProfiles: map[string]AuthProfile{"google": profile, "never": {Scheme: SchemeOAuth2, Provider: "custom", ClientID: "cid", TokenURL: ts.URL + "/token", Hosts: []string{hostPort}, TokenFile: filepath.Join(dir, "never.json")}},
		Guard:        guardFor(ts), Transport: ts.Client().Transport,
	})
	// .
	// .
	envelope := []string{"net.outbound:" + hostPort, "net.outbound:other.example:443"}
	b := h.Bind("p", packagefmt.TierT2, envelope)
	api := ts.URL + "/api/calendar"

	// .
	m := dispatch(t, b, netParams(api, `{"auth_profile":"google"}`))
	wantResult(t, m, statusSucceeded, "")
	if !strings.Contains(string(m["operation_result"]), `"saw":"Bearer at-0"`) {
		t.Fatalf("the access token rides as a bearer header: %s", m["operation_result"])
	}
	if f.issued != 0 {
		t.Fatal("a fresh token needs no round-trip")
	}
	// .
	before := f.apiCalls.Load()
	wantResult(t, dispatch(t, b, netParams("https://other.example:443/api/x", `{"auth_profile":"google"}`)), statusDenied, reasonAuthScopeMismatch)
	if f.apiCalls.Load() != before {
		t.Fatal("a target off the host list dials nothing")
	}
	// .
	wantResult(t, dispatch(t, b, netParams(ts.URL+"/api/"+CredentialPlaceholder, `{"auth_profile":"google"}`)), statusDenied, reasonAuthInvalid)

	// .
	writeTokens(t, tokenFile, "at-old", "rt-0", time.Now().Add(-time.Minute))
	h.ReplacePolicy(h.policy.grants, h.policy.profiles)
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
		if !strings.Contains(string(m["operation_result"]), `"saw":"Bearer at-1"`) {
			t.Fatalf("caller %d rides the refreshed token: %s", i, m["operation_result"])
		}
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

	// .
	// .
	f.mu.Lock()
	f.current = "at-rotated-elsewhere"
	f.mu.Unlock()
	m = dispatch(t, b, netParams(api, `{"auth_profile":"google"}`))
	wantResult(t, m, statusSucceeded, "")
	if !strings.Contains(string(m["operation_result"]), `"saw":"Bearer at-2"`) || f.issued != 2 {
		t.Fatalf("a 401 forces one refresh and one retry: %s (issued %d)", m["operation_result"], f.issued)
	}
	whole, _ := json.Marshal(m)
	if strings.Contains(string(whole), "rt-0") || strings.Contains(string(whole), "csecret") {
		t.Fatalf("a reply carries a secret: %s", whole)
	}

	// .
	f.mu.Lock()
	f.revoked = true
	f.current = "elsewhere"
	f.mu.Unlock()
	m = dispatch(t, b, netParams(api, `{"auth_profile":"google"}`))
	wantResult(t, m, statusDenied, reasonAuthDisconnected)
	if whole, _ = json.Marshal(m); !strings.Contains(string(whole), "Plugins page") {
		t.Fatalf("the denial names where the operator reconnects: %s", whole)
	}
	// .
	before = f.apiCalls.Load()
	wantResult(t, dispatch(t, b, netParams(api, `{"auth_profile":"never"}`)), statusDenied, reasonAuthDisconnected)
	if f.apiCalls.Load() != before {
		t.Fatal("a disconnected profile dials nothing")
	}
}

// .
// .
// .
// .
func TestOAuth2ProfileContractsAndTheirLimits(t *testing.T) {
	p := AuthProfile{Scheme: SchemeOAuth2, Provider: "google", ClientID: "cid", TokenFile: "/tmp/t.json", Scopes: []string{"https://www.googleapis.com/auth/calendar.readonly"}}
	params, tpl, err := p.Contract()
	if err != nil || params.TokenURL != "https://oauth2.googleapis.com/token" || params.AuthorizeParams["access_type"] != "offline" || len(tpl.Hosts) < 3 || !strings.Contains(params.Scope, "calendar.readonly") {
		t.Fatalf("the template fills the contract: %+v %+v %v", params, tpl, err)
	}
	p.Hosts = []string{"www.googleapis.com:443"}
	if _, tpl, _ := p.Contract(); len(tpl.Hosts) != 1 {
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

	// .
	// .
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
