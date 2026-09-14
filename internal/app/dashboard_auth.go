package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// .
// .
// .
// .
// .
func (a *App) ensureDashboardToken() {
	a.cfgMu.Lock()
	cfg := a.cfg
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
	// .
	if !cfg.Dashboard.RequireToken && !loopbackBind(cfg.Dashboard.Host) {
		log.Printf("dashboard: bind %q is not loopback and require_token was false — REQUIRING a token, because Host and Origin gates bind a browser and not a direct client", dashboardBindName(cfg.Dashboard.Host))
		cfg.Dashboard.RequireToken = true
		a.cfg = cfg
	}
	if !cfg.Dashboard.RequireToken || cfg.Dashboard.AuthTokenSHA256 != "" {
		a.cfgMu.Unlock()
		return
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		a.cfgMu.Unlock()
		// .
		// .
		log.Printf("dashboard: token mint failed (%v) — access stays refused until a token exists", err)
		return
	}
	token := hex.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(token))
	cfg.Dashboard.AuthTokenSHA256 = hex.EncodeToString(sum[:])
	_, perr := saveConfig(cfg)
	a.cfgMu.Unlock()
	a.mintedTokenMu.Lock()
	a.mintedToken = token
	a.mintedTokenMu.Unlock()
	if perr != nil {
		log.Printf("dashboard: minted token could not be persisted (%v) — it holds for THIS run and is re-minted next boot", perr)
	}
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
	if !writeDashboardToken(dashboardTokenPath(*cfg), token) {
		log.Printf("dashboard: the access token could not be written to %s — the boot console is the only copy", dashboardTokenPath(*cfg))
	}
	log.Printf("dashboard: access token minted; the config keeps its SHA-256")
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
func readDashboardToken(cfg Config) string {
	body, err := os.ReadFile(dashboardTokenPath(cfg))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

// .
// .
// .
func writeDashboardToken(path, token string) bool {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return false
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		f.Close()
		return false
	}
	if err := f.Close(); err != nil {
		return false
	}
	// .
	_ = os.Chmod(path, 0600)
	return true
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
func (a *App) newDashboard(handler *dashboard.WSHandler) *dashboard.Server {
	a.ensureDashboardToken()
	c := a.configSnapshot().Dashboard
	d := dashboard.New(c.Host, c.Port, handler)
	d.SetAccessToken(c.RequireToken, c.AuthTokenSHA256)
	return d
}
