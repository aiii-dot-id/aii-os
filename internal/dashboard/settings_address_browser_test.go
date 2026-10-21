//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestTheSettingsSectionIsInTheAddress(t *testing.T) {
	page := `<!doctype html><html><body><div id="settings-stack"></div>
<script type="module">
import { S } from '/state.js';
import { renderSettings } from '/views/settings.js';
import { run } from '/__harness.js';
run(async (assert) => {
  S.view = 'settings'; S.providersLoaded = true; S.providers = [];
  S.config = { llm: {}, speech: {}, dashboard: { host: '192.0.2.10', port: 8181, tls: true, require_token: false },
    prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [] };
  renderSettings();
  assert(location.hash === '#/settings/substrate', 'the address does not name the section shown: ' + location.hash);
  const entries = history.length;
  document.querySelector('[data-sec="witness"]').click();
  assert(location.hash === '#/settings/witness', 'a section switch did not reach the address: ' + location.hash);
  assert(history.length === entries, 'switching sections added history entries');
  assert(S.settingsSection('nonsense') === false && location.hash === '#/settings/witness', 'a name that is not a section moved the page');
  assert(S.settingsSection('prompt') === true, 'a real section was refused');
  renderSettings();
  const shown = document.querySelector('[data-sec="prompt"]');
  assert(location.hash === '#/settings/prompt' && shown && !shown.classList.contains('ghost'), 'the named section is not the one shown');
  S.view = 'chat'; S.settingsSection('agency'); renderSettings();
  assert(location.hash === '#/settings/prompt', 'a render off the Settings page wrote the address');
});
</script></body></html>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js", "static/util.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { providers: [], config: null, providersLoaded: false };`)
	modules["/ws.js"] = []byte(`export function send() { return 'req-1'; } export function query() { return 'req-1'; }`)
	modules["/pending.js"] = []byte(`export function pendingSlot() { return { arm(){}, claim(){ return false; }, drop(){}, waiting(){ return false; } }; } export function anyPending() { return false; }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, page, modules)
}

func TestAReloadLandsOnTheSettingsSectionItLeft(t *testing.T) {
	page := `<!doctype html>
<nav id="nav"><button type="button" class="nav-item active" data-view="chat" aria-current="page"></button><button type="button" class="nav-item" data-view="settings"></button></nav>
<div id="views"></div><div id="view-chat" class="view on"></div><div id="view-settings" class="view"></div>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div>
<div id="composer-wrap"></div><div id="proj-space"></div><div id="dock"></div>
<span id="pill-proj"></span><div id="view-projects" class="view"></div>
<script type="module">
import { report, assert } from '/__harness.js';
import { S } from '/state.js';
location.hash = '#/settings/speech';
const entriesBeforeRestore = history.length;
const { parseHash, go } = await import('/app.js');
try {
  assert(location.hash === '#/settings/speech' && history.length === entriesBeforeRestore, 'cold Settings restore rewrote the section address');
  const r = parseHash('#/settings/speech');
  assert(r.view === 'settings' && r.project === 'speech', 'the grammar cannot say a section: ' + JSON.stringify(r));
  assert(S.view === 'settings' && (window.__sec || [])[0] === 'speech', 'the reload did not land on the section: view=' + S.view + ' sections=' + JSON.stringify(window.__sec));
  const settings = document.querySelector('.nav-item[data-view="settings"]'), chat = document.querySelector('.nav-item[data-view="chat"]');
  assert(settings.getAttribute('aria-current') === 'page' && !chat.hasAttribute('aria-current'), 'the destination in view is not the current one');
  S.connected = true; go('settings');
  assert(window.__sectionRequests === 1, 'connection did not request the selected section');
  assert(history.length === entriesBeforeRestore, 'connected Settings entry added a spurious history entry');
  location.hash = '#/settings/witness';
  setTimeout(() => {
    try {
      assert(S.view === 'settings' && window.__sec.at(-1) === 'witness' && window.__renders > 0, 'the router did not move the section: ' + JSON.stringify(window.__sec) + ' renders ' + window.__renders);
      report('OK');
    } catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
  }, 80);
} catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
</script>`
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
	runPageInEngines(t, page, map[string][]byte{
		"/app.js":   appJS,
		"/state.js": state,
		"/util.js":  util,
		"/ws.js": stub(`export function send() { return ''; }
export function query(q) { (window.__queries ||= []).push(q); }
export function connect() {}
export function wake() {}
`),
		"/sections.js":       stub("export function initSections() {}\nexport function sectionTitle(v) { return v; }\nexport function publish() {}\nexport function onSections() {}\nexport function onLayout() {}\n"),
		"/voice.js":          stub("export function wireMic() {}\nexport function bindTransport() {}\nexport function speak() {}\nexport function render() {}\nexport function connectionLost() {}\n"),
		"/views/chat.js":     stub("export function scrollThread() {}\nexport function addMsg() {}\nexport function attachSpeaker() {}\nexport function sysLine() {}\n"),
		"/views/home.js":     stub("export function renderHome() {}\n"),
		"/views/memory.js":   stub("export function renderMemory() {}\n"),
		"/views/identity.js": stub("export function renderIdentity() {}\n"),
		"/views/plugins.js":  stub("export function renderPlugins() {}\nexport function rejectPluginAction() { return false; }\nexport function applyPluginStored() {}\n"),
		"/views/settings.js": stub("import { S } from '../state.js';\nS.settingsSection = n => { (window.__sec ||= []).push(n); return true; };\nS.settingsAddress = () => '#/settings/' + (window.__sec?.at(-1) || 'substrate');\nS.requestSettingsSection = () => { window.__sectionRequests = (window.__sectionRequests || 0) + 1; };\nexport function renderSettings() { window.__renders = (window.__renders || 0) + 1; }\n"),
		"/views/projects.js": stub("export function renderProjects() {}\nexport function renderProjPill() {}\nexport function viewProject() {}\n"),
		"/panel.js":          stub("export function renderPanel() {}\n"),
		"/overlay.js":        stub("export function holdReloadWhile() {}\nexport function restoreDraft() {}\nexport function onOverlayChanged() {}\n"),
		"/theme.js":          stub("export function onTheme() {}\n"),
		"/presence.js":       stub("export function renderPresence() {}\nexport function setThinking() {}\n"),
	})
}

func TestSettingsSectionRequestsWaitForConnection(t *testing.T) {
	page := `<!doctype html><div id="settings-stack"></div>
<script type="module">
import {S} from '/state.js';import '/views/settings.js';import {calls} from '/ws.js';import {run} from '/__harness.js';
run(assert=>{
 S.view='settings';S.connected=false;
 S.settingsSection('backups');assert(calls.length===0,'address restore sent Backups before connection');
 S.connected=true;S.requestSettingsSection?.();assert(calls.join()==='backups','connection did not fetch the addressed Backups section: '+calls);
 S.connected=false;S.settingsSection('held-mail');assert(calls.length===1,'address restore sent Held Mail before connection');
 S.connected=true;S.requestSettingsSection?.();assert(calls.join()==='backups,held_mail','connection did not fetch addressed Held Mail: '+calls);
 assert(S.settingsAddress?.()==='#/settings/held-mail','section owner did not provide its complete address');
});</script>`
	modules := map[string][]byte{
		"/state.js":             []byte(`export const S={providers:[],providersLoaded:true};`),
		"/ws.js":                []byte(`export const calls=[];export function query(q){calls.push(q)}export function send(){return ''}`),
		"/views/backups.js":     []byte(`import {query} from '/ws.js';export function requestBackups(){query('backups')}export function backupsHTML(){return ''}export function wireBackups(){}export function forgetBackupsSecrets(){}`),
		"/views/mail-repair.js": []byte(`import {query} from '/ws.js';export function requestHeldMail(){query('held_mail')}export function mailRepairHTML(){return ''}export function wireMailRepair(){}export function mailRepairDisconnected(){}`),
	}
	runPageInEngines(t, page, modules)
}
