package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

func TestShellAuthenticationUsesTheRunningOwnerAcrossRotation(t *testing.T) {
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	cfg.Identity.KeyPath = filepath.Join(filepath.Dir(cfg.SourcePath), "identity.sec")
	cfg.Dashboard = DashboardConfig{Host: "127.0.0.1", Port: 0}
	a := New(cfg)
	a.embeddedAuthentication = true
	d, err := a.newDashboard(&dashboard.WSHandler{})
	if err != nil {
		t.Fatal(err)
	}
	a.dashboard = d
	if _, err := d.Start(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })
	base := a.DashboardURL()
	origin := strings.TrimSuffix(base, "/")
	first, err := a.DashboardAccessTokenForOrigin(origin)
	if err != nil || first == "" || !d.AccessTokenRequired() {
		t.Fatal("shell has no usable required credential", err)
	}
	for _, wrong := range []string{"", "null", "https" + strings.TrimPrefix(origin, "http"), "http://localhost:" + d.BoundPort(), "http://127.0.0.1:1", origin + "/auth/token", origin + "?x=1", origin + "#x", strings.Replace(origin, "://", "://attacker@", 1)} {
		if token, err := a.DashboardAccessTokenForOrigin(wrong); token != "" || !errors.Is(err, ErrDashboardOrigin) {
			t.Fatalf("credential escaped to %q: %v", wrong, err)
		}
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	status := func(want int) {
		t.Helper()
		r, err := client.Get(base + "auth/token")
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != want {
			t.Fatalf("authentication status %d, want %d", r.StatusCode, want)
		}
	}
	login := func() string {
		t.Helper()
		token, err := a.DashboardAccessTokenForOrigin(origin)
		if err != nil {
			t.Fatal(err)
		}
		r, err := client.Post(base+"auth/token", "text/plain", strings.NewReader(token))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 204 || len(r.Cookies()) != 1 || !r.Cookies()[0].HttpOnly {
			t.Fatal("ordinary login did not issue its HttpOnly cookie")
		}
		return token
	}
	status(401)
	login()
	status(204)
	if err := a.rearmDashboardToken(DashboardConfig{RequireToken: false, AccessToken: "rotated-token"}); err != nil {
		t.Fatal(err)
	}
	if !d.AccessTokenRequired() {
		t.Fatal("mobile loopback was downgraded")
	}
	status(401)
	// The file may describe a refused/future setting; never hand that token
	// to the shell instead of the one the serving dashboard actually accepts.
	a.cfgMu.Lock()
	a.cfg.Dashboard.AccessToken = "not-serving"
	a.cfgMu.Unlock()
	if token := login(); token != "rotated-token" {
		t.Fatal("shell received a stale or future credential")
	}
	status(204)
	if err := a.rearmDashboardToken(DashboardConfig{}); err == nil {
		t.Fatal("missing credential relaxed mobile admission")
	}
	status(204)
	for _, invalid := range []string{" bad", "bad ", strings.Repeat("x", dashboard.AccessTokenMaxBytes+1)} {
		if err := a.rearmDashboardToken(DashboardConfig{AccessToken: invalid}); err == nil {
			t.Fatal("unusable rotation replaced a working token")
		}
		status(204)
	}
	jar, _ = cookiejar.New(nil)
	client.Jar = jar // expired/removed cookie
	status(401)
	login()
	status(204)
	// A direct client can forge both headers but cannot use the in-process bridge.
	r, _ := http.NewRequest(http.MethodGet, base+"auth/token", nil)
	r.Header.Set("Origin", origin)
	reply, err := (&http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	reply.Body.Close()
	if reply.StatusCode != 401 {
		t.Fatal("another local client was admitted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(cfg.SourcePath), "dashboard-token")); !os.IsNotExist(err) {
		t.Fatal("a second credential file exists")
	}
}

func TestMobileAdmissionDoesNotRewriteDesktopOrRollbackPolicy(t *testing.T) {
	for _, token := range []string{"", "usable-dormant-token", " leading", "trailing ", "\t", strings.Repeat("x", dashboard.AccessTokenMaxBytes+1)} {
		t.Run("dormant", func(t *testing.T) {
			cfg := defaultConfig()
			cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
			cfg.Identity.KeyPath = filepath.Join(filepath.Dir(cfg.SourcePath), "identity.sec")
			cfg.Dashboard = DashboardConfig{Host: "127.0.0.1", AccessToken: token}
			if _, err := saveConfig(cfg); err != nil {
				t.Fatal(err)
			}
			a := New(cfg)
			a.embeddedAuthentication = true
			d, err := a.newDashboard(&dashboard.WSHandler{})
			if err != nil {
				t.Fatal(err)
			}
			if !d.AccessTokenRequired() || validateDashboardAccessToken(a.dashboardToken()) != nil {
				t.Fatal("mobile has no usable authenticated route")
			}
			if cfg.Dashboard.RequireToken {
				t.Fatal("mobile rewrote the operator's desktop/rollback policy")
			}
			if token == "usable-dormant-token" && cfg.Dashboard.AccessToken != token {
				t.Fatal("valid existing token was replaced")
			}
			reloaded, err := LoadConfig(cfg.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.Dashboard.RequireToken || reloaded.Dashboard.AccessToken != a.dashboardToken() {
				t.Fatal("persisted config changes the old reader's policy")
			}
			desktop := New(reloaded)
			plain, err := desktop.newDashboard(&dashboard.WSHandler{})
			if err != nil {
				t.Fatal(err)
			}
			if plain.AccessTokenRequired() {
				t.Fatal("desktop inherited mobile-only enforcement")
			}
		})
	}
}

func TestMobileTokenPersistenceFailureDoesNotPublishOrChangePolicy(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.KeyPath = filepath.Join(dir, "identity.sec")
	cfg.Dashboard = DashboardConfig{Host: "127.0.0.1"}
	if err := os.Mkdir(cfg.SourcePath, 0700); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.embeddedAuthentication = true
	if d, err := a.newDashboard(&dashboard.WSHandler{}); d != nil || err == nil {
		t.Fatal("undurable credential was published")
	}
	if cfg.Dashboard.RequireToken || cfg.Dashboard.AccessToken != "" || a.dashboardToken() != "" {
		t.Fatal("failed persistence changed the configuration or published a bearer")
	}
	if err := os.Remove(cfg.SourcePath); err != nil {
		t.Fatal(err)
	}
	d, err := a.newDashboard(&dashboard.WSHandler{})
	if err != nil || !d.AccessTokenRequired() || a.dashboardToken() == "" {
		t.Fatal("repairing the write failure did not recover admission", err)
	}
	if cfg.Dashboard.RequireToken {
		t.Fatal("retry changed desktop/rollback policy")
	}
}

func TestAuthenticatedShellKeepsLiveSafeAndRestoredIdentityAccess(t *testing.T) {
	for _, mode := range []string{"live", "safe", "restored"} {
		t.Run(mode, func(t *testing.T) {
			rig := newRestoreRig(t)
			rig.cfg.Genesis = GenesisConfig{ServerURL: "http://127.0.0.1:1", FirewallURL: "http://127.0.0.1:1", BootstrapURL: "http://127.0.0.1:1"}
			rig.cfg.Certificate.ServerURL = "none"
			if mode == "restored" {
				_, snap := rig.lived(t, 1, "before", true)
				rig.lived(t, 1, "after", false)
				a := rig.boot(t)
				plan, err := continuityDoor(a).RestorePlan(continuity.SourceSnapshot, snap)
				if err == nil {
					err = continuityDoor(a).Restore(continuity.SourceSnapshot, snap, plan.ConfirmText)
				}
				stopApp(t, a)
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "safe" {
				tamperChain(t, rig.keyPath, rig.ledgerPath)
			}
			cfg := rig.cfg
			cfg.Dashboard.Host, cfg.Dashboard.Port, cfg.Dashboard.TLS = "127.0.0.1", 0, false
			cfg.Certificate.ServerURL = "none"
			cfg.Dashboard.RequireToken = false
			var token string
			for boot := 0; boot < 2; boot++ {
				a := New(cfg)
				a.ring5Content = genesistest.NewRoot(t).Ring5(t)
				if err := a.StartAuthenticatedEmbedded(); err != nil {
					stopApp(t, a)
					t.Fatal(err)
				}
				func() {
					defer stopApp(t, a)
					_, safe := a.SafeMode()
					if safe != (mode == "safe") {
						t.Fatalf("unexpected SAFE posture %v", safe)
					}
					base := a.DashboardURL()
					got, err := a.DashboardAccessTokenForOrigin(strings.TrimSuffix(base, "/"))
					if err != nil || got == "" {
						t.Fatal("credential unavailable", err)
					}
					if boot == 0 {
						token = got
					} else if got != token {
						t.Fatal("restart replaced the credential")
					}
					client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
					reply, err := client.Get(base + "auth/token")
					if err != nil {
						t.Fatal(err)
					}
					reply.Body.Close()
					if reply.StatusCode != 401 {
						t.Fatal("unauthenticated client admitted")
					}
					reply, err = client.Post(base+"auth/token", "text/plain", strings.NewReader(got))
					if err != nil {
						t.Fatal(err)
					}
					reply.Body.Close()
					if reply.StatusCode != 204 || len(reply.Cookies()) != 1 {
						t.Fatal("shell could not sign in")
					}
				}()
			}
		})
	}
}
