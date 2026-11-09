//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/version"
)

// hiddenNav is the drivers' shared reading of the page: whether every
// destination of the navigation is drawn.
const hiddenNav = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const fail = m => report('FAIL: ' + m);
const navDrawn = () => {
  const nav = [...document.querySelectorAll('#nav .nav-item')].filter(el => el.getClientRects().length);
  return nav.length >= 3 && nav.every(el => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0; });
};
`

func TestAStylesheetThatHidesThePageHidesShippedButNotPlain(t *testing.T) {
	hidingOverlay := func(t *testing.T) (string, *Server) {
		dir := filepath.Join(t.TempDir(), "ui")
		writeOverlay(t, dir, "custom.css", "body{display:none}\n")
		writeOverlay(t, dir, "custom.js", "window.__customRan = true;\n")
		s := New("127.0.0.1", 0, &WSHandler{})
		s.SetUIOverlay(dir)
		approveOverlay(t, s, dir, "custom.js")
		s.AllowHost("example.test:8443")
		return dir, s
	}

	t.Run("shipped shows the fault", func(t *testing.T) {
		_, s := hidingOverlay(t)
		runDashboardInEngines(t, s, "/shipped/", hiddenNav+`
(async () => {
  const { ready } = await import('/shipped/recovery.js');
  await ready;
  if (getComputedStyle(document.body).display !== 'none') return fail('the identity\'s custom.css does not apply under /shipped/: body is ' + getComputedStyle(document.body).display);
  if (navDrawn()) return fail('the navigation is drawn under /shipped/ with a custom.css that hides the page');
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`)
	})

	t.Run("plain applies none of it", func(t *testing.T) {
		runDashboardInEachEngine(t, func(t *testing.T) *Server {
			dir, s := hidingOverlay(t)
			t.Cleanup(func() {
				for _, name := range []string{"custom.css", "custom.js"} {
					if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
						t.Errorf("%s is still in place after the set-aside", name)
					}
				}
				aside := filepath.Join(dir, "custom.css.set-aside-"+version.Authored())
				if b, err := os.ReadFile(aside); err != nil || string(b) != "body{display:none}\n" {
					t.Errorf("custom.css was not kept as %s: %q %v", filepath.Base(aside), b, err)
				}
				if code, body := frameGet(t, s, "/custom.css"); code != 200 || body == "body{display:none}\n" {
					t.Errorf("after the set-aside the ordinary address still serves the hiding stylesheet (%d)", code)
				}
			})
			return s
		}, "/plain/", hiddenNav+`
(async () => {
  const { ready } = await import('/plain/recovery.js');
  await ready;
  const failed = document.getElementById('boot-failure');
  if (failed) return fail('the plain dashboard did not start: ' + failed.textContent);
  if (getComputedStyle(document.body).display === 'none') return fail('the identity\'s custom.css applies under /plain/');
  if (!navDrawn()) return fail('the navigation is not drawn under /plain/');
  if (window.__customRan) return fail('custom.js ran under /plain/');
  const strays = performance.getEntriesByType('resource').map(e => new URL(e.name).pathname)
    .filter(p => !p.startsWith('/plain/') && !p.startsWith('/__') && (p.startsWith('/overlay/') || /\.(css|js|html)$/.test(p)));
  if (strays.length) return fail('/plain/ asked for a frame file or the overlay outside /plain/: ' + strays.join(' '));
  const note = document.getElementById('boot-plain');
  if (!note || !note.textContent.includes('Plain: nothing of your changes to this page is applied here, neither scripts nor styles.')) return fail('the note does not say nothing is applied: ' + (note && note.textContent));
  const act = [...note.querySelectorAll('button')].find(b => b.textContent === 'Set aside your changes to this page');
  if (!act) return fail('the note offers no set-aside: ' + note.textContent);
  window.confirm = () => true;
  act.click();
  const until = Date.now() + 10000;
  while (!note.textContent.includes('Set aside: ') && !note.textContent.includes('Not set aside') && Date.now() < until) await new Promise(r => setTimeout(r, 50));
  if (!note.textContent.includes('Set aside: custom.css, custom.js.')) return fail('the set-aside did not say it took custom.css and custom.js: ' + note.textContent);
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`)
	})
}

// The note under /plain/ is a bar in the page's flow like the others: at a
// phone's size the composer, the microphone and every destination stay
// reachable, and it can be dismissed.
func TestThePlainNoteLeavesAPhonePageUsable(t *testing.T) {
	page := recoveryShell(t, "/plain/", unobstructed+`
run(async () => {
  await panelLeavesThePageUsable('boot-plain');
});`)
	runPageInEnginesAtSize(t, page, recoveryModules(t, "export {};\n"), 375, 812)
}
