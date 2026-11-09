//go:build !windows

package dashboard

import "testing"

// SETTINGS → APP IS THE BOARD, AND DOES WHAT IT SAYS (spec step 18, the
// board SettingsApp). On the real Settings page: the heading and its
// sentence, then Appearance, their mark, Notifications, Updates and Remove,
// in the board's order. Choosing a theme sets it in this browser. The update
// switch saves through the configuration door at once; a readback keeps
// focus on it and draws what is configured; a press while a save is on its
// way is taken back and says why. A withdrawn release is said beside the
// state, never under Details. An unsaved cadence is kept through a readback.
func TestSettingsAppIsTheBoardAndDoesWhatItSays(t *testing.T) {
	page := `<!doctype html>
<div class="app" id="frame"><div id="settings-stack"></div></div>
<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { frames } from './ws.js';
import { assert, run, until } from './__harness.js';
run(async () => {
  S.config = ` + settingsAppConfig(false) + `;
  S.update = { enabled: true, current_version: '0.2.1', checked_at: new Date(Date.now() - 12 * 60000).toISOString(), automatic: false,
    running_withdrawn: true, available_version: '0.2.2', release_url: 'https://example.invalid/releases/0.2.2' };
  S.stats = { name: 'Taylor', uninstall_offered: true };
  let chosen = 'system';
  S.themeChoice = () => chosen; S.setThemeChoice = (v) => { chosen = v; };
  S.providersLoaded = true; S.view = 'settings';
  renderSettings();
  S.settingsSection('updates'); S.requestSettingsSection(); renderSettings();
  const app = () => document.querySelector('[data-app]');
  assert(app() && app().querySelector('h2').textContent === 'App' && app().querySelector('.settings-app__intro').textContent === 'This browser, this computer, and the version of AII OS running.', 'the section and its sentence');
  // App's stylesheet shapes App alone: the page's frame (.app) keeps no width
  // of App's once it has loaded.
  await until(() => document.querySelector('link[data-app-css]')?.sheet);
  assert(getComputedStyle(document.getElementById('frame')).maxWidth === 'none' && getComputedStyle(document.getElementById('frame')).display !== 'flex', 'App\'s stylesheet shaped the page\'s frame: max-width ' + getComputedStyle(document.getElementById('frame')).maxWidth + ', display ' + getComputedStyle(document.getElementById('frame')).display);
  assert(getComputedStyle(app()).maxWidth === '820px', 'App\'s stylesheet did not load: ' + getComputedStyle(app()).maxWidth);
  const heads = [...app().querySelectorAll('h3')].map(h => h.textContent);
  assert(JSON.stringify(heads) === JSON.stringify(['Appearance', 'Taylor\'s mark', 'Notifications', 'Updates', 'Remove AII OS from this computer']), 'the parts in the board\'s order: ' + JSON.stringify(heads));

  // Appearance: three choices, the one in force checked; a choice sets it.
  const radios = () => [...app().querySelectorAll('input[type="radio"][name="app-theme"]')];
  assert(radios().length === 3 && radios()[0].checked && radios().map(r => r.parentElement.textContent).join('|') === 'As this device is set|Light|Dark', 'the theme choices: ' + radios().map(r => r.parentElement.textContent + (r.checked ? '*' : '')).join('|'));
  radios()[2].click();
  assert(chosen === 'dark', 'choosing Dark did not set the theme: ' + chosen);
  assert(app().textContent.includes('Colours Taylor has chosen for this page may change these.'), 'the theme hint names them');

  // Updates: the state names the version running; the withdrawal is beside it.
  const state = app().querySelector('[data-update-state]');
  assert(state.textContent.startsWith('AII OS 0.2.1 · Update available: 0.2.2 — automatic apply is off'), 'the state line: ' + state.textContent);
  const gone = app().querySelector('[data-update-withdrawn]');
  assert(gone && !gone.closest('details'), 'a withdrawn release is said beside the state, not under Details');
  assert(app().querySelector('a.settings-app__link').textContent === 'What changed in 0.2.2', 'what changed in the version offered');

  // The switch saves at once, and nothing else with it.
  const box = () => app().querySelector('#cfg-uauto');
  assert(box().getAttribute('role') === 'switch' && !box().checked, 'the switch is off, as configured');
  box().focus();
  box().click();
  const saved = frames.filter(f => f.type === 'config_set').at(-1);
  assert(saved && saved.config['updates.automatic'] === true && Object.keys(saved.config).length === 1, 'the switch did not save itself at once: ' + JSON.stringify(saved));
  // The readback: what is configured is drawn, and focus stays on the switch.
  S.config = ` + settingsAppConfig(true) + `;
  renderSettings();
  assert(box().checked, 'after the readback the switch is not what is configured');
  assert(document.activeElement === box(), 'focus did not stay on the switch through the readback: ' + (document.activeElement && document.activeElement.outerHTML.slice(0, 80)));
  assert(app().querySelector('[data-update-state]').textContent.includes('it is downloaded, verified and installed on the next check'), 'the state line does not follow the switch: ' + app().querySelector('[data-update-state]').textContent);

  // A press while a save is on its way is taken back, and says why.
  const before = frames.length;
  window.__configBusy = true;
  box().click();
  window.__configBusy = false;
  assert(box().checked === true && frames.length === before, 'a press while a save was on its way was kept or sent');
  assert(app().querySelector('[data-update-auto-said]').textContent === 'Waiting for AII OS to confirm…', 'the taken-back press says nothing: ' + app().querySelector('[data-update-auto-said]').textContent);

  // An unsaved cadence is kept through a readback, under Details.
  const field = () => app().querySelector('details [data-cog-key="updates.check_interval_minutes"]');
  assert(field(), 'the cadence field is not under Details');
  field().value = '30';
  field().dispatchEvent(new Event('input', { bubbles: true }));
  renderSettings();
  assert(field().value === '30', 'an unsaved cadence was lost on the readback: ' + field().value);
});
</script>`
	modules := settingsPageModules()
	modules["/pending.js"] = []byte(`export function pendingSlot() { return { arm(){ return true; }, claim(){ return false; }, drop(){}, waiting(){ return !!window.__configBusy; } }; } export function anyPending() { return false; }`)
	runPageInEngines(t, page, modules)
}

// settingsAppConfig is a configuration enough to draw Settings → App, with
// updates.automatic as given and the cadence on the Updates part.
func settingsAppConfig(automatic bool) string {
	a := "false"
	if automatic {
		a = "true"
	}
	return `{ llm: { provider: 'p', model: 'm' }, dashboard: { host: '127.0.0.1', port: 1, tls: false },
    prompt: {}, witness: {}, logs: {}, plugins: {}, agency: {}, restart_required: [],
    updates: { automatic: ` + a + `, check_interval_minutes: 60 },
    cognition: { settings: [{ key: 'updates.check_interval_minutes', min: 15, max: 1440, default: 60, unit: 'minutes', card: 'updates' }] } }`
}

// APP'S SAVES FINISH THROUGH THEIR OWN ANSWERS (a review 2026-11-01 08:50Z, the
// three save paths). With the real pending slots and Settings' own answer
// handlers: the update switch says beside itself that it is being checked,
// ignores an answer to another request, says Saved on its own, says its
// refusal, says a lost connection, and is taken back when nothing was sent.
// The cadence form's Save sends the cadence alone; its matching answer
// retires the draft, so a later configuration is drawn. On a build without
// updates the cadence Save works and says there is nothing to save.
func TestSettingsAppSavesFinishThroughTheirOwnAnswers(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { renderSettings, acceptSettingsConfig, rejectSettingsConfig, settingsConnectionLost } from './views/settings.js';
import { frames } from './ws.js';
import { assert, run } from './__harness.js';
run(async () => {
  const cfg = (auto, every) => ({ llm: { provider: 'p', model: 'm' }, dashboard: { host: '127.0.0.1', port: 1, tls: false },
    prompt: {}, witness: {}, logs: {}, plugins: {}, agency: {}, restart_required: [],
    updates: { automatic: auto, check_interval_minutes: every },
    cognition: { settings: [{ key: 'updates.check_interval_minutes', min: 15, max: 1440, default: 60, unit: 'minutes', card: 'updates' }] } });
  S.config = cfg(false, 60);
  S.update = { enabled: true, current_version: '0.2.1', checked_at: 'today', automatic: false };
  S.providersLoaded = true; S.view = 'settings';
  renderSettings();
  S.settingsSection('updates'); S.requestSettingsSection(); renderSettings();
  const box = () => document.querySelector('#cfg-uauto');
  const said = () => document.querySelector('[data-update-auto-said]').textContent;
  const last = () => frames.filter(f => f.type === 'config_set').at(-1);

  // A press: the value sent is shown, held, and said to be checked.
  box().click();
  const a = last();
  assert(a && a.config['updates.automatic'] === true && Object.keys(a.config).length === 1, 'the switch did not send itself alone: ' + JSON.stringify(a));
  assert(box().checked && box().disabled && said().startsWith('Checking and saving'), 'while checked: checked ' + box().checked + ', disabled ' + box().disabled + ', said "' + said() + '"');
  // An answer to another request is not this one's.
  acceptSettingsConfig('req-other'); renderSettings();
  assert(said().startsWith('Checking and saving'), 'an answer to another request was taken as the switch\'s: ' + said());
  // Its own answer: Saved, beside the switch, and the switch free again.
  S.config = cfg(true, 60);
  acceptSettingsConfig(a.request_id); renderSettings();
  assert(said() === 'Saved.' && box().checked && !box().disabled, 'its own answer: "' + said() + '", checked ' + box().checked + ', disabled ' + box().disabled);

  // Its own refusal is said beside it, and the switch shows what is configured.
  box().click();
  rejectSettingsConfig('updates.automatic: the host refused it', last().request_id);
  assert(said().includes('the host refused it') && box().checked === true, 'its refusal: "' + said() + '", checked ' + box().checked);
  // A lost connection is said beside it.
  box().click();
  settingsConnectionLost();
  assert(said().startsWith('Connection lost before configuration confirmation') && box().checked === true, 'a lost connection: "' + said() + '", checked ' + box().checked);
  // Nothing sent: said, and the press taken back.
  const before = frames.length;
  window.__offline = true; box().click(); window.__offline = false;
  assert(frames.length === before && said().startsWith('Not connected') && box().checked === true, 'a press that was not sent: frames ' + (frames.length - before) + ', "' + said() + '", checked ' + box().checked);

  // The cadence form: its Save sends the cadence alone, and the matching
  // answer retires the draft, so a later configuration is what is drawn.
  const field = () => document.querySelector('details [data-cog-key="updates.check_interval_minutes"]');
  field().value = '30';
  field().dispatchEvent(new Event('input', { bubbles: true }));
  document.querySelector('[data-save="updates"]').click();
  const c = last();
  assert(c && c.config['updates.check_interval_minutes'] === 30 && !('updates.automatic' in c.config), 'the cadence Save did not send the cadence alone: ' + JSON.stringify(c && c.config));
  S.config = cfg(true, 30);
  acceptSettingsConfig(c.request_id); renderSettings();
  S.config = cfg(true, 45); renderSettings();
  assert(field().value === '45', 'the acknowledged cadence was still held as an unsaved edit: ' + field().value);

  // A build without updates: no switch, and the cadence Save still works.
  S.update = { enabled: false, current_version: 'dev' };
  renderSettings();
  assert(!box(), 'a build without updates draws the switch');
  document.querySelector('[data-save="updates"]').click();
  renderSettings();
  const bar = document.querySelector('details [data-said="updates"]');
  assert(bar && bar.textContent === 'Nothing to save — no setting was changed.', 'the cadence Save on a build without updates: ' + (bar && bar.textContent));
});
</script>`
	modules := settingsPageModules()
	delete(modules, "/pending.js")
	modules["/ws.js"] = []byte(`export const frames = []; let n = 0;
export function send(f) { if (window.__offline) return ''; const id = 'req-' + (++n); frames.push(Object.assign({ request_id: id }, f)); return id; }
export function query(name, e) { return send(Object.assign({ type: 'query', query: name }, e || {})); }`)
	runPageInEngines(t, page, modules)
}

// APP'S SWITCH IS ANSWERED THROUGH THE PAGE'S OWN DISPATCHER (the proof
// carried from step 18's first slice, host 2026-11-01 09:04Z). Over the real
// ws.js and a fake socket: a press is sent alone and held; an answer to
// another request leaves it held; its own configuration answer says Saved and
// gives the focus back to the switch; its own refusal is said beside it; a
// lost connection is said; and a press with no socket sends nothing, is
// taken back and says so.
func TestAppsSwitchIsAnsweredThroughThePagesOwnDispatcher(t *testing.T) {
	page := substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { connect } from './ws.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
` + offerSettingsJS + `run(() => {
 const sockets = [];
 window.setTimeout = () => 1; window.clearTimeout = () => {};
 window.WebSocket = class {
   constructor() { this.readyState = 0; this.frames = []; sockets.push(this); }
   send(bytes) { this.frames.push(JSON.parse(bytes)); }
   recv(frame) { this.onmessage({ data: JSON.stringify(frame) }); }
 };
 const cfg = auto => Object.assign(` + settingsAppConfig(false) + `, { updates: { automatic: auto, check_interval_minutes: 60 } });
 S.view = 'settings'; S.identityExists = true; S.providersLoaded = true; S.config = cfg(false);
 S.update = { enabled: true, current_version: '0.2.1', checked_at: 'today', automatic: false };
 connect(); const socket = sockets.at(-1); socket.readyState = 1; socket.onopen();
 renderSettings(); S.settingsSection('updates'); S.requestSettingsSection(); renderSettings();
 const box = () => document.getElementById('cfg-uauto'), said = () => document.querySelector('[data-update-auto-said]').textContent;
 const last = () => socket.frames.at(-1);

 box().focus(); box().click(); const a = last();
 assert(a.type === 'config_set' && a.config['updates.automatic'] === true && Object.keys(a.config).length === 1, 'the press was not sent alone: ' + JSON.stringify(a));
 assert(box().disabled && said().startsWith('Checking and saving'), 'the press is not held while it is checked: "' + said() + '"');
 socket.recv({ type: 'config', request_id: 'foreign', config: cfg(true) });
 assert(box().disabled && said().startsWith('Checking and saving'), 'an answer to another request settled the switch: "' + said() + '"');
 socket.recv({ type: 'config', request_id: a.request_id, config: cfg(true) });
 assert(said() === 'Saved.' && box().checked && !box().disabled, 'its own answer: "' + said() + '", checked ' + box().checked + ', disabled ' + box().disabled);
 assert(document.activeElement === box(), 'the focus did not come back to the switch when its save settled: ' + (document.activeElement && document.activeElement.outerHTML.slice(0, 80)));

 box().click(); const b = last();
 socket.recv({ type: 'error', request_id: b.request_id, message: 'updates.automatic: the host refused it' });
 assert(said().includes('the host refused it') && box().checked === true && !box().disabled, 'its own refusal: "' + said() + '", checked ' + box().checked);

 box().click();
 socket.readyState = 3; socket.onclose();
 assert(said().startsWith('Connection lost before configuration confirmation') && box().checked === true, 'a lost connection: "' + said() + '", checked ' + box().checked);

 const before = socket.frames.length;
 box().click();
 assert(socket.frames.length === before && said().startsWith('Not connected') && box().checked === true, 'a press with no socket: frames ' + (socket.frames.length - before) + ', "' + said() + '", checked ' + box().checked);
});
</script>`
	ws, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	modules := wsPageModules(ws, []byte(`export const S={providers:[]};`))
	for _, file := range []string{"views/settings.js", "util.js"} {
		data, err := staticFS.ReadFile("static/" + file)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+file] = data
	}
	runPageInEngines(t, page, modules)
}
