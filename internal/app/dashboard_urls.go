package app

import (
	"fmt"
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
}
