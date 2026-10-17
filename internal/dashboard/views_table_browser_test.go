//go:build !windows

package dashboard

import "testing"

const viewsTablePage = `<!doctype html>
<div id="nav"></div><div id="views"></div>
<div id="view-chat" class="view"></div><div id="view-home" class="view"></div><div id="view-projects" class="view"></div>
<div id="view-memory" class="view"></div><div id="view-identity" class="view"></div><div id="view-plugins" class="view"></div>
<div id="view-settings" class="view"></div><div id="view-record" class="view"></div>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div>
<div id="composer-wrap"></div><div id="proj-space"></div><div id="dock"></div><span id="pill-proj"></span>
<script type="module">
import { assert, run } from '/__harness.js';
import { S } from '/state.js';
import { go } from '/app.js';
run(() => {
  const did = window.__did = [];
  S.interactionWake = () => did.push('wake');
  S.requestSettingsSection = () => did.push('section');
  S.connected = true;
  const want = {
    home: ['q:status', 'q:continuity', 'q:projects', 'q:work', 'renderHome', 'panel', 'Home'],
    chat: ['wake', 'panel', 'Chat'],
    projects: ['q:projects', 'renderProjects', 'panel', 'Projects'],
    memory: ['q:identity', 'renderMemory', 'panel', 'Memory'],
    identity: ['q:identity', 'q:continuity', 'renderIdentity', 'panel', 'Identity'],
    settings: ['q:config', 'q:tools', 'q:sandbox', 'section', 'renderSettings', 'panel', 'Settings'],
    plugins: ['q:config', 'renderPlugins', 'panel', 'Plugins'],
    record: ['renderRecord', 'panel', 'Record'],
  };
  for (const v of Object.keys(want)) {
    did.length = 0;
    go(v);
    const got = did.concat([document.getElementById('crumb').textContent]);
    assert(got.join() === want[v].join(), v + ' read and drew ' + JSON.stringify(got) + ', want ' + JSON.stringify(want[v]));
    assert(document.getElementById('view-' + v).classList.contains('on'), v + ' is not the view shown');
  }
});
</script>`

func TestEachViewReadsAndDrawsWhatItAlwaysHas(t *testing.T) {
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
	modules["/ws.js"] = []byte("export function send() { return ''; }\nexport function query(q) { (window.__did ||= []).push('q:' + q); }\nexport function connect() {}\nexport function wake() {}\n")
	modules["/panel.js"] = []byte("export function renderPanel() { (window.__did ||= []).push('panel'); }\n")
	modules["/views/home.js"] = rec("renderHome")
	modules["/views/memory.js"] = rec("renderMemory")
	modules["/views/identity.js"] = rec("renderIdentity")
	modules["/views/plugins.js"] = append(rec("renderPlugins"), []byte(`export function rejectPluginAction() { return false; }`)...)
	modules["/views/record.js"] = rec("renderRecord")
	modules["/views/projects.js"] = append(rec("renderProjects"), []byte("export function renderProjPill() {}\nexport function viewProject() {}\n")...)
	modules["/views/settings.js"] = append(rec("renderSettings"), []byte("export function acceptSettingsConfig() {}\nexport function rejectSettingsConfig() {}\nexport function acceptProviderSave() {}\nexport function rejectProviderSave() {}\nexport function acceptSpeechLists() {}\nexport function rejectSpeechLists() {}\nexport function acceptDashboardToken() {}\nexport function settingsConnectionLost() {}\n")...)
	modules["/views/backups.js"] = stubModule("forgetBackupsSecrets")
	modules["/announce.js"] = stubModule("announce")
	runPageInEngines(t, viewsTablePage, modules)
}
