//go:build !windows

package app

import (
	"strings"
	"testing"
	"time"
)

func TestAskingForATokenJourneyInARealBrowser(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Asking")
	cfg := safebootConfig(t, dir, "Asking", keyPath, ledgerPath, dbPath)
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if a.dashboard.AccessTokenRequired() {
		t.Fatal("the identity already asks for a token")
	}
	origin := a.dashboard.Origin()

	asker := openInChrome(t, chrome, origin, "/#/settings/dashboard")
	other := openInChrome(t, chrome, origin, "/#/home")
	awaitOnPage(t, asker, "the control is offered on a private connection", `
    const b = document.querySelector('[data-token-require]'), send = document.getElementById('send-btn');
    last = 'control: ' + !!b + ', socket open: ' + !!(send && !send.disabled);
    return b && send && !send.disabled ? last : null;`)
	awaitOnPage(t, other, "the other page is connected", `
    const send = document.getElementById('send-btn');
    last = 'socket open: ' + !!(send && !send.disabled);
    return send && !send.disabled ? last : null;`)

	if _, err := asker.eval(`window.confirm = () => true; document.querySelector('[data-token-require]').click(); 'pressed'`, false, 10*time.Second); err != nil {
		t.Fatalf("press: %v", err)
	}
	back := awaitOnPage(t, asker, "the asking page comes back signed in on its own", `
    const said = document.querySelector('[data-token-require-said]'), send = document.getElementById('send-btn');
    const asked = !!document.getElementById('dashboard-signin');
    last = 'said: ' + (said ? said.textContent : '-') + ', socket open: ' + !!(send && !send.disabled) + ', sign-in asked: ' + asked;
    if (asked) return 'ASKED ' + last;
    return said && said.textContent.includes('Asked for') && send && !send.disabled ? last : null;`)
	if strings.HasPrefix(back, "ASKED") {
		t.Fatalf("the asking page was asked for the token it holds: %s", back)
	}
	awaitOnPage(t, other, "the other page is cut off and asks for the token", `
    const form = document.getElementById('dashboard-signin'), send = document.getElementById('send-btn');
    last = 'sign-in form: ' + !!form + ', socket open: ' + !!(send && !send.disabled);
    return form && send && send.disabled ? last : null;`)

	onDisk, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	token := onDisk.Dashboard.AccessToken
	if !onDisk.Dashboard.RequireToken || token == "" || token != a.dashboardToken() || !a.dashboard.AccessTokenRequired() {
		t.Fatal("config.json and the running server do not hold the requirement and one token")
	}

	if _, err := asker.eval(`document.querySelector('[data-token-reveal]').click(); 'eye'`, false, 10*time.Second); err != nil {
		t.Fatalf("eye: %v", err)
	}
	if shown := awaitOnPage(t, asker, "the eye shows the token", `
    const f = document.getElementById('cfg-dtoken');
    last = 'field: ' + (f ? f.value.length + ' chars' : 'absent');
    return f && f.value ? f.value : null;`); shown != token {
		t.Fatalf("the eye showed %d chars, not the token asked for", len(shown))
	}

	if _, err := other.eval(`const f = document.querySelector('#dashboard-signin form');
    f.querySelector('input').value = '`+token+`'; f.dispatchEvent(new Event('submit', { cancelable: true })); 'submitted'`, false, 10*time.Second); err != nil {
		t.Fatalf("sign in: %v", err)
	}
	awaitOnPage(t, other, "the other page gets back in with the token", `
    const send = document.getElementById('send-btn');
    last = 'sign-in form: ' + !!document.getElementById('dashboard-signin') + ', socket open: ' + !!(send && !send.disabled);
    return !document.getElementById('dashboard-signin') && send && !send.disabled ? last : null;`)
}
