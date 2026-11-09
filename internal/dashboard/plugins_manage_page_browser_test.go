//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

// A PLUGIN'S CARD, OPENED UNDER MANAGE, SAVES THROUGH THE PAGE'S OWN SOCKET
// (step 16b). On the real page, over the real dispatcher and a scripted host:
// Manage opens the card from its row; a save leaves as the settings door's one
// request; the host's refusal of that request is said beside the card and the
// edit stays; its acceptance is said and the value stands; away and back, the
// card is still open with that value; a save the connection lost is said as
// such, and the edit stays.
const pluginsManagePageJS = viewsRedrawHeadJS + `const sent = window.__sent;
const plugin = limit => ({ id: 'org.example.one', title: 'One', version: '1.0.0', tier: 'T1', kind: 'plugin', mode: 'wasm', variant: 'wasm', grants: {},
  settings: [{ key: 'limit', type: 'number', title: 'Limit', value: limit, default: 5 }] });
const config = (limit, requestID) => ({ type: 'config', request_id: requestID, config: { llm: {}, speech: { stt: {}, tts: {}, services: [] },
  prompt: {}, witness: {}, logs: {}, agency: {}, identity: {}, public_name: {}, contacts: [], channels: [], restart_required: [],
  dashboard: { host: '127.0.0.1', port: 1, tls: false },
  plugins: { autoload: 'T1', catalog: [], pending: [], skips: [], stored: [], installed: [plugin(limit)] } } });
const card = () => document.querySelector('[data-manage-of="org.example.one"]');
const limit = () => card() && card().querySelector('[data-pset-key="limit"]');
const said = () => (document.querySelector('[data-said="plugin:org.example.one"]') || {}).textContent || '';
const type = v => { const el = limit(); el.value = v; el.dispatchEvent(new Event('input', { bubbles: true })); };
const save = () => { card().querySelector('[data-save="plugin:org.example.one"]').click(); return sent.filter(m => m.type === 'config_set').at(-1); };
async function run() {
  await start();
  sockets[0].say(config(8));
  location.hash = '#/settings/permissions';
  await until(() => document.querySelector('[data-manage="org.example.one"]'), 6000).catch(() => {});
  const manage = document.querySelector('[data-manage="org.example.one"]');
  check(manage && card() && card().hidden, 'the list does not fold the card under Manage');
  if (!manage) return finish();
  manage.click();
  check(limit() && limit().getClientRects().length > 0 && limit().value === '8', 'Manage did not open the card with its setting');
  type('19');
  const first = save();
  check(first && first.config['plugins.settings.org.example.one.limit'] === 19, 'the save did not leave as the door\'s one request: ' + JSON.stringify(first));
  sockets[0].say({ type: 'error', request_id: first.request_id, message: 'fixture refusal' });
  await until(() => /fixture refusal/.test(said()), 4000).catch(() => {});
  check(/fixture refusal/.test(said()) && limit().value === '19', 'a refusal was not said beside the card, or the edit was lost: ' + said());
  const second = save();
  check(second && second.request_id !== first.request_id, 'the second save did not leave');
  sockets[0].say(config(19, second.request_id));
  await until(() => /saved/i.test(said()), 4000).catch(() => {});
  check(/saved/i.test(said()) && limit().value === '19', 'the acceptance was not said, or the value did not stand: ' + said());
  location.hash = '#/home';
  await until(() => S.view === 'home', 4000);
  location.hash = '#/settings/permissions';
  await until(() => S.view === 'plugins' && limit(), 4000).catch(() => {});
  check(card() && !card().hidden && limit().value === '19', 'away and back, the card was closed or lost its value');
  type('23');
  const third = save();
  check(third && third.config['plugins.settings.org.example.one.limit'] === 23, 'the third save did not leave: ' + JSON.stringify(third));
  sockets[0].close();
  await until(() => /Connection lost/.test(said()), 4000).catch(() => {});
  check(/Connection lost/.test(said()) && limit().value === '23', 'a save the connection lost was not said, or the edit was lost: ' + said());
  finish();
}
run().catch(failed);
`

func TestAPluginsCardIsSavedThroughThePagesOwnSocket(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, pluginsManagePageJS, homeStyles)
}
