//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"path/filepath"
	"strings"
	"testing"
)

// EVERY BUTTON THE PAGE DRAWS IS AT LEAST 24 BY 24 PX (WCAG 2.2, 2.5.8;
// spec section 6 and step 1). The voice menu's button was 18 by 32 and is
// now half of Talk's split button. Measured in the browser, in every view
// the navigation opens, with Talk and its menu drawn as the page draws
// them once voice is set up, at the page's own address, as shipped
// (/shipped/) and with nothing of the identity's applied (/plain/), with
// an identity's stylesheet in the overlay. A button that is not drawn has
// no size and is not counted; the views are opened so that theirs are.
func TestEveryButtonMeetsTheTargetSize(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ui")
	writeOverlay(t, dir, "custom.css", "/* the identity's own styles */\n")
	for _, start := range []string{"/", "/shipped/", "/plain/"} {
		start := start
		name := strings.Trim(start, "/")
		if name == "" {
			name = "page"
		}
		t.Run(name, func(t *testing.T) {
			// A born identity that speaks its replies, so the page draws the
			// conversation's composer (not drawn over first run) and Talk's
			// voice menu in it, as it does once voice is set up.
			s := New("127.0.0.1", 0, &WSHandler{
				GetStats: func() (*StatsResponse, error) { return &StatsResponse{Name: "Mira", ReplyVoice: "Fixture voice"}, nil },
			})
			s.SetUIOverlay(dir)
			runDashboardInEngines(t, s, start, strings.ReplaceAll(targetSizeDriver, "__START__", start))
		})
	}
}

const targetSizeDriver = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const fail = m => report('FAIL: ' + m);
const frames = (n) => new Promise(r => { const step = () => (n-- > 0 ? requestAnimationFrame(step) : r()); step(); });
const until = (cond, ms) => new Promise(r => { const end = Date.now() + ms; const tick = () => (cond() || Date.now() > end) ? r() : setTimeout(tick, 15); tick(); });
const name = (b) => (b.id ? '#' + b.id : '') + (b.className ? '.' + String(b.className).trim().split(/\s+/).join('.') : '') +
  ' "' + (b.getAttribute('aria-label') || b.textContent || '').trim().slice(0, 30) + '"';
(async () => {
  const { ready } = await import('__START__recovery.js');
  await ready;
  const failed = document.getElementById('boot-failure');
  if (failed) return fail('the page did not start: ' + failed.textContent);
  const small = new Set();
  let counted = 0;
  const measure = (where) => {
    for (const b of document.querySelectorAll('button')) {
      if (!b.getClientRects().length || getComputedStyle(b).visibility === 'hidden') continue;
      const r = b.getBoundingClientRect();
      counted++;
      if (r.width < 23.5 || r.height < 23.5) small.add(where + ': ' + name(b) + ' is ' + r.width.toFixed(1) + ' by ' + r.height.toFixed(1));
    }
  };
  await frames(2);
  measure('at rest');
  // Every view, by its address: the four places and the tabs within them.
  const ADDRESS = { chat: '#/chat', home: '#/home', projects: '#/projects', identity: '#/identity/who', memory: '#/identity/beliefs',
    record: '#/identity/record', settings: '#/settings', plugins: '#/settings/permissions' };
  const open = async (view) => {
    location.hash = ADDRESS[view];
    const pane = document.getElementById('view-' + view);
    await until(() => pane && pane.getClientRects().length > 0, 5000);
    await frames(3);
  };
  // Talk and its menu are drawn in the conversation once voice is set up.
  // The identity here speaks its replies (reply_voice), so the page draws
  // the split button itself, on its own status frame; the test waits for
  // the page's drawing and measures it, and nothing it does races that.
  await open('chat');
  const more = document.getElementById('mic-more');
  await until(() => more.getClientRects().length > 0 && more.getBoundingClientRect().width > 0, 5000);
  const split = more.getBoundingClientRect();
  if (split.width < 24 || split.height < 24) return fail('the voice menu button is ' + split.width + ' by ' + split.height);
  measure('chat');
  for (const view of Object.keys(ADDRESS)) {
    await open(view);
    measure(view);
  }
  if (counted < 20) return fail('only ' + counted + ' buttons were drawn and measured; the views did not open');
  if (small.size) return fail('buttons under 24 by 24 px:\n' + [...small].join('\n'));
  report('OK\n' + counted + ' drawn buttons measured, all at least 24 by 24 px');
})().catch(e => fail((e && e.message) || String(e)));
`

// A THEME.JSON STILL APPLIES, WITH EITHER NAME (spec step 1, "wrong if").
// theme.css keeps the old names as aliases of the roles in tokens.css, and
// theme.js sets the role an old name stands for. So a file written with the
// old names colours both what reads them and what reads the roles; one
// written with the roles colours both too; a role the file sets itself is
// never overwritten by an old name; a solid panel wins over a translucent
// one; and taking the file away returns the page to its tokens.
func TestAThemeJSONAppliesThroughOldNamesAndRoles(t *testing.T) {
	read := func(name string) string {
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	page := `<!doctype html><html><head><style>` + read("tokens/tokens.css") + read("theme.css") + `
#old { color: var(--txt); background: var(--panel); }
#role { color: var(--text); background: var(--surface); }
</style></head><body><div id="old">old</div><div id="role">role</div>
<script type="module">
import { run } from '/__harness.js';
import { onTheme } from '/theme.js';
run((assert) => {
  const cs = (id, p) => getComputedStyle(document.getElementById(id))[p];
  const ground = cs('role', 'color');
  onTheme({ tokens: { '--txt': 'rgb(1, 2, 3)' } });
  assert(cs('old', 'color') === 'rgb(1, 2, 3)', 'an old name no longer colours what reads it: ' + cs('old', 'color'));
  assert(cs('role', 'color') === 'rgb(1, 2, 3)', 'an old name does not reach the role it stands for: ' + cs('role', 'color'));
  onTheme({ tokens: { '--text': 'rgb(4, 5, 6)' } });
  assert(cs('old', 'color') === 'rgb(4, 5, 6)' && cs('role', 'color') === 'rgb(4, 5, 6)', 'a role does not colour both: ' + cs('old', 'color') + ' / ' + cs('role', 'color'));
  onTheme({ tokens: { '--txt': 'rgb(1, 2, 3)', '--text': 'rgb(7, 8, 9)' } });
  assert(cs('role', 'color') === 'rgb(7, 8, 9)', 'an old name overwrote the role the file sets itself: ' + cs('role', 'color'));
  onTheme({ tokens: { '--panel': 'rgba(1, 1, 1, 0.5)', '--panel-solid': 'rgb(9, 9, 9)' } });
  assert(cs('role', 'backgroundColor') === 'rgb(9, 9, 9)', 'the solid panel did not win the surface: ' + cs('role', 'backgroundColor'));
  onTheme(null);
  assert(cs('role', 'color') === ground && cs('old', 'color') === ground, 'taking the file away left its colours: ' + cs('role', 'color') + ' / ' + cs('old', 'color'));
  assert(document.documentElement.getAttribute('style') === null || document.documentElement.style.length === 0, 'taking the file away left properties on the page: ' + document.documentElement.getAttribute('style'));
});
</script></body></html>`
	runPageInEngines(t, page, nil)
}

// AN IDENTITY'S OWN COPY OF index.html, MADE BEFORE tokens.css, STILL
// GETS THE ROLES. Its copy links theme.css and not tokens.css, and never
// receives upgrades (internal/app/overlay_README.md); theme.css imports
// tokens.css itself, so the old names it aliases still resolve: the paper
// is the paper and the text is the text, rather than nothing.
func TestAnOldIndexInTheOverlayStillGetsTheRoles(t *testing.T) {
	shell, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	const link = `<link rel="stylesheet" href="./tokens/tokens.css">` + "\n"
	if !strings.Contains(string(shell), link) {
		t.Fatal("the shipped index.html no longer links tokens.css as this test expects")
	}
	dir := filepath.Join(t.TempDir(), "ui")
	writeOverlay(t, dir, "index.html", strings.Replace(string(shell), link, "", 1))
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetUIOverlay(dir)
	runDashboardInEngines(t, s, "/", `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const fail = m => report('FAIL: ' + m);
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  if (document.querySelector('link[href$="tokens/tokens.css"]')) return fail('the overlay index.html still links tokens.css; the test proves nothing');
  document.documentElement.setAttribute('data-theme', 'light');
  const cs = getComputedStyle(document.body);
  if (cs.backgroundColor !== 'rgb(245, 242, 236)') return fail('the page is not on paper under an old index.html: ' + cs.backgroundColor);
  if (cs.color !== 'rgb(26, 25, 22)') return fail('the text role does not reach the page under an old index.html: ' + cs.color);
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`)
}
