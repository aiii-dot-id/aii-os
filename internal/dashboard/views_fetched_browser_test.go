//go:build !windows

package dashboard

import (
	"regexp"
	"strings"
	"testing"
)

// SETTINGS IS FETCHED WHEN FIRST OPENED, AND OFFERS ITS MODULES (step 21).
// The shipped page, in every engine: the conversation opens without Settings or Plugins
// fetched; opened, by its address or by its door before it is in, Settings
// is fetched and shows the section asked for; what it exports is offered to
// the socket (a frame delivered to it is TestSettingsHearsItsAnswersOnceItIsIn's);
// and once it is in, opening it again draws it at once.
func viewsFetchedJS(open string) string {
	return `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const fetched = path => performance.getEntriesByType('resource').some(e => new URL(e.name).pathname === path);
const section = () => (document.getElementById('settings-stack') || { dataset: {} }).dataset.section;
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  await until(() => S.view === 'chat', 4000);
  // waits by design: an absence is read, after the conversation has had the time to
  // fetch what it would.
  await new Promise(r => setTimeout(r, 300));
  check(!fetched('/views/settings.js') && !fetched('/views/plugins.js'), 'the conversation fetched Settings or Plugins: ' + performance.getEntriesByType('resource').map(e => new URL(e.name).pathname).filter(p => p.startsWith('/views/')).join(', '));
  check(typeof S.openSettings === 'function', 'Settings has no door before it is in');
  const want = ` + open + `;
  await until(() => section() === want, 6000);
  check(fetched('/views/settings.js'), 'Settings was not fetched when it was opened');
  check(section() === want, 'Settings did not open the section asked for: ' + section() + ', not ' + want);
  const notOffered = [` + settingsLaterJS() + `].filter(n => typeof (S.later || {})[n] !== 'function');
  check(notOffered.length === 0, 'Settings did not offer the socket: ' + notOffered.join(', '));
  // Once it is in, it draws at once, in the step that opens it.
  S.go('chat'); await until(() => S.view === 'chat', 2000);
  const stack = document.getElementById('settings-stack');
  if (stack) stack.replaceChildren();
  S.go('settings');
  check(stack && stack.children.length > 0, 'Settings, once in, did not draw in the step that opened it');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`
}

// settingsLaterNames are the names the socket calls through LATER that
// Settings answers. Each must be a function in S.later once Settings opens:
// a name that loses its exporter fails silently, since its frames then
// answer nothing. The list is checked against ws.js and the Settings index,
// both ways, before the page runs, so it cannot drift from either.
var settingsLaterNames = []string{
	"renderSettings", "acceptSettingsConfig", "rejectSettingsConfig",
	"acceptProviderSave", "rejectProviderSave", "acceptSpeechLists",
	"rejectSpeechLists", "acceptAccessCode", "rejectAccessCode",
	"settingsConnectionLost", "settingsSettled", "localPart",
}

func settingsLaterJS() string {
	return "'" + strings.Join(settingsLaterNames, "', '") + "'"
}

// settingsLaterNamesFollowTheCode stops the test by name when the list no
// longer names exactly what ws.js calls through LATER and the index exports.
func settingsLaterNamesFollowTheCode(t *testing.T) {
	t.Helper()
	ws, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	index, err := staticFS.ReadFile("static/views/settings.js")
	if err != nil {
		t.Fatal(err)
	}
	later := map[string]bool{}
	for _, m := range regexp.MustCompile(`const \{([^}]*)\} = LATER;`).FindAllStringSubmatch(string(ws), -1) {
		for _, n := range strings.Split(m[1], ",") {
			if n = strings.TrimSpace(n); n != "" {
				later[n] = true
			}
		}
	}
	if len(later) == 0 {
		t.Fatal("precondition: ws.js has no `const { ... } = LATER;`, so the list cannot be checked against it")
	}
	exported := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^export (?:async )?function (\w+)`).FindAllStringSubmatch(string(index), -1) {
		exported[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`(?m)^export \{([^}]*)\} from`).FindAllStringSubmatch(string(index), -1) {
		for _, n := range strings.Split(m[1], ",") {
			exported[strings.TrimSpace(n)] = true
		}
	}
	listed := map[string]bool{}
	for _, n := range settingsLaterNames {
		listed[n] = true
		if !later[n] {
			t.Fatalf("precondition: ws.js no longer calls %s through LATER; the list follows ws.js", n)
		}
	}
	for n := range later {
		if exported[n] && !listed[n] {
			t.Fatalf("precondition: the Settings index exports %s and ws.js calls it through LATER, but the list does not name it", n)
		}
	}
}

func TestSettingsIsFetchedWhenFirstOpenedAndOffersItsModules(t *testing.T) {
	settingsLaterNamesFollowTheCode(t)
	t.Run("by its address", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsFetchedJS(`(location.hash = '#/settings/backups', 'backups')`), homeStyles)
	})
	t.Run("by its door", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, viewsFetchedJS(`(S.openSettings && S.openSettings('speech'), 'speech')`), homeStyles)
	})
}

// A DOOR OPENED BEFORE SETTINGS IS IN HOLDS ONLY ITS MOMENT'S INTENT (step
// 21). The shipped page, with Settings held from arriving until the page
// lets it. Opened on the conversation and then left for Projects, the door is retired:
// Settings' arrival does not take the page back. Opened, left and come back
// to the conversation, it is retired too, and this case runs Settings' real door, so a
// door wrongly run is seen as the page going to Settings. Two doors opened
// in turn: only the newer is honoured. A door opened in the same step as two
// of the page's own changes of address is still honoured: those are the
// navigations before it, not after.
const settingsHeldJS = `import { S } from '/state.js';
window.__doors = [];
await new Promise(r => { window.__releaseSettings = r; });
S.openSettings = (...a) => { window.__doors.push(['openSettings', ...a]); };
S.openUpdates = (...a) => { window.__doors.push(['openUpdates', ...a]); };
export function renderSettings() {}
export const SECTIONS = [];
`

// settingsRealHeldJS holds the shipped Settings, served beside it, from
// arriving: its own doors replace the stand-ins as it is evaluated, and the
// group that brings it is held until the page lets it.
const settingsRealHeldJS = `export * from './settings-real.js';
await new Promise(r => { window.__releaseSettings = r; });
`

func settingsDoorJS(mode string) string {
	return `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const mode = '` + mode + `';
const at = async (hash, view) => { location.hash = hash; await until(() => S.view === view, 4000); };
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  await until(() => S.view === 'chat', 4000);
  if (mode === 'twice') { S.go('projects'); S.go('chat'); }
  S.openSettings('speech');
  await until(() => typeof window.__releaseSettings === 'function', 4000);
  if (mode === 'leave') await at('#/projects', 'projects');
  if (mode === 'return') { await at('#/projects', 'projects'); await at('#/chat', 'chat'); }
  if (mode === 'newer') S.openSettings('backups');
  window.__releaseSettings();
  await until(() => S.later && typeof S.later.renderSettings === 'function', 4000);
  // what the arrival does, it does in the steps after it
  await until(() => (window.__doors || []).length > 0 || S.view === 'settings', 500);
  const doors = JSON.stringify(window.__doors || []);
  if (mode === 'leave') check(window.__doors.length === 0 && S.view === 'projects', 'a door opened before Projects took the page back when Settings arrived: ' + doors + ', view ' + S.view);
  if (mode === 'return') check(S.view === 'chat' && location.hash === '#/chat', 'a door opened before leaving the conversation and coming back took the page to Settings when it arrived: view ' + S.view + ' at ' + location.hash);
  if (mode === 'newer') check(doors === JSON.stringify([['openSettings', 'backups']]), 'the doors honoured were not the newest alone: ' + doors);
  if (mode === 'twice') {
    await until(() => S.view === 'settings', 3000);
    check(S.view === 'settings' && location.hash === '#/settings/voice', "a door opened in the same step as two of the page's own changes of address was dropped when Settings arrived: view " + S.view + ' at ' + location.hash);
  }
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`
}

func TestADoorOpenedBeforeSettingsIsInHoldsOnlyItsMomentsIntent(t *testing.T) {
	served := map[string][]byte{"/views/settings.js": []byte(settingsHeldJS)}
	t.Run("left for Projects", func(t *testing.T) {
		runRealPageServing(t, statusFakeSocketJS, settingsDoorJS("leave"), homeStyles, served)
	})
	t.Run("away and back, with Settings' real door", func(t *testing.T) {
		real, err := staticFS.ReadFile("static/views/settings.js")
		if err != nil {
			t.Fatal(err)
		}
		runRealPageServing(t, statusFakeSocketJS, settingsDoorJS("return"), homeStyles, map[string][]byte{"/views/settings.js": []byte(settingsRealHeldJS), "/views/settings-real.js": real})
	})
	t.Run("the newer of two", func(t *testing.T) {
		runRealPageServing(t, statusFakeSocketJS, settingsDoorJS("newer"), homeStyles, served)
	})
	t.Run("after two of the page's own changes of address, with Settings' real door", func(t *testing.T) {
		real, err := staticFS.ReadFile("static/views/settings.js")
		if err != nil {
			t.Fatal(err)
		}
		runRealPageServing(t, statusFakeSocketJS, settingsDoorJS("twice"), homeStyles, map[string][]byte{"/views/settings.js": []byte(settingsRealHeldJS), "/views/settings-real.js": real})
	})
}

// SETTINGS HEARS ITS ANSWERS ONCE IT IS IN (step 21). The shipped page: a
// configuration that arrives before Settings is in answers nothing and
// stops nothing. Opened, Settings saves its Prompt card, and the host's
// answer, delivered on the socket, reaches it through later(): "Saved."
// beside the card; saved again, the host's refusal reaches it the same way.
const settingsAnswersJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const config = () => ({ llm: { provider: 'Anthropic', model: 'm1' }, speech: {}, prompt: { max_tokens: 8000, recent_turns: 20 }, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [],
  dashboard: { host: '127.0.0.1', port: 8181, tls: false, require_token: false }, secrets_ok: true });
const said = () => ((document.querySelector('[data-said="dashboard"]') || {}).textContent || '');
const lastSave = () => (sent.filter(f => f.type === 'config_set').at(-1) || {}).request_id;
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  sockets[0].say({ type: 'providers', providers: [{ name: 'Anthropic', chat: true, models: ['m1', 'm2'], default_model: 'm1', status: 'ok', preselect: true }] });
  await until(() => S.view === 'chat', 4000);
  sockets[0].say({ type: 'config', request_id: 'before-settings', config: config() });
  check(!(S.later && S.later.acceptSettingsConfig), 'Settings was in before it was opened');
  location.hash = '#/settings/dashboard';
  await until(() => document.querySelector('[data-save="dashboard"]'), 6000);
  const save = document.querySelector('[data-save="dashboard"]');
  check(save, 'Settings did not draw its Access and devices form');
  if (save) {
    save.click();
    const first = lastSave();
    check(first, 'the form sent no save');
    sockets[0].say({ type: 'config', request_id: first, config: config() });
    await until(() => said().includes('Saved.'), 3000);
    check(said().includes('Saved.'), "the host's answer to a save did not reach Settings: " + JSON.stringify(said()));
    document.querySelector('[data-save="dashboard"]').click();
    const second = lastSave();
    check(second && second !== first, 'the second save was not sent');
    sockets[0].say({ type: 'error', request_id: second, message: 'prompt.max_tokens: must be at least 1000' });
    await until(() => said().includes('must be at least 1000'), 3000);
    check(said().includes('prompt.max_tokens: must be at least 1000'), "the host's refusal of a save did not reach Settings: " + JSON.stringify(said()));
  }
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

func TestSettingsHearsItsAnswersOnceItIsIn(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, settingsAnswersJS, homeStyles)
}
