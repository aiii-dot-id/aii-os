package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// ensureDashboardToken (R74) mints the dashboard access token when the
// operator has required one and none exists yet. The raw token exists
// for one moment: printed to the boot log and held in memory for the
// mobile shell's one-time fetch; the config keeps the SHA-256 alone,
// so reading the file never yields the credential.
func (a *App) ensureDashboardToken() {
	a.cfgMu.Lock()
	cfg := a.cfg
	if !cfg.Dashboard.RequireToken || cfg.Dashboard.AuthTokenSHA256 != "" {
		a.cfgMu.Unlock()
		return
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		a.cfgMu.Unlock()
		// Required with no token on record fails CLOSED at the server:
		// every session is refused until a mint succeeds.
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
	// The raw value bypasses the log package on purpose: logsink tees
	// that stream into rotated files under the identity home, and a
	// credential resting in a log defeats keeping only its hash in the
	// config (D77). Stderr is the boot console — shown once, persisted
	// by nothing of ours.
	fmt.Fprintf(os.Stderr, "dashboard: access token minted (R74), shown ONCE on this console — store it now: %s\n", token)
	log.Printf("dashboard: access token minted (R74) — raw value on the boot console only; the config keeps its SHA-256")
}

// DashboardMintedToken returns the R74 token IF this boot minted it
// and nothing has taken it yet, else "" — and CLEARS it as it hands
// it over, so the mobile bind surface's pickup is one-time in fact,
// not just in documentation (D77). Every later caller gets "".
func (a *App) DashboardMintedToken() string {
	a.mintedTokenMu.Lock()
	defer a.mintedTokenMu.Unlock()
	t := a.mintedToken
	a.mintedToken = ""
	return t
}

// newDashboard is the ONE construction path for the operator surface —
// live, firstboot, and SAFE all build here, so R74 access control is
// uniform across every mode the dashboard can serve in.
func (a *App) newDashboard(handler *dashboard.WSHandler) *dashboard.Server {
	a.ensureDashboardToken()
	c := a.configSnapshot().Dashboard
	d := dashboard.New(c.Host, c.Port, handler)
	d.SetAccessToken(c.RequireToken, c.AuthTokenSHA256)
	return d
}
