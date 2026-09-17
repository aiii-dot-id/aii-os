//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
func TestPluginsViewRendersAndSavesDeclaredSettings(t *testing.T) {
	page := `<!doctype html>
<div id="plugins-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderPlugins } from './views/plugins.js';
import { acceptSettingsConfig, configFeedbackHTML } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  const LOCALES = ['auto'].concat(Array.from({ length: 40 }, (_, i) => 'loc-' + String(i).padStart(2, '0'))).concat(['de-DE']);
  S.config = { llm: {}, dashboard: {}, prompt: {}, witness: {}, logs: {}, agency: {}, restart_required: [],
    plugins: { autoload: 'T0', installed: [
      { id: 'com.example.memory', version: '0.1.0', tier: 'T1', mode: 'in-process', variant: 'linux-x86_64-wasm', tools: ['pl_a', 'pl_b'], capabilities: ['ring4.kv'], grants: { kv: false },
        settings: [
          { key: 'recall_limit', type: 'number', title: 'Recall limit', description: 'How many texts a recall returns', default: 5, value: 8, minimum: 1, maximum: 50 },
          { key: 'verbose', type: 'boolean', title: 'Verbose', default: false },
          { key: 'mode', type: 'enum', title: 'Mode', values: ['fast', 'exact'], default: 'fast' },
          { key: 'api_key', type: 'secret', title: 'API key', required: true, handles: ['acme'] },
          { key: 'label', type: 'string', title: 'Label' },
          { key: 'legacy_top_k', undeclared: true, value: 40, title: 'legacy_top_k', description: 'saved for an earlier release; this one does not offer it and nothing reads it' } ] },
      { id: 'com.example.plain', version: '0.2.0', tier: 'T0', mode: 'supervised', variant: 'linux-x86_64-wasm', tools: ['pl_c'] },
      { id: 'id.example.voice', version: '0.1.0', tier: 'T3', mode: 'supervised', variant: 'macos-arm64-native', family: 'voice_interface', applies: 'next_session',
        settings: [
          { key: 'recognition_language', type: 'enum', title: 'Recognition language', values: LOCALES, labels: { 'de-DE': 'German (Germany)', 'auto': 'Detect automatically' }, default: 'auto', value: 'de-DE', effective: 'de-DE' },
          { key: 'voice', type: 'enum', title: 'Speaking voice', values: ['alba', 'ryan'], labels: { alba: 'Alba (Scottish English)' }, default: 'alba', value: 'retired-voice', effective: 'alba', invalid: 'must be one of [alba ryan]' },
          { key: 'top_k', type: 'integer', title: 'Top-k', default: 40, minimum: 1, maximum: 200, value: 2.5, effective: 40, invalid: 'must be a whole number' },
          { key: 'seed', type: 'integer', title: 'Seed', default: 17 } ],
        session_settings: { 'vs-1': { recognition_language: 'de-DE', top_k: 40, extra: 'x' } } },
      { id: 'id.example.quietvoice', version: '0.1.0', tier: 'T3', mode: 'supervised', variant: 'macos-arm64-native', family: 'voice_interface', applies: 'next_session',
        settings: [ { key: 'voice', type: 'enum', title: 'Speaking voice', values: ['alba'], default: 'alba' } ],
        acts: [ { id: 'act-0011', operation: 'enroll', summary: 'Enroll a speaker from three finals', effects: 'write.local', session: 'vs-1',
                  args: { session_id: 'vs-1', finals: [3, 5, 8], label: 'Sam' },
                  finals: [ { sequence: 3, text: 'my name is Sam', heard: true }, { sequence: 5, text: '', heard: false } ],
                  proposed: '2026-09-11T19:00:00Z', expires: '2026-09-11T19:10:00Z' } ] } ] } };
  S.providers = []; S.providersLoaded = true; S.view = 'plugins';
  renderPlugins();
  const st = document.getElementById('plugins-stack');
  assert(st.textContent.includes('com.example.memory') && st.textContent.includes('0.1.0'), 'the release is named');
  assert(st.textContent.includes('2 operations, reached through the tools organ'), 'operations are counted, not listed in the prompt: ' + st.textContent);
  assert(st.textContent.includes('no settings declared'), 'a plugin without a declaration says so');
  const limit = st.querySelector('[data-pset-key="recall_limit"]');
  assert(limit && limit.type === 'number' && limit.value === '8' && limit.min === '1' && limit.max === '50', 'the number field carries the value and its bounds');
  assert(st.textContent.includes('default 5'), 'the declared default is stated');
  const secret = st.querySelector('[data-pset-key="api_key"]');
  assert(secret && secret.tagName === 'SELECT' && secret.querySelector('option[value="acme"]'), 'a secret is a choice among handles, never a text field');
  assert(st.textContent.includes('required'), 'a required setting is marked');
  limit.value = '12';
  st.querySelector('[data-pset-key="verbose"]').checked = true;
  st.querySelector('[data-pset-key="mode"]').value = 'exact';
  secret.value = 'acme';
  st.querySelector('[data-pset-key="label"]').value = '';
  st.querySelector('[data-save="plugin:com.example.memory"]').click();
  const sent = frames.find(f => f.type === 'config_set');
  assert(sent, 'a save sends config_set');
  const ch = sent.config;
  assert(ch['plugins.settings.com.example.memory.recall_limit'] === 12, 'a number is sent as a number: ' + JSON.stringify(ch));
  assert(ch['plugins.settings.com.example.memory.verbose'] === true, 'a boolean is sent as a boolean');
  assert(ch['plugins.settings.com.example.memory.mode'] === 'exact', 'an enum is sent as its value');
  assert(ch['plugins.settings.com.example.memory.api_key'] === 'acme', 'a secret is sent as the handle name');
  assert(ch['plugins.settings.com.example.memory.label'] === null, 'an emptied field clears the value');
  assert(!('plugins.autoload' in ch), 'a plugin save touches nothing else');
  // A VALUE AN EARLIER RELEASE LEFT BEHIND. It is shown with the value
  // it holds and the one control that makes sense for it; an untouched
  // one is left alone, so saving the card never discards a choice the
  // operator has not been asked about.
  const orphan = st.querySelector('[data-pset-key="legacy_top_k"]');
  assert(orphan && orphan.type === 'checkbox' && orphan.dataset.psetType === 'forget', 'a value this release does not declare offers only forgetting: ' + (orphan && orphan.outerHTML));
  assert(st.textContent.includes('saved value 40') && st.textContent.includes('does not offer it'), 'and says what it holds and why it is there: ' + st.textContent);
  assert(!('plugins.settings.com.example.memory.legacy_top_k' in ch), 'an untouched orphan is left exactly as it is');
  // THE READ-BACK LOOKS WHERE THE STATE PUTS A PLUGIN'S VALUES (found live
  // every plugin-settings save said "did not come back as
  // saved" while the card showed them). The host answers with the state;
  // the card carries the values; the acknowledgement says saved.
  const memory = S.config.plugins.installed[0];
  memory.settings.find(s => s.key === 'recall_limit').value = 12;
  memory.settings.find(s => s.key === 'verbose').value = true;
  memory.settings.find(s => s.key === 'mode').value = 'exact';
  memory.settings.find(s => s.key === 'api_key').value = 'acme';
  delete memory.settings.find(s => s.key === 'label').value;
  assert(acceptSettingsConfig(sent.request_id || 'req-1'), 'the acknowledgement is claimed against the save');
  renderPlugins();
  const said = document.querySelector('[data-said="plugin:com.example.memory"]');
  assert(said && said.className.includes('good'), 'a save that came back on the card is saved, not doubted, and says so beside its own button: ' + (said ? said.outerHTML : 'nothing said'));
  assert(!document.querySelector('[data-said="catalog"]') && !document.querySelector('[data-said="plugins"]'), 'another card was told about a save it did not make');
  assert(!configFeedbackHTML(), 'the answer was also put at the top of the page: ' + configFeedbackHTML());
  // A value that truly did not come back is still named — the card
  // re-rendered from the saved state first, so only the changed field
  // differs.
  renderPlugins();
  st.querySelector('[data-pset-key="recall_limit"]').value = '15';
  st.querySelector('[data-save="plugin:com.example.memory"]').click();
  memory.settings.find(s => s.key === 'recall_limit').value = 12;
  assert(acceptSettingsConfig('req-1'), 'the second acknowledgement is claimed');
  renderPlugins();
  const doubted = document.querySelector('[data-said="plugin:com.example.memory"]');
  assert(doubted && doubted.textContent.includes('plugins.settings.com.example.memory.recall_limit did not come back as saved'), 'a value the state does not carry is named: ' + (doubted ? doubted.textContent : 'nothing said'));
  assert(!doubted.textContent.includes('verbose'), 'values that came back are not named');
  // Ticked, the forget box clears it — the only write an orphan takes.
  renderPlugins();
  st.querySelector('[data-pset-key="legacy_top_k"]').checked = true;
  st.querySelector('[data-save="plugin:com.example.memory"]').click();
  const forgotFrame = frames.filter(f => f.type === 'config_set').pop(), forgot = forgotFrame.config;
  assert(forgot['plugins.settings.com.example.memory.legacy_top_k'] === null, 'ticked, it clears the value: ' + JSON.stringify(forgot));
  // ONE SAVE AT A TIME: every bar waits while one is being checked — a
  // second save would take the pending slot and orphan the first — so
  // the next press comes after this one is answered.
  renderPlugins();
  assert(st.querySelector('[data-save="grants:com.example.memory"]').disabled, 'another bar was pressable while a save was being checked');
  acceptSettingsConfig(forgotFrame.request_id || 'req-1');
  // Grants read back from the card the same way.
  renderPlugins();
  const kv = st.querySelector('[data-grant-plugin="com.example.memory"][data-grant-field="kv"]');
  assert(kv, 'the signed capability offers its grant');
  assert(st.querySelector('[data-save="grants:com.example.memory"]').textContent === 'Save grants', 'a button keeps its own words');
  kv.checked = true;
  st.querySelector('[data-save="grants:com.example.memory"]').click();
  const gsent = frames.filter(f => f.type === 'config_set').pop().config;
  assert(gsent['plugins.grants.com.example.memory.kv'] === true, 'the grant is sent: ' + JSON.stringify(gsent));
  memory.grants = { kv: true, read_only: false }; // the server always answers read_only; the page sent it with the boxes
  assert(acceptSettingsConfig('req-1'), 'the grant acknowledgement is claimed');
  renderPlugins();
  const grantSaid = document.querySelector('[data-said="grants:com.example.memory"]');
  assert(grantSaid && grantSaid.className.includes('good'), 'a grant that came back is saved, beside the button that saved it: ' + (grantSaid ? grantSaid.outerHTML : 'nothing said'));

  // CHOICES WITH NAMES, LONG LISTS, WHOLE NUMBERS, STALE VALUES, AND
  // WHEN A SAVE APPLIES, on the resident voice engine's card.
  const lang = st.querySelector('[data-pset-key="recognition_language"]');
  assert(lang && lang.tagName === 'SELECT' && lang.options.length === 42, 'every one of the 42 choices is offered: ' + (lang && lang.options.length));
  assert(lang.value === 'de-DE', 'the stored choice is selected');
  const de = lang.querySelector('option[value="de-DE"]');
  assert(de && de.textContent === 'German (Germany)' && de.title === 'de-DE', 'a labelled choice shows its label alone, the stable value on hover: ' + (de && de.textContent));
  assert(lang.querySelector('option[value="loc-39"]').textContent === 'loc-39', 'an unlabelled choice shows its value');
  const filter = st.querySelector('[data-choice-filter-for="recognition_language"]');
  assert(filter && filter.nextElementSibling === lang, 'a long list gets a filter box beside its select');
  filter.value = 'german'; filter.dispatchEvent(new Event('input'));
  assert(lang.querySelector('option[value="de-DE"]') && !lang.querySelector('option[value="loc-07"]') && lang.options.length === 1, 'the filter narrows what is listed, by label, case-insensitively: ' + lang.options.length);
  assert(lang.value === 'de-DE', 'the chosen value stays chosen');
  filter.value = 'loc-1'; filter.dispatchEvent(new Event('input'));
  assert(lang.querySelector('option[value="de-DE"]') && lang.value === 'de-DE', 'the chosen value stays listed and chosen whatever the filter says');
  assert(lang.querySelector('option[value="loc-12"]') && !lang.querySelector('option[value="loc-22"]') && lang.options.length === 11, 'the filter matches values too: ' + lang.options.length);
  assert(filter.title === '11 of 42 listed', 'the filter says how many of the choices it lists: ' + filter.title);
  filter.value = ''; filter.dispatchEvent(new Event('input'));
  assert(lang.options.length === 42 && lang.value === 'de-DE', 'an empty filter lists every choice again, the chosen one still chosen');
  assert(!st.querySelector('[data-choice-filter-for="voice"]'), 'a short list has no filter box');
  const voice = st.querySelector('[data-pset-key="voice"]');
  assert(voice.value === 'retired-voice' && voice.options[0].textContent.includes('saved, not offered by this release'), 'a stored choice this release no longer offers stays visible as such: ' + voice.options[0].textContent);
  assert(voice.querySelector('option[value="alba"]').textContent === 'Alba (Scottish English)', 'labels on the offered choices');
  const card = voice.closest('.card');
  assert(card.textContent.includes('your saved value "retired-voice" must be one of [alba ryan] — in effect: Alba (Scottish English) until you choose again'), 'the stale choice is named with what is in effect: ' + card.textContent);
  const topk = st.querySelector('[data-pset-key="top_k"]');
  assert(topk.type === 'number' && topk.step === '1' && topk.value === '2.5', 'an integer field steps by one and shows the stored value');
  assert(card.textContent.includes('your saved value 2.5 must be a whole number — in effect: 40 until you choose again'), 'a stored fraction is named, the default in effect: ' + card.textContent);
  assert(card.textContent.includes('the next spoken session opens with these; a session already running keeps the values it opened with'), 'a resident engine says when a save applies: ' + card.textContent);
  assert(st.querySelector('[data-pset-key="recall_limit"]').closest('.card').textContent.includes('the plugin reads it on its next call'), 'a per-call plugin says so');
  assert(card.textContent.includes('in effect in spoken session vs-1 — extra: "x" · recognition_language: German (Germany) · top_k: 40'), 'what the live session runs with is shown, labelled where declared: ' + card.textContent);
  assert(!st.querySelector('[data-pset-key="recall_limit"]').closest('.card').textContent.includes('spoken session'), 'a per-call plugin shows no session line');
  const quiet = st.querySelector('[data-save="plugin:id.example.quietvoice"]').closest('.card');
  assert(quiet.textContent.includes('no spoken session is open — the next one opens with the saved values'), 'a resident engine without a session says so: ' + quiet.textContent);
  // AWAITING YOUR CONFIRMATION (seam 1): the act, its arguments, what was heard, and the two decisions.
  assert(quiet.textContent.includes('AWAITING YOUR CONFIRMATION') && quiet.textContent.includes('Enroll a speaker from three finals') && quiet.textContent.includes('enroll · write.local · session vs-1'), 'the act is shown for a person: ' + quiet.textContent);
  assert(quiet.textContent.includes('label') && quiet.textContent.includes('Sam') && quiet.textContent.includes('#3 “my name is Sam”') && quiet.textContent.includes('#5 never heard on this session'), 'arguments and heard finals: ' + quiet.textContent);
  assert(quiet.textContent.includes('runs once, with exactly these arguments, when you confirm'), 'the one-shot rule is stated');
  const confirm = quiet.querySelector('[data-act-decision="confirm"]'), deny = quiet.querySelector('[data-act-decision="deny"]');
  assert(confirm && deny && confirm.dataset.actId === 'act-0011', 'Confirm and Deny name the act');
  confirm.click();
  const decided = frames.filter(f => f.type === 'plugin').pop();
  assert(decided && decided.plugin.action === 'confirm' && decided.plugin.id === 'id.example.quietvoice' && decided.plugin.act === 'act-0011', 'Confirm sends the decision on the plugin action message: ' + JSON.stringify(decided));
  assert(confirm.disabled && deny.disabled && confirm.textContent === 'Running…', 'one decision per act: both buttons are held');
  st.querySelector('[data-pset-key="seed"]').value = '21';
  topk.value = '3.5';
  st.querySelector('[data-save="plugin:id.example.voice"]').click();
  const vs = frames.filter(f => f.type === 'config_set').pop().config;
  assert(vs['plugins.settings.id.example.voice.seed'] === 21, 'an integer is sent as a number: ' + JSON.stringify(vs));
  assert(vs['plugins.settings.id.example.voice.top_k'] === 3.5, 'a fraction is sent as typed, for the host to refuse by key: ' + JSON.stringify(vs));
  assert(vs['plugins.settings.id.example.voice.recognition_language'] === 'de-DE', 'an enum is sent as its value, never its label');
  assert(vs['plugins.settings.id.example.voice.voice'] === 'retired-voice', 'an untouched stale choice is sent as stored, for the host to refuse by name');
});
</script>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js", "static/views/plugins.js", "static/pending.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { providers: [], config: null, providersLoaded: false };`)
	modules["/util.js"] = []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;'); export const hueOf = () => 0; export const copyText = async () => true;`)
	modules["/ws.js"] = []byte(`export const frames = [];
export function send(f) { frames.push(f); return 'req-1'; }
export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, page, modules)
}
