//go:build !windows

package app

import (
	"strings"
	"testing"
	"time"
)

// THE ASK-FOR-A-TOKEN JOURNEY, with nothing test-shaped inside it: a
// born identity on its live loopback listener, asking no browser for a token;
// two real Chromes on the real page; Settings → Access → Ask every device to
// sign in pressed in the DOM of one. That page must come back on its own —
// the act closed its socket, and the answer carried its cookie — with no
// sign-in asked of the person; the OTHER page, which loaded when nothing was
// asked, must be cut off and ask for the token; config.json and the running
// server must hold the requirement and one new token; the page must show
// no token; and the other page must get back in with it through its form.
//
// Blink only, as the rotation journey: the JOIN is what this proves; the
// control's render in Gecko is the fixture test's
// (TestSettingsOffersAskingForATokenOnlyWhileNoneIsAskedAndPrivate).
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

	asker := openInChrome(t, chrome, origin, "/#/settings/access")
	other := openInChrome(t, chrome, origin, "/#/chat")
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
    return said && said.textContent.includes('On. Every other') && send && !send.disabled ? last : null;`)
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

	// The page shows no token anywhere, and the other page signs in with
	// what aii dashboard-token prints.
	if shown, err := asker.eval(`document.documentElement.outerHTML.includes('`+token+`') ? 'SHOWN' : 'none'`, false, 10*time.Second); err != nil || shown != "none" {
		t.Fatalf("the page that asked every device to sign in shows the token (%q; %v)", shown, err)
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
