package app

import (
	"fmt"
	"strings"
)

// .
// .
// .
// .
// .
// .
// .
func (a *App) printDashboardURLs() {
	d := a.dashboard
	local, origin, addr := d.LocalURL(), d.Origin(), d.AddressURL()
	printed := map[string]bool{}
	line := func(url, note string) {
		if url == "" || printed[url] {
			return
		}
		printed[url] = true
		if len(printed) == 1 {
			fmt.Printf("Dashboard: %s  (%s)\n", url, note)
			return
		}
		fmt.Printf("           %s  (%s)\n", url, note)
	}
	line(local, "this machine")
	line(origin, "any device")
	line(addr, "by address; the browser warns — this machine's own certificate")
	a.printAccessLink(local)
}

// .
// .
// .
// .
// .
// .
// .
func (a *App) printAccessLink(local string) {
	cfg := a.configSnapshot()
	if !cfg.Dashboard.RequireToken {
		return
	}
	token := readDashboardToken(cfg)
	if token == "" {
		fmt.Printf("Access token: required, and no readable copy is kept at %s.\n", dashboardTokenPath(cfg))
		fmt.Printf("              Remove dashboard.auth_token_sha256 from %s and restart to mint a fresh one.\n", cfg.SourcePath)
		return
	}
	if local != "" {
		fmt.Printf("Open it here: %s/?token=%s\n", strings.TrimRight(local, "/"), token)
		fmt.Printf("              one click sets the cookie; the token then leaves the address bar\n")
	}
	fmt.Printf("              the token is %s, kept in %s (0600)\n", token, dashboardTokenPath(cfg))
}
