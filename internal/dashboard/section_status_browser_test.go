//go:build !windows

package dashboard

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/aiii-dot-id/aii-os/internal/sections"
)

// reportingTree is a built-in section's real tree, with its page given one
// more script: the section cannot reach the rig, and a page cannot read inside
// a sandboxed frame, so the script tells the page what the frame holds. Nothing
// else of the section differs.
type reportingTree struct{ builtInTree }

func (t reportingTree) Open(name string) (fs.File, error) {
	switch name {
	case "__report.js":
		return fstest.MapFS{name: {Data: []byte(sectionReportJS)}}.Open(name)
	case "index.html":
		page, err := fs.ReadFile(t.builtInTree, name)
		if err != nil {
			return nil, err
		}
		withReport := strings.Replace(string(page), "<script", `<script type="module" src="./__report.js"></script><script`, 1)
		return fstest.MapFS{name: {Data: []byte(withReport)}}.Open(name)
	}
	return t.builtInTree.Open(name)
}

const sectionReportJS = `
let said = '';
const refused = [];
// WebKit's own date field reports its inner style under this policy, and draws the
// same to the pixel either way (measured): the only source let pass, in that engine.
const webkit = /AppleWebKit/.test(navigator.userAgent) && !/Chrome/.test(navigator.userAgent);
document.addEventListener('securitypolicyviolation', e => {
  if (webkit && /\/views\/identity\/when\.js$/.test(e.sourceFile)) return;
  refused.push(e.violatedDirective + ' ' + (e.blockedURI || e.sample) + ' at ' + e.sourceFile + ':' + e.lineNumber);
});
const tell = () => {
  const root = getComputedStyle(document.documentElement);
  const now = JSON.stringify({ type: 'section-holds', view: (document.querySelector('.view.on') || {}).id || '', text: (document.querySelector('.view.on') || document.body).innerText, marker: root.getPropertyValue('--overlay-marker').trim(), own: getComputedStyle(document.body).getPropertyValue('--overlay-own').trim(), ink: root.getPropertyValue('--text').trim(), scheme: root.getPropertyValue('color-scheme').trim(), column: root.getPropertyValue('--column-wide').trim(), origin: String(self.origin), refused });
  if (now !== said) { said = now; parent.postMessage(JSON.parse(now), '*'); }
};
setInterval(tell, 50);
// Every drawing, too: what is said for a moment is still said.
new MutationObserver(tell).observe(document.documentElement, { subtree: true, childList: true, characterData: true });
window.addEventListener('message', e => {
  if (e.data && e.data.type === 'key') document.body.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...e.data.key }));
  if (e.data && e.data.type === 'follow') { const a = [...document.querySelectorAll('.view.on a[href]')].find(x => x.textContent.trim() === e.data.words); if (a) a.click(); }
  if (e.data && e.data.type === 'where') {
    const b = [...document.querySelectorAll('.view.on button')].find(x => x.textContent.trim() === e.data.words);
    if (b) b.scrollIntoView({ block: 'center' }); // as a reader brings it into view before pressing
    const r = b && b.getBoundingClientRect();
    parent.postMessage({ type: 'where', x: r ? r.x + r.width / 2 : -1, y: r ? r.y + r.height / 2 : -1 }, '*');
  }
  // What assistive technology reads of the view shown: every control a person can
  // reach, with its role and its accessible name; the heading; the live regions.
  if (e.data && e.data.type === 'a11y') {
    const view = document.querySelector('.view.on') || document.body;
    const shown = el => el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden';
    const labelled = el => (el.getAttribute('aria-labelledby') || '').split(' ').filter(Boolean).map(id => (document.getElementById(id) || {}).textContent || '').join(' ');
    const typed = el => (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA') ? el.getAttribute('placeholder') : '';
    const nameOf = el => (el.getAttribute('aria-label') || labelled(el) || [...(el.labels || [])].map(l => l.textContent).join(' ') || el.getAttribute('title') || typed(el) || el.textContent || '').trim();
    const native = { A: 1, BUTTON: 1, INPUT: 1, SELECT: 1, TEXTAREA: 1, SUMMARY: 1 };
    const controls = [...view.querySelectorAll('a[href],button,input:not([type="hidden"]),select,textarea,summary,[role="tab"],[tabindex]:not([tabindex="-1"])')].filter(shown);
    parent.postMessage({ type: 'a11y', view: view.id || '', controls: controls.length,
      unnamed: controls.filter(el => !nameOf(el)).map(el => el.outerHTML.slice(0, 120)),
      unroled: controls.filter(el => !native[el.tagName] && !el.getAttribute('role')).map(el => el.outerHTML.slice(0, 120)),
      heading: (([...view.querySelectorAll('h1,h2,h3,[role="heading"]')].find(shown) || {}).textContent || '').trim(),
      region: (([...view.querySelectorAll('section[aria-label],[role="region"][aria-label]')].find(shown) || { getAttribute: () => '' }).getAttribute('aria-label') || '').trim(),
      live: [...document.querySelectorAll('[aria-live],[role="status"],[role="alert"],[role="log"]')].map(el => el.tagName.toLowerCase() + (el.id ? '#' + el.id : '') + (el.className && typeof el.className === 'string' ? '.' + el.className.split(' ')[0] : '') + '[' + (el.getAttribute('aria-live') || el.getAttribute('role')) + ']') }, '*');
  }
  if (!e.data || e.data.type !== 'press') return;
  const b = [...document.querySelectorAll('.view.on button')].find(x => x.textContent.trim() === e.data.words);
  if (b) { const d = b.closest('details'); if (d) d.open = true; b.click(); }
});
`

// STATUS RUNS AS A BUILT-IN SECTION, its own view module unchanged, on the
// dashboard's own server, in three engines:
//   - nothing of it is fetched until Status is first opened: Home costs nothing;
//   - the frame's own copy draws nothing, and the page's module is not fetched;
//   - what the host sent reaches it through the bridge alone: its heading, its
//     blocks and the identity's name;
//   - its colours follow the page's theme, both ways;
//   - "Reflect now" asks the host through the bridge, and its answer
//     is said beside it.
const statusSectionDriverJS = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
let held = null;
window.addEventListener('message', e => { if (e.data && e.data.type === 'section-holds') held = e.data; });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
const frame = () => document.querySelector('iframe.section-frame[title="Status"]');
const fetched = p => performance.getEntriesByType('resource').some(r => r.name.endsWith(p));
(async () => {
  // Home, connected and drawn: nothing of the section has been fetched.
  await until(() => document.getElementById('send-btn') && (document.getElementById('p-state') || {}).textContent);
  // waits by design: that nothing was fetched is what is held, once the
  // socket's first frames, the sections list among them, have had time to come.
  await new Promise(r => setTimeout(r, 800));
  check(!frame(), 'the Status section was mounted before Status was opened');
  check(!fetched('/sections-frames.js') && !fetched('/built-in-sections/status/index.html'), 'the section was fetched before Status was opened');
  location.hash = '#/settings';
  await until(() => held && /Everything is working/.test(held.text));
  if (!frame()) return report('FAIL: the Status section was not mounted when opened');
  check(/^\/built-in-sections\/status\/~[0-9a-f]{64}\/index\.html$/.test(frame().getAttribute('src')), 'the section is at ' + frame().getAttribute('src'));
  check(frame().getAttribute('sandbox') === 'allow-scripts', 'the section frame is sandboxed as ' + frame().getAttribute('sandbox'));
  check(held && held.origin === 'null', 'the section is not an opaque origin: ' + (held && held.origin));
  check(held && /Everything is working/.test(held.text), 'the section did not say the heading: ' + (held && held.text));
  check(held && /Model/.test(held.text) && /Right now/.test(held.text) && /Mira/.test(held.text), 'the section did not draw its blocks from what the host sent: ' + (held && held.text));
  const own = document.getElementById('view-status');
  await until(() => getComputedStyle(own).visibility === 'hidden');
  check(getComputedStyle(own).visibility === 'hidden', 'the page\'s own Status still stands beside the section');
  check(!document.getElementById('status-stack').textContent, 'the page drew its own Status behind the section');
  const column = getComputedStyle(document.querySelector('[id="view-section:status"]')).getPropertyValue('--column-wide').trim();
  check(held && column && held.column === column, 'the section\'s column is ' + (held && held.column) + ', its place\'s ' + column);

  for (const theme of ['dark', 'light']) {
    document.documentElement.setAttribute('data-theme', theme);
    document.documentElement.style.colorScheme = theme;
    (await import('/state.js')).S.onThemeApplied?.();
    const ink = getComputedStyle(document.documentElement).getPropertyValue('--text').trim();
    await until(() => held && held.ink === ink);
    check(held && held.ink === ink, theme + ': the section\'s ink is ' + (held && held.ink) + ', the page\'s ' + ink);
    check(held && held.scheme === theme, theme + ': the section\'s colour scheme is ' + (held && held.scheme));
  }

  frame().contentWindow.postMessage({ type: 'press', words: 'Reflect now' }, '*');
  await until(() => held && /Asked at/.test(held.text));
  check(held && /Asked at/.test(held.text), 'the host\'s answer to the press was not said beside it: ' + (held && held.text));
  check(held && !held.refused.length, 'the section\'s policy refused what it drew: ' + (held && held.refused.join('; ')));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

// builtInSection is the built-in section id as the binary registers it. A
// section whose tree differs from its digest is not registered, and a test
// that needs it fails here, by name, rather than on a list without it.
func builtInSection(t testing.TB, id string) *sections.Section {
	t.Helper()
	for _, s := range BuiltInSections() {
		if s.Decl.ID == id {
			return s
		}
	}
	t.Fatalf("the built-in %s section is not registered: a file of its tree differs from built_in_digests/%s.txt; AII_UPDATE_SECTION_DIGESTS=1 go test ./internal/dashboard/ -run '^TestABuiltInSectionIsCheckedAgainstItsDigests$' writes it again", id, id)
	return nil
}

func statusSectionHandler(pressed *int) *WSHandler {
	return &WSHandler{
		GetStats:      func() (*StatsResponse, error) { return &StatsResponse{Name: "Mira"}, nil },
		GetContinuity: func() (*ContinuityState, error) { return &ContinuityState{Mode: "normal", LedgerSeq: 9}, nil },
		PassNow: func(context.Context) (PassNowResult, error) {
			*pressed++
			return PassNowResult{Moved: true, AskedAt: "2026-11-01T12:00:00Z"}, nil
		},
	}
}

func TestStatusRunsAsABuiltInSection(t *testing.T) {
	pressed := 0
	runDashboardInEachEngine(t, func(t *testing.T) *Server {
		tree, err := builtInStatusTree()
		if err != nil {
			t.Fatal(err)
		}
		if built := BuiltInSections(); len(built) != 2 {
			t.Fatalf("the binary registers %d built-in sections", len(built))
		}
		status := builtInSection(t, "status")
		status.FS = reportingTree{tree}
		reg := sections.NewRegistry()
		if err := reg.Register(status); err != nil {
			t.Fatal(err)
		}
		s := New("127.0.0.1", 0, statusSectionHandler(&pressed))
		s.SetSections(reg)
		return s
	}, "/", statusSectionDriverJS)
	if pressed == 0 {
		t.Error("the host was never asked for the next pass")
	}
}

// A PLACE OPENED BEFORE THE SECTIONS LIST CAME IS THE SECTION'S WHEN IT COMES: a
// reload at Status's own address, with the page's own view on its way when the list
// comes, and with it already drawn. It must not take the place back after.
const statusOpenedEarlyDriverJS = `
location.hash = '#/settings';
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
let held = null;
window.addEventListener('message', e => { if (e.data && e.data.type === 'section-holds') held = e.data; });
const mine = document.getElementById('status-stack');
(async () => {
  if (__DRAWN__) {
    await until(() => /Everything is working/.test(mine.textContent));
    if (!/Everything is working/.test(mine.textContent)) return report('FAIL: rig: the page did not draw its own Status first: ' + mine.textContent);
    await fetch('/__release', { method: 'POST' });
  }
  await until(() => held && /Everything is working/.test(held.text));
  if (!held) return report('FAIL: the section never took the place it was opened early for');
  const own = document.getElementById('view-status');
  await until(() => getComputedStyle(own).visibility === 'hidden');
  if (getComputedStyle(own).visibility !== 'hidden') return report('FAIL: the page\'s own Status still stands beside the section');
  report('OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func TestAPlaceOpenedBeforeTheSectionsListIsTheSectionsWhenItComes(t *testing.T) {
	pressed := 0
	section := func(t *testing.T) *sections.Section {
		tree, err := builtInStatusTree()
		if err != nil {
			t.Fatal(err)
		}
		status := builtInSection(t, "status")
		status.FS = reportingTree{tree}
		return status
	}
	t.Run("the page's view on its way", func(t *testing.T) {
		runDashboardInEachEngine(t, func(t *testing.T) *Server {
			reg := sections.NewRegistry()
			if err := reg.Register(section(t)); err != nil {
				t.Fatal(err)
			}
			s := New("127.0.0.1", 0, statusSectionHandler(&pressed))
			s.SetSections(reg)
			return s
		}, "/", strings.ReplaceAll(statusOpenedEarlyDriverJS, "__DRAWN__", "false"))
	})
	// The list comes when the driver asks, once the page has drawn its own.
	t.Run("the page's view already drawn", func(t *testing.T) {
		runDashboardInEachEngine(t, func(t *testing.T) *Server {
			reg := sections.NewRegistry()
			s := New("127.0.0.1", 0, statusSectionHandler(&pressed))
			s.SetSections(reg)
			next := s.server.Handler
			s.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/__release" {
					if err := reg.Register(section(t)); err != nil {
						t.Error(err)
					}
					s.BroadcastSections()
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
			})
			return s
		}, "/", strings.ReplaceAll(statusOpenedEarlyDriverJS, "__DRAWN__", "true"))
	})
}

// A BUILT-IN SECTION THAT DOES NOT RUN LEAVES THE PLACE TO THE PAGE. A module of
// its tree does not arrive, so it says no hello; when its page has loaded, the
// page draws the view itself, the same module, and nothing stands blank.
const statusFallbackDriverJS = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
(async () => {
  await until(() => document.getElementById('send-btn') && (document.getElementById('p-state') || {}).textContent);
  location.hash = '#/settings';
  const own = () => document.getElementById('status-stack');
  await until(() => own() && /Everything is working/.test(own().textContent) && getComputedStyle(document.getElementById('view-status')).visibility !== 'hidden');
  if (!own() || !/Everything is working/.test(own().textContent)) return report('FAIL: the page did not draw Status when its section did not run: ' + (own() ? own().textContent : 'no stack'));
  if (getComputedStyle(document.getElementById('view-status')).visibility === 'hidden') return report('FAIL: the page drew Status, and it is hidden');
  const box = document.querySelector('[id="view-section:status"]');
  await until(() => !box || getComputedStyle(box).visibility === 'hidden');
  if (box && getComputedStyle(box).visibility !== 'hidden') return report('FAIL: the section that did not run still stands in the place');
  report('OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func TestABuiltInSectionThatDoesNotRunLeavesThePlaceToThePage(t *testing.T) {
	pressed := 0
	runDashboardInEachEngine(t, func(t *testing.T) *Server {
		reg := sections.NewRegistry()
		if err := reg.Register(&sections.Section{
			Decl:    sections.Decl{ID: "status", Title: "Status", Slot: "view", Entry: "index.html"},
			BuiltIn: true, Views: []string{"status"},
			FS: fstest.MapFS{
				"index.html": {Data: []byte(`<!doctype html><meta charset="utf-8"><title>Status</title><script type="module" src="./main.js"></script>`)},
				"main.js":    {Data: []byte(`import '/section-api.js'; import './not-there.js';`)},
			},
		}); err != nil {
			t.Fatal(err)
		}
		s := New("127.0.0.1", 0, statusSectionHandler(&pressed))
		s.SetSections(reg)
		return s
	}, "/", statusFallbackDriverJS)
}

// THE BINARY'S BUILT-IN SECTION IS WHAT ITS BUILD EMBEDDED: every file it serves
// matches the digest the build carries, and one that does not refuses the
// section. A change to a file a section shares with the frame changes its
// digest: AII_UPDATE_SECTION_DIGESTS=1 writes the new manifest.
func TestABuiltInSectionIsCheckedAgainstItsDigests(t *testing.T) {
	for _, sec := range []struct {
		id, file, carried string
		tree              func() (builtInTree, error)
	}{
		{"status", "built_in_digests/status.txt", builtInStatusDigests, builtInStatusTree},
		{"about", "built_in_digests/about.txt", builtInAboutDigests, builtInAboutTree},
	} {
		tree, err := sec.tree()
		if err != nil {
			t.Fatal(err)
		}
		got, err := tree.digests()
		if err != nil {
			t.Fatal(err)
		}
		if os.Getenv("AII_UPDATE_SECTION_DIGESTS") != "" {
			if err := os.WriteFile(sec.file, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if got != sec.carried {
			t.Fatalf("%s is stale; run with AII_UPDATE_SECTION_DIGESTS=1 to write:\n%s", sec.file, got)
		}
		if !tree.verified(sec.id, sec.carried) {
			t.Fatalf("the binary's own %s section was refused", sec.id)
		}
		changed := builtInTree{own: fstest.MapFS{"index.html": {Data: []byte("changed")}}, frame: tree.frame, shared: tree.shared}
		if changed.verified(sec.id, sec.carried) {
			t.Errorf("a %s section whose file differs from its digest was not refused", sec.id)
		}
	}
	if os.Getenv("AII_UPDATE_SECTION_DIGESTS") != "" {
		t.Skip("the manifests are written; run again to test them")
	}
	if len(BuiltInSections()) != 2 {
		t.Fatal("the binary's own sections are not both registered")
	}
}

// A BUILT-IN SECTION'S TREE HOLDS WHAT ITS MODULES IMPORT: from its page, every
// script and sheet it names, and every module those import, is in the tree, or
// is one of the two addresses the frame serves it across origins. A view that
// gains an import outside the tree fails here, not as a place left to the page.
func TestABuiltInSectionsTreeHoldsWhatItLoads(t *testing.T) {
	for view, build := range map[string]func() (builtInTree, error){"views/settings/status.js": builtInStatusTree, "views/memory.js": builtInAboutTree} {
		tree, err := build()
		if err != nil {
			t.Fatal(err)
		}
		loads := regexp.MustCompile(`(?:src|href)="([^"]+)"`)
		seen := map[string]bool{}
		var walk func(p string)
		walk = func(p string) {
			if seen[p] {
				return
			}
			seen[p] = true
			b, err := fs.ReadFile(tree, p)
			if err != nil {
				t.Errorf("%s is not in the tree: %v", p, err)
				return
			}
			var named []string
			switch path.Ext(p) {
			case ".html":
				for _, m := range loads.FindAllStringSubmatch(string(b), -1) {
					named = append(named, m[1])
				}
			case ".js":
				for _, m := range staticRef.FindAllStringSubmatch(string(b), -1) {
					named = append(named, m[1]+m[2])
				}
			}
			for _, n := range named {
				if strings.HasPrefix(n, "/") {
					if n != "/section-api.js" && !strings.HasPrefix(n, "/fonts/") {
						t.Errorf("%s names %s, which a section cannot load", p, n)
					}
					continue
				}
				walk(path.Join(path.Dir(p), n))
			}
		}
		walk("index.html")
		if !seen[view] {
			t.Errorf("the walk never reached %s", view)
		}
		for _, p := range tree.shared {
			if !seen[p] && path.Ext(p) == ".js" {
				t.Errorf("the tree names %s, which nothing loads", p)
			}
		}
	}
}

// AN IDENTITY'S OVERLAY APPLIES INSIDE THE STATUS SECTION, as it does in the
// page: its custom.css, and its approved copy of a file the view runs on, by the
// address minted for the signed-in page, without a token and under one.
const statusOverlayDriverJS = `
import { signIn } from '/pagekey.js';
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
let held = null;
window.addEventListener('message', e => { if (e.data && e.data.type === 'section-holds') held = e.data; });
const token = '__TOKEN__';
(async () => {
  if (token && sessionStorage.getItem('overlay-signed') !== '1') {
    const signed = await signIn(token);
    if (signed.status !== 204) return report('FAIL: sign-in answered ' + signed.status);
    sessionStorage.setItem('overlay-signed', '1');
    location.reload();
    return;
  }
  location.hash = '#/settings';
  await until(() => held && /All is well here/.test(held.text) && held.own === '7');
  if (!held) return report('FAIL: the section told the page nothing');
  if (!/All is well here/.test(held.text)) return report('FAIL: the identity\'s copy of a file did not apply inside the section: ' + held.text);
  if (held.own !== '7') return report('FAIL: the identity\'s custom.css did not apply inside the section: ' + JSON.stringify(held.own));
  report('OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func statusOverlay(t *testing.T) (dir string, approved []ApprovedScript) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "ui")
	shipped, err := staticFS.ReadFile("static/strings/status.js")
	if err != nil {
		t.Fatal(err)
	}
	copied := strings.Replace(string(shipped), "Everything is working", "All is well here", 1)
	if copied == string(shipped) {
		t.Fatal("rig: the heading's words are not in strings/status.js")
	}
	writeOverlay(t, dir, "strings/status.js", copied)
	// A rule of :root would reach the section as a token, its custom.css or not.
	writeOverlay(t, dir, "custom.css", "body { --overlay-own: 7; }\n")
	return dir, []ApprovedScript{{Path: "strings/status.js", SHA256: digestOf(t, filepath.Join(dir, "strings/status.js"))}}
}

func TestAnIdentitysOverlayAppliesInsideTheStatusSection(t *testing.T) {
	const token = "overlay-token-0123456789abcdef0123456789abcdef"
	for _, run := range []struct{ name, token string }{{"without a token", ""}, {"signed in under a token", token}} {
		t.Run(run.name, func(t *testing.T) {
			pressed := 0
			runDashboardInEachEngine(t, func(t *testing.T) *Server {
				tree, err := builtInStatusTree()
				if err != nil {
					t.Fatal(err)
				}
				status := builtInSection(t, "status")
				status.FS = reportingTree{tree}
				reg := sections.NewRegistry()
				if err := reg.Register(status); err != nil {
					t.Fatal(err)
				}
				dir, approved := statusOverlay(t)
				s := New("127.0.0.1", 0, statusSectionHandler(&pressed))
				s.SetSections(reg)
				s.SetUIOverlay(dir)
				s.SetApprovedScripts(approved)
				if run.token != "" {
					s.SetAccessToken(true, run.token)
				}
				return s
			}, "/", strings.ReplaceAll(statusOverlayDriverJS, "__TOKEN__", run.token))
		})
	}
}

// ANOTHER SITE READS NOTHING OF THE OVERLAY. The built-in route says the
// cross-origin header, so a page of another origin can read what it answers:
// with no token on, the worst case, that is the shipped bytes alone. A made-up
// address is refused, and an installed section's route says no such header.
const otherSitePage = `<!doctype html><script type="module">
import { run } from '/__harness.js';
const site = '__SITE__';
run(async assert => {
  const css = await (await fetch(site + '/built-in-sections/status/custom.css')).text();
  assert(!css.includes('overlay-own'), 'another site read the identity\'s custom.css: ' + css);
  const words = await (await fetch(site + '/built-in-sections/status/strings/status.js')).text();
  assert(words.includes('Everything is working') && !words.includes('All is well here'), 'another site read the identity\'s copy of a file');
  const madeUp = await fetch(site + '/built-in-sections/status/~' + '0'.repeat(64) + '/custom.css').then(r => 'read ' + r.status, () => 'refused');
  assert(madeUp === 'refused', 'a made-up address answered another site: ' + madeUp);
  const installed = await fetch(site + '/sections/installed/index.html').then(r => 'read ' + r.status, () => 'refused');
  assert(installed === 'refused', 'another site could read an installed section: ' + installed);
});
</script>`

func TestAnotherSiteReadsNothingOfTheOverlay(t *testing.T) {
	pressed := 0
	dir, approved := statusOverlay(t)
	installed := t.TempDir()
	if err := os.WriteFile(filepath.Join(installed, "index.html"), []byte("<!doctype html>installed"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := sections.NewRegistry()
	for _, sec := range append(BuiltInSections(), &sections.Section{
		Decl: sections.Decl{ID: "installed", Title: "Installed", Slot: "rail", Entry: "index.html"}, Dir: installed, PackageID: "id.example.installed",
	}) {
		if err := reg.Register(sec); err != nil {
			t.Fatal(err)
		}
	}
	s := New("127.0.0.1", 0, statusSectionHandler(&pressed))
	s.SetSections(reg)
	s.SetUIOverlay(dir)
	s.SetApprovedScripts(approved)
	srv := httptest.NewServer(s.server.Handler)
	defer srv.Close()
	s.AllowHost(strings.TrimPrefix(srv.URL, "http://"))
	runPageInEngines(t, strings.ReplaceAll(otherSitePage, "__SITE__", srv.URL), nil)
}
