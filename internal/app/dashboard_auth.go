package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

const shortAccessToken = 16

func (a *App) ensureDashboardToken() error {
	cfg := a.configSnapshot()
	changed := false
	minted := ""

	if !cfg.Dashboard.RequireToken && !loopbackBind(cfg.Dashboard.Host) {
		logsink.Warn("dashboard.decision", "bind %q is not loopback and require_token was false — REQUIRING a token, because Host and Origin gates bind a browser and not a direct client", dashboardBindName(cfg.Dashboard.Host))
		cfg.Dashboard.RequireToken = true
		changed = true
	}

	required := cfg.Dashboard.RequireToken || a.embeddedAuthentication

	if cfg.Dashboard.LegacyAuthTokenSHA256 != "" {
		if cfg.Dashboard.AccessToken == "" && required {
			logsink.Warn("dashboard.decision", "the access token from the previous format is retired — it was printed on every boot — and a new one is minted; read it with `aii dashboard-token`")
		}
		cfg.Dashboard.LegacyAuthTokenSHA256 = ""
		changed = true
	}
	if cfg.Dashboard.AccessToken != "" && cfg.Dashboard.LegacyAuthTokenSHA256 != "" {
		cfg.Dashboard.LegacyAuthTokenSHA256 = ""
		changed = true
	}

	dormantInvalid := a.embeddedAuthentication && !cfg.Dashboard.RequireToken && validateDashboardAccessToken(cfg.Dashboard.AccessToken) != nil
	if required && (cfg.Dashboard.AccessToken == "" || dormantInvalid) {
		if cfg.Dashboard.AccessToken != "" {
			logsink.Warn("dashboard.decision", "inactive access token cannot be used by login — minting the mobile credential in config.json")
		}
		var err error
		if minted, err = mintDashboardToken(); err != nil {
			return err
		}
		cfg.Dashboard.AccessToken = minted
		changed = true
	}
	if required {

		if n := len(cfg.Dashboard.AccessToken); n < shortAccessToken && !loopbackBind(cfg.Dashboard.Host) {
			logsink.Warn("dashboard.refusal", "the access token in %s is %d bytes long on a network bind; clear dashboard.access_token to mint a strong one", cfg.SourcePath, n)
		}
		if err := validateDashboardAccessToken(cfg.Dashboard.AccessToken); err != nil {
			return fmt.Errorf("dashboard access token in %s: %w; clear dashboard.access_token to mint a new one", cfg.SourcePath, err)
		}
	}
	if changed {
		if _, err := saveConfig(&cfg); err != nil {
			return fmt.Errorf("persist dashboard access token in %s: %w", cfg.SourcePath, err)
		}
		a.cfgMu.Lock()
		*a.cfg = cfg
		a.cfgMu.Unlock()
	}

	if cfg.Dashboard.AccessToken != "" || !cfg.Dashboard.RequireToken {
		path := dashboardTokenPath(cfg)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {

			logsink.Warn("dashboard.error", "the retired token file %s could not be removed (%v); it grants nothing and can be deleted by hand", path, err)
		}
	}
	if minted != "" {
		a.mintedTokenMu.Lock()
		a.mintedToken = minted
		a.mintedTokenMu.Unlock()
		logsink.Info("dashboard.decision", "access token minted and stored in config.json")
	}
	return nil
}

func dashboardTokenPath(cfg Config) string {
	dir := filepath.Dir(cfg.Identity.KeyPath)
	if dir == "" || dir == "." {
		dir = filepath.Dir(cfg.SourcePath)
	}
	if dir == "" || dir == "." {
		return "dashboard-token"
	}
	return filepath.Join(dir, "dashboard-token")
}

func loopbackBind(host string) bool {
	h := strings.TrimSpace(host)
	if h == "" {
		return false
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil {
		return ip.IsLoopback()
	}

	return false
}

func dashboardBindName(host string) string {
	if strings.TrimSpace(host) == "" {
		return "every interface"
	}
	return host
}

func (a *App) DashboardMintedToken() string {
	a.mintedTokenMu.Lock()
	defer a.mintedTokenMu.Unlock()
	t := a.mintedToken
	a.mintedToken = ""
	return t
}

func (a *App) newDashboard(handler *dashboard.WSHandler) (*dashboard.Server, error) {
	if err := a.ensureDashboardToken(); err != nil {
		return nil, err
	}
	c := a.configSnapshot().Dashboard
	d := dashboard.New(c.Host, c.Port, handler)
	d.SetAccessToken(a.embeddedAuthentication || c.RequireToken, c.AccessToken)
	a.setDashboardToken("")
	if d.AccessTokenRequired() {
		a.setDashboardToken(c.AccessToken)
	}
	return d, nil
}

func (a *App) dashboardToken() string {
	a.dashboardTokenMu.Lock()
	defer a.dashboardTokenMu.Unlock()
	return a.dashboardAccessToken
}

func (a *App) StartAuthenticatedEmbedded() error {
	a.embeddedAuthentication = true
	return a.StartEmbedded()
}

var ErrDashboardOrigin = errors.New("dashboard credential requires the current local dashboard origin")

func (a *App) DashboardAccessTokenForOrigin(origin string) (string, error) {
	want, err := url.Parse(a.DashboardURL())
	if err != nil || want.Host == "" {
		return "", ErrDashboardOrigin
	}
	got, err := url.Parse(origin)
	if err != nil || got.User != nil || got.RawQuery != "" || got.Fragment != "" || (got.Path != "" && got.Path != "/") {
		return "", ErrDashboardOrigin
	}
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	if got.Scheme != want.Scheme || got.Hostname() != want.Hostname() || port(got) != port(want) {
		return "", ErrDashboardOrigin
	}
	return a.dashboardToken(), nil
}

func (a *App) setDashboardToken(token string) {
	a.dashboardTokenMu.Lock()
	a.dashboardAccessToken = token
	a.dashboardTokenMu.Unlock()
}

type dashboardTokenRefusal struct{ Requirement string }

func (r *dashboardTokenRefusal) Error() string {
	return "dashboard authentication unchanged: " + r.Requirement + "; keeping the running token and connection"
}

func (a *App) rearmDashboardToken(d DashboardConfig) error {
	if a.dashboard == nil {
		return nil
	}

	bind := a.dashboard.BindHost()
	required := a.embeddedAuthentication || d.RequireToken || !loopbackBind(bind)
	if !required {
		a.pn.mu.Lock()
		if a.pn.relay != nil {
			a.pn.mu.Unlock()
			return &dashboardTokenRefusal{Requirement: "an access token is required while a relay can carry connections"}
		}

		a.dashboard.SetAccessToken(false, d.AccessToken)
		a.pn.mu.Unlock()
		a.setDashboardToken("")
		logsink.Warn("dashboard.decision", "access token no longer required on %s", dashboardBindName(bind))
		return nil
	}
	return a.installDashboardToken(d.AccessToken)
}

func (a *App) installDashboardToken(token string) error {
	if err := validateDashboardAccessToken(token); err != nil {
		return &dashboardTokenRefusal{Requirement: err.Error()}
	}
	a.dashboard.SetAccessToken(true, token)
	a.setDashboardToken(token)
	logsink.Info("dashboard.decision", "a new access token is in force — every browser signs in with it (aii dashboard-token)")
	return nil
}

var (
	errNoTokenToRotate      = errors.New("this dashboard is not asking for an access token, so there is none to rotate — turn it on with Settings → Dashboard → Ask for an access token")
	errTokenAlreadyRequired = errors.New("this dashboard already asks for an access token — there is nothing to turn on; Rotate access token replaces it")
	errDashboardNotServing  = errors.New("the dashboard is not serving, so there is no running server to put a token in force on")
)

func (a *App) rotateDashboardAccess() error  { return a.newDashboardToken(false, saveConfig) }
func (a *App) requireDashboardAccess() error { return a.newDashboardToken(true, saveConfig) }

func (a *App) newDashboardToken(require bool, persist func(*Config) (bool, error)) error {
	token, err := mintDashboardToken()
	if err != nil {
		return err
	}
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	if a.dashboard == nil {
		return errDashboardNotServing
	}
	switch asked := a.dashboard.AccessTokenRequired(); {
	case require && asked:
		return errTokenAlreadyRequired
	case !require && !asked:
		return errNoTokenToRotate
	}
	candidate := *a.cfg
	candidate.Dashboard.AccessToken = token
	act := "rotated"
	if require {
		candidate.Dashboard.RequireToken = true
		act = "asked for"
	}
	published, persistErr := persist(&candidate)
	if persistErr != nil && !published {
		return fmt.Errorf("the access token was not %s: %w", act, persistErr)
	}
	*a.cfg = candidate
	if err := a.installDashboardToken(token); err != nil {
		return err
	}
	if persistErr != nil {
		return fmt.Errorf("the access token was %s and is in force, but the directory durability of config.json is unconfirmed: %w", act, persistErr)
	}
	return nil
}

var (
	errDashboardTokenEmpty      = errors.New("a nonempty access token is required by the running listener")
	errDashboardTokenWhitespace = errors.New("access token has leading or trailing whitespace")
	errDashboardTokenSize       = fmt.Errorf("access token exceeds %d bytes", dashboard.AccessTokenMaxBytes)
)

func validateDashboardAccessToken(token string) error {
	switch {
	case strings.TrimSpace(token) == "":
		return errDashboardTokenEmpty
	case len(token) > dashboard.AccessTokenMaxBytes:
		return errDashboardTokenSize
	case strings.TrimSpace(token) != token:
		return errDashboardTokenWhitespace
	default:
		return nil
	}
}

func mintDashboardToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("dashboard access token mint: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func RotateDashboardToken(path string) (string, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return "", err
	}
	token, err := mintDashboardToken()
	if err != nil {
		return "", err
	}
	cfg.Dashboard.AccessToken = token
	cfg.Dashboard.RequireToken = true
	if _, err := saveConfig(cfg); err != nil {
		return "", err
	}
	return token, nil
}
