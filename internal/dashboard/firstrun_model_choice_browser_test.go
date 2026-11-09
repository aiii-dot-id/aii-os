//go:build !windows

package dashboard

import "testing"

// THE MODEL A PERSON CHOSE STANDS. A service's models are asked for as soon as
// it is chosen, and the answer can come after someone has opened "Other models"
// and chosen one: it used to replace the list and choose the recommendation,
// and Begin then sent a model nobody chose. A key typed afterwards did the same.
// Their choice stands while the list holds it, and Begin sends it. If the list no
// longer holds it, nothing is chosen in its place: Begin waits and says "Choose a
// model first.", and the list marks the one recommended. With no choice of
// theirs the recommendation is chosen, and another service starts afresh.
const firstRunChoiceJS = `import { report, until } from '/__harness.js';
const sockets = window.__sockets, sent = window.__sent;
const $ = id => document.getElementById(id);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const pause = ms => new Promise(r => setTimeout(r, ms));
const shown = el => !!el && el.getClientRects().length > 0;
const say = m => sockets[0].say(m);
const providers = [
  { name: 'Alpha', endpoint: 'https://alpha.test/v1', models: ['a-1', 'a-rec', 'a-2'], default_model: 'a-rec', chat: true, has_key: true },
  { name: 'Beta', endpoint: 'https://beta.test/v1', models: ['b-rec', 'b-1', 'b-2'], default_model: 'b-rec', chat: true },
];
const other = async name => {
  const r = document.querySelector('[data-row="other"] input'); r.checked = true; r.dispatchEvent(new Event('change'));
  const sel = $('fb-provider'); sel.value = String(providers.findIndex(p => p.name === name)); sel.dispatchEvent(new Event('change'));
  await pause(50);
};
const answer = async (name, models) => {
  const q = sent.filter(f => f.type === 'query' && f.query === 'discover').at(-1);
  say({ type: 'models', request_id: q.request_id, provider: name, model_list: models });
  await pause(50);
};
const take = async m => { const s = $('fb-model'); s.value = m; s.dispatchEvent(new Event('change')); await pause(50); };
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); say({ type: 'status', stats: { name: '(not born)' } });
  await until(() => $('fb-birth') && $('fb-choices'), 5000);
  say({ type: 'config', config: { genesis: { agent_memory_off: false }, llm: {}, speech: {}, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [], dashboard: {} } });
  say({ type: 'providers', providers }); await pause(200);
  const list = $('fb-model'), begin = $('fb-birth'), line = $('fb-result');
  // With no choice of theirs, the answer leaves the recommendation chosen.
  await other('Alpha');
  await answer('Alpha', ['a-1', 'a-2', 'a-rec', 'a-3']);
  check(list.value === 'a-rec', 'a late list did not leave the recommendation chosen: ' + list.value);
  // Chosen before the answer comes: it stands, and the list stays open.
  await other('Beta');
  $('fb-model-more').click(); await take('b-2');
  await answer('Beta', ['b-rec', 'b-1', 'b-2', 'b-3']);
  check(list.value === 'b-2', 'a late list took away the model they chose: ' + list.value);
  check(shown(list) && !begin.disabled, 'with their choice standing, the list closed or Begin waits');
  // A key typed after the choice asks the service again; it stands through that too.
  $('fb-apikey').value = 'k'; $('fb-apikey').dispatchEvent(new Event('change')); await pause(50);
  check(list.value === 'b-2', 'typing a key put another model in place of theirs: ' + list.value);
  await answer('Beta', ['b-rec', 'b-2']);
  check(list.value === 'b-2', 'the answer to the key took away the model they chose: ' + list.value);
  // Gone from the list the next answer gives: nothing stands in its place.
  $('fb-apikey').value = 'k2'; $('fb-apikey').dispatchEvent(new Event('change')); await pause(50);
  await answer('Beta', ['b-rec', 'b-1']);
  check(list.value === '' && list.selectedIndex === -1, 'a model that left the list was replaced: ' + list.value);
  check(begin.disabled, 'Begin can be pressed with their model gone and none chosen');
  check(line.textContent === 'Choose a model first.', 'Begin does not say why it waits: ' + line.textContent);
  check(shown(list), 'the list is not there to choose from');
  // It stays that way: a key typed since, and every answer after it, choose nothing in its place.
  $('fb-apikey').value = 'k3'; $('fb-apikey').dispatchEvent(new Event('change')); await pause(50);
  check(list.value === '' && begin.disabled && line.textContent === 'Choose a model first.', 'a key typed after the model left chose another: ' + [list.value, begin.disabled, line.textContent].join());
  await answer('Beta', ['b-rec', 'b-1']);
  check(list.value === '' && begin.disabled && line.textContent === 'Choose a model first.', 'a later answer chose another: ' + [list.value, begin.disabled, line.textContent].join());
  const marked = [...list.options].filter(o => o.textContent.includes('Recommended')).map(o => o.value);
  check(marked.join() === 'b-rec', 'the list does not show the recommendation: ' + marked.join());
  // Choosing again, the recommended one as any other, ends the wait.
  await take('b-rec');
  check(list.value === 'b-rec' && !begin.disabled && line.textContent === '', 'choosing again did not end the wait: ' + [list.value, begin.disabled, line.textContent].join());
  await take('b-1');
  // A model only the service's answer listed stands through a key typed since, whose
  // first list is the one known before that answer; only the next answer can say it left.
  $('fb-apikey').value = 'k4'; $('fb-apikey').dispatchEvent(new Event('change')); await pause(50);
  await answer('Beta', ['b-rec', 'b-1', 'b-late']);
  await take('b-late');
  $('fb-apikey').value = 'k5'; $('fb-apikey').dispatchEvent(new Event('change')); await pause(50);
  check(list.value === 'b-late' && !begin.disabled, 'the list known before the answer took away a model the answer listed: ' + list.value);
  await answer('Beta', ['b-rec', 'b-late']);
  check(list.value === 'b-late' && !begin.disabled, 'the next answer took away a model it holds: ' + list.value);
  $('fb-apikey').value = 'k6'; $('fb-apikey').dispatchEvent(new Event('change')); await pause(50);
  await answer('Beta', ['b-rec', 'b-1']);
  check(list.value === '' && begin.disabled, 'an answer without that model left another chosen: ' + list.value);
  // Another service starts afresh, and coming back shows the recommendation.
  await other('Alpha');
  check(list.value === 'a-rec' && !shown(list) && shown($('fb-model-chosen')), 'another service kept the old choice: ' + list.value);
  await other('Beta');
  check(list.value === 'b-rec', 'coming back kept a choice made before: ' + list.value);
  // Begin sends what they chose.
  $('fb-model-more').click(); await take('b-1');
  await answer('Beta', ['b-rec', 'b-1', 'b-9']);
  check(list.value === 'b-1' && !begin.disabled, 'their choice did not stand to Begin: ' + list.value);
  begin.click(); await pause(50);
  const born = sent.filter(f => f.type === 'genesis').at(-1);
  check(born && born.genesis.provider === 'Beta' && born.genesis.model === 'b-1', 'Begin did not send the model they chose: ' + JSON.stringify(born && born.genesis));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestTheModelAPersonChoseStands(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, firstRunChoiceJS, homeStyles)
}
