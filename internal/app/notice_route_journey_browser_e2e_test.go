//go:build !windows

package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// THE NOTICE ROUTE, END TO END (step 4; what the top of Chat's journey
// proved, kept): a born identity's own dashboard in a real Chrome, opened
// fresh at Settings' Messages card, holds the switch that sends the
// operator's notices off the dashboard, drawn whatever any other setting
// says; Chat carries no box that shows or hides it. The route is refused,
// in the door's words, until a line is marked as the operator's; kept by
// the host, not this browser, it is said on every page, survives a reload
// and reaches a second browser with its own storage; switched off from
// that browser, it is off on both.
//
// COVERAGE LIMIT, NAMED: Blink only, as the other journeys; the module's
// rendering in Gecko and WebKit is the dashboard's fixture test's
// (TestTheNoticeRouteIsASwitchOnTheMessagesCard).
func TestTheNoticeRouteJourneyInARealBrowser(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Journey")
	a := New(safebootConfig(t, dir, "Journey", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	installScriptedAdapter(t, a, "org.example.telegram", "telegram", func(context.Context) (tools.Result, error) {
		return tools.Result{Output: `{"receipt":"ok"}`}, nil
	})
	a.channelRoutes(context.Background()) // described, as the sweep describes an installed adapter
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, false)))

	origin := a.dashboard.Origin()
	one := openInChrome(t, chrome, origin, "/#/settings/people")
	await := func(page *cdp, what, predicate string) {
		t.Helper()
		got, err := page.evalRetry(awaitJS(predicate, 20000), true, 30*time.Second, 5)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if got != "ok" {
			t.Fatalf("%s: %s", what, got)
		}
	}
	click := func(page *cdp, selector string) {
		t.Helper()
		// A control is clicked once it is there: the page renders it after the
		// step before, and a loaded host clicks first (2026-09-29, a gate under a
		// load average of seventy: "Cannot read properties of null").
		await(page, "the control "+selector, `last = 'absent'; return document.querySelector('`+selector+`') ? 'ok' : null;`)
		if _, err := page.eval(`(() => { document.querySelector('`+selector+`').click(); return 'clicked'; })()`, false, 10*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	const sw = "[data-notice-route-card] [data-notice-route-switch]"

	// ---- Fresh: the switch on the Messages card, off; no box on Chat.
	await(one, "the switch on the Messages card", `
    const box = document.querySelector('`+sw+`');
    last = box ? 'checked=' + box.checked + ' drawn=' + (box.offsetParent !== null) : 'no switch';
    if (!box || box.checked || box.offsetParent === null) return null;
    if (document.getElementById('expert-bar') || document.querySelector('[data-expert]')) return 'FAIL a box that shows or hides controls is still on the page';
    if (!document.querySelector('[data-notice-route-card] [data-route-line]').hidden) return 'FAIL an absent route is said';
    return 'ok';
  `)

	// ---- The route: refused in the door's words until a line is yours.
	click(one, sw)
	await(one, "the door's refusal beside the switch", `
    const said = document.querySelector('[data-notice-route-card] [data-notice-said]');
    last = said ? said.textContent : 'nothing said';
    return said && !said.hidden && said.textContent.includes('mark a contact line as you first') && !document.querySelector('`+sw+`').checked ? 'ok' : null;
  `)
	if a.configSnapshot().Dashboard.NoticesOffDashboard {
		t.Fatal("a refused route is on")
	}
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true)))
	a.dashboard.BroadcastStatus() // as the Settings page's save of the contacts does
	await(one, "the line marked as you", `
    const lines = document.querySelector('[data-notice-route-card] [data-notice-lines]');
    last = lines ? lines.textContent : 'no lines';
    return lines && lines.textContent.includes('Telegram') ? 'ok' : null;
  `)

	// ---- On: kept by the host, not this browser, and said on every page.
	two := openInChrome(t, chrome, origin, "/#/settings/people")
	await(two, "a second browser's switch", `last = 'absent'; return document.querySelector('`+sw+`') ? 'ok' : null;`)
	click(one, sw)
	for _, page := range []*cdp{one, two} {
		await(page, "the active route said on every page", `
    const line = document.querySelector('[data-notice-route-card] [data-notice-lines]'), box = document.querySelector('`+sw+`');
    last = (line ? line.textContent : 'no line') + ' checked=' + (box && box.checked);
    return line && line.textContent === 'To your lines above, in order: Telegram. This page keeps every notice either way.' && box.checked &&
      !Object.keys(localStorage).some(k => /notice|route|expert/i.test(k)) ? 'ok' : null;
  `)
	}
	waitUntil(t, func() bool { return a.configSnapshot().Dashboard.NoticesOffDashboard }, "the host kept the route")
	raw, err := os.ReadFile(a.configSnapshot().SourcePath)
	if err != nil || !strings.Contains(string(raw), `"notices_off_dashboard": true`) {
		t.Fatalf("the file does not carry the route (%v)", err)
	}
	if _, err := one.eval(`(() => { location.reload(); return 'reloading'; })()`, false, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	await(one, "the route survives a reload", `
    const box = document.querySelector('`+sw+`');
    last = box ? 'checked=' + box.checked : 'no switch';
    return box && box.checked ? 'ok' : null;
  `)

	// ---- Off, from the other browser: off on both, and on the host.
	click(two, sw)
	for _, page := range []*cdp{one, two} {
		await(page, "the route off on every page", `
    const line = document.querySelector('[data-notice-route-card] [data-route-line]'), box = document.querySelector('`+sw+`');
    last = 'checked=' + (box && box.checked) + ' line hidden=' + (line && line.hidden);
    return box && !box.checked && line && line.hidden ? 'ok' : null;
  `)
	}
	waitUntil(t, func() bool { return !a.configSnapshot().Dashboard.NoticesOffDashboard }, "the host kept the route off")
}
