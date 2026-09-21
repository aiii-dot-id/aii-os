package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// .
// .
// .
// .
// .
const shortAccessToken = 16

func (a *App) ensureDashboardToken() error {
	cfg := a.configSnapshot()
	changed := false
	minted := ""

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if !cfg.Dashboard.RequireToken && !loopbackBind(cfg.Dashboard.Host) {
		logsink.Warn("dashboard.decision", "bind %q is not loopback and require_token was false — REQUIRING a token, because Host and Origin gates bind a browser and not a direct client", dashboardBindName(cfg.Dashboard.Host))
		cfg.Dashboard.RequireToken = true
		changed = true
	}

	// .
	// .
	// .
	// .
	// .
	// .
	if cfg.Dashboard.LegacyAuthTokenSHA256 != "" {
		if cfg.Dashboard.AccessToken == "" && cfg.Dashboard.RequireToken {
			logsink.Warn("dashboard.decision", "the access token from the previous format is retired — it was printed on every boot — and a new one is minted; read it with `aii dashboard-token`")
		}
		cfg.Dashboard.LegacyAuthTokenSHA256 = ""
		changed = true
	}
	if cfg.Dashboard.AccessToken != "" && cfg.Dashboard.LegacyAuthTokenSHA256 != "" {
		cfg.Dashboard.LegacyAuthTokenSHA256 = ""
		changed = true
	}
	if cfg.Dashboard.RequireToken && cfg.Dashboard.AccessToken == "" {
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return fmt.Errorf("dashboard access token mint: %w", err)
		}
		minted = hex.EncodeToString(raw[:])
		cfg.Dashboard.AccessToken = minted
		changed = true
	}
	if cfg.Dashboard.RequireToken {
		// .
		// .
		// .
		if n := len(cfg.Dashboard.AccessToken); n < shortAccessToken && !loopbackBind(cfg.Dashboard.Host) {
			logsink.Warn("dashboard.refusal", "the access token in %s is %d bytes long on a network bind; clear dashboard.access_token to mint a strong one", cfg.SourcePath, n)
		}
		if len(cfg.Dashboard.AccessToken) > dashboard.AccessTokenMaxBytes {
			return fmt.Errorf("dashboard access token in %s exceeds %d bytes; clear dashboard.access_token to mint a new one", cfg.SourcePath, dashboard.AccessTokenMaxBytes)
		}
		if strings.TrimSpace(cfg.Dashboard.AccessToken) != cfg.Dashboard.AccessToken {
			return fmt.Errorf("dashboard access token in %s has leading or trailing whitespace; clear dashboard.access_token to mint a new one", cfg.SourcePath)
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

	// .
	// .
	// .
	if cfg.Dashboard.AccessToken != "" || !cfg.Dashboard.RequireToken {
		path := dashboardTokenPath(cfg)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			// .
			// .
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

// .
// .
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

// .
// .
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
	// .
	// .
	return false
}

// .
func dashboardBindName(host string) string {
	if strings.TrimSpace(host) == "" {
		return "every interface"
	}
	return host
}

// .
// .
// .
// .
func (a *App) DashboardMintedToken() string {
	a.mintedTokenMu.Lock()
	defer a.mintedTokenMu.Unlock()
	t := a.mintedToken
	a.mintedToken = ""
	return t
}

// .
// .
// .
func (a *App) newDashboard(handler *dashboard.WSHandler) (*dashboard.Server, error) {
	if err := a.ensureDashboardToken(); err != nil {
		return nil, err
	}
	c := a.configSnapshot().Dashboard
	d := dashboard.New(c.Host, c.Port, handler)
	d.SetAccessToken(c.RequireToken, c.AccessToken)
	a.setDashboardToken("")
	if d.AccessTokenRequired() {
		a.setDashboardToken(c.AccessToken)
	}
	return d, nil
}

// .
// .
func (a *App) dashboardToken() string {
	a.dashboardTokenMu.Lock()
	defer a.dashboardTokenMu.Unlock()
	return a.dashboardAccessToken
}

func (a *App) setDashboardToken(token string) {
	a.dashboardTokenMu.Lock()
	a.dashboardAccessToken = token
	a.dashboardTokenMu.Unlock()
}

// .
// .
// .
// .
// .
// .
// .
func (a *App) rearmDashboardToken(d DashboardConfig) {
	if a.dashboard == nil {
		return
	}
	// .
	// .
	// .
	// .
	// .
	// .
	bind := a.dashboard.BindHost()
	required := d.RequireToken || !loopbackBind(bind)
	if required && strings.TrimSpace(d.AccessToken) == "" {
		logsink.Warn("dashboard.refusal", "config reload found no access token where one is required — keeping the running one; clear it only by restarting")
		return
	}
	a.dashboard.SetAccessToken(required, d.AccessToken)
	if a.dashboard.AccessTokenRequired() {
		a.setDashboardToken(d.AccessToken)
		logsink.Info("dashboard.decision", "access token rotated — every signed-in browser signs in again with the new one (aii dashboard-token)")
		return
	}
	a.setDashboardToken("")
	logsink.Warn("dashboard.decision", "access token no longer required on %s", dashboardBindName(bind))
}

// .
// .
// .
func RotateDashboardToken(path string) (string, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return "", err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("mint: %w", err)
	}
	token := hex.EncodeToString(raw[:])
	cfg.Dashboard.AccessToken = token
	cfg.Dashboard.RequireToken = true
	if _, err := saveConfig(cfg); err != nil {
		return "", err
	}
	return token, nil
}
