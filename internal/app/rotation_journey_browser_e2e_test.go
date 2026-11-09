//go:build !windows

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"
)

// THE ROTATION JOURNEY, with nothing test-shaped inside it: a born
// identity on its live listener with a token required, a real Chrome signed
// in on the real page, Settings → Phone and other devices → Sign out all
// devices pressed in the DOM. The page must come back on its own — the
// rotation closed its socket, and the answer carried its new cookie — with
// no sign-in asked of the person; config.json and the running server must
// hold one new token; the old cookie must open nothing; and the page must
// show neither token.
//
// Blink only, as the projects journey: the JOIN is what this proves; the
// control's render in three engines is the phone page's fixture test's
// (TestPhoneAndOtherDevicesShowsTheCodeAtOnce).
func TestRotationJourneyInARealBrowser(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Rotation")
	cfg := safebootConfig(t, dir, "Rotation", keyPath, ledgerPath, dbPath)
	cfg.Dashboard.RequireToken = true
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	origin := a.dashboard.Origin()
	old := a.dashboardToken()
	if old == "" || !a.dashboard.AccessTokenRequired() {
		t.Fatal("the identity is not asking for a token")
	}

	page := openInChrome(t, chrome, origin, "/#/settings/phone")
	// THE PAGE IS THERE BEFORE IT IS SCRIPTED. The debug target appears as
	// soon as the browser has a tab, which is before the document at the
	// dashboard's origin has loaded; a relative fetch from the blank page
	// before it has no base to resolve against. On a starved host the five
	// one-second tries below all landed in that gap and a landing gate
	// failed (2026-10-13: "Failed to parse URL from /auth/token") with no
	// fault in the product. The wait is on the document, not on a clock.
	if at, err := page.evalRetry(awaitJS(`
    last = 'at ' + location.href + ', ' + document.readyState;
    return location.origin === `+"`"+origin+"`"+` && document.readyState === 'complete' ? last : null;
  `, 60000), true, 70*time.Second, 5); err != nil || strings.HasPrefix(at, "TIMEOUT") {
		t.Fatalf("the dashboard's page never loaded in the browser: %q %v", at, err)
	}
	// The person signs in as the page's own form does: the token to the
	// login route, which writes the cookie and answers the page key the page
	// keeps (pagekey.js); then the page loads again.
	signIn, err := page.evalRetry(`import('/pagekey.js').then(m => m.signIn('`+old+`')).then(r => String(r.status))`, true, 10*time.Second, 5)
	if err != nil || signIn != "204" {
		t.Fatalf("signing the browser in: %q %v", signIn, err)
	}
	if _, err := page.eval(`location.reload(); 'reloading'`, false, 10*time.Second); err != nil {
		t.Fatalf("reload: %v", err)
	}
	ready, err := page.evalRetry(awaitJS(`
    const b = document.querySelector('[data-sign-out-all]'), send = document.getElementById('send-btn');
    last = 'sign-out control: ' + !!b + ', socket open: ' + !!(send && !send.disabled);
    return b && send && !send.disabled ? last : null;
  `, 20000), true, 30*time.Second, 5)
	if err != nil || strings.HasPrefix(ready, "TIMEOUT") {
		t.Fatalf("the signed-in page never offered the rotation: %s %v", ready, err)
	}

	if _, err := page.eval(`window.confirm = () => true; document.querySelector('[data-sign-out-all]').click(); 'pressed'`, false, 10*time.Second); err != nil {
		t.Fatalf("press: %v", err)
	}
	back, err := page.evalRetry(awaitJS(`
    const said = document.querySelector('[data-sign-out-said]'), send = document.getElementById('send-btn');
    const asked = !!document.getElementById('dashboard-signin');
    last = 'said: ' + (said ? said.textContent : '-') + ', socket open: ' + !!(send && !send.disabled) + ', sign-in asked: ' + asked;
    if (asked) return 'ASKED ' + last;
    return said && said.textContent.includes('Every other device is signed out') && send && !send.disabled ? last : null;
  `, 20000), true, 30*time.Second, 3)
	if err != nil || strings.HasPrefix(back, "TIMEOUT") || strings.HasPrefix(back, "ASKED") {
		t.Fatalf("the rotating page did not come back signed in on its own: %s %v", back, err)
	}
	t.Logf("after the rotation: %s", back)

	onDisk, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	rotated := onDisk.Dashboard.AccessToken
	if rotated == old || rotated != a.dashboardToken() {
		t.Fatalf("config.json and the running server do not hold one new token")
	}
	sum := sha256.Sum256([]byte(old))
	req, _ := http.NewRequest(http.MethodGet, origin+"/auth/token", nil)
	req.Header.Set("Cookie", "__Host-aii_token_"+a.dashboard.BoundPort()+"="+hex.EncodeToString(sum[:]))
	res, err := tlsClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the old cookie still signs in after the rotation: %d", res.StatusCode)
	}

	// The page shows neither token.
	shown, err := page.eval(`const html = document.documentElement.outerHTML; html.includes('`+rotated+`') || html.includes('`+old+`') ? 'SHOWN' : 'none'`, false, 10*time.Second)
	if err != nil || shown != "none" {
		t.Fatalf("after the rotation the page shows a token (%q; %v)", shown, err)
	}
}
