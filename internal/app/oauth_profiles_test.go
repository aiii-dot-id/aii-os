package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .
// .
// .
// .
type profileAuthority struct {
	mu      sync.Mutex
	codes   []url.Values
	revoked []string
	polls   int
}

func (f *profileAuthority) serve(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		f.codes = append(f.codes, r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			if r.PostForm.Get("code_verifier") == "" || r.PostForm.Get("client_secret") != "csecret" {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"invalid_request"}`)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at-code", "refresh_token": "rt-code", "expires_in": 3600})
		case "urn:ietf:params:oauth:grant-type:device_code":
			f.polls++
			if f.polls < 2 {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"authorization_pending"}`)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at-device", "refresh_token": "rt-device", "expires_in": 3600})
		default:
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"unsupported_grant_type"}`)
		}
	})
	mux.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"device_code": "dc", "user_code": "WXYZ-1234", "verification_uri": "https://auth.example/device", "expires_in": 600, "interval": 0.01})
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.PostForm.Get("token"))
		f.mu.Unlock()
	})
	return httptest.NewTLSServer(mux)
}

func profileApp(t *testing.T, ts *httptest.Server) *App {
	t.Helper()
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")}
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.oauthTransport = ts.Client().Transport
	a.oauthGuard = func(_ context.Context, raw string) error {
		if strings.HasPrefix(raw, ts.URL) {
			return nil
		}
		return tools.FetchGuard(context.Background(), raw)
	}
	a.signInTimeout = 20 * time.Second
	return a
}

// .
// .
// .
// .
// .
// .
func TestAnAuthProfileIsCreatedConnectedAndDisconnectedFromThePage(t *testing.T) {
	f := &profileAuthority{}
	ts := f.serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	host, _ := url.Parse(ts.URL)

	// .
	for _, bad := range []dashboard.AuthProfileEdit{
		{Name: "Bad Name", Provider: "github", ClientID: "cid", Services: map[string]string{"issues": "read"}},
		{Name: "x", Provider: "acme", ClientID: "cid"},
		{Name: "x", Provider: "github", Services: map[string]string{"issues": "read"}},
		{Name: "x", Provider: "github", ClientID: "cid"},
		{Name: "x", Provider: "custom", ClientID: "cid", Scopes: []string{"s"}},
	} {
		if err := a.SetAuthProfile(bad); err == nil {
			t.Fatalf("refused: %+v", bad)
		}
	}
	edit := dashboard.AuthProfileEdit{Name: "gh", Provider: "custom", ClientID: "cid", ClientSecret: "csecret", Scopes: []string{"repo"},
		Hosts: []string{host.Host}, AuthorizeURL: ts.URL + "/authorize", TokenURL: ts.URL + "/token", DeviceURL: ts.URL + "/device", RevokeURL: ts.URL + "/revoke"}
	if err := a.SetAuthProfile(edit); err != nil {
		t.Fatal(err)
	}
	prof := a.configSnapshot().Plugins.AuthProfiles["gh"]
	if prof.Scheme != broker.SchemeOAuth2 || prof.ClientID != "cid" || prof.ClientSecretFile == "" || prof.TokenFile == "" || len(prof.Scopes) != 1 {
		t.Fatalf("the profile landed: %+v", prof)
	}
	raw, err := os.ReadFile(prof.ClientSecretFile)
	if err != nil || strings.TrimSpace(string(raw)) != "csecret" {
		t.Fatalf("the secret is in its private file: %q %v", raw, err)
	}
	if fi, _ := os.Stat(prof.ClientSecretFile); fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the secret file is private: %v", fi.Mode())
	}
	onDisk, _ := os.ReadFile(a.configSnapshot().SourcePath)
	if strings.Contains(string(onDisk), "csecret") || !strings.Contains(string(onDisk), `"gh"`) {
		t.Fatalf("config carries the profile and never the secret: %s", onDisk)
	}
	views := a.authProfileViews(&Config{Plugins: a.configSnapshot().Plugins})
	if len(views) != 1 || views[0].State != "disconnected" || !views[0].HasClientSecret || views[0].ClientID != "cid" || !views[0].CanDevice {
		t.Fatalf("the view: %+v", views)
	}
	if b, _ := json.Marshal(views); strings.Contains(string(b), "csecret") {
		t.Fatal("a view carries the secret")
	}
	// .
	edit.ClientSecret = ""
	edit.Scopes = []string{"repo", "read:user"}
	if err := a.SetAuthProfile(edit); err != nil {
		t.Fatal(err)
	}
	if p := a.configSnapshot().Plugins.AuthProfiles["gh"]; p.ClientSecretFile != prof.ClientSecretFile || len(p.Scopes) != 2 {
		t.Fatalf("an edit keeps the secret and takes the scopes: %+v", p)
	}

	// .
	// .
	u, err := a.SignInProfile("gh")
	if err != nil {
		t.Fatal(err)
	}
	au, _ := url.Parse(u)
	if !strings.HasPrefix(u, ts.URL+"/authorize") || au.Query().Get("code_challenge") == "" || au.Query().Get("state") == "" || au.Query().Get("client_id") != "cid" {
		t.Fatalf("the authorize URL: %s", u)
	}
	redirect := au.Query().Get("redirect_uri") + "?code=the-code&state=" + au.Query().Get("state")
	if err := a.CompleteProfileSignIn("gh", "http://127.0.0.1:8187/oauth/callback?code=the-code&state=wrong"); err == nil {
		t.Fatal("a wrong state does not complete")
	}
	if err := a.CompleteProfileSignIn("gh", redirect); err != nil {
		t.Fatalf("complete: %v", err)
	}
	st, err := oauth.ReadProfileState(prof.TokenFile)
	if err != nil || !st.Connected || !st.Refreshable {
		t.Fatalf("connected, refreshable: %+v %v", st, err)
	}
	if fi, _ := os.Stat(prof.TokenFile); fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the token file is private: %v", fi.Mode())
	}
	if v := a.authProfileViews(&Config{Plugins: a.configSnapshot().Plugins}); v[0].State != "connected" {
		t.Fatalf("the view says connected: %+v", v)
	}
	if err := a.CompleteProfileSignIn("gh", redirect); err == nil {
		t.Fatal("a completed sign-in cannot complete twice")
	}

	// .
	if err := a.DisconnectProfile("gh"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	rev := append([]string(nil), f.revoked...)
	f.mu.Unlock()
	if len(rev) != 1 || rev[0] != "rt-code" {
		t.Fatalf("the refresh token is revoked: %v", rev)
	}
	if _, err := os.Stat(prof.TokenFile); !os.IsNotExist(err) {
		t.Fatal("the token file is removed")
	}
	if v := a.authProfileViews(&Config{Plugins: a.configSnapshot().Plugins}); v[0].State != "disconnected" {
		t.Fatalf("disconnected: %+v", v)
	}

	// .
	// .
	dv, err := a.DeviceSignInProfile("gh")
	if err != nil || dv.UserCode != "WXYZ-1234" || dv.VerificationURI == "" {
		t.Fatalf("device: %+v %v", dv, err)
	}
	if v := a.authProfileViews(&Config{Plugins: a.configSnapshot().Plugins}); v[0].Device == nil || v[0].Device.UserCode != "WXYZ-1234" {
		t.Fatalf("the code is on the profile while pending: %+v", v[0])
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if st, _ := oauth.ReadProfileState(prof.TokenFile); st.Connected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the device sign-in did not complete")
		}
		time.Sleep(20 * time.Millisecond)
	}
	raw, _ = os.ReadFile(prof.TokenFile)
	if !strings.Contains(string(raw), "at-device") {
		t.Fatalf("the device tokens are stored: %s", raw)
	}

	// .
	if err := a.DeleteAuthProfile("gh"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.configSnapshot().Plugins.AuthProfiles["gh"]; ok {
		t.Fatal("the profile is deleted")
	}
	if _, err := os.Stat(prof.ClientSecretFile); !os.IsNotExist(err) {
		t.Fatal("the secret file is removed")
	}
	if _, err := a.SignInProfile("gh"); err == nil {
		t.Fatal("a deleted profile cannot connect")
	}
}

// .
// .
// .
func TestATemplateProfileKnowsWhatItsAuthorityAllows(t *testing.T) {
	f := &profileAuthority{}
	ts := f.serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	if err := a.SetAuthProfile(dashboard.AuthProfileEdit{Name: "google-cal", Provider: "google", ClientID: "cid", ClientSecret: "csecret", Services: map[string]string{"calendar": "read"}}); err != nil {
		t.Fatal(err)
	}
	p := a.configSnapshot().Plugins.AuthProfiles["google-cal"]
	if len(p.Hosts) != 0 {
		t.Fatalf("a template profile leaves the hosts to the template: %v", p.Hosts)
	}
	params, tpl, err := p.Contract()
	if err != nil || !strings.Contains(params.Scope, "calendar.readonly") || len(tpl.Hosts) < 3 || params.AuthorizeParams["prompt"] != "consent" {
		t.Fatalf("the template fills the contract: %+v %+v %v", params, tpl, err)
	}
	v := a.authProfileViews(&Config{Plugins: a.configSnapshot().Plugins})
	if len(v) != 1 || v[0].CanDevice || v[0].Provider != "google" || len(v[0].Hosts) < 3 {
		t.Fatalf("the view: %+v", v)
	}
	if _, err := a.DeviceSignInProfile("google-cal"); err == nil || !strings.Contains(err.Error(), "calendar") {
		t.Fatalf("the device route is refused by name: %v", err)
	}
	if err := a.SetAuthProfile(dashboard.AuthProfileEdit{Name: "google-cal", Provider: "google", ClientID: "cid", Services: map[string]string{"calendar": "modify"}}); err != nil {
		t.Fatal(err)
	}
	if p := a.configSnapshot().Plugins.AuthProfiles["google-cal"]; len(p.Scopes) != 2 {
		t.Fatalf("modify takes the read and the modify scopes: %v", p.Scopes)
	}
}
