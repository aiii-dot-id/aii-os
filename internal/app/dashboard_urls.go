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
	var urls []string
	line := func(url, note string) {
		if url == "" || printed[url] {
			return
		}
		printed[url] = true
		urls = append(urls, url)
		if len(printed) == 1 {
			fmt.Printf("Dashboard: %s  (%s)\n", url, note)
			return
		}
		fmt.Printf("           %s  (%s)\n", url, note)
	}
	line(local, "this machine")
	line(origin, "any device")
	line(addr, "by address; the browser warns — this machine's own certificate")
	a.printAccessLink(urls)
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
// .
// .
// .
func (a *App) printAccessLink(urls []string) {
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
	for _, l := range accessLines(urls, token, dashboardTokenPath(cfg)) {
		fmt.Println(l)
	}
}

// .
// .
func accessLines(urls []string, token, tokenPath string) []string {
	var out []string
	for _, u := range urls {
		if u == "" {
			continue
		}
		label := "          or:"
		if len(out) == 0 {
			label = "Open it here:"
		}
		out = append(out, fmt.Sprintf("%s %s/?token=%s", label, strings.TrimRight(u, "/"), token))
	}
	if len(out) > 0 {
		out = append(out, "              one click sets the cookie; the token then leaves the address bar")
	}
	return append(out, fmt.Sprintf("              the token is %s, kept in %s (0600)", token, tokenPath))
}
