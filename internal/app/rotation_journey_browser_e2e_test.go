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

	page := openInChrome(t, chrome, origin, "/#/settings/dashboard")

	if at, err := page.evalRetry(awaitJS(`
    last = 'at ' + location.href + ', ' + document.readyState;
    return location.origin === `+"`"+origin+"`"+` && document.readyState === 'complete' ? last : null;
  `, 60000), true, 70*time.Second, 5); err != nil || strings.HasPrefix(at, "TIMEOUT") {
		t.Fatalf("the dashboard's page never loaded in the browser: %q %v", at, err)
	}
	// The person signs in as the page's own form does: the token to the
	// login route, which writes the cookie; then the page loads again.
	signIn, err := page.evalRetry(`fetch('/auth/token', { method: 'POST', credentials: 'same-origin', body: '`+old+`' }).then(r => String(r.status))`, true, 10*time.Second, 5)
	if err != nil || signIn != "204" {
		t.Fatalf("signing the browser in: %q %v", signIn, err)
	}
	if _, err := page.eval(`location.reload(); 'reloading'`, false, 10*time.Second); err != nil {
		t.Fatalf("reload: %v", err)
	}
	ready, err := page.evalRetry(awaitJS(`
    const b = document.querySelector('[data-token-rotate]'), send = document.getElementById('send-btn');
    last = 'rotate control: ' + !!b + ', socket open: ' + !!(send && !send.disabled);
    return b && send && !send.disabled ? last : null;
  `, 20000), true, 30*time.Second, 5)
	if err != nil || strings.HasPrefix(ready, "TIMEOUT") {
		t.Fatalf("the signed-in page never offered the rotation: %s %v", ready, err)
	}

	if _, err := page.eval(`window.confirm = () => true; document.querySelector('[data-token-rotate]').click(); 'pressed'`, false, 10*time.Second); err != nil {
		t.Fatalf("press: %v", err)
	}
	back, err := page.evalRetry(awaitJS(`
    const said = document.querySelector('[data-token-rotate-said]'), send = document.getElementById('send-btn');
    const asked = !!document.getElementById('dashboard-signin');
    last = 'said: ' + (said ? said.textContent : '-') + ', socket open: ' + !!(send && !send.disabled) + ', sign-in asked: ' + asked;
    if (asked) return 'ASKED ' + last;
    return said && said.textContent.includes('Rotated') && send && !send.disabled ? last : null;
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
	req.Header.Set("Cookie", "aii_token_"+a.dashboard.BoundPort()+"="+hex.EncodeToString(sum[:]))
	res, err := tlsClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the old cookie still signs in after the rotation: %d", res.StatusCode)
	}

	if _, err := page.eval(`document.querySelector('[data-token-reveal]').click(); 'eye'`, false, 10*time.Second); err != nil {
		t.Fatalf("eye: %v", err)
	}
	shown, err := page.evalRetry(awaitJS(`
    const f = document.getElementById('cfg-dtoken');
    last = 'field: ' + (f ? f.value.length + ' chars' : 'absent');
    return f && f.value ? f.value : null;
  `, 10000), true, 20*time.Second, 3)
	if err != nil || shown != rotated {
		t.Fatalf("the eye did not show the new token after the rotation (%d chars; %v)", len(shown), err)
	}
}
