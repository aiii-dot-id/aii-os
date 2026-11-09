//go:build !windows

package dashboard

import "testing"

// EACH VIEW READS AND DRAWS WHAT IT ALWAYS HAS. Opening a view through the
// real app.js asks the host for that view's frames, draws that view and
// names it in the crumb — whichever module holds the table (views.js
// since T34; app.js before). Each view is opened in turn, and what it asked
// for and drew is compared with the whole table once it has drawn: a view
// fetched when first opened draws when it is in (step 21).
const viewsTablePage = `<!doctype html>
<div id="nav"></div><div id="views"></div>
<div id="view-chat" class="view"></div><div id="view-home" class="view"></div><div id="view-projects" class="view"></div>
<div id="view-memory" class="view"></div><div id="view-identity" class="view"></div><div id="view-between" class="view"></div><div id="view-experiences" class="view"></div><div id="view-plugins" class="view"></div>
<div id="view-status" class="view"><div id="status-stack"></div></div><div id="view-settings" class="view"></div><div id="view-record" class="view"></div>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div>
<div id="composer-wrap"></div><div id="proj-space"></div><div id="dock"></div><span id="pill-proj"></span>
<script type="module">
import { assert, run, until } from '/__harness.js';
import { S } from '/state.js';
import { go } from '/app.js';
run(async () => {
  const did = window.__did = [];
  S.interactionWake = () => did.push('wake');
  S.connected = true;
  const want = {
    chat: ['q:status', 'q:continuity', 'q:projects', 'q:work', 'q:identity', 'renderHome', 'wake', 'Chat'],
    projects: ['q:projects', 'q:work', 'renderProjects', 'Projects'],
    memory: ['q:identity', 'renderMemory', 'Memory'],
    identity: ['q:identity', 'q:continuity', 'q:work', 'renderIdentity', 'Identity'],
    between: ['q:identity', 'q:continuity', 'renderBetween', 'Between you'],
    experiences: ['q:identity', 'renderExperiences', 'Experiences'],
    status: ['q:status', 'q:continuity', 'q:config', 'q:providers', 'q:tools', 'q:work', 'q:asks', 'q:spend', 'Status'],
    settings: ['q:config', 'q:tools', 'q:sandbox', 'section', 'renderSettings', 'Settings'],
    plugins: ['q:config', 'renderPlugins', 'Plugins'],
    record: ['renderRecord', 'Record'],
  };
  for (const v of Object.keys(want)) {
    did.length = 0;
    go(v);
    const crumb = document.getElementById('crumb').textContent;
    // A view whose address moves draws again as the address settles; it is
    // read once.
    const got = () => did.filter((x, i, a) => x !== a[i - 1]).concat([crumb]);
    await until(() => got().join() === want[v].join(), 3000);
    assert(got().join() === want[v].join(), v + ' read and drew ' + JSON.stringify(got()) + ', want ' + JSON.stringify(want[v]));
    assert(document.getElementById('view-' + v).classList.contains('on'), v + ' is not the view shown');
    if (v === 'status') assert(document.getElementById('status-heading'), 'Status was not drawn');
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
	modules["/views/home.js"] = rec("renderHome")
	modules["/views/memory.js"] = rec("renderMemory")
	modules["/views/identity.js"] = rec("renderIdentity")
	modules["/views/identity/between.js"] = rec("renderBetween")
	modules["/views/plugins.js"] = append(rec("renderPlugins"), []byte(`export function rejectPluginAction() { return false; }`)...)
	modules["/views/record.js"] = append(rec("renderRecord"), stubModule("readRest", "payloadBox")...) // step 19's sent panel imports the record's readers
	modules["/views/identity/record.js"] = append(rec("renderRecord"), stubModule("readRest", "payloadBox")...)
	modules["/views/identity/experiences.js"] = rec("renderExperiences")
	modules["/views/projects.js"] = append(rec("renderProjects"), []byte("export function renderProjPill() {}\nexport function viewProject() {}\n")...)
	// Settings sets its own hook for the section it opens at, as it arrives.
	modules["/views/settings.js"] = append(rec("renderSettings"), []byte("import { S } from '/state.js';\nS.requestSettingsSection = () => (window.__did ||= []).push('section');\nexport function acceptSettingsConfig() {}\nexport function rejectSettingsConfig() {}\nexport function acceptProviderSave() {}\nexport function rejectProviderSave() {}\nexport function acceptSpeechLists() {}\nexport function rejectSpeechLists() {}\nexport function settingsConnectionLost() {}\n")...)
	modules["/views/backups.js"] = stubModule("forgetBackupsSecrets")
	modules["/announce.js"] = stubModule("announce")
	runPageInEngines(t, viewsTablePage, modules)
}
