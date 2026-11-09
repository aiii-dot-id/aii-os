//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE DECORATION IS GONE (spec section 4 and step 2): no sky, no stars, no
// gradient fill and no glow, and Send is the solid accent. The shipped
// stylesheets and page use none of the names that drew them, theme.css
// keeps each name only as its solid stand-in for an identity's own copy of
// layout.css (spec section 0, overlay compatibility), and the page as it
// ships, at its own address and as shipped, draws none of the elements.
func TestTheDecorationIsGone(t *testing.T) {
	for _, name := range []string{"static/tokens/tokens.css", "static/theme.css", "static/layout.css", "static/index.html"} {
		b, err := staticFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, gone := range []string{"var(--glow)", "var(--grad", "var(--sky)", "var(--stars)", `class="sky"`, `class="stars`} {
			if strings.Contains(string(b), gone) {
				t.Errorf("%s still holds %s", name, gone)
			}
		}
	}
	theme, err := staticFS.ReadFile("static/theme.css")
	if err != nil {
		t.Fatal(err)
	}
	kept := tokenBlock(t, string(theme), ":root {")
	for name, want := range map[string]string{"--grad": "var(--acc)", "--grad-hot": "var(--acc)", "--glow": "0 0 0 transparent", "--sky": "none", "--stars": "none"} {
		if got, ok := kept[name]; !ok || got != want {
			t.Errorf("theme.css: %s is %q, not its stand-in %q", name, got, want)
		}
	}
	for _, start := range []string{"/", "/shipped/"} {
		start := start
		t.Run(strings.Trim("page"+start, "/"), func(t *testing.T) {
			s := New("127.0.0.1", 0, &WSHandler{})
			runDashboardInEngines(t, s, start, strings.ReplaceAll(`
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const fail = m => report('FAIL: ' + m);
(async () => {
  const { ready } = await import('__START__recovery.js');
  await ready;
  const left = document.querySelectorAll('.sky, .stars, .stars2');
  if (left.length) return fail(left.length + ' decorative elements are still drawn');
  location.hash = '#/chat';
  for (let i = 0; i < 300 && !(document.getElementById('view-chat') || {}).classList?.contains('on'); i++) await new Promise(r => setTimeout(r, 15));
  const send = document.getElementById('send-btn');
  const probe = document.createElement('i'); probe.style.color = 'var(--accent)'; document.body.appendChild(probe);
  for (const theme of ['light', 'dark']) {
    document.documentElement.setAttribute('data-theme', theme);
    const cs = getComputedStyle(send), accent = getComputedStyle(probe).color;
    if (cs.backgroundImage !== 'none') return fail(theme + ': Send is drawn with ' + cs.backgroundImage);
    if (cs.backgroundColor !== accent) return fail(theme + ': Send is ' + cs.backgroundColor + ', not the accent ' + accent);
    if (cs.boxShadow !== 'none') return fail(theme + ': Send glows: ' + cs.boxShadow);
  }
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`, "__START__", start))
		})
	}
}

// AN IDENTITY'S OWN COPY OF layout.css, FROM BEFORE THIS STEP, STILL PAINTS
// ITS WORDS (spec section 0: old token names stay as aliases). 0.2.0's
// layout.css draws the identity's name in a message, the greeting and a
// first-run heading as transparent text over --grad or --grad-hot, and
// glows with --glow. Kept in the overlay under the new theme, each of those
// words is painted in the accent, and nothing glows.
func TestAKeptLayoutStillPaintsItsWords(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ui")
	b, err := os.ReadFile(filepath.Join("testdata", "overlay-0.2.0", "layout.css"))
	if err != nil {
		t.Fatal(err)
	}
	writeOverlay(t, dir, "layout.css", string(b))
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetUIOverlay(dir)
	runDashboardInEngines(t, s, "/", `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const fail = m => report('FAIL: ' + m);
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  const sheet = [...document.styleSheets].find(x => (x.href || '').endsWith('/layout.css'));
  let kept = false; try { kept = [...sheet.cssRules].some(r => (r.cssText || '').includes('var(--grad)')); } catch (e) {}
  if (!kept) return fail('the page is not running the kept layout.css');
  const box = document.createElement('div');
  box.innerHTML = '<div class="msg identity"><span class="who">Mira</span></div><div class="greet">Good evening</div><div class="fb-card"><h2>Welcome</h2></div><button class="home-talk" type="button">Talk</button>';
  document.body.appendChild(box);
  const probe = document.createElement('i'); probe.style.color = 'var(--accent)'; document.body.appendChild(probe);
  for (const theme of ['light', 'dark']) {
    document.documentElement.setAttribute('data-theme', theme);
    const accent = getComputedStyle(probe).color;
    for (const sel of ['.msg.identity .who', '.greet', '.fb-card h2']) {
      const cs = getComputedStyle(box.querySelector(sel));
      const paint = cs.color === 'rgba(0, 0, 0, 0)' || cs.color === 'transparent' ? cs.backgroundColor : cs.color;
      if (paint !== accent) return fail(theme + ': ' + sel + ' is painted ' + paint + ' (colour ' + cs.color + ', background ' + cs.backgroundColor + ' ' + cs.backgroundImage + '), not the accent ' + accent);
    }
    const glow = getComputedStyle(box.querySelector('.home-talk')).boxShadow;
    if (glow !== 'none' && !/^(rgba\(0, 0, 0, 0\)|transparent) 0px 0px 0px( 0px)?$|^0px 0px 0px( 0px)? (rgba\(0, 0, 0, 0\)|transparent)$/.test(glow)) return fail(theme + ': the kept layout still glows: ' + glow);
  }
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`)
}
