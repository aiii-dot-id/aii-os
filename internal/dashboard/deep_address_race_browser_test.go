//go:build !windows

package dashboard

import "testing"

const deepAddressRacePage = `<!doctype html>
<div id="nav"></div><div id="views"></div>
<div id="view-chat" class="view"></div><div id="view-home" class="view"></div><div id="view-projects" class="view"></div>
<div id="view-memory" class="view"></div><div id="view-identity" class="view"></div><div id="view-plugins" class="view"></div>
<div id="view-settings" class="view"></div><div id="view-record" class="view"></div>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div>
<div id="composer-wrap"></div><div id="proj-space"></div><div id="dock"></div><span id="pill-proj"></span>
<script type="module">
import { assert, run, until } from '/__harness.js';
import { S } from '/state.js';
import { go } from '/app.js';
const moved = () => new Promise(done => addEventListener('hashchange', done, { once: true }));
run(async () => {
  const did = window.__did = [];
  S.connected = true;
  for (const [address, view, shown] of [['#/record/6', 'record', 'renderRecord'], ['#/projects/p1', 'projects', 'viewProject:p1']]) {
    if (location.hash !== '#/chat') { const m = moved(); go('chat'); await m; }
    assert(S.view === 'chat' && location.hash === '#/chat', 'the page is not on chat: ' + S.view + ' ' + location.hash);
    did.length = 0;
    const m = moved();
    location.hash = address;
    go(S.view); // the socket opens before the address's hashchange
    await m;
    await until(() => did.includes(shown));
    assert(location.hash === address, 'the address ' + address + ' was written over: ' + location.hash);
    assert(S.view === view && document.getElementById('view-' + view).classList.contains('on'), address + ' shows ' + S.view);
    assert(did.includes(shown), address + ' was not drawn: ' + JSON.stringify(did));
  }
});
</script>`

func TestADeepAddressSurvivesTheSocketOpening(t *testing.T) {
	read := func(p string) []byte {
		b, err := staticFS.ReadFile("static/" + p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	rec := func(name string) []byte {
		return []byte("export function " + name + "() { (window.__did ||= []).push('" + name + "'); }\n")
	}
	modules := browserModuleStubs()
	modules["/app.js"] = read("app.js")
	modules["/state.js"] = read("state.js")
	modules["/util.js"] = read("util.js")
	modules["/ws.js"] = []byte("export function send() { return ''; }\nexport function query() {}\nexport function connect() {}\nexport function wake() {}\n")
	modules["/panel.js"] = []byte("export function renderPanel() {}\n")
	modules["/views/record.js"] = rec("renderRecord")
	modules["/views/projects.js"] = append(rec("renderProjects"), []byte("export function renderProjPill() {}\nexport function viewProject(id) { (window.__did ||= []).push('viewProject:' + id); }\n")...)
	modules["/views/settings.js"] = append(rec("renderSettings"), []byte("export function acceptSettingsConfig() {}\nexport function rejectSettingsConfig() {}\nexport function acceptProviderSave() {}\nexport function rejectProviderSave() {}\nexport function acceptSpeechLists() {}\nexport function rejectSpeechLists() {}\nexport function acceptDashboardToken() {}\nexport function settingsConnectionLost() {}\n")...)
	modules["/views/backups.js"] = stubModule("forgetBackupsSecrets")
	modules["/announce.js"] = stubModule("announce")
	runPageInEngines(t, deepAddressRacePage, modules)
}
