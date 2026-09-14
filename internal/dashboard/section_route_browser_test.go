//go:build !windows

// .
// .

package dashboard

import "testing"

// .
// .

// .
// .
// .
// .
// .
// .
// .

// .
// .
// .
// .
// .
const sectionRoutePage = `<!doctype html>
<div id="nav"></div><div id="views"></div>
<div id="view-chat" class="view"></div>
<div id="view-section:pm" class="view"></div>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div>
<div id="composer-wrap"></div><div id="proj-space"></div><div id="dock"></div>
<span id="pill-proj"></span><div id="view-projects" class="view"></div>
<script type="module">
import { report, assert } from '/__harness.js';
import { S } from '/state.js';
import { go, parseHash } from '/app.js';

// The grammar must be able to SAY it, or nothing downstream can hold.
const r = parseHash('#/section:pm');
if (r.view !== 'section:pm') {
  report('FAIL: the route grammar cannot express a section view: ' + JSON.stringify(r));
} else {
  go('section:pm');
  if (location.hash !== '#/section:pm') {
    report('FAIL: go did not address the section: ' + location.hash);
  } else {
    // THE TICK THAT MATTERED. hashchange fires async; the blanking
    // happened here, one turn after the tab looked fine.
    setTimeout(() => {
      const view = document.getElementById('view-section:pm');
      try {
        assert(S.view === 'section:pm',
          'the hashchange moved the operator off the section they opened: S.view=' + S.view);
        assert(view && view.classList.contains('on'),
          'THE SECTION VIEW WENT BLANK one tick after opening — go("") turned every view off');
        assert(location.hash === '#/section:pm',
          'the section address was rewritten to ' + location.hash);
        report('OK');
      } catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
    }, 80);
  }
}
</script>`

func TestASectionTabSurvivesItsOwnClick(t *testing.T) {
	appJS, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	state, err := staticFS.ReadFile("static/state.js")
	if err != nil {
		t.Fatal(err)
	}
	util, err := staticFS.ReadFile("static/util.js")
	if err != nil {
		t.Fatal(err)
	}
	stub := func(body string) []byte { return []byte(body) }
	runPageInEngines(t, sectionRoutePage, map[string][]byte{
		"/app.js":   appJS,
		"/state.js": state,
		"/util.js":  util,
		"/ws.js": stub(`export function send() { return ''; }
export function query() {}
export function connect() {}
export function wake() {}
`),
		"/sections.js":       stub("export function initSections() {}\nexport function sectionTitle(v) { return v; }\nexport function publish() {}\nexport function onSections() {}\nexport function onLayout() {}\nexport function onTokensChanged() {}\n"),
		"/voice.js":          stub("export function wireMic() {}\nexport function bindTransport() {}\nexport function speak() {}\nexport function render() {}\nexport function connectionLost() {}\n"),
		"/views/chat.js":     stub("export function scrollThread() {}\nexport function addMsg() {}\nexport function attachSpeaker() {}\nexport function sysLine() {}\n"),
		"/views/home.js":     stub("export function renderHome() {}\n"),
		"/views/memory.js":   stub("export function renderMemory() {}\n"),
		"/views/identity.js": stub("export function renderIdentity() {}\n"),
		"/views/plugins.js":  stub("export function renderPlugins() {}\n"),
		"/views/settings.js": stub("export function renderSettings() {}\n"),
		"/views/projects.js": stub("export function renderProjects() {}\nexport function renderProjPill() {}\nexport function viewProject() {}\n"),
		"/panel.js":          stub("export function renderPanel() {}\n"),
		"/overlay.js":        stub("export function restoreDraft() {}\nexport function onOverlayChanged() {}\n"),
		"/theme.js":          stub("export function onTheme() {}\n"),
		"/presence.js":       stub("export function renderPresence() {}\nexport function setThinking() {}\n"),
	})
}
