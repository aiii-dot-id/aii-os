//go:build !windows

package dashboard

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/version"
)

func recoveryShell(t *testing.T, at, assertions string) string {
	t.Helper()
	raw, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	shell := string(raw)
	head := `<head>
<base href="/">
<script>if (location.pathname !== '` + at + `') history.replaceState(null, '', '` + at + `');</script>`
	if !strings.Contains(shell, "<head>") || !strings.Contains(shell, "</body>") {
		t.Fatal("index.html has no <head> or </body>")
	}
	shell = strings.Replace(shell, "<head>", head, 1)
	return strings.Replace(shell, "</body>", `<script type="module">
import { assert, run, until } from './__harness.js';
`+assertions+`
</script>
</body>`, 1)
}

func recoveryModules(t *testing.T, app string) map[string][]byte {
	t.Helper()
	m := map[string][]byte{
		"/app.js":     []byte(app),
		"/stale.js":   []byte("export const unrelated = 1;\n"),
		"/custom.js":  []byte("export {};\n"),
		"/custom.css": nil,
	}
	for _, css := range []string{"theme.css", "layout.css"} {
		b, err := staticFS.ReadFile("static/" + css)
		if err != nil {
			t.Fatal(err)
		}
		m["/"+css] = b
	}
	return m
}

const unobstructed = `
const describe = el => el ? (el.id ? '#' + el.id : el.tagName.toLowerCase() + (el.className ? '.' + String(el.className).split(' ')[0] : '')) + (el.closest('[role=alert],[role=status]') ? ' (in the panel)' : '') : 'nothing';
function reachable(name, el) {
  assert(el, name + ' is not on the page');
  const r = el.getBoundingClientRect();
  assert(r.width > 0 && r.height > 0, name + ' is not drawn');
  assert(r.top >= 0 && r.left >= 0 && r.bottom <= innerHeight + 0.5 && r.right <= innerWidth + 0.5,
    name + ' is outside the screen: ' + [r.left, r.top, r.right, r.bottom].map(Math.round).join(',') + ' of ' + innerWidth + 'x' + innerHeight);
  const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
  assert(hit === el || el.contains(hit), name + ' is covered by ' + describe(hit));
}
async function panelLeavesThePageUsable(id) {
  assert(innerWidth <= 767, 'not at phone width: ' + innerWidth);
  await until(() => document.getElementById(id), 10000);
  const settled = () => document.documentElement.clientHeight === innerHeight;
  await until(settled, 10000);
  await new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)));
  await until(settled, 10000);
  const box = document.getElementById(id);
  assert(box, 'the panel ' + id + ' never appeared');
  const pos = getComputedStyle(box).position;
  assert(pos !== 'fixed' && pos !== 'absolute' && pos !== 'sticky', 'the panel is laid over the page (position ' + pos + ')');
  document.getElementById('mic').hidden = false;
  reachable('the composer', document.getElementById('msg-input'));
  reachable('the microphone', document.getElementById('mic'));
  const nav = [...document.querySelectorAll('#nav .nav-item')].filter(el => !el.hidden);
  assert(nav.length >= 7, 'the bottom navigation has ' + nav.length + ' destinations');
  for (const el of nav) reachable('the ' + el.textContent.trim() + ' destination', el);
  const close = [...box.querySelectorAll('button')].find(b => b.getAttribute('aria-label'));
  assert(close, 'the panel has no labelled control that dismisses it');
  reachable('the dismiss control', close);
  close.focus();
  assert(document.activeElement === close, 'the dismiss control cannot take keyboard focus');
  close.click();
  assert(!document.getElementById(id), 'dismissing did not remove the panel');
}
`

func TestTheFailurePanelLeavesAPhonePageUsable(t *testing.T) {
	page := recoveryShell(t, "/", unobstructed+`
run(async () => {
  await panelLeavesThePageUsable('boot-failure');
});`)
	runPageInEnginesAtSize(t, page, recoveryModules(t, "import { accountDisclosure } from './stale.js';\nexport { accountDisclosure };\n"), 390, 844)
}

func TestTheShippedNoteLeavesAPhonePageUsable(t *testing.T) {
	page := recoveryShell(t, "/shipped/", unobstructed+`
run(async () => {
  await panelLeavesThePageUsable('boot-shipped');
  const again = await import('./recovery.js?again');
  await again.ready;
  assert(!document.getElementById('boot-shipped'), 'the dismissed note came back in the same tab');
});`)
	runPageInEnginesAtSize(t, page, recoveryModules(t, "export {};\n"), 390, 844)
}

func runDashboardInEngines(t *testing.T, s *Server, startPath, driver string) {
	t.Helper()
	engines := resolveEngines()
	if len(engines) == 0 {
		skipWithoutEngines(t)
	}
	for _, found := range engines {
		found := found
		t.Run(found.engine.name, func(t *testing.T) {
			results := make(chan string, 4)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/__result" {
					body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					select {
					case results <- string(body):
					default:
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if driver != "" && r.URL.Path == "/__driver.js" {
					w.Header().Set("Content-Type", "text/javascript")
					io.WriteString(w, driver)
					return
				}
				if driver != "" && r.URL.Path == startPath {
					rec := httptest.NewRecorder()
					s.server.Handler.ServeHTTP(rec, r)
					for k, v := range rec.Header() {
						if k != "Content-Length" && k != "Etag" {
							w.Header()[k] = v
						}
					}
					w.WriteHeader(rec.Code)
					io.WriteString(w, strings.Replace(rec.Body.String(), "</body>", `<script type="module" src="/__driver.js"></script></body>`, 1))
					return
				}
				s.server.Handler.ServeHTTP(w, r)
			}))
			defer srv.Close()
			s.AllowHost(strings.TrimPrefix(srv.URL, "http://"))
			runServerInOneEngine(t, found.engine, found.path, srv.URL+startPath, results, 0, 0, 0)
		})
	}
}

func staleCopy(t *testing.T) string {
	t.Helper()
	b, err := staticFS.ReadFile("static/views/plugin-setting.js")
	if err != nil {
		t.Fatal(err)
	}
	imported := regexp.MustCompile(`import\s*\{([^}]*)\}\s*from\s*'[./]*(?:views/)?plugin-setting\.js'`)
	var names []string
	err = fs.WalkDir(staticFS, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".js") || path == "static/views/plugin-setting.js" {
			return err
		}
		src, err := staticFS.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range imported.FindAllStringSubmatch(string(src), -1) {
			for _, n := range strings.Split(m[1], ",") {
				if n = strings.TrimSpace(n); n != "" {
					names = append(names, n)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		for _, kind := range []string{"function ", "const ", "let "} {
			decl := "export " + kind + n
			if i := strings.Index(string(b), decl); i >= 0 {
				next := string(b)[i+len(decl):]
				if next != "" && (next[0] == '(' || next[0] == ' ' || next[0] == '=') {
					return string(b)[:i] + kind + n + next
				}
			}
		}
	}
	t.Fatalf("no shipped module imports a declared export of views/plugin-setting.js (imported: %v); the stale copy below would not be stale", names)
	return ""
}

const customDriver = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const fail = m => report('FAIL: ' + m);
(async () => {
  const { ready } = await import('./recovery.js');
  await ready;
  const box = document.getElementById('boot-failure');
  const mark = (id, prop) => getComputedStyle(document.getElementById(id))[prop];
  const listed = await (await fetch('/overlay/replacements', { cache: 'no-store' })).json();
  const inPlace = listed.files.some(f => f.path === 'views/plugin-setting.js');
  if (inPlace) {
    if (!box) return fail('the stale copy did not stop the page');
    const said = box.textContent;
    if (!said.includes('Your views/plugin-setting.js replaces a shipped dashboard file and may not match this version.')) return fail('the alert does not name the file: ' + said);
    for (const innocent of ['custom.css', 'custom.js', 'mine.js', 'theme.css']) {
      if (said.includes(innocent)) return fail('the alert names ' + innocent + ', which cannot stop the page: ' + said);
    }
    const act = [...box.querySelectorAll('button')].find(b => b.textContent === 'Set aside and reload');
    if (!act) return fail('the alert offers no set-aside: ' + said);
    act.click();
    return;
  }
  if (box) return fail('after setting aside, the page still did not start: ' + box.textContent);
  if (mark('p-name', 'outlineColor') !== 'rgb(1, 2, 3)') return fail('custom.css no longer applies: ' + mark('p-name', 'outlineColor'));
  if (mark('p-state', 'outlineColor') !== 'rgb(4, 5, 6)') return fail('the replaced theme.css no longer applies: ' + mark('p-state', 'outlineColor'));
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`

func TestAStaleCopyIsNamedAndSetAsideAndThePageStarts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ui")
	theme, err := staticFS.ReadFile("static/theme.css")
	if err != nil {
		t.Fatal(err)
	}
	stale := staleCopy(t)
	writeOverlay(t, dir, "views/plugin-setting.js", stale)
	writeOverlay(t, dir, "custom.css", "#p-name { outline: 3px solid rgb(1, 2, 3); }\n")
	writeOverlay(t, dir, "theme.css", string(theme)+"\n#p-state { outline: 2px solid rgb(4, 5, 6); }\n")
	writeOverlay(t, dir, "views/mine.js", "export const mine = 1;\n")
	writeOverlay(t, dir, "custom.js", customDriver)
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetUIOverlay(dir)

	approveOverlay(t, s, dir, "views/plugin-setting.js", "views/mine.js", "custom.js")

	runDashboardInEngines(t, s, "/", "")

	if _, err := os.Lstat(filepath.Join(dir, "views", "plugin-setting.js")); err == nil {
		t.Error("the stale copy is still in place")
	}
	aside := filepath.Join(dir, "views", "plugin-setting.js.set-aside-"+version.Authored())
	if b, err := os.ReadFile(aside); err != nil || string(b) != stale {
		t.Errorf("the stale copy was not kept as %s: %v", filepath.Base(aside), err)
	}
	for _, kept := range []string{"custom.css", "custom.js", "theme.css", "views/mine.js"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(kept))); err != nil {
			t.Errorf("setting aside took %s with it: %v", kept, err)
		}
	}
}

func TestTheDashboardAsShippedKeepsTheIdentitysStylesAndNoneOfItsScripts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ui")
	theme, err := staticFS.ReadFile("static/theme.css")
	if err != nil {
		t.Fatal(err)
	}
	writeOverlay(t, dir, "views/plugin-setting.js", staleCopy(t))
	writeOverlay(t, dir, "custom.css", "#p-name { outline: 3px solid rgb(1, 2, 3); }\n")
	writeOverlay(t, dir, "theme.css", string(theme)+"\n#p-state { outline: 2px solid rgb(4, 5, 6); }\n")
	writeOverlay(t, dir, "custom.js", "window.__customRan = true;\ndocument.documentElement.dataset.custom = 'ran';\n")
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetUIOverlay(dir)
	runDashboardInEngines(t, s, "/shipped/", `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const fail = m => report('FAIL: ' + m);
(async () => {
  const { ready } = await import('/shipped/recovery.js');
  await ready;
  const failed = document.getElementById('boot-failure');
  if (failed) return fail('the dashboard as shipped did not start: ' + failed.textContent);
  const mark = (id) => getComputedStyle(document.getElementById(id)).outlineColor;
  if (mark('p-name') !== 'rgb(1, 2, 3)') return fail('custom.css does not apply under /shipped/: ' + mark('p-name'));
  if (mark('p-state') !== 'rgb(4, 5, 6)') return fail('the replaced theme.css does not apply under /shipped/: ' + mark('p-state'));
  if (window.__customRan || document.documentElement.dataset.custom) return fail('custom.js ran under /shipped/');
  const note = document.getElementById('boot-shipped');
  if (!note || !note.textContent.includes('your scripts are not loaded')) return fail('the note does not say what is not loaded: ' + (note && note.textContent));
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`)
}
