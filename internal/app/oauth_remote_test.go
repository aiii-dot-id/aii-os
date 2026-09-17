package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

func TestSignInCancellationAndReplacementPreventLatePublication(t *testing.T) {
	for _, action := range []string{"cancel", "replace", "alias"} {
		t.Run(action, func(t *testing.T) {
			f := newSignInFixture(t, "codex")
			entered, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				json.NewEncoder(w).Encode(map[string]any{"access_token": testJWT(t, "late"), "refresh_token": "late-refresh", "expires_in": 3600})
			}))
			defer server.Close()
			f.a.oauthTransport = server.Client().Transport
			f.a.oauthGuard = func(context.Context, string) error { return nil }
			reg, err := f.a.loadProviders()
			if err != nil {
				t.Fatal(err)
			}
			reg.Providers[0].CredentialOptions["oauth_token_url"] = server.URL
			if _, err := saveProvidersFile(f.a.providersPath(), reg); err != nil {
				t.Fatal(err)
			}
			auth, err := f.a.SignInProvider(f.name)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- f.a.CompleteSignIn(f.name, f.redirect+"?code=c&state="+stateOf(t, auth)) }()
			<-entered
			if action == "cancel" {
				err = f.a.CancelSignIn(f.name)
			} else if action == "alias" {
				alias := reg.Providers[0]
				alias.Name = "Alias"
				reg.Providers = append(reg.Providers, alias)
				if _, err = saveProvidersFile(f.a.providersPath(), reg); err == nil {
					_, err = f.a.SignInProvider(alias.Name)
				}
			} else {
				_, err = f.a.SignInProvider(f.name)
			}
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err == nil {
				t.Fatal("late completion succeeded")
			}
			dest, _ := f.a.ownedCredentialPath("codex")
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatal("cancelled exchange wrote credentials")
			}
		})
	}
}

func TestDashboardCallbackUsesTheDestinationBoundAtStart(t *testing.T) {
	f := newSignInFixture(t, "codex")
	auth, err := f.a.SignInProvider(f.name)
	if err != nil {
		t.Fatal(err)
	}
	dest, _ := f.a.ownedCredentialPath("codex")
	// .
	// .
	u, _ := url.Parse(auth)
	if u.Query().Get("redirect_uri") != f.redirect {
		t.Fatal("configured callback changed")
	}
	f.a.cfg.Identity.LedgerPath = filepath.Join(t.TempDir(), "another-ledger.jsonl")
	if err := f.a.OAuthCallback("issued-code", u.Query().Get("state"), ""); err != nil {
		t.Fatal(err)
	}
	src, err := oauth.NewOwnedConfigured("codex", dest, oauth.OAuthParams{TokenURL: "https://authority.example/token", ClientID: "configured"})
	if err != nil || !src.Owned() {
		t.Fatalf("bound original destination: %v", err)
	}
	if err := f.a.OAuthCallback("issued-code", u.Query().Get("state"), ""); err == nil {
		t.Fatal("callback replay accepted")
	}
}

func TestConfiguredCredentialFilenameCannotEscapeIdentity(t *testing.T) {
	f := newSignInFixture(t, "codex")
	for _, bad := range []string{"../outside.json", "/outside.json", "C:\\outside.json", "a/b.json", ".."} {
		if _, err := ownedCredentialFile("new-vendor", bad); err == nil {
			t.Fatalf("unsafe filename accepted: %q", bad)
		}
		if _, _, err := f.a.ownedCredential("codex", bad); err == nil {
			t.Fatalf("invalid filename silently permits borrowed credential fallback: %q", bad)
		}
	}
	if got, err := ownedCredentialFile("new-vendor", "my-provider.json"); err != nil || got != "my-provider.json" {
		t.Fatalf("generic file rejected: %v", err)
	}
}

func TestOAuthDefaultsResolveWithoutBeingSavedAsOperatorOverrides(t *testing.T) {
	a := newProvidersApp(t)
	original := providerRegistry{Providers: []providerEntry{{Name: "renamed", Credential: "codex", APIType: "openai"}}}
	if _, err := saveProvidersFile(a.providersPath(), &original); err != nil {
		t.Fatal(err)
	}
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if reg.Providers[0].OAuth != "openai-chatgpt" || reg.Providers[0].signIn.SignIn != "openai_device" {
		t.Fatal("renaming lost the configured contract")
	}
	if _, err := saveProvidersFile(a.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(a.providersPath())
	if err != nil {
		t.Fatal(err)
	}
	var disk providerRegistry
	if err := jsonUnmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	if disk.Providers[0].OAuth != "" || len(disk.OAuth) != 0 {
		t.Fatal("saving pinned shipped OAuth defaults")
	}
	// .
	reg.OAuth = map[string]oauth.Provider{"fixture": {ClientID: "custom", TokenURL: "https://auth.example/token", CredentialFile: "fixture.json", SignIn: "device", DeviceURL: "https://auth.example/device"}}
	reg.Providers = append(reg.Providers, providerEntry{Name: "new", Credential: "file", OAuth: "fixture"})
	if err := bindOAuth(reg); err != nil {
		t.Fatal(err)
	}
	if !canSignIn(reg.Providers[1]) {
		t.Fatal("configured generic provider cannot sign in")
	}
}

// .
func fixtureSignInContext(t *testing.T, a *App) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	a.bgCtx = ctx
	t.Cleanup(cancel)
}
