//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// A SETTINGS SECTION IS AN ADDRESS. Opening Speech and reloading landed on
// Substrate: the section lived in a variable. The address now names the
// group shown (#/settings/voice), and a section after its group's first
// (#/settings/model/providers); an old name shows its new address; it is
// rewritten in place, so switching adds no history; a name that is not a
// section moves nothing; a render off the Settings page writes no address.
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
  assert(location.hash === '#/settings/model', 'the address does not name the group shown: ' + location.hash);
  const entries = history.length;
  S.settingsSection('sandbox'); S.requestSettingsSection(); renderSettings();
  assert(location.hash === '#/settings/sandbox', 'a section switch did not reach the address: ' + location.hash);
  assert(history.length === entries, 'switching sections added history entries');
  assert(S.settingsSection('nonsense') === false && location.hash === '#/settings/sandbox', 'a name that is not a section moved the page');
  assert(S.settingsSection('providers') === true, 'a real section was refused');
  renderSettings();
  const shown = document.querySelector('#settings-stack [data-section="providers"]'), named = document.querySelector('#settings-stack [data-settings-page-name]');
  assert(location.hash === '#/settings/model/providers' && shown && named && named.textContent === 'Model', 'the named section is not the one shown, on a page that says Model');
  // Each step moves to another section: All settings' address, a section
  // after its group's first, and an old name; each marks its group.
  for (const [name, at, group] of [['all', '#/settings/model', 'model'], ['model/providers', '#/settings/model/providers', 'model'],
    ['speech', '#/settings/voice', 'voice']]) {
    assert(S.settingsSection(name) === true, name + ' was refused');
    renderSettings();
    assert(location.hash === at, name + ' is at ' + location.hash + ', not ' + at);
    assert(S.settingsGroup() === group, name + ' marks ' + S.settingsGroup() + ' in the list, not ' + group);
  }
  S.view = 'chat'; S.settingsSection('substrate'); renderSettings();
  assert(location.hash === '#/settings/voice', 'a render off the Settings page wrote the address');
  // Technical's forms are its panels now: each old address lands on its panel.
  for (const [old, panel] of [['witness', 'verification'], ['prompt', 'limits'], ['agency', 'helpers'], ['identity', 'reflection'], ['logs', 'logs'], ['storage', 'storage'], ['limits', 'limits']]) {
    assert(S.settingsSection(old) === false && location.hash === '#/settings/technical/' + panel, old + ' did not land on its panel: ' + location.hash);
  }
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

// A RELOAD LANDS WHERE IT LEFT, AND THE ROUTER KNOWS SECTIONS. The real
// app.js restores #/settings/speech, and Settings, fetched when it is first
// drawn on connection (step 21), is told the section once it is in; a later
// #/settings/sandbox moves the section without leaving the page; the
// destination in view is aria-current in the navigation.
func TestAReloadLandsOnTheSettingsSectionItLeft(t *testing.T) {
	page := `<!doctype html>
<nav id="nav"><button type="button" class="nav-item active" data-view="chat" aria-current="page"></button><button type="button" class="nav-item" data-view="settings"></button></nav>
<div id="views"></div><div id="view-chat" class="view on"></div><div id="view-settings" class="view"></div>
<div id="crumb"></div><div id="toast"></div><div id="firstboot"></div>
<div id="composer-wrap"></div><div id="proj-space"></div><div id="dock"></div>
<span id="pill-proj"></span><div id="view-projects" class="view"></div>
<script type="module">
import { report, assert, until } from '/__harness.js';
import { S } from '/state.js';
location.hash = '#/settings/speech';
const entriesBeforeRestore = history.length;
const { parseHash, go } = await import('/app.js');
try {
  assert(location.hash === '#/settings/speech' && history.length === entriesBeforeRestore, 'cold Settings restore rewrote the section address');
  const r = parseHash('#/settings/speech');
  assert(r.view === 'settings' && r.project === 'speech', 'the grammar cannot say a section: ' + JSON.stringify(r));
  const settings = document.querySelector('.nav-item[data-view="settings"]'), chat = document.querySelector('.nav-item[data-view="chat"]');
  assert(settings.getAttribute('aria-current') === 'page' && !chat.hasAttribute('aria-current'), 'the destination in view is not the current one');
  // Settings is fetched when it is first drawn, on connection, and told the
  // section the address names once it is in (step 21).
  S.connected = true; go('settings');
  await until(() => (window.__sec || []).length > 0, 3000);
  assert(S.view === 'settings' && (window.__sec || [])[0] === 'speech', 'the reload did not land on the section: view=' + S.view + ' sections=' + JSON.stringify(window.__sec));
  assert(window.__sectionRequests === 1, 'connection did not request the selected section');
  assert(history.length === entriesBeforeRestore, 'connected Settings entry added a spurious history entry');
  location.hash = '#/settings/sandbox';
  setTimeout(() => {
    try {
      assert(S.view === 'settings' && window.__sec.at(-1) === 'sandbox' && window.__renders > 0, 'the router did not move the section: ' + JSON.stringify(window.__sec) + ' renders ' + window.__renders);
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
		"/sections.js":               stub("export function initSections() {}\nexport function sectionTitle(v) { return v; }\nexport function publish() {}\nexport function onSections() {}\nexport function onLayout() {}\n"),
		"/voice.js":                  stub("export function wireMic() {}\nexport function bindTransport() {}\nexport function speak() {}\nexport function render() {}\nexport function connectionLost() {}\n"),
		"/views/chat.js":             stub("export function scrollThread() {}\nexport function addMsg() {}\nexport function attachSpeaker() {}\nexport function sysLine() {}\n"),
		"/views/home.js":             stub("export function renderHome() {}\n"),
		"/views/memory.js":           stub("export function renderMemory() {}\n"),
		"/views/identity.js":         stub("export function renderIdentity() {}\n"),
		"/views/identity/between.js": stub("export function renderBetween() {}\n"),
		"/views/plugins.js":          stub("export function renderPlugins() {}\nexport function rejectPluginAction() { return false; }\nexport function applyPluginStored() {}\n"),
		"/views/settings.js":         stub("import { S } from '../state.js';\nS.settingsSection = n => { (window.__sec ||= []).push(n); return true; };\nS.settingsAddress = () => '#/settings/' + (window.__sec?.at(-1) || 'substrate');\nS.requestSettingsSection = () => { if (!S.connected) return; window.__sectionRequests = (window.__sectionRequests || 0) + 1; };\nexport function renderSettings() { window.__renders = (window.__renders || 0) + 1; }\n"),
		"/views/projects.js":         stub("export function renderProjects() {}\nexport function renderProjPill() {}\nexport function viewProject() {}\n"),
		// The rest of Settings' group, fetched with it when it first opens.
		"/views/plugin-setting.js": stub(""),
		"/sandbox.js":              stub(""),
		"/tools.js":                stub(""),
		"/views/backups.js":        stub("export function forgetBackupsSecrets() {}\n"),
		"/views/messages.js":       stub(""),
		"/views/mail-repair.js":    stub(""),
		"/views/expert.js":         stub(""),
		"/panel.js":                stub("export function renderPanel() {}\n"),
		"/overlay.js":              stub("export function holdReloadWhile() {}\nexport function restoreDraft() {}\nexport function onOverlayChanged() {}\nexport function reloadForTheOverlay() {}\n"),
		"/theme.js":                stub("export function onTheme() {}\n"),
		"/presence.js":             stub("export function renderPresence() {}\nexport function setThinking() {}\n"),
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
 S.connected=false;S.settingsSection('held-mail');assert(calls.length===1,'address restore sent Unsent messages before connection');
 S.connected=true;S.requestSettingsSection?.();assert(calls.join()==='backups,messages,held_mail','connection did not fetch addressed People and messages: '+calls);
 assert(S.settingsAddress?.()==='#/settings/people','section owner did not provide its complete address');
});</script>`
	modules := map[string][]byte{
		"/state.js":             []byte(`export const S={providers:[],providersLoaded:true};`),
		"/ws.js":                []byte(`export const calls=[];export function query(q){calls.push(q)}export function send(){return ''}`),
		"/views/backups.js":     []byte(`import {query} from '/ws.js';export function requestBackups(){query('backups')}export function backupsHTML(){return ''}export function wireBackups(){}export function forgetBackupsSecrets(){}`),
		"/views/mail-repair.js": []byte(`import {query} from '/ws.js';export function requestHeldMail(){query('held_mail')}export function mailRepairHTML(){return ''}export function wireMailRepair(){}export function mailRepairDisconnected(){}export function openUnsent(){}`),
		"/views/messages.js":    []byte(`import {query} from '/ws.js';export function requestMessages(){query('messages')}export function messagesHTML(){return ''}export function wireMessages(){}export function messagesDisconnected(){}export function installedChannels(){return []}`),
	}
	runPageInEngines(t, page, modules)
}
