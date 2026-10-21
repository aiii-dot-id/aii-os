//go:build !windows

package dashboard

import "testing"

/*
A SECTION TAB MUST SURVIVE ITS OWN CLICK.

	sections.js mounts a main-tabs section as a first-class view and
	routes it with go('section:<id>'). go writes the address for every
	view, so the hash became "#/section:pm" — and parseHash matched
	[a-z]+ only, so it read that as NOTHING. The hashchange that followed
	compared an empty route against S.view, found them different, and
	called go(''), which turns every view off: the section painted for
	one tick and the whole stage went blank.

	The echo guard is state equality, so it structurally cannot hold for
	a view the grammar cannot express. This drives the REAL app.js and
	WAITS THROUGH the hashchange — the event is async, and a synchronous
	assertion would pass against the broken router.
*/
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

const r = parseHash('#/section:pm');
if (r.view !== 'section:pm') {
  report('FAIL: the route grammar cannot express a section view: ' + JSON.stringify(r));
} else {
  let textVisibility=0;S.refreshMessageText=()=>textVisibility++;
  go('section:pm');
  assert(textVisibility===1,'navigation did not update visible conversation reads');
  if (location.hash !== '#/section:pm') {
    report('FAIL: go did not address the section: ' + location.hash);
  } else {
    setTimeout(() => {
      const view = document.getElementById('view-section:pm');
      try {
        assert(S.view === 'section:pm',
          'the hashchange moved the operator off the section they opened: S.view=' + S.view);
        assert(view && view.classList.contains('on'),
          'THE SECTION VIEW WENT BLANK one tick after opening — go("") turned every view off');
        assert(location.hash === '#/section:pm',
          'the section address was rewritten to ' + location.hash);
        S.connected = true; S.view = 'home';
        S.refreshCurrentView();
        assert(['status','continuity','projects','work'].every(q => (window.__queries||[]).includes(q)),
          'foreground refresh omitted a current-view readback: ' + JSON.stringify(window.__queries));
        assert(view.classList.contains('on'), 'foreground readback rebuilt the current view');
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
export function query(q) { (window.__queries ||= []).push(q); }
export function connect() {}
export function wake() {}
`),
		"/sections.js":       stub("export function initSections() {}\nexport function sectionTitle(v) { return v; }\nexport function publish() {}\nexport function onSections() {}\nexport function onLayout() {}\nexport function onTokensChanged() {}\n"),
		"/voice.js":          stub("export function wireMic() {}\nexport function bindTransport() {}\nexport function speak() {}\nexport function render() {}\nexport function connectionLost() {}\n"),
		"/views/chat.js":     stub("export function scrollThread() {}\nexport function addMsg() {}\nexport function attachSpeaker() {}\nexport function sysLine() {}\n"),
		"/views/home.js":     stub("export function renderHome() {}\n"),
		"/views/memory.js":   stub("export function renderMemory() {}\n"),
		"/views/identity.js": stub("export function renderIdentity() {}\n"),
		"/views/plugins.js":  stub("export function renderPlugins() {}\nexport function rejectPluginAction() { return false; }\nexport function applyPluginStored() {}\n"),
		"/views/settings.js": stub("export function renderSettings() {}\n"),
		"/views/projects.js": stub("export function renderProjects() {}\nexport function renderProjPill() {}\nexport function viewProject() {}\n"),
		"/panel.js":          stub("export function renderPanel() {}\n"),
		"/overlay.js":        stub("export function holdReloadWhile() {}\nexport function restoreDraft() {}\nexport function onOverlayChanged() {}\n"),
		"/theme.js":          stub("export function onTheme() {}\n"),
		"/presence.js":       stub("export function renderPresence() {}\nexport function setThinking() {}\n"),
	})
}

func TestPluginSectionNavigationUsesFocusableButtons(t *testing.T) {
	page := `<!doctype html><div id="nav"></div><div id="views"></div><div id="slot-rail"></div><div id="slot-panel"></div><div id="slot-home"></div><div id="slot-project"></div>
<script type="module">
import {onSections,onLayout} from '/sections.js';import {S} from '/state.js';import {run} from '/__harness.js';
run(assert=>{
 const slots={'main-tabs':['fixture']};onLayout({profiles:{desktop:slots,mobile:slots,phone:slots,tablet:slots}});
 onSections([{id:'fixture',title:'Fixture plugin',slot:'main-tabs',entry:'index.html'}]);
 const nav=document.querySelector('[data-view="section:fixture"]');assert(nav,'plugin destination not mounted');
 assert(nav.tagName==='BUTTON'&&nav.type==='button','plugin destination is not a native keyboard button');
 nav.focus();assert(document.activeElement===nav,'plugin destination cannot receive focus');nav.click();
 assert(S.view==='section:fixture','plugin navigation did not activate its destination');
});</script>`
	runPageInEngines(t, page, map[string][]byte{
		"/app.js": []byte(`import {S} from '/state.js';export function go(v){S.view=v}export function toast(){}`),
		"/ws.js":  []byte(`export function send(){return ''}export function query(){return ''}`),
	})
}
