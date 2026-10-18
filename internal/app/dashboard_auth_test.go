package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func TestEnsureDashboardTokenPersistsOneOriginalInConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{SourcePath: filepath.Join(dir, "config.json"), Dashboard: DashboardConfig{Host: "127.0.0.1"}}
	a := New(cfg)
	if err := a.ensureDashboardToken(); err != nil {
		t.Fatal(err)
	}
	if cfg.Dashboard.AccessToken != "" {
		t.Fatal("a token was minted without require_token")
	}

	logs := logsink.CaptureForTest(t)
	cfg.Dashboard.RequireToken = true
	if err := a.ensureDashboardToken(); err != nil {
		t.Fatal(err)
	}
	token := cfg.Dashboard.AccessToken
	if len(token) != 64 {
		t.Fatalf("minted token was not stored: %q", token)
	}
	if strings.Contains(logs.String(), token) {
		t.Fatal("the access token reached the persistent log stream")
	}
	body, err := os.ReadFile(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"access_token": "`+token+`"`) {
		t.Fatal("config.json does not contain the dashboard token")
	}
	if strings.Contains(string(body), "auth_token_sha256") {
		t.Fatal("the old verifier was persisted beside the token")
	}

	if err := a.ensureDashboardToken(); err != nil {
		t.Fatal(err)
	}
	if cfg.Dashboard.AccessToken != token {
		t.Fatal("an existing configured token was re-minted")
	}
}

// THE TOKEN RIDES IN NO CONFIG FRAME, AND A ROTATION IS LIVE. The frame
// reaches every socket on every save, which made a stolen cookie worth the
// whole credential; the screen that wants the token asks by name, and a
// new token in the file re-arms the running server at the next reload —
// every cookie minted under the old one stops.
func TestTheDashboardTokenIsAskedForAndRotatesLive(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.KeyPath = filepath.Join(dir, "identity.key")
	cfg.Dashboard.Host = "127.0.0.1"
	cfg.Dashboard.RequireToken = true
	cfg.Dashboard.AccessToken = "the-first-token-of-honest-length"
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	d, err := a.newDashboard(&dashboard.WSHandler{})
	if err != nil {
		t.Fatal(err)
	}
	a.dashboard = d
	if got := a.dashboardToken(); got != "the-first-token-of-honest-length" {
		t.Fatalf("the seam does not answer the running token: %q", got)
	}
	state := a.configState()
	if !state.Dashboard.RequireToken {
		t.Fatal("the page was not told a token is required")
	}
	if raw, _ := json.Marshal(state); strings.Contains(string(raw), "the-first-token") {
		t.Fatalf("the token rides in the configuration frame: %s", raw)
	}
	// ROTATE: the file changes, the reload re-arms the server.
	fresh, err := RotateDashboardToken(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	a.rearmDashboardToken(reloaded.Dashboard)
	if got := a.dashboardToken(); got != fresh || !d.AccessTokenRequired() {
		t.Fatalf("the rotation did not reach the running server: %q required=%v", got, d.AccessTokenRequired())
	}
}

func TestConfiguredDashboardTokenMustFitTheLoginContract(t *testing.T) {
	for _, tc := range []struct {
		name, token, want string
	}{
		{name: "leading whitespace", token: " token", want: "leading or trailing whitespace"},
		{name: "trailing whitespace", token: "token\n", want: "leading or trailing whitespace"},
		{name: "too large", token: strings.Repeat("x", dashboard.AccessTokenMaxBytes+1), want: "exceeds 4096 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := defaultConfig()
			cfg.SourcePath = filepath.Join(dir, "config.json")
			cfg.Identity.KeyPath = filepath.Join(dir, "identity.key")
			cfg.Dashboard.Host = "127.0.0.1"
			cfg.Dashboard.RequireToken = true
			cfg.Dashboard.AccessToken = tc.token
			a := New(cfg)
			if err := a.ensureDashboardToken(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid token error = %v, want %q", err, tc.want)
			}
			if a.dash.dashboardAccessToken != "" {
				t.Fatal("invalid token became the running credential")
			}
		})
	}
}

// A CREDENTIAL THE PREVIOUS FORMAT PRINTED ON EVERY BOOT IS RETIRED, not
// carried forward: it sits in journals, scrolled consoles and browser
// histories. The old pair is dropped, a fresh token is minted, and the
// file that held the printed one is removed.
func TestALegacyDashboardTokenIsRetiredNotCarriedForward(t *testing.T) {
	dir := t.TempDir()
	const printed = "legacy-token-printed-on-every-boot"
	sum := sha256.Sum256([]byte(printed))
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.KeyPath = filepath.Join(dir, "identity.key")
	cfg.Dashboard.Host = "127.0.0.1"
	cfg.Dashboard.RequireToken = true
	cfg.Dashboard.LegacyAuthTokenSHA256 = hex.EncodeToString(sum[:])
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	legacyPath := dashboardTokenPath(*cfg)
	if err := os.WriteFile(legacyPath, []byte(printed+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	if err := a.ensureDashboardToken(); err != nil {
		t.Fatal(err)
	}
	if cfg.Dashboard.AccessToken == "" || cfg.Dashboard.AccessToken == printed || cfg.Dashboard.LegacyAuthTokenSHA256 != "" {
		t.Fatalf("the printed credential was carried forward: %+v", cfg.Dashboard)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("the file that held the printed token remains: %v", err)
	}
	reloaded, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Dashboard.AccessToken != cfg.Dashboard.AccessToken || reloaded.Dashboard.LegacyAuthTokenSHA256 != "" {
		t.Fatal("the fresh token was not durable")
	}
}

// A LEGACY FILE THAT DOES NOT MATCH ITS HASH IS NO LONGER A REASON TO REFUSE
// BOOT: neither half is carried forward, so neither can be wrong.
func TestAMismatchedLegacyFileIsRetiredWithoutRefusingBoot(t *testing.T) {
	dir := t.TempDir()
	sum := sha256.Sum256([]byte("right"))
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.KeyPath = filepath.Join(dir, "identity.key")
	cfg.Dashboard.Host = "127.0.0.1"
	cfg.Dashboard.RequireToken = true
	cfg.Dashboard.LegacyAuthTokenSHA256 = hex.EncodeToString(sum[:])
	legacyPath := dashboardTokenPath(*cfg)
	if err := os.WriteFile(legacyPath, []byte("wrong\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	if err := a.ensureDashboardToken(); err != nil {
		t.Fatalf("a stale legacy file refused the identity its boot: %v", err)
	}
	if cfg.Dashboard.AccessToken == "" || cfg.Dashboard.AccessToken == "wrong" || cfg.Dashboard.LegacyAuthTokenSHA256 != "" {
		t.Fatalf("the legacy pair was not retired: %+v", cfg.Dashboard)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("the stale file remains: %v", err)
	}
}

// A LEFTOVER THAT CANNOT BE REMOVED CARRIES NO AUTHORITY AND REFUSES NO
// BOOT: a directory where the retired file would be is a log line.
func TestAnUnremovableLeftoverTokenFileDoesNotRefuseBoot(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.KeyPath = filepath.Join(dir, "identity.key")
	cfg.Dashboard.Host = "127.0.0.1"
	cfg.Dashboard.RequireToken = true
	cfg.Dashboard.AccessToken = "a-token-of-honest-length"
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	// A non-empty directory in the file's place: os.Remove cannot take it.
	leftover := dashboardTokenPath(*cfg)
	if err := os.MkdirAll(filepath.Join(leftover, "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	if err := a.ensureDashboardToken(); err != nil {
		t.Fatalf("a leftover nobody can use refused the boot: %v", err)
	}
}

func TestBootSaysAShortTokenBehindARelay(t *testing.T) {
	boot := func(mode string) *logsink.Capture {
		t.Helper()
		cfg := &Config{SourcePath: filepath.Join(t.TempDir(), "config.json"), Dashboard: DashboardConfig{Host: "127.0.0.1", RequireToken: true, AccessToken: "fifteen-chars-x"}}
		cfg.Certificate.RouteMode = mode
		logs := logsink.CaptureForTest(t)
		if err := New(cfg).ensureDashboardToken(); err != nil {
			t.Fatal(err)
		}
		if cfg.Dashboard.AccessToken != "fifteen-chars-x" {
			t.Fatal("a short token was replaced: the operator chose it, and rotating is theirs to do")
		}
		return logs
	}
	said := boot("relay")
	if !said.Contains("15 characters") || !said.Contains("relay") || !said.Contains("Rotate access token") {
		t.Fatalf("boot did not say the token is short behind a relay, or how to replace it: %s", said.String())
	}
	if said.Contains("fifteen-chars-x") {
		t.Fatal("the token reached the log")
	}
	if quiet := boot("direct"); quiet.Contains("15 characters") {
		t.Fatalf("a loopback dashboard with no relay was warned about its token: %s", quiet.String())
	}
}
