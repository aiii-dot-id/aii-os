//go:build !windows

// Runs through the unix browser-engine rig (browserengine_test.go, same tag).

package dashboard

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// keyRig serves s to one engine at its own loopback port, signs the browser
// in by an access code's link (the real link page, as a launcher and a
// scanned code open it), and answers what the rig's own paths say:
// /__result fills results, /__harness.js and /__driver.js are the page's
// test modules, and "/" is the driver's page when driver is not empty, the
// dashboard's own index.html otherwise. stubs, when given, stand in for the
// dashboard's own modules of those paths.
func keyRig(t *testing.T, s *Server, driver string, results chan string, stubs ...map[string]string) (bound, start string) {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/__result":
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			select {
			case results <- string(body):
			default:
			}
			rw.WriteHeader(http.StatusNoContent)
			return
		case "/__harness.js", "/__driver.js":
			rw.Header().Set("Content-Type", "text/javascript")
			rw.Header().Set("Cache-Control", "no-store")
			if r.URL.Path == "/__harness.js" {
				io.WriteString(rw, harnessJS)
			} else {
				io.WriteString(rw, driver)
			}
			return
		case "/":
			if driver != "" {
				rw.Header().Set("Content-Type", "text/html; charset=utf-8")
				rw.Header().Set("Content-Security-Policy", uiCSP)
				io.WriteString(rw, `<!doctype html><html><body><script type="module" src="/__driver.js"></script></body></html>`)
				return
			}
		}
		if len(stubs) > 0 {
			if stub, ok := stubs[0][r.URL.Path]; ok {
				rw.Header().Set("Content-Type", "text/javascript")
				io.WriteString(rw, stub)
				return
			}
		}
		s.server.Handler.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)
	bound = srv.Listener.Addr().String()
	s.AllowHost(bound)
	srv.Start()
	st, err := s.issueAccessCode(s.auth.Load(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	return bound, srv.URL + accessCodeLink("", st.Code)
}

func TestAPageOnAnotherPortGetsTheCookieAndCannotAct(t *testing.T) {
	engines := resolveEngines()
	if len(engines) == 0 {
		skipWithoutEngines(t)
	}
	for _, found := range engines {
		t.Run(found.engine.name, func(t *testing.T) {
			s := New("127.0.0.1", 0, &WSHandler{})
			s.SetAccessToken(true, pageKeyTestToken)
			s.refusedPause = func(time.Duration) {}

			// The listener on another port: it keeps the cookie the browser
			// sends it, and its page reports what its own storage holds.
			results := make(chan string, 4)
			var mu sync.Mutex
			var given []*http.Cookie
			other := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/__result":
					body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					select {
					case results <- string(body):
					default:
					}
					rw.WriteHeader(http.StatusNoContent)
				default:
					mu.Lock()
					given = append(given, r.Cookies()...)
					mu.Unlock()
					rw.Header().Set("Content-Type", "text/html; charset=utf-8")
					io.WriteString(rw, `<!doctype html><body><script>
let key = null; try { key = localStorage.getItem('aii.signin') || sessionStorage.getItem('aii.signin'); } catch (e) {}
fetch('/__result', { method: 'POST', body: key ? 'FAIL: a page on another port read the page key' : 'OK' });
</script></body>`)
				}
			}))
			defer other.Close()

			driver := `
import { report, assert, until } from '/__harness.js';
import { pageKey, signedFetch } from '/pagekey.js';
// No report on success: the verdict is the other port's page's, after the
// navigation below.
(async () => {
  const key = pageKey();
  assert(key && key.startsWith('pk1.'), 'the link page left no page key in this page\'s storage');
  // The socket with its key: the session begins and answers.
  let framed = false, closed = 0;
  const socket = new WebSocket('ws://' + location.host + '/ws?interaction_version=1');
  socket.onopen = () => { socket.send(JSON.stringify({ type: 'hello', page_key: key })); socket.send(JSON.stringify({ type: 'probe', request_id: 'p' })); };
  socket.onmessage = e => { if (JSON.parse(e.data).type === 'probe') framed = true; };
  socket.onclose = e => { closed = e.code; };
  await until(() => framed || closed, 15000);
  assert(framed, 'the signed-in socket was refused: ' + closed);
  socket.close();
  // An acting route with both halves passes the wall (the identity here
  // has no speaking service, which is the handler's answer, not the wall's).
  const said = await signedFetch('/speech/say', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{"text":"hi"}' });
  assert(said.status !== 401, 'the acting route refused both halves');
  const bare = await fetch('/speech/say', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{"text":"hi"}' });
  assert(bare.status === 401 && bare.headers.get('Aii-Missing') === 'page-key', 'the page\'s own cookie alone was taken: ' + bare.status);
  location.href = '` + other.URL + `/';
})().catch(e => report('FAIL: ' + ((e && e.message) || String(e))));
`
			bound, start := keyRig(t, s, driver, results)
			runServerInOneEngine(t, found.engine, found.path, start, results, 0, 0, 0)

			mu.Lock()
			var cookie string
			for _, c := range given {
				if c.Name == cookieName(bound) {
					cookie = c.Value
				}
			}
			mu.Unlock()
			if cookie == "" {
				t.Fatal("the browser sent the listener on another port no dashboard cookie: the fault this test is about did not occur")
			}
			if res := ask(t, bound, http.MethodPost, "/speech/say", cookie, ""); res.StatusCode != http.StatusUnauthorized {
				t.Errorf("the cookie the other port was given acted on the dashboard: %d", res.StatusCode)
			}
			if frame, code, _ := firstFrame(dialHello(t, bound, cookie, "")); frame != "" || code != closeSignIn {
				t.Errorf("the cookie the other port was given opened the socket: frame %q, close %d", frame, code)
			}
		})
	}
}

// THE REAL PAGE SIGNS IN BY ITS LINK AND KEEPS ITS SIGN-IN. A browser that
// opens an access code's link (as the desktop launchers do every time) on a
// dashboard that asks for its token arrives on the dashboard's own page with
// no form shown: the page's socket is admitted with the page key the link
// page kept. When every session is closed under it (a restart, a dropped
// network) the page reconnects with the key from its storage and is admitted
// again.
func TestTheRealPageSignsInByItsLinkAndKeepsItsSignIn(t *testing.T) {
	engines := resolveEngines()
	if len(engines) == 0 {
		skipWithoutEngines(t)
	}
	for _, found := range engines {
		t.Run(found.engine.name, func(t *testing.T) {
			s := New("127.0.0.1", 0, &WSHandler{})
			s.SetAccessToken(true, pageKeyTestToken)
			results := make(chan string, 4)
			_, start := keyRig(t, s, "", results)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				open := func() bool {
					for ctx.Err() == nil {
						if s.PageOpen() {
							return true
						}
						time.Sleep(50 * time.Millisecond)
					}
					return false
				}
				if !open() {
					return
				}
				s.closeAdmitted()
				time.Sleep(200 * time.Millisecond)
				if open() {
					results <- "OK"
				}
			}()
			runServerInOneEngine(t, found.engine, found.path, start, results, 0, 0, 0)
		})
	}
}

// A DRAWN PICTURE CARRIES A TICKET THAT OPENS IT ONCE. A reply that names a
// picture of the identity's world draws it, from an address that carries a
// ticket minted when the picture came into view; the same address asked
// again opens nothing, and says why in words.
func TestADrawnPictureCarriesATicketThatOpensItOnce(t *testing.T) {
	engines := resolveEngines()
	if len(engines) == 0 {
		skipWithoutEngines(t)
	}
	world := t.TempDir()
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(world, "proof.png"), picture.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, found := range engines {
		t.Run(found.engine.name, func(t *testing.T) {
			s := New("127.0.0.1", 0, &WSHandler{AdmitFile: func(p string) (string, string, bool) { return world, p, p == filepath.Base(p) }})
			s.SetAccessToken(true, pageKeyTestToken)
			driver := `
import { run, assert, until } from '/__harness.js';
import { drawReply } from '/views/attach.js';
run(async () => {
  const body = document.createElement('div');
  document.body.appendChild(body);
  drawReply(body, '![the proof](proof.png)');
  const img = body.querySelector('img');
  assert(img, 'the reply drew no picture');
  await until(() => img.complete && img.naturalWidth > 0, 15000);
  assert(img.naturalWidth === 1, 'the picture was not drawn: ' + img.src);
  assert(/[?&]ticket=/.test(img.src), 'the picture was drawn without a ticket: ' + img.src);
  const again = await fetch(img.src, { cache: 'no-store' });
  assert(again.status === 401, 'the ticketed address opened its file twice: ' + again.status);
  assert((await again.text()).includes('was good once'), 'a spent ticket was refused without its words');
});
`
			results := make(chan string, 4)
			// The chat's own toast is the page's; this page has none.
			_, start := keyRig(t, s, driver, results, map[string]string{"/app.js": "export function toast() {}"})
			runServerInOneEngine(t, found.engine, found.path, start, results, 0, 0, 0)
		})
	}
}

// A SIGN-IN WITH STORAGE REFUSED LASTS THE PAGE AND SAYS SO. A browser that
// refuses the dashboard's storage (both kinds) still signs in: the page key is
// held in the page's memory, sent with every request, and the page knows to
// say that the sign-in lasts until the tab is reloaded or closed.
func TestASignInWithStorageRefusedLastsThePageAndSaysSo(t *testing.T) {
	runPageInEngines(t, `<!doctype html><script type="module">
import { run, assert } from '/__harness.js';
Storage.prototype.setItem = function () { throw new DOMException('refused', 'SecurityError'); };
run(async () => {
  const { signIn, pageKey, keptInMemoryOnly, signedFetch, MEMORY_ONLY } = await import('/pagekey.js');
  const sent = [];
  window.fetch = async (url, init) => {
    sent.push(new Headers((init || {}).headers).get('Aii-Page-Key'));
    return new Response(null, { status: 204, headers: url === '/auth/token' ? { 'Aii-Page-Key': 'pk1.held-in-memory' } : {} });
  };
  assert(!keptInMemoryOnly() && pageKey() === '', 'a page that has not signed in holds a key');
  const reply = await signIn('the-token');
  assert(reply.ok, 'the sign-in was not answered');
  assert(pageKey() === 'pk1.held-in-memory', 'the key was not held where storage is refused: ' + pageKey());
  assert(keptInMemoryOnly() && MEMORY_ONLY.includes("Reload or close this tab and you'll sign in again"), 'the page does not know to say its sign-in lasts only the tab');
  await signedFetch('/record');
  assert(sent[sent.length - 1] === 'pk1.held-in-memory', 'a request went without the key held in memory');
});
</script>`, nil)
}

// A BROWSER HOLDING THE COOKIE AND NO PAGE KEY CAN SIGN IN, on the real server
// and the real page. The browser is signed in by the app's link, its page key
// is then taken away and the page loaded again: the state of a Windows beta
// user's Edge (2026-11-02), whose page redrew "Loading conversation…" and the
// sign-in form about once a second and could not be signed in. The form must
// stand still for five seconds with what was typed, never mention a token, be
// the only message (no "Reconnecting…" above it or in the reply box), and sign
// the page in from what is typed into it.
func TestABrowserWithTheCookieAndNoPageKeySignsIn(t *testing.T) {
	engines := resolveEngines()
	if len(engines) == 0 {
		skipWithoutEngines(t)
	}
	// custom.js is the page's own last module, loaded on every load; here it
	// drives the page through the three loads.
	const driver = `
const phase = sessionStorage.getItem('rig.phase') || '';
const say = verdict => fetch('/__result', { method: 'POST', body: verdict });
const until = async (ok, why) => {
  for (const end = Date.now() + 20000; !ok(); await new Promise(r => setTimeout(r, 50))) if (Date.now() > end) throw Error(why);
};
if (phase === '') {
  sessionStorage.setItem('rig.phase', 'keyless');
  localStorage.removeItem('aii.signin'); sessionStorage.removeItem('aii.signin');
  location.reload();
} else if (phase === 'keyless') {
  try {
    await until(() => document.querySelector('#dashboard-signin input'), 'no sign-in form');
    const box = document.getElementById('dashboard-signin'), input = box.querySelector('input');
    input.value = 'typed';
    // waits by design: the form must stand past the old loop's one-second retry, several times over
    await new Promise(r => setTimeout(r, 5000));
    if (document.getElementById('dashboard-signin') !== box || input.value !== 'typed') throw Error('the form was drawn again or its typing lost');
    if (/token/i.test(box.textContent)) throw Error('the form asks for a token: ' + box.textContent);
    if (document.body.textContent.includes('Reconnecting')) throw Error('the page says it is reconnecting while it asks for a sign-in');
    const line = document.getElementById('reply-line');
    if (line && line.textContent) throw Error('the reply box speaks of the connection while the page asks for a sign-in: ' + line.textContent);
    sessionStorage.setItem('rig.phase', 'signing');
    input.value = 'TOKEN'; box.querySelector('form').requestSubmit();
    await until(() => !document.getElementById('dashboard-signin'), 'the sign-in did not take');
    say('SIGNED');
  } catch (e) { say('FAIL: ' + e.message); }
} else if (phase === 'signing') {
  say('SIGNED');
}
`
	for _, found := range engines {
		t.Run(found.engine.name, func(t *testing.T) {
			s := New("127.0.0.1", 0, &WSHandler{})
			s.SetAccessToken(true, pageKeyTestToken)
			said, verdicts := make(chan string, 4), make(chan string, 4)
			_, start := keyRig(t, s, "", said, map[string]string{"/custom.js": strings.Replace(driver, "TOKEN", pageKeyTestToken, 1)})
			go func() {
				first := <-said
				if first != "SIGNED" {
					verdicts <- first
					return
				}
				for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
					if s.PageOpen() {
						verdicts <- "OK"
						return
					}
				}
				verdicts <- "FAIL: the page said it was signed in, and no socket of it was admitted"
			}()
			runServerInOneEngine(t, found.engine, found.path, start, verdicts, 0, 0, 0)
		})
	}
}
