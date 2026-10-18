//go:build !windows

package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestInABrowserAScriptOfTheIdentitysRunsOnlyOnceApproved(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}
	const token = "a-token-of-honest-length-for-this-test-0123456789"
	dir := filepath.Join(t.TempDir(), "ui")
	writeOverlay(t, dir, "custom.css", "#p-name { outline: 3px solid rgb(1, 2, 3); }\n")
	writeOverlay(t, dir, "custom.js", "document.documentElement.dataset.custom = 'v1';\n")
	writeOverlay(t, dir, "inline.html", "<!doctype html><html><head><meta charset=\"utf-8\"></head><body><script>document.documentElement.dataset.inline = 'ran';</script></body></html>\n")
	var s *Server
	s = New("127.0.0.1", 0, &WSHandler{ApproveOverlayScript: func(a ApprovedScript) error {
		s.SetApprovedScripts([]ApprovedScript{a})
		return nil
	}})
	s.SetUIOverlay(dir)

	page := func(want string) string {
		return `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const fail = m => report('FAIL: ' + m);
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  const mark = getComputedStyle(document.getElementById('p-name')).outlineColor;
  if (mark !== 'rgb(1, 2, 3)') return fail('the identity\'s stylesheet does not apply: ' + mark);
  const ran = document.documentElement.dataset.custom || '';
  if (ran !== ` + "'" + want + "'" + `) return fail('custom.js: want ' + JSON.stringify(` + "'" + want + "'" + `) + ', got ' + JSON.stringify(ran));
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`
	}

	t.Run("unapproved, it does not run", func(t *testing.T) {
		runDashboardInEngines(t, s, "/", page(""))
	})

	approveThroughTheRoute(t, s, token, "custom.js")
	t.Run("approved, it runs", func(t *testing.T) {
		runDashboardInEngines(t, s, "/", page("v1"))
	})

	writeOverlay(t, dir, "custom.js", "document.documentElement.dataset.custom = 'v2';\n")
	t.Run("changed, it does not run until approved again", func(t *testing.T) {
		runDashboardInEngines(t, s, "/", page(""))
	})

	t.Run("an inline script never runs", func(t *testing.T) {
		runDashboardInEngines(t, s, "/inline.html", `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
report(document.documentElement.dataset.inline ? 'FAIL: an inline script ran' : 'OK');
`)
	})
}

func workerRig(t *testing.T, s *Server, open bool, driver string) {
	t.Helper()
	engines := resolveEngines()
	if len(engines) == 0 {
		t.Skip("no browser engine available")
	}
	withoutWorkerSrc := func(policy string) string {
		var kept []string
		for _, d := range strings.Split(policy, ";") {
			if !strings.HasPrefix(strings.TrimSpace(d), "worker-src") {
				kept = append(kept, d)
			}
		}
		return strings.Join(kept, ";")
	}
	for _, found := range engines {
		t.Run(found.engine.name, func(t *testing.T) {
			var earlier atomic.Bool
			results := make(chan string, 4)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/__result":
					body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					select {
					case results <- string(body):
					default:
					}
					w.WriteHeader(http.StatusNoContent)
					return
				case "/__earlier", "/__now":
					earlier.Store(r.URL.Path == "/__earlier")
					w.WriteHeader(http.StatusNoContent)
					return
				case "/__driver.js":
					w.Header().Set("Content-Type", "text/javascript")
					io.WriteString(w, driver)
					return
				}
				if earlier.Load() {
					r.Header.Del("Service-Worker") // the build before: the file it names
				}
				if r.URL.Path == "/" && r.Header.Get("Service-Worker") == "" {
					rec := httptest.NewRecorder()
					s.server.Handler.ServeHTTP(rec, r)
					for k, v := range rec.Header() {
						if k != "Content-Length" && k != "Etag" {
							w.Header()[k] = v
						}
					}
					if earlier.Load() || open {
						w.Header().Set("Content-Security-Policy", withoutWorkerSrc(rec.Header().Get("Content-Security-Policy")))
					}
					w.WriteHeader(rec.Code)
					io.WriteString(w, strings.Replace(rec.Body.String(), "</body>", `<script type="module" src="/__driver.js"></script></body>`, 1))
					return
				}
				s.server.Handler.ServeHTTP(w, r)
			}))
			defer srv.Close()
			s.AllowHost(strings.TrimPrefix(srv.URL, "http://"))
			runServerInOneEngine(t, found.engine, found.path, srv.URL+"/", results, 0, 0, 0)
		})
	}
}

func workerFixture(t *testing.T) *Server {
	t.Helper()
	const token = "a-token-of-honest-length-for-this-test-0123456789"
	dir := filepath.Join(t.TempDir(), "ui")
	writeOverlay(t, dir, "worker.js", `self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', e => e.waitUntil(self.clients.claim()));
self.addEventListener('fetch', e => {
  if (new URL(e.request.url).pathname === '/__sw') e.respondWith(new Response('from-worker'));
});
`)
	var s *Server
	s = New("127.0.0.1", 0, &WSHandler{ApproveOverlayScript: func(a ApprovedScript) error {
		s.SetApprovedScripts([]ApprovedScript{a})
		return nil
	}})
	s.SetUIOverlay(dir)
	approveThroughTheRoute(t, s, token, "worker.js")
	return s
}

const workerDriverHead = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const fail = m => report('FAIL: ' + m);
const tick = () => new Promise(r => setTimeout(r, 25));
const until = async (cond, n) => { for (let i = 0; i < n; i++) { if (await cond()) return true; await tick(); } return false; };
const answered = async () => (await fetch('/__sw', { cache: 'no-store' })).text();
`

func TestInABrowserAnApprovedScriptIsNotInstalledAsAServiceWorker(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}
	s := workerFixture(t)
	workerRig(t, s, true, workerDriverHead+`
if (typeof document !== 'undefined') (async () => {
  const served = await fetch('/worker.js', { cache: 'no-store' });
  if (!served.ok || !(await served.text()).includes('from-worker')) return fail('the approved script is not served to the page: ' + served.status);
  if (!('serviceWorker' in navigator)) return report('OK\nthis engine offers a page no service workers');
  await navigator.serviceWorker.register('/worker.js');
  const gone = await until(async () => (await navigator.serviceWorker.getRegistrations()).length === 0, 600);
  const said = await answered();
  if (said === 'from-worker') return fail('the identity\'s script answered the page as a service worker');
  if (!gone) {
    for (const r of await navigator.serviceWorker.getRegistrations()) await r.unregister();
    return fail('the registered worker did not remove itself');
  }
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`)
}

func TestInABrowserAWorkerAnEarlierVersionLetInIsGoneAfterTheNextNavigation(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}
	s := workerFixture(t)
	workerRig(t, s, false, workerDriverHead+`
if (typeof document !== 'undefined') (async () => {
  if (!('serviceWorker' in navigator)) return report('OK\nthis engine offers a page no service workers');
  const phase = sessionStorage.getItem('phase');
  if (!phase) {
    await fetch('/__earlier', { method: 'POST' });
    sessionStorage.setItem('phase', 'earlier');
    location.reload();
    return;
  }
  if (phase === 'earlier') {
    if (!document.querySelector('script[src="/__driver.js"]')) return fail('the earlier page did not load');
    let reg;
    try { reg = await navigator.serviceWorker.register('/worker.js'); }
    catch (e) { return fail('the earlier build\'s page registered no worker: ' + ((e && e.message) || e)); }
    await until(async () => !!(reg.active && navigator.serviceWorker.controller), 400);
    const said = await answered();
    if (said !== 'from-worker') return fail('the earlier build\'s worker did not take the page: ' + said.slice(0, 40));
    await fetch('/__now', { method: 'POST' });
    sessionStorage.setItem('phase', 'now');
    location.reload();
    return;
  }
  const gone = await until(async () => (await navigator.serviceWorker.getRegistrations()).length === 0, 600);
  const said = await answered();
  if (!gone || said === 'from-worker') {
    for (const r of await navigator.serviceWorker.getRegistrations()) await r.unregister();
    return fail('the earlier worker stayed after a navigation: gone=' + gone + ' /__sw=' + said.slice(0, 40));
  }
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`)
}

func approveThroughTheRoute(t *testing.T, s *Server, token, rel string) {
	t.Helper()
	const site = "http://example.test:8443"
	s.AllowHost("example.test:8443")
	s.SetAccessToken(true, token)
	w := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, site+"/auth/token", strings.NewReader(token)))
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if strings.HasPrefix(c.Name, "aii_token_") {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("signing in set no cookie (%d)", w.Code)
	}
	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, site+"/overlay/scripts/file?path="+rel, nil)
	r.AddCookie(cookie)
	s.server.Handler.ServeHTTP(w, r)
	digest := w.Header().Get("X-Script-SHA256")
	if w.Code != http.StatusOK || digest == "" {
		t.Fatalf("the file was not shown: %d %q", w.Code, w.Body)
	}
	body, _ := json.Marshal(ApprovedScript{Path: rel, SHA256: digest})
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, site+"/overlay/scripts/approve", strings.NewReader(string(body)))
	r.Header.Set("Origin", site)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	s.server.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("the approval was refused: %d %q", w.Code, w.Body)
	}
	s.SetAccessToken(false, "")
}

func TestInABrowserTheOperatorApprovesAScriptOnTheCard(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}
	const token = "a-token-of-honest-length-for-this-test-0123456789"
	const v1 = "document.documentElement.dataset.custom = 'v1';\n"
	engines := resolveEngines()
	if len(engines) == 0 {
		t.Skip("no browser engine available")
	}
	for _, found := range engines {
		t.Run(found.engine.name, func(t *testing.T) {
			t.Setenv("AII_BROWSER_ENGINES", found.engine.name)
			approveOnTheCard(t, token, v1)
		})
	}
}

func approveOnTheCard(t *testing.T, token, v1 string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ui")
	writeOverlay(t, dir, "card.html", "<!doctype html><html><head><meta charset=\"utf-8\"></head><body><div id=\"card\"></div></body></html>\n")
	writeOverlay(t, dir, "custom.js", v1)
	digest := digestOf(t, filepath.Join(dir, "custom.js"))
	var s *Server
	s = New("127.0.0.1", 0, &WSHandler{ApproveOverlayScript: func(a ApprovedScript) error {
		s.SetApprovedScripts([]ApprovedScript{a})
		return nil
	}})
	s.SetUIOverlay(dir)
	s.SetAccessToken(true, token)

	runDashboardInEngines(t, s, "/card.html", `
import { scriptsCardHTML, wireScriptsCard } from '/scripts.js';
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const fail = m => report('FAIL: ' + m);
const until = async (ok, what) => {
  for (let i = 0; i < 100; i++) { if (ok()) return; await new Promise(r => setTimeout(r, 50)); }
  throw new Error('waited for ' + what);
};
(async () => {
  const signed = await fetch('/auth/token', { method: 'POST', body: '`+token+`', credentials: 'same-origin' });
  if (signed.status !== 204) return fail('sign-in answered ' + signed.status);
  window.confirm = () => true;
  const root = document.getElementById('card');
  const render = () => { root.innerHTML = scriptsCardHTML(); wireScriptsCard(root, render); };
  render();
  await until(() => root.querySelector('[data-script="custom.js"]'), 'the card to list custom.js');
  if (!root.textContent.includes('awaiting your approval')) return fail('custom.js is not shown as awaiting approval: ' + root.textContent);
  if (!root.textContent.includes('`+digest+`')) return fail('the card does not show the digest of the file');
  if (root.querySelector('[data-script-approve]')) return fail('approval was offered before the file was shown');
  root.querySelector('[data-script-show="custom.js"]').click();
  await until(() => (root.querySelector('[data-script-text]') || {}).textContent === `+"`"+`document.documentElement.dataset.custom = 'v1';\n`+"`"+`, 'the file shown as text');
  if (document.documentElement.dataset.custom) return fail('showing the file ran it');
  root.querySelector('[data-script-approve="custom.js"]').click();
  await until(() => root.querySelector('[data-script-said]'), 'an answer to the approval');
  const said = root.querySelector('[data-script-said]').textContent;
  if (!said.startsWith('Approved')) return fail('the approval was not taken: ' + said);
  await until(() => root.querySelector('[data-script="custom.js"]') && root.querySelector('[data-script="custom.js"]').textContent.startsWith('approved'), 'the row to say approved');
  const served = await (await fetch('/custom.js', { cache: 'no-store' })).text();
  if (served !== `+"`"+`document.documentElement.dataset.custom = 'v1';\n`+"`"+`) return fail('the approved script is not served: ' + JSON.stringify(served));
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`)
}
