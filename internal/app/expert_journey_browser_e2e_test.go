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

func TestTheExpertJourneyInARealBrowser(t *testing.T) {
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
	one := openInChrome(t, chrome, origin, "/")
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

		await(page, "the control "+selector, `last = 'absent'; return document.querySelector('`+selector+`') ? 'ok' : null;`)
		if _, err := page.eval(`(() => { document.querySelector('`+selector+`').click(); return 'clicked'; })()`, false, 10*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	// What the page offered before Expert existed, visible or not, and none
	// of it inside what Expert reveals.
	const before = `
    const ids = ['msg-input', 'send-btn', 'chat-substrate-wrap', 'thread', 'composer-wrap', 'mic', 'converse', 'effort'];
    const panel = document.querySelector('#expert-bar [data-expert-panel]');
    const seen = ids.map(id => { const el = document.getElementById(id); return id + ':' + (!!el && el.offsetParent !== null) + ':' + (!!el && panel.contains(el)); });
    `

	await(one, "Expert at the top of Chat", `
    const bar = document.getElementById('expert-bar'), chat = document.getElementById('view-chat');
    last = 'bar hidden=' + (bar && bar.hidden) + ' chat on=' + (chat && chat.classList.contains('on'));
    if (!bar || bar.hidden || !chat.classList.contains('on') || chat.firstElementChild !== bar) return null;
    const substrate = document.getElementById('chat-substrate-wrap'), effort = document.getElementById('effort');
    last = 'providers drawn=' + (substrate.offsetParent !== null) + ' configuration drawn=' + !effort.hidden;
    if (substrate.offsetParent === null || effort.hidden) return null;
    const box = bar.querySelector('[data-expert]');
    if (!box || box.checked || box.offsetParent === null) return null;
    const top = bar.getBoundingClientRect().top, thread = document.getElementById('thread').getBoundingClientRect().top;
    if (top > thread) return 'FAIL the bar is below the thread';
    if (!bar.querySelector('[data-expert-panel]').hidden || !bar.querySelector('[data-route-line]').hidden) return 'FAIL Expert off shows controls or an absent route';
    return 'ok';
  `)
	offBefore, err := one.eval(`(() => {`+before+` return seen.join(','); })()`, false, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range strings.Split(offBefore, ",") { // id:visible:inside Expert's panel
		if strings.HasSuffix(f, ":true") {
			t.Fatalf("an existing control lives inside what Expert reveals: %s", f)
		}
	}

	// ---- Expert on: kept by the host, not this browser.
	click(one, "#expert-bar [data-expert]")
	await(one, "Expert's controls", `
    const panel = document.querySelector('#expert-bar [data-expert-panel]');
    last = panel ? panel.textContent : 'no panel';
    return !panel.hidden && panel.querySelector('[data-notice-route]') && panel.textContent.includes('Quinn on telegram') === false ? 'ok' : null;
  `)
	waitUntil(t, func() bool { return a.configSnapshot().Dashboard.Expert }, "the host kept Expert")
	raw, err := os.ReadFile(a.configSnapshot().SourcePath)
	if err != nil || !strings.Contains(string(raw), `"expert": true`) {
		t.Fatalf("the file does not carry Expert (%v)", err)
	}
	onAfter, err := one.eval(`(() => {`+before+` return seen.join(','); })()`, false, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if onAfter != offBefore {
		t.Fatalf("Expert changed what the page shows besides its own controls:\n off: %s\n  on: %s", offBefore, onAfter)
	}
	two := openInChrome(t, chrome, origin, "/")
	await(two, "a second browser reads Expert from the host", `
    const box = document.querySelector('#expert-bar [data-expert]');
    last = box ? 'checked=' + box.checked : 'no box';
    return box && box.checked && !document.querySelector('#expert-bar [data-expert-panel]').hidden && !Object.keys(localStorage).some(k => /expert/i.test(k)) ? 'ok' : null;
  `)
	if _, err := one.eval(`(() => { location.reload(); return 'reloading'; })()`, false, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	await(one, "Expert survives a reload", `
    const box = document.querySelector('#expert-bar [data-expert]');
    last = box ? 'checked=' + box.checked : 'no box';
    return box && box.checked ? 'ok' : null;
  `)

	// ---- The route: refused in the door's words until a line is yours.
	click(one, "#expert-bar [data-notice-route]")
	await(one, "the door's refusal beside the control", `
    const said = document.querySelector('#expert-bar [data-notice-said]');
    last = said ? said.textContent : 'nothing said';
    return said && !said.hidden && said.textContent.includes('mark a contact line as you first') && !document.querySelector('#expert-bar [data-notice-route]').checked ? 'ok' : null;
  `)
	if a.configSnapshot().Dashboard.NoticesOffDashboard {
		t.Fatal("a refused route is on")
	}
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true)))
	a.dashboard.BroadcastStatus() // as the Settings page's save of the contacts does
	await(one, "the line marked as you", `
    const lines = document.querySelector('#expert-bar [data-notice-lines]');
    last = lines ? lines.textContent : 'no lines';
    return lines && lines.textContent.includes('Quinn on telegram') ? 'ok' : null;
  `)
	click(one, "#expert-bar [data-notice-route]")
	for _, page := range []*cdp{one, two} {
		await(page, "the active route said on every page", `
    const line = document.querySelector('#expert-bar [data-route-line]');
    last = line ? line.textContent : 'no line';
    return line && !line.hidden && line.textContent === 'Your notices also go to Quinn on telegram when no page is open.' ? 'ok' : null;
  `)
	}
	if !a.configSnapshot().Dashboard.NoticesOffDashboard {
		t.Fatal("the route the page kept is not on")
	}

	// ---- Expert off, from the other browser: the controls go, the route stays.
	click(two, "#expert-bar [data-expert]")
	for _, page := range []*cdp{one, two} {
		await(page, "Expert off hides the controls and leaves the route said", `
    const bar = document.getElementById('expert-bar');
    const line = bar.querySelector('[data-route-line]');
    last = 'expert=' + bar.querySelector('[data-expert]').checked + ' panel hidden=' + bar.querySelector('[data-expert-panel]').hidden + ' line=' + line.textContent;
    return !bar.querySelector('[data-expert]').checked && bar.querySelector('[data-expert-panel]').hidden && !line.hidden && line.textContent.includes('Quinn on telegram') ? 'ok' : null;
  `)
	}
	waitUntil(t, func() bool { return !a.configSnapshot().Dashboard.Expert }, "the host kept Expert off")
	if !a.configSnapshot().Dashboard.NoticesOffDashboard {
		t.Fatal("Expert off switched the route off")
	}
}
