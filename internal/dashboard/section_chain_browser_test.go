//go:build !windows

package dashboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/aiii-dot-id/aii-os/internal/sections"
)

// A BUILT-IN SECTION RUNS THE WHOLE CHAIN IN A REAL BROWSER. The dashboard's
// own server holds a built-in section (the binary's own bytes, here an
// in-memory tree) and a layout that places it; the shipped page mounts it
// (sections.js) in a sandboxed frame, served by the built-in route under the
// section's own policy. Inside, the section does what a section does,
// through the shipped section API alone:
//   - it says hello, and is given the frame's tokens, and sets its words in
//     the frame's own face;
//   - it subscribes to a topic it names, and is sent the current state at
//     once, before the subscription is answered;
//   - it acts by a command it names, and hears the host's own answer: the
//     projects the host sent for a select it took, and the host's words for
//     one it refused;
//   - it reads a page of the record through the frame (record.read), which
//     reads with its own key;
//   - it is refused a topic and a command its section.json does not name;
//   - its origin is opaque, and a request it makes is refused by its policy.
//
// It runs without a token and with one (signed in, the phone's case), and
// under SAFE, where the built-in section stays and an installed one is held
// back, with the note that says so.
//
// An installed section does not run yet. Its files keep the cookie wall, and
// its frame's loads carry no cookie in Blink or Gecko, and its modules are
// refused across origins in every engine.
// The last case holds that named interim state: placed beside the built-in
// section, under a token, signed in, it says nothing. When installed sections
// are given an address of their own, this case is the one that changes.
//
// The section cannot reach the rig (its policy allows no connection), so it
// tells the page what it saw, and the page's driver judges it, with the
// frame's sandbox attribute and address as the page holds them.
const sectionChainFixtureJS = `import { ready } from '/section-api.js';
const out = { origin: String(self.origin) };
const tell = m => parent.postMessage({ type: 'fixture-report', report: m }, '*');
(async () => {
  const api = await ready();
  out.tokens = Object.keys(api.tokens() || {}).filter(k => k.startsWith('--')).length;
  // The stylesheet that names the face may still be on its way.
  out.font = false;
  for (let i = 0; i < 100 && out.font === false; i++) {
    out.font = await document.fonts.load("16px 'Fraunces'").then(f => f.length > 0 && document.fonts.check("16px 'Fraunces'"), e => 'refused: ' + e);
    if (out.font === false) await new Promise(r => setTimeout(r, 50));
  }
  let status = null;
  out.subscribed = await api.data.subscribe('status', d => { if (!status) status = d; }).then(() => 'answered', e => 'refused: ' + e.message);
  out.statusAtAnswer = status ? status.name || '(no name)' : null;
  out.identity = await api.data.subscribe('identity', () => {}).then(() => 'granted', e => 'refused: ' + e.message);
  out.select = await api.act('project.select', { id: 'p-one' }).then(d => ({ ok: true, d }), e => ({ ok: false, e: e.message }));
  out.refused = await api.act('project.select', { id: 'p-missing' }).then(d => ({ ok: true, d }), e => ({ ok: false, e: e.message }));
  out.record = await api.act('record.read', {}).then(d => ({ ok: true, d }), e => ({ ok: false, e: e.message }));
  out.passNow = await api.act('pass_now', {}).then(() => 'granted', e => 'refused: ' + e.message);
  out.request = await fetch('/status').then(r => 'answered ' + r.status, () => 'refused');
  tell(out);
})().catch(e => tell({ stopped: String((e && e.message) || e) }));
`

// sectionChainDriverJS signs in first where the server asks for a token
// (then loads the page again, signed in: the rig gives this driver to every
// load of the start page), and judges what the section told it.
const sectionChainDriverJS = `
import { signIn } from '/pagekey.js';
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const token = '__TOKEN__', safe = __SAFE__, installed = __INSTALLED__;
(async () => {
  if (token && sessionStorage.getItem('chain-signed') !== '1') {
    const signed = await signIn(token);
    if (signed.status !== 204) return report('FAIL: sign-in answered ' + signed.status);
    sessionStorage.setItem('chain-signed', '1');
    location.reload();
    return;
  }
  let installedSpoke = false;
  window.addEventListener('message', e => { if (e.data && e.data.type === 'installed-report') installedSpoke = true; });
  let told = null;
  window.addEventListener('message', e => { if (e.data && e.data.type === 'fixture-report' && !told) told = { out: e.data.report, from: e.source }; });
  for (let i = 0; i < 300 && !told; i++) await new Promise(r => setTimeout(r, 50));
  const frame = document.querySelector('iframe.section-frame[title="Fixture"]');
  if (!frame) return report('FAIL: the built-in section was not mounted');
  if (!told) return report('FAIL: the built-in section told the page nothing in 15 s (sandbox ' + frame.getAttribute('sandbox') + ', src ' + frame.getAttribute('src') + ')');
  const o = told.out;
  check(told.from === frame.contentWindow, 'the report came from somewhere other than the section frame');
  check(frame.getAttribute('sandbox') === 'allow-scripts', 'the section frame is sandboxed as ' + JSON.stringify(frame.getAttribute('sandbox')));
  check(/^\/built-in-sections\/fixture\/~[0-9a-f]{64}\/index\.html$/.test(frame.getAttribute('src')), 'the built-in section is mounted from ' + frame.getAttribute('src'));
  if (o.stopped) return report('FAIL: the section stopped: ' + o.stopped);
  check(o.origin === 'null', 'the section\'s origin is not opaque: ' + o.origin);
  check(o.tokens > 0, 'the section was given no tokens');
  check(o.font === true, 'the section could not set its words in the frame\'s face: ' + o.font);
  check(o.subscribed === 'answered' && o.statusAtAnswer === 'Mira', 'subscribing to status did not send the current state at once: ' + o.subscribed + ', state when answered ' + JSON.stringify(o.statusAtAnswer));
  check(/^refused: topic not declared/.test(o.identity), 'a topic the section did not name: ' + o.identity);
  check(o.select && o.select.ok && Array.isArray(o.select.d) && o.select.d.some(p => p.id === 'p-one'), 'the host\'s answer to a select it took did not reach the section: ' + JSON.stringify(o.select));
  check(o.refused && !o.refused.ok && o.refused.e === 'no project is called p-missing', 'the host\'s refusal did not reach the section in its words: ' + JSON.stringify(o.refused));
  check(o.record && o.record.ok && o.record.d && Array.isArray(o.record.d.rows) && o.record.d.rows.some(r => String(r.seq) === '3'), 'record.read did not hand the section the page the frame read: ' + JSON.stringify(o.record));
  check(/^refused: command not declared/.test(o.passNow), 'a command the section did not name: ' + o.passNow);
  check(o.request === 'refused', 'a request from the section was not refused: ' + o.request);
  if (installed) {
    const inst = document.querySelector('iframe.section-frame[title="Installed"]');
    check(inst && inst.getAttribute('src') === '/sections/installed/index.html', 'the installed section was not mounted from its own route: ' + (inst ? inst.getAttribute('src') : 'not mounted'));
    // waits by design: the installed section's silence over a span is what
    // is held, the built-in section beside it having already spoken.
    await new Promise(done => setTimeout(done, 3000));
    check(!installedSpoke, 'an installed section ran under a token: the named interim state no longer holds, and this case should change with it');
  }
  if (safe) {
    check(!document.querySelector('iframe.section-frame[title="Installed"]'), 'an installed section was mounted under SAFE');
    const note = document.querySelector('#slot-rail .sections-safe');
    check(note && /SAFE: test reason/.test(note.textContent), 'SAFE did not say it holds a section back: ' + (note ? note.textContent : 'no note'));
  }
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

// chainRecord is a record of one entry, for record.read.
type chainRecord struct{}

func (chainRecord) RecordPage(_ context.Context, before uint64, _, _ string, _, _ int) ([]RecordRow, error) {
	if before != 0 {
		return nil, nil
	}
	return []RecordRow{{Seq: 3, Time: "2026-11-01T12:00:00Z", Type: "turn", Ring: -1, Bytes: 5, Head: []byte("hello")}}, nil
}
func (chainRecord) RecordPayload(context.Context, uint64, int64, int64) ([]byte, int64, error) {
	return nil, 0, errors.New("not held")
}
func (chainRecord) RecordTypes(context.Context) ([]string, error) { return []string{"turn"}, nil }

func TestABuiltInSectionRunsTheWholeChainInARealBrowser(t *testing.T) {
	const token = "chain-token-0123456789abcdef0123456789abcdef"
	for _, run := range []struct {
		name, token     string
		safe, installed bool
	}{
		{"without a token", "", false, false},
		{"signed in under a token", token, false, false},
		{"under SAFE, beside an installed section", "", true, false},
		{"under a token, beside an installed section, which does not run yet", token, false, true},
	} {
		t.Run(run.name, func(t *testing.T) {
			reg := sections.NewRegistry()
			if run.safe {
				reg.SetSafeSource(func() (string, bool) { return "test reason", true })
			}
			if err := reg.Register(&sections.Section{
				Decl: sections.Decl{ID: "fixture", Title: "Fixture", Slot: "panel", Entry: "index.html",
					Topics: []string{"status"}, Commands: []string{"project.select", "record.read"}},
				BuiltIn: true,
				FS: fstest.MapFS{
					"index.html": {Data: []byte(`<!doctype html><meta charset="utf-8"><title>Fixture</title><link rel="stylesheet" href="/fonts/fonts.css"><script type="module" src="main.js"></script>`)},
					"main.js":    {Data: []byte(sectionChainFixtureJS)},
				},
			}); err != nil {
				t.Fatal(err)
			}
			layout := `{"v":1,"profiles":{"desktop":{"panel":["fixture"]},"mobile":{"panel":["fixture"]}}}`
			if run.safe || run.installed {
				dir := t.TempDir()
				for name, body := range map[string]string{
					"index.html": `<!doctype html><meta charset="utf-8"><title>Installed</title><script type="module" src="main.js"></script>`,
					"main.js":    `import { ready } from '/section-api.js'; ready().then(() => parent.postMessage({ type: 'installed-report' }, '*')); parent.postMessage({ type: 'installed-report' }, '*');`,
				} {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := reg.Register(&sections.Section{
					Decl: sections.Decl{ID: "installed", Title: "Installed", Slot: "rail", Entry: "index.html"},
					Dir:  dir, PackageID: "id.example.installed",
				}); err != nil {
					t.Fatal(err)
				}
				layout = `{"v":1,"profiles":{"desktop":{"panel":["fixture"],"rail":["installed"]},"mobile":{"panel":["fixture"],"rail":["installed"]}}}`
			}
			var mu sync.Mutex
			var asked []string
			h := &WSHandler{
				GetStats:    func() (*StatsResponse, error) { return &StatsResponse{Name: "Mira"}, nil },
				GetProjects: func() ([]ProjectState, error) { return []ProjectState{{ID: "p-one", Name: "One", State: "open"}}, nil },
				ProjectAct: func(req ProjectRequest) error {
					mu.Lock()
					asked = append(asked, req.Action+" "+req.ID)
					mu.Unlock()
					if req.ID == "p-missing" {
						return errors.New("no project is called p-missing")
					}
					return nil
				},
				Record: chainRecord{},
			}
			s := New("127.0.0.1", 0, h)
			s.SetSections(reg)
			s.SetLayoutSource(func() []byte { return []byte(layout) })
			if run.token != "" {
				s.SetAccessToken(true, run.token)
			}
			flag := map[bool]string{true: "true", false: "false"}
			runDashboardInEngines(t, s, "/", strings.NewReplacer("__TOKEN__", run.token, "__SAFE__", flag[run.safe], "__INSTALLED__", flag[run.installed]).Replace(sectionChainDriverJS))
			mu.Lock()
			defer mu.Unlock()
			if !strings.Contains(strings.Join(asked, ","), "select p-one") {
				t.Errorf("the host was not asked to select p-one: %q", asked)
			}
		})
	}
}
