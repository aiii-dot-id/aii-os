package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// A settings commit replaces the entire configuration under cfgMu, even
// when the identity's ledger path itself does not change. The plugin card
// must read that path under the same lock as every other config reader.
func TestCredentialsDirectoryDuringSettingsPublication(t *testing.T) {
	cfg := Config{}
	cfg.Identity.LedgerPath = filepath.Join(t.TempDir(), "ledger.db")
	a := &App{cfg: &cfg}
	want := filepath.Join(identityCredentials(cfg.Identity.LedgerPath), "profiles")
	start := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-start
		for i := 0; i < 10000; i++ {
			a.cfgMu.Lock()
			next := *a.cfg
			*a.cfg = next
			a.cfgMu.Unlock()
		}
	}()
	close(start)
	for i := 0; i < 10000; i++ {
		got, err := a.credentialsDir()
		if err != nil || got != want {
			t.Errorf("credentials directory changed during publication: %q, %v", got, err)
			break
		}
	}
	<-done
}

// profileAuthority is a fake authority: an authorize page is never
// fetched (the operator's browser would), the token endpoint exchanges a
// code or a device code, the device endpoint hands out a code, the
// revoke endpoint records what it was told — and, while revokeFails is
// set, answers that it is unavailable.
type profileAuthority struct {
	mu                 sync.Mutex
	codes              []url.Values
	revoked            []string
	revokeFails        bool
	polls              int
	deviceReplyReady   chan struct{}
	deviceReplyRelease <-chan struct{}
}

func (f *profileAuthority) serve(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.codes = append(f.codes, r.PostForm)
		if r.PostForm.Get("grant_type") == "urn:ietf:params:oauth:grant-type:device_code" {
			f.polls++
		}
		polls := f.polls
		f.mu.Unlock()
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
			if polls < 2 {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"authorization_pending"}`)
				return
			}
			if f.deviceReplyReady != nil && polls == 2 {
				close(f.deviceReplyReady)
			}
			if f.deviceReplyRelease != nil {
				select {
				case <-f.deviceReplyRelease:
				case <-r.Context().Done():
					return
				}
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
		fails := f.revokeFails
		f.mu.Unlock()
		if fails {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":"temporarily_unavailable"}`)
		}
	})
	return httptest.NewTLSServer(mux)
}

// tokenFileOf is where the host keeps a profile's own token file.
func tokenFileOf(t *testing.T, a *App, prof broker.AuthProfile) string {
	t.Helper()
	path, owned, err := a.tokenFile(prof)
	if err != nil || !owned {
		t.Fatalf("token_file %q is the identity's own, under its credentials directory: %s, %v, %v", prof.TokenFile, path, owned, err)
	}
	return path
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
		return firewall.FetchGuard(context.Background(), raw)
	}
	a.signInTimeout = 20 * time.Second
	return a
}

// A profile is created from the page with its secret written to a
// private file and never echoed; connected by the pasted redirect (the
// code exchanged with PKCE and the client secret, the token file written
// and owned); reconnected by device code; disconnected with the grant
// revoked and the file gone; deleted with its files. The config file
// carries the profile and never a secret.
func TestAnAuthProfileIsCreatedConnectedAndDisconnectedFromThePage(t *testing.T) {
	ready, reply := make(chan struct{}), make(chan struct{})
	f := &profileAuthority{deviceReplyReady: ready, deviceReplyRelease: reply}
	ts := f.serve(t)
	defer ts.Close()
	releaseReply := sync.OnceFunc(func() { close(reply) })
	defer releaseReply()
	a := profileApp(t, ts)
	host, _ := url.Parse(ts.URL)

	// Refusals first: a bad name, an unknown provider, no client id.
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
	edit := dashboard.AuthProfileEdit{RedirectURI: "https://dashboard.example/oauth/callback", Name: "gh", Provider: "custom", ClientID: "cid", ClientSecret: "csecret", Scopes: []string{"repo"},
		Hosts: []string{host.Host}, AuthorizeURL: ts.URL + "/authorize", TokenURL: ts.URL + "/token", DeviceURL: ts.URL + "/device", RevokeURL: ts.URL + "/revoke"}
	if err := a.SetAuthProfile(edit); err != nil {
		t.Fatal(err)
	}
	prof := a.configSnapshot().Plugins.AuthProfiles["gh"]
	if prof.Scheme != broker.SchemeOAuth2 || prof.ClientID != "cid" || prof.ClientSecretFile == "" || prof.TokenFile != "gh.json" || len(prof.Scopes) != 1 {
		t.Fatalf("the profile landed: %+v", prof)
	}
	raw, err := os.ReadFile(prof.ClientSecretFile)
	if err != nil || strings.TrimSpace(string(raw)) != "csecret" {
		t.Fatalf("the secret is in its private file: %q %v", raw, err)
	}
	if ok, err := fileperm.IsRestrictedToOwner(prof.ClientSecretFile); err != nil || !ok {
		t.Fatalf("the secret file is private: %v", err)
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
	// An edit without a secret keeps the stored one.
	edit.ClientSecret = ""
	edit.Scopes = []string{"repo", "read:user"}
	if err := a.SetAuthProfile(edit); err != nil {
		t.Fatal(err)
	}
	if p := a.configSnapshot().Plugins.AuthProfiles["gh"]; p.ClientSecretFile != prof.ClientSecretFile || len(p.Scopes) != 2 {
		t.Fatalf("an edit keeps the secret and takes the scopes: %+v", p)
	}

	// Connect: the authorize URL carries PKCE and the state; the pasted
	// redirect completes it; the token file is written, owned.
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
	st, err := oauth.ReadProfileState(tokenFileOf(t, a, prof))
	if err != nil || !st.Connected || !st.Refreshable {
		t.Fatalf("connected, refreshable: %+v %v", st, err)
	}
	if ok, err := fileperm.IsRestrictedToOwner(tokenFileOf(t, a, prof)); err != nil || !ok {
		t.Fatalf("the token file is private: %v", err)
	}
	if v := a.authProfileViews(&Config{Plugins: a.configSnapshot().Plugins}); v[0].State != "connected" {
		t.Fatalf("the view says connected: %+v", v)
	}
	if err := a.CompleteProfileSignIn("gh", redirect); err == nil {
		t.Fatal("a completed sign-in cannot complete twice")
	}

	tokenPath := tokenFileOf(t, a, prof)
	tokenName := filepath.Join(filepath.Dir(tokenPath), "second-token")
	secretName := filepath.Join(filepath.Dir(prof.ClientSecretFile), "second-secret")
	linked := os.Link(tokenPath, tokenName) == nil && os.Link(prof.ClientSecretFile, secretName) == nil
	if err := a.DisconnectProfile("gh"); err != nil {
		t.Fatal(err)
	}
	if linked {
		if got, err := os.ReadFile(tokenName); err != nil || len(got) != 0 {
			t.Errorf("a name made for the token file holds nothing of it after the disconnect: %q %v", got, err)
		}
	} else {
		t.Log("hard links unavailable here: the second names are not asked")
	}
	f.mu.Lock()
	rev := append([]string(nil), f.revoked...)
	f.mu.Unlock()
	if len(rev) != 1 || rev[0] != "rt-code" {
		t.Fatalf("the refresh token is revoked: %v", rev)
	}
	if _, err := os.Stat(tokenFileOf(t, a, prof)); !os.IsNotExist(err) {
		t.Fatal("the token file is removed")
	}
	if v := a.authProfileViews(&Config{Plugins: a.configSnapshot().Plugins}); v[0].State != "disconnected" {
		t.Fatalf("disconnected: %+v", v)
	}

	// Reconnect by device code: the code is shown, the host polls, the
	// tokens land.
	dv, err := a.DeviceSignInProfile("gh")
	if err != nil || dv.UserCode != "WXYZ-1234" || dv.VerificationURI == "" {
		t.Fatalf("device: %+v %v", dv, err)
	}
	// Observe the pending view while the fixture authority holds its reply,
	// not in a guessed interval before its second poll happens to complete.
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("device poll did not reach the controlled authority reply")
	}
	if v := a.authProfileViews(&Config{Plugins: a.configSnapshot().Plugins}); v[0].Device == nil || v[0].Device.UserCode != "WXYZ-1234" {
		t.Fatalf("the code is on the profile while pending: %+v", v[0])
	}
	releaseReply()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if st, _ := oauth.ReadProfileState(tokenFileOf(t, a, prof)); st.Connected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the device sign-in did not complete")
		}
		time.Sleep(20 * time.Millisecond)
	}
	raw, _ = os.ReadFile(tokenFileOf(t, a, prof))
	if !strings.Contains(string(raw), "at-device") {
		t.Fatalf("the device tokens are stored: %s", raw)
	}

	// Delete: disconnected (revoked again), the secret file gone, the entry gone.
	if err := a.DeleteAuthProfile("gh"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.configSnapshot().Plugins.AuthProfiles["gh"]; ok {
		t.Fatal("the profile is deleted")
	}
	if _, err := os.Stat(prof.ClientSecretFile); !os.IsNotExist(err) {
		t.Fatal("the secret file is removed")
	}
	if linked {
		if got, err := os.ReadFile(secretName); err != nil || len(got) != 0 {
			t.Errorf("a name made for the secret file holds nothing of it after the profile is deleted: %q %v", got, err)
		}
	}
	if _, err := a.SignInProfile("gh"); err == nil {
		t.Fatal("a deleted profile cannot connect")
	}
}

// Google's device flow does not serve mail or calendar: a Google profile
// with a calendar scope is refused the device route by name, and a
// template profile resolves its endpoints and hosts from the table.
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
	params, tpl, err := p.Contract(embeddedRegistry().OAuth)
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

func TestConfiguredOAuthClientAndScopesNeedNoDuplicateProfileInput(t *testing.T) {
	ts := (&profileAuthority{}).serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	if err := a.SetAuthProfile(dashboard.AuthProfileEdit{Name: "shared-client", Provider: "openai-chatgpt"}); err != nil {
		t.Fatal(err)
	}
	profile, err := a.authProfile("shared-client")
	if err != nil {
		t.Fatal(err)
	}
	if profile.ClientID != "" || len(profile.Scopes) != 0 {
		t.Fatal("profile pinned inherited client or scopes")
	}
	catalog, err := a.oauthContracts()
	if err != nil {
		t.Fatal(err)
	}
	params, _, err := profile.Contract(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if params.ClientID != catalog["openai-chatgpt"].ClientID || params.Scope != strings.Join(catalog["openai-chatgpt"].BaseScopes, " ") {
		t.Fatal("profile did not inherit the common contract")
	}
}

func TestDisconnectCancelsSignInsForSharedTokenFile(t *testing.T) {
	authority := &profileAuthority{}
	ts := authority.serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	dir, err := a.credentialsDir()
	if err != nil {
		t.Fatal(err)
	}
	profile := broker.AuthProfile{Scheme: broker.SchemeOAuth2, Provider: "custom", ClientID: "cid", AuthorizeURL: ts.URL + "/authorize", TokenURL: ts.URL + "/token", RedirectURI: "https://ui.example/oauth/callback", Scopes: []string{"scope"}, Hosts: []string{"api.example:443"}, TokenFile: filepath.Join(dir, "shared.json")}
	if err := a.commitAuthProfiles("", func(m map[string]broker.AuthProfile) { m["first"] = profile; m["second"] = profile }); err != nil {
		t.Fatal(err)
	}
	auth, err := a.SignInProfile("first")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(auth)
	if err := a.DisconnectProfile("second"); err != nil {
		t.Fatal(err)
	}
	if err := a.OAuthCallback("code", u.Query().Get("state"), ""); err != errNoSignIn {
		t.Fatalf("disconnected alias retained an attempt: %v", err)
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if len(authority.codes) != 0 {
		t.Fatal("cancelled alias reached token exchange")
	}
}

// Deletion includes consent begun during the remote revocation call, whether
// that consent is pending, exchanging, or has already written a credential.
func TestDeleteProfileFencesConsentDuringRevocation(t *testing.T) {
	for _, phase := range []string{"pending", "exchanging", "connected"} {
		t.Run(phase, func(t *testing.T) {
			revoking, finishRevoke := make(chan struct{}), make(chan struct{})
			exchanging, finishExchange := make(chan struct{}), make(chan struct{})
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/revoke" {
					close(revoking)
					<-finishRevoke
					return
				}
				if phase == "exchanging" {
					close(exchanging)
					<-finishExchange
				}
				json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-new", "refresh_token": "fixture-refresh", "expires_in": 3600})
			}))
			t.Cleanup(ts.Close)
			t.Cleanup(func() {
				for _, c := range []chan struct{}{finishRevoke, finishExchange} {
					select {
					case <-c:
					default:
						close(c)
					}
				}
			})
			a := profileApp(t, ts)
			u, _ := url.Parse(ts.URL)
			edit := dashboard.AuthProfileEdit{Name: "fixture", Provider: "custom", ClientID: "fixture-client", ClientSecret: "fixture-secret", Scopes: []string{"read"}, Hosts: []string{u.Host}, AuthorizeURL: ts.URL + "/authorize", TokenURL: ts.URL + "/token", RevokeURL: ts.URL + "/revoke", RedirectURI: "https://phone.example/oauth/callback"}
			if err := a.SetAuthProfile(edit); err != nil {
				t.Fatal(err)
			}
			prof := a.configSnapshot().Plugins.AuthProfiles[edit.Name]
			params, tpl, err := a.contractFor(edit.Name, prof)
			if err != nil {
				t.Fatal(err)
			}
			if err := oauth.WriteTokenFile(tokenFileOf(t, a, prof), &oauth.Tokens{Access: "fixture-old", Refresh: "fixture-old-refresh"}); err != nil {
				t.Fatal(err)
			}
			deleted := make(chan error, 1)
			go func() { deleted <- a.DeleteAuthProfile(edit.Name) }()
			select {
			case <-revoking:
			case <-time.After(5 * time.Second):
				t.Fatal("revocation did not start")
			}
			auth, err := a.SignInProfile(edit.Name)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { a.CancelProfileSignIn(edit.Name) })
			authURL, _ := url.Parse(auth)
			state := authURL.Query().Get("state")
			completed := make(chan error, 1)
			if phase != "pending" {
				go func() { completed <- a.OAuthCallback("fixture-code", state, "") }()
				if phase == "connected" {
					if err := <-completed; err != nil {
						t.Fatal(err)
					}
				} else {
					select {
					case <-exchanging:
					case <-time.After(5 * time.Second):
						t.Fatal("exchange did not start")
					}
				}
			}
			close(finishRevoke)
			select {
			case err := <-deleted:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("deletion blocked on consent")
			}
			if phase == "exchanging" {
				close(finishExchange)
				select {
				case err := <-completed:
					if err == nil {
						t.Fatal("deleted profile accepted a late exchange")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("cancelled exchange did not finish")
				}
			} else if err := a.OAuthCallback("fixture-code", state, ""); err == nil {
				t.Fatal("deleted profile accepted a callback")
			}
			if _, ok := a.configSnapshot().Plugins.AuthProfiles[edit.Name]; ok {
				t.Fatal("deleted profile remains live")
			}
			persisted, err := LoadConfig(a.configSnapshot().SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := persisted.Plugins.AuthProfiles[edit.Name]; ok {
				t.Fatal("deleted profile survives restart")
			}
			for _, path := range []string{tokenFileOf(t, a, prof), prof.ClientSecretFile} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("deleted profile left a credential file: %v", err)
				}
			}
			// A caller that read the profile before deletion cannot register later.
			if _, err := a.startSignIn(profileSignInPrefix+edit.Name, profileSignInPrefix+edit.Name, tokenFileOf(t, a, prof), tpl, params, &prof); !errors.Is(err, errSignInSuperseded) {
				t.Fatalf("stale profile started consent: %v", err)
			}
		})
	}
}

func TestProfileChangeRefusesCapturedExchange(t *testing.T) {
	ts := (&profileAuthority{}).serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	edit := dashboard.AuthProfileEdit{Name: "fixture", Provider: "custom", ClientID: "cid", ClientSecret: "csecret", Scopes: []string{"read"}, Hosts: []string{"api.example:443"}, AuthorizeURL: ts.URL + "/authorize", TokenURL: ts.URL + "/token", RedirectURI: "https://phone.example/oauth/callback"}
	if err := a.SetAuthProfile(edit); err != nil {
		t.Fatal(err)
	}
	auth, err := a.SignInProfile(edit.Name)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(auth)
	key := profileSignInPrefix + edit.Name
	a.providers.custody.mu.Lock()
	pending := a.providers.custody.signIns[key]
	a.providers.custody.mu.Unlock()
	// File reloads can replace the profile without traversing the UI edit path.
	a.cfgMu.Lock()
	prof := a.cfg.Plugins.AuthProfiles[edit.Name]
	prof.ClientID = "replacement-client"
	a.cfg.Plugins.AuthProfiles = map[string]broker.AuthProfile{edit.Name: prof}
	a.cfgMu.Unlock()
	if err := a.publishSignIn(key, pending, &oauth.Tokens{Access: "fixture-stale"}); !errors.Is(err, errSignInSuperseded) {
		t.Fatalf("changed profile published: %v", err)
	}
	if _, err := os.Stat(tokenFileOf(t, a, prof)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale exchange wrote credentials: %v", err)
	}
	a.CancelProfileSignIn(edit.Name)
	if err := a.OAuthCallback("fixture-code", u.Query().Get("state"), ""); err == nil {
		t.Fatal("cancelled callback succeeded")
	}
}

func TestProfileSecretEditCancelsCapturedConsent(t *testing.T) {
	ts := (&profileAuthority{}).serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	edit := dashboard.AuthProfileEdit{Name: "fixture", Provider: "custom", ClientID: "cid", ClientSecret: "csecret", Scopes: []string{"read"}, Hosts: []string{"api.example:443"}, AuthorizeURL: ts.URL + "/authorize", TokenURL: ts.URL + "/token", RedirectURI: "https://phone.example/oauth/callback"}
	if err := a.SetAuthProfile(edit); err != nil {
		t.Fatal(err)
	}
	auth, err := a.SignInProfile(edit.Name)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(auth)
	edit.ClientSecret = "replacement-secret"
	if err := a.SetAuthProfile(edit); err != nil {
		t.Fatal(err)
	}
	if err := a.OAuthCallback("fixture-code", u.Query().Get("state"), ""); !errors.Is(err, errNoSignIn) {
		t.Fatalf("old consent survived a secret edit: %v", err)
	}
	if err := a.DeleteAuthProfile(edit.Name); err != nil {
		t.Fatal(err)
	}
	if err := a.SetAuthProfile(edit); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SignInProfile(edit.Name); err != nil {
		t.Fatalf("recreated profile cannot sign in: %v", err)
	}
	a.CancelProfileSignIn(edit.Name)
}

// A manual authority owns its return page. Saving a default must not turn that
// inherited URL into an operator override that blocks a later contract update.
func TestManualProfileInheritsItsConfiguredReturn(t *testing.T) {
	f := &profileAuthority{}
	server := f.serve(t)
	defer server.Close()
	a := profileApp(t, server)
	catalog, err := a.oauthContracts()
	if err != nil {
		t.Fatal(err)
	}
	template := catalog["anthropic-claude"]
	if err := a.SetAuthProfile(dashboard.AuthProfileEdit{Name: "manual", Provider: "anthropic-claude", RedirectURI: template.RedirectURI}); err != nil {
		t.Fatal(err)
	}
	profile := a.configSnapshot().Plugins.AuthProfiles["manual"]
	if profile.RedirectURI != "" {
		t.Fatal("inherited return was persisted as an override")
	}
	params, _, err := a.contractFor("manual", profile)
	if err != nil || params.RedirectURI != template.RedirectURI || params.TokenEncoding != template.TokenEncoding {
		t.Fatal("manual profile lost the shared contract")
	}
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	template.RedirectURI = "https://authority.example/updated-return"
	if reg.OAuth == nil {
		reg.OAuth = map[string]oauth.Provider{} // a fresh file carries no block until the operator writes one
	}
	reg.OAuth["anthropic-claude"] = template
	if _, err := saveProvidersFile(a.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	params, _, err = a.contractFor("manual", profile)
	if err != nil || params.RedirectURI != template.RedirectURI {
		t.Fatal("profile ignored the updated contract")
	}
	for _, view := range a.providerViews() {
		if view.Name == "anthropic-claude" {
			if view.SignIn != "manual" || view.RedirectURI != template.RedirectURI {
				t.Fatal("profile form was not given its method and registered return")
			}
			return
		}
	}
	t.Fatal("manual contract missing from profile templates")
}

// THE DISCONNECT DOOR SAYS WHAT HAPPENED. An authority that does not
// confirm revocation leaves the credential removed here and the profile's
// view saying so, with the route; a new consent supersedes the note; a
// confirmed revocation adds nothing; and a delete whose revocation is
// unconfirmed answers with it, since no view is left to say it.
func TestDisconnectSaysWhenTheAuthorityDidNotConfirmRevocation(t *testing.T) {
	f := &profileAuthority{revokeFails: true}
	ts := f.serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	host, _ := url.Parse(ts.URL)
	edit := dashboard.AuthProfileEdit{Name: "acct", Provider: "custom", ClientID: "cid", ClientSecret: "csecret", Scopes: []string{"read"},
		Hosts: []string{host.Host}, AuthorizeURL: ts.URL + "/authorize", TokenURL: ts.URL + "/token", RevokeURL: ts.URL + "/revoke", RedirectURI: "https://dashboard.example/oauth/callback"}
	if err := a.SetAuthProfile(edit); err != nil {
		t.Fatal(err)
	}
	prof := a.configSnapshot().Plugins.AuthProfiles["acct"]
	view := func() dashboard.AuthProfileView {
		t.Helper()
		v := a.authProfileViews(&Config{Plugins: a.configSnapshot().Plugins})
		if len(v) != 1 {
			t.Fatalf("one profile: %+v", v)
		}
		return v[0]
	}
	if v := view(); v.Revocation != "" {
		t.Fatalf("an authority that offers revocation needs no note before a disconnect: %q", v.Revocation)
	}
	if err := oauth.WriteTokenFile(tokenFileOf(t, a, prof), &oauth.Tokens{Access: "at-held", Refresh: "rt-held"}); err != nil {
		t.Fatal(err)
	}

	// The authority refuses: the credential is gone here, and the view says
	// the authority did not confirm, with the route.
	if err := a.DisconnectProfile("acct"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tokenFileOf(t, a, prof)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the credential is removed regardless: %v", err)
	}
	f.mu.Lock()
	asked := append([]string(nil), f.revoked...)
	f.mu.Unlock()
	if len(asked) != 1 || asked[0] != "rt-held" {
		t.Fatalf("the authority was asked to revoke the removed grant: %v", asked)
	}
	v := view()
	if v.State != "disconnected" || !strings.Contains(v.Revocation, "did not confirm") || !strings.Contains(v.Revocation, "account page") {
		t.Fatalf("the view says the authority did not confirm, with the route: %+v", v)
	}
	if b, _ := json.Marshal(v); !strings.Contains(string(b), `"revocation":`) {
		t.Fatalf("the page receives the note: %s", b)
	}

	// A new consent supersedes the note.
	u, err := a.SignInProfile("acct")
	if err != nil {
		t.Fatal(err)
	}
	au, _ := url.Parse(u)
	if err := a.CompleteProfileSignIn("acct", au.Query().Get("redirect_uri")+"?code=the-code&state="+au.Query().Get("state")); err != nil {
		t.Fatal(err)
	}
	if v := view(); v.State != "connected" || v.Revocation != "" {
		t.Fatalf("a reconnected profile carries no note: %+v", v)
	}

	// A confirmed revocation says nothing extra.
	f.mu.Lock()
	f.revokeFails = false
	f.mu.Unlock()
	if err := a.DisconnectProfile("acct"); err != nil {
		t.Fatal(err)
	}
	if v := view(); v.State != "disconnected" || v.Revocation != "" {
		t.Fatalf("a confirmed revocation adds nothing: %+v", v)
	}

	// Delete: the view goes with the profile, so the answer carries it.
	if err := oauth.WriteTokenFile(tokenFileOf(t, a, prof), &oauth.Tokens{Access: "at-again", Refresh: "rt-again"}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.revokeFails = true
	f.mu.Unlock()
	err = a.DeleteAuthProfile("acct")
	if !errors.Is(err, errDeletedUnrevoked) || !strings.Contains(err.Error(), "did not confirm") || !strings.Contains(err.Error(), "account page") {
		t.Fatalf("a delete whose revocation is unconfirmed answers with it: %v", err)
	}
	if _, ok := a.configSnapshot().Plugins.AuthProfiles["acct"]; ok {
		t.Fatal("the profile is deleted all the same")
	}
	a.providers.custody.mu.Lock()
	left := a.providers.custody.unrevoked["acct"]
	a.providers.custody.mu.Unlock()
	if left {
		t.Fatal("a deleted profile's note goes with it")
	}
}

// Microsoft's template names no revocation endpoint (nor do GitHub's or
// Slack's): the view says revocation is not available there, with the
// route, before and after a disconnect; a template that offers revocation
// needs no such note; deleting a profile whose credential was removed
// without revocation answers with the same note, and deleting one that
// never held a credential answers quietly.
func TestATemplateWithoutRevocationSaysSoOnItsView(t *testing.T) {
	ts := (&profileAuthority{}).serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	for _, e := range []dashboard.AuthProfileEdit{
		{Name: "google-cal", Provider: "google", ClientID: "cid", Services: map[string]string{"calendar": "read"}},
		{Name: "ms-mail", Provider: "microsoft", ClientID: "cid", Services: map[string]string{"mail": "read"}},
	} {
		if err := a.SetAuthProfile(e); err != nil {
			t.Fatal(err)
		}
	}
	views := func() map[string]dashboard.AuthProfileView {
		out := map[string]dashboard.AuthProfileView{}
		for _, v := range a.authProfileViews(&Config{Plugins: a.configSnapshot().Plugins}) {
			out[v.Name] = v
		}
		return out
	}
	notAvailable := func(note string) bool {
		return strings.Contains(note, "not available for microsoft") && strings.Contains(note, "account page")
	}
	if v := views(); !notAvailable(v["ms-mail"].Revocation) || v["google-cal"].Revocation != "" {
		t.Fatalf("microsoft says revocation is not available; google says nothing: %+v", v)
	}
	prof := a.configSnapshot().Plugins.AuthProfiles["ms-mail"]
	if err := oauth.WriteTokenFile(tokenFileOf(t, a, prof), &oauth.Tokens{Access: "at-ms", Refresh: "rt-ms"}); err != nil {
		t.Fatal(err)
	}
	if err := a.DisconnectProfile("ms-mail"); err != nil {
		t.Fatal(err)
	}
	if v := views()["ms-mail"]; v.State != "disconnected" || !notAvailable(v.Revocation) {
		t.Fatalf("after a disconnect the view still says so: %+v", v)
	}
	if err := a.DeleteAuthProfile("ms-mail"); !errors.Is(err, errDeletedUnrevoked) || !notAvailable(err.Error()) {
		t.Fatalf("the delete answers with the note its view can no longer show: %v", err)
	}
	if err := a.DeleteAuthProfile("google-cal"); err != nil {
		t.Fatalf("a profile that never held a credential deletes quietly: %v", err)
	}
}

func TestAnOAuthServiceAddedInTheSlotProvidersFileConnectsAndRides(t *testing.T) {
	const service, handle, plugin = "example-notes", "notes", "org.example.notes"
	var mu sync.Mutex
	var revoked, rode []string
	authority := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		f := r.PostForm
		switch {
		case r.URL.Path == "/token" && f.Get("grant_type") == "authorization_code" && f.Get("code") == "the-code" &&
			f.Get("code_verifier") != "" && f.Get("client_id") == "cid" && f.Get("client_secret") == "csecret":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at-notes", "refresh_token": "rt-notes", "expires_in": 3600})
		case r.URL.Path == "/revoke":
			mu.Lock()
			revoked = append(revoked, f.Get("token"))
			mu.Unlock()
		default:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_request"}`)
		}
	}))
	defer authority.Close()
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		rode = append(rode, r.Header.Get("Authorization"))
		mu.Unlock()
		fmt.Fprint(w, `{"notes":[]}`)
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

	// The slot: config.json grants the plugin the handle and the service's
	// host (list grants are the config file's), and nothing else is told.
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")}
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Plugins.Grants = map[string]broker.Grant{plugin: {Hosts: []string{apiHost}, CredentialHandles: []string{handle}}}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.oauthTransport = authority.Client().Transport
	a.oauthGuard = admit(authority)
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "broker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	// The broker starts empty: every grant, profile and contract it uses
	// reaches it through the app's own policy door. It is told where this
	// identity keeps its profiles' own token files, as the app's own
	// start tells it: nowhere else is a token file its to refresh.
	profiles, err := a.credentialsDir()
	if err != nil {
		t.Fatal(err)
	}
	h, err := broker.New(broker.Config{Store: st, ProfilesDir: profiles, Guard: admit(api, authority), Transport: api.Client().Transport})
	if err != nil {
		t.Fatal(err)
	}
	a.pluginOpts = &pluginhost.Options{Broker: h}
	b := h.Bind(plugin, packagefmt.TierT2, []string{"net.outbound:" + apiHost})
	call := func() (status, reason string) {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"operation": "http.get", "target": map[string]string{"url": api.URL + "/v1/notes"}, "arguments": map[string]string{"auth_profile": handle}})
		reply, err := b.Dispatch(context.Background(), "invoke-call", params)
		if err != nil {
			t.Fatal(err)
		}
		var r struct {
			Status string `json:"status"`
			Reason string `json:"reason_code"`
		}
		if err := json.Unmarshal(reply, &r); err != nil {
			t.Fatalf("%s: %v", reply, err)
		}
		return r.Status, r.Reason
	}
	edit := dashboard.AuthProfileEdit{Name: handle, Provider: service, ClientID: "cid", ClientSecret: "csecret",
		Services: map[string]string{"notes": "read"}, RedirectURI: "https://dashboard.example/oauth/callback"}
	if err := a.SetAuthProfile(edit); err == nil {
		t.Fatal("a service no contract names is refused")
	}

	// The third party's whole change: one named contract in the slot's
	// providers.json, the file the host scaffolded on first use.
	if _, err := a.loadProviders(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(a.providersPath())
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	file["oauth"], _ = json.Marshal(map[string]any{service: map[string]any{
		"name": "Example Notes", "authorize_url": authority.URL + "/authorize", "token_url": authority.URL + "/token",
		"revoke_url": authority.URL + "/revoke", "hosts": []string{apiHost},
		"services": map[string]any{"notes": map[string]any{"read": []string{"notes.read"}, "modify": []string{"notes.write"}}},
	}})
	if raw, err = json.MarshalIndent(file, "", "  "); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.providersPath(), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	offered := false
	var names []string
	for _, p := range a.providerViews() {
		names = append(names, p.Name)
		offered = offered || p.Name == service && len(p.Services) == 1 && p.Services[0].Name == "notes" && len(p.Hosts) == 1 && p.Hosts[0] == apiHost
	}
	if !offered {
		t.Fatalf("the page's picker offers the slot's service with its services and host: %v", names)
	}
	if err := a.SetAuthProfile(edit); err != nil {
		t.Fatalf("a profile is made from the slot's contract: %v", err)
	}
	if s, r := call(); s != "denied" || r != "NET_AUTH_PROFILE_DISCONNECTED" {
		t.Fatalf("the broker knows the profile and refuses it by name until consent: %s %s", s, r)
	}

	u, err := a.SignInProfile(handle)
	if err != nil {
		t.Fatal(err)
	}
	au, _ := url.Parse(u)
	q := au.Query()
	if !strings.HasPrefix(u, authority.URL+"/authorize?") || q.Get("client_id") != "cid" || q.Get("scope") != "notes.read" || q.Get("code_challenge") == "" {
		t.Fatalf("consent goes to the slot contract's authority: %s", u)
	}
	if err := a.OAuthCallback("the-code", q.Get("state"), ""); err != nil {
		t.Fatalf("the dashboard callback completes it: %v", err)
	}
	if v := a.authProfileViews(&Config{Plugins: a.configSnapshot().Plugins}); len(v) != 1 || v[0].State != "connected" || v[0].Provider != service {
		t.Fatalf("the view says connected: %+v", v)
	}
	if s, r := call(); s != "succeeded" {
		t.Fatalf("the brokered call succeeds: %s %s", s, r)
	}
	mu.Lock()
	got := append([]string(nil), rode...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "Bearer at-notes" {
		t.Fatalf("the issued token rides to the service's host: %q", got)
	}

	if err := a.DisconnectProfile(handle); err != nil {
		t.Fatal(err)
	}
	if s, r := call(); s != "denied" || r != "NET_AUTH_PROFILE_DISCONNECTED" {
		t.Fatalf("a disconnected profile is refused by name: %s %s", s, r)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(revoked) != 1 || revoked[0] != "rt-notes" || len(rode) != 1 {
		t.Fatalf("the grant is revoked at the contract's endpoint and nothing rides after: revoked %q, rode %q", revoked, rode)
	}
}

func TestARefusedDisconnectIsStillRevoked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory's mode does not refuse a removal on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root removes from a directory at mode 0500")
	}
	f := &profileAuthority{}
	ts := f.serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	host, _ := url.Parse(ts.URL)
	edit := dashboard.AuthProfileEdit{RedirectURI: "https://dashboard.example/oauth/callback", Name: "gh", Provider: "custom", ClientID: "cid", ClientSecret: "csecret", Scopes: []string{"repo"},
		Hosts: []string{host.Host}, AuthorizeURL: ts.URL + "/authorize", TokenURL: ts.URL + "/token", RevokeURL: ts.URL + "/revoke"}
	if err := a.SetAuthProfile(edit); err != nil {
		t.Fatal(err)
	}
	prof := a.configSnapshot().Plugins.AuthProfiles["gh"]
	u, err := a.SignInProfile("gh")
	if err != nil {
		t.Fatal(err)
	}
	au, _ := url.Parse(u)
	if err := a.CompleteProfileSignIn("gh", au.Query().Get("redirect_uri")+"?code=the-code&state="+au.Query().Get("state")); err != nil {
		t.Fatalf("complete: %v", err)
	}
	// The directory the token file resolves to (a new profile names it by
	// name, row 4.5); never the working directory.
	dir := filepath.Dir(tokenFileOf(t, a, prof))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	a.providers.custody.mu.Lock()
	a.providers.custody.unrevoked = map[string]bool{"gh": true}
	a.providers.custody.mu.Unlock()
	if err := a.DisconnectProfile("gh"); err == nil {
		t.Fatal("a removal the directory refuses is answered as refused")
	}
	f.mu.Lock()
	rev := append([]string(nil), f.revoked...)
	f.mu.Unlock()
	if len(rev) != 1 || rev[0] != "rt-code" {
		t.Errorf("the refused disconnect still asked the authority to revoke the refresh token: %v", rev)
	}
	a.providers.custody.mu.Lock()
	still := a.providers.custody.unrevoked["gh"]
	a.providers.custody.mu.Unlock()
	if still {
		t.Error("a revocation the authority confirmed clears the earlier note that it was not revoked")
	}
}

func TestADisconnectRevokesWhatTheFileHeldWhenItWasRemoved(t *testing.T) {
	f := &profileAuthority{}
	ts := f.serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	host, _ := url.Parse(ts.URL)
	edit := dashboard.AuthProfileEdit{RedirectURI: "https://dashboard.example/oauth/callback", Name: "gh", Provider: "custom", ClientID: "cid", ClientSecret: "csecret", Scopes: []string{"repo"},
		Hosts: []string{host.Host}, AuthorizeURL: ts.URL + "/authorize", TokenURL: ts.URL + "/token", RevokeURL: ts.URL + "/revoke"}
	if err := a.SetAuthProfile(edit); err != nil {
		t.Fatal(err)
	}
	u, err := a.SignInProfile("gh")
	if err != nil {
		t.Fatal(err)
	}
	au, _ := url.Parse(u)
	if err := a.CompleteProfileSignIn("gh", au.Query().Get("redirect_uri")+"?code=the-code&state="+au.Query().Get("state")); err != nil {
		t.Fatalf("complete: %v", err)
	}
	tokenRemovalWindow = func(path string) {
		if err := oauth.WriteTokenFile(path, &oauth.Tokens{Access: "at-rotated", Refresh: "rt-rotated"}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { tokenRemovalWindow = nil })
	if err := a.DisconnectProfile("gh"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	rev := append([]string(nil), f.revoked...)
	f.mu.Unlock()
	if len(rev) != 1 || rev[0] != "rt-rotated" {
		t.Errorf("the disconnect revokes the token the file held when it was removed: %v", rev)
	}
}
