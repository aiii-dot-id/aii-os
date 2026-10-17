//go:build !windows

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestADashboardThatCannotStartOffersTheDashboardAsShipped(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Shipped")
	a := New(safebootConfig(t, dir, "Shipped", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	ui := filepath.Join(dir, "ui")
	customize := func(name, body string) {
		t.Helper()
		if err := os.RemoveAll(ui); err != nil {
			t.Fatal(err)
		}
		if name == "" {
			return
		}
		full := filepath.Join(ui, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	page := openInChrome(t, chrome, a.dashboard.Origin(), "/")
	await := func(what, predicate string) {
		t.Helper()
		got, err := page.evalRetry(awaitJS(predicate, 20000), true, 30*time.Second, 5)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if got != "ok" {
			t.Fatalf("%s: %s", what, got)
		}
	}
	act := func(js string) {
		t.Helper()
		if _, err := page.eval(`(() => { `+js+`; return 'done'; })()`, false, 10*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	// Started: connected (the presence line has left "connecting"), with
	// nothing saying the page failed. Each predicate names the address it
	// expects, so a look at the page being left behind never answers it.
	started := func(at string) string {
		return `
    if (location.pathname !== '` + at + `') { last = 'at ' + location.pathname; return null; }
    const failed = document.getElementById('boot-failure');
    if (failed) return 'FAIL the page says it did not start: ' + failed.textContent;
    const st = document.getElementById('p-state');
    last = 'presence=' + (st && st.textContent.trim());
    if (!st || st.textContent.trim() === 'connecting') return null;`
	}

	// ---- The control: nothing customized, the page starts and says nothing.
	await("the dashboard starts", started("/")+`
    return document.getElementById('boot-shipped') ? 'FAIL the ordinary page says it is the one as shipped' : 'ok';`)

	for _, c := range []struct{ how, file, body, says string }{
		{"a module without what the app imports from it", "views/chat.js", "export const unrelated = 1;\n", "chat.js"},
		{"a module that does not parse", "util.js", "export function (\n", ""},
		{"an app that throws as it starts", "app.js", "throw new Error('a customized app.js threw as it started');\n", "threw as it started"},
	} {
		customize(c.file, c.body)
		// A mark on the page being left, so only the page loaded after the
		// customization can answer.
		act(`window.__left = true; location.href = '/'`)
		await(c.how+", not approved: the page starts as shipped", `
    if (window.__left) { last = 'the page being left'; return null; }`+started("/")+`
    return 'ok';`)
		sum := sha256.Sum256([]byte(c.body))
		if err := a.approveOverlayScript(dashboard.ApprovedScript{Path: c.file, SHA256: hex.EncodeToString(sum[:])}); err != nil {
			t.Fatalf("%s: the operator's approval: %v", c.how, err)
		}
		act(`location.href = '/'`)
		await(c.how+": the page says it did not start", `
    if (location.pathname !== '/') { last = 'at ' + location.pathname; return null; }
    const box = document.getElementById('boot-failure');
    if (!box) { last = 'no failure panel'; return null; }
    last = box.textContent;
    if (box.getAttribute('role') !== 'alert') return 'FAIL the panel is not an alert';
    if (!box.textContent.includes('The dashboard did not start')) return 'FAIL ' + box.textContent;
    if (!box.textContent.includes('`+c.says+`')) return 'FAIL the panel does not carry the cause: ' + box.textContent;
    const out = [...box.querySelectorAll('a')].find(a => a.textContent === 'Open the dashboard as shipped');
    if (!out || !out.getAttribute('href').startsWith('/shipped/')) return 'FAIL no way out to /shipped/';
    return 'ok';`)
		act(`[...document.querySelectorAll('#boot-failure a')].find(a => a.textContent === 'Open the dashboard as shipped').click()`)
		await(c.how+": the dashboard as shipped starts", started("/shipped/")+`
    const here = document.getElementById('boot-shipped');
    if (!here) { last = 'no note'; return null; }
    if (here.getAttribute('role') !== 'status' || !here.textContent.includes('your scripts are not loaded')) return 'FAIL ' + here.textContent;
    const back = [...here.querySelectorAll('a')].find(a => a.textContent === 'Back to your dashboard');
    if (!back || !back.getAttribute('href').startsWith('/') || back.getAttribute('href').startsWith('/shipped')) return 'FAIL no way back';
    return 'ok';`)
		if _, err := os.Stat(filepath.Join(ui, c.file)); err != nil {
			t.Fatalf("%s: the customization left the disk: %v", c.how, err)
		}
	}

	// ---- Fixed: the customization gone, the way back lands on a page that
	// starts and says nothing.
	customize("", "")
	act(`[...document.querySelectorAll('#boot-shipped a')].find(a => a.textContent === 'Back to your dashboard').click()`)
	await("back on the dashboard", started("/")+`
    return document.getElementById('boot-shipped') ? 'FAIL the ordinary page says it is the one as shipped' : 'ok';`)
}
