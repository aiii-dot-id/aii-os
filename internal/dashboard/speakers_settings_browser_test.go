//go:build !windows

package dashboard

import "testing"

const speakersSettingsPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, saveConfigSection, acceptSettingsConfig, speakersReadback } from './views/settings.js';
import { assert, run } from './__harness.js';

run(() => {
  const byId = id => document.getElementById(id);
  const sets = () => frames.filter(f => f.type === 'config_set').map(f => f.config);
  S.identityExists = true;
  S.providersLoaded = true;
  S.providers = [];
  S.config = { llm: { provider: 'OpenAI', model: 'm', resolved_provider: 'OpenAI', resolved_model: 'm', timeout_seconds: 120 },
    speech: { stt: { provider: '' }, tts: { provider: '' }, services: [],
      speakers: { mode: 'ignore', uids: ['visitor'], unidentified: 'withhold', revision: 4, withheld_finals: 2, withheld_partials: 7 } } };
  S.openSettings('speech');
  const card = byId('sp-speakers');
  assert(card, 'the Speakers card is on the speech page');
  assert(byId('sp-speakers-mode').value === 'ignore' && byId('sp-speakers-uids').value === 'visitor' && byId('sp-speakers-unid').value === 'withhold', 'the policy in force is drawn');
  const rb = byId('sp-speakers-readback').textContent;
  assert(rb.includes('Everyone but 1 listed speaker') && rb.includes('revision 4') && rb.includes('2 finals, 7 partials'), 'the readback names the rule, the revision and the counts: ' + rb);
  assert(!byId('sp-speakers-list-row').hidden && !byId('sp-speakers-unid-row').hidden, 'ignore shows the list and the unidentified rule');
  byId('sp-speakers-mode').value = 'all'; byId('sp-speakers-mode').onchange();
  assert(byId('sp-speakers-list-row').hidden && byId('sp-speakers-unid-row').hidden, 'all hides both rows');
  byId('sp-speakers-mode').value = 'only'; byId('sp-speakers-mode').onchange();
  assert(!byId('sp-speakers-list-row').hidden && byId('sp-speakers-unid-row').hidden, 'only shows the list, not the unidentified rule');
  byId('sp-speakers-uids').value = 'quinn-one, ada.2  visitor';
  saveConfigSection('speech_speakers');
  const sent = sets();
  assert(sent.length === 1 && sent[0]['speech.speakers'], 'one config_set with the policy: ' + JSON.stringify(sent));
  const pol = sent[0]['speech.speakers'];
  assert(pol.mode === 'only' && JSON.stringify(pol.uids) === '["quinn-one","ada.2","visitor"]' && !('unidentified' in pol), 'the object is the mode and the ids, the unidentified rule only under ignore: ' + JSON.stringify(pol));
  S.config.speech.speakers = { ...pol, uids: ['quinn-one', 'wrong-speaker', 'visitor'], revision: 5 };
  acceptSettingsConfig(frames.filter(f => f.type === 'config_set').at(-1).request_id); renderSettings();
  assert(byId('settings-stack').textContent.includes('Save could not be verified') && byId('sp-speakers-uids').value === 'quinn-one, ada.2  visitor', 'different speaker IDs falsely confirmed or erased the draft');
  saveConfigSection('speech_speakers');
  S.config.speech.speakers = { ...pol, revision: 6, withheld_finals: 2 };
  assert(acceptSettingsConfig(frames.filter(f => f.type === 'config_set').at(-1).request_id), 'speaker acknowledgement not claimed');
  renderSettings();
  assert(!byId('settings-stack').textContent.includes('Save could not be verified'), 'structured speaker policy was not confirmed');
  S.config.speech.speakers = { mode: 'all', uids: null, revision: 7 };
  renderSettings();
  assert(byId('sp-speakers-mode').value === 'all', 'confirmed policy draft hid later readback');
  saveConfigSection('speech_speakers');
  acceptSettingsConfig(frames.filter(f => f.type === 'config_set').at(-1).request_id); renderSettings();
  assert(!byId('settings-stack').textContent.includes('Save could not be verified'), 'empty speaker list did not match its null readback');
  assert(speakersReadback({ mode: 'all', uids: [], revision: 0 }).startsWith('Everyone is heard'), 'all reads back as everyone');
});
</script>`

func TestTheSpeakersCardEditsThePolicyWhole(t *testing.T) {
	runPageInEngines(t, speakersSettingsPage, substrateModules(t))
}

const speakersHeardPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run, until } from './__harness.js';

const asked = [];
let refuse = '', plain = '';
const realFetch = window.fetch;
const answer = (ok, status, body) => Promise.resolve({ ok, status, text: async () => body, json: async () => JSON.parse(body) });
window.fetch = (url, options = {}) => {
  if (!String(url).startsWith('/speech/heard')) return realFetch(url, options);
  const after = new URL(url, location.href).searchParams.get('after');
  asked.push(after);
  if (plain) return answer(false, 403, plain);
  if (refuse) return answer(false, 409, JSON.stringify({ error: refuse }));
  if (after === '7') return answer(true, 200, JSON.stringify({ scope: 's', rows: [
    { seq: 3, observed_at: '2026-10-13T10:00:00Z', delivery: 'delivered', delivery_reason: '', attribution: 'speaker quinn-one', text: 'an earlier voice' } ] }));
  return answer(true, 200, JSON.stringify({ scope: 's', next_after_seq: 7, rows: [
    { seq: 9, session: 'vs-room', final: 4, track: 'track-a', observed_at: '2026-10-13T10:02:00Z', delivery: 'withheld', delivery_reason: 'speaker not on the list', initial_attribution: 'speaker identification pending', attribution: 'speaker identification unresolved', speaker_uuid: '0b6f0c9e-1111-4222-8333-444455556666', text: 'withheld <b>banking</b> sentence' },
    { seq: 7, observed_at: '2026-10-13T10:01:00Z', delivery: 'delivered', delivery_reason: '', attribution: 'speaker quinn-one', text: 'long words', text_truncated: true } ] }));
};

run(async () => {
  const byId = id => document.getElementById(id);
  S.identityExists = true; S.providersLoaded = true; S.providers = [];
  S.config = { llm: { provider: 'OpenAI', model: 'm', resolved_provider: 'OpenAI', resolved_model: 'm', timeout_seconds: 120 },
    speech: { stt: { provider: '' }, tts: { provider: '' }, services: [], speakers: { mode: 'only', uids: ['quinn-one'], revision: 2, withheld_finals: 1 },
      heard_history: { capacity: 256, retention_seconds: 600, text_bytes: 8192, page_rows: 20 } } };
  S.openSettings('speech');
  assert(byId('sp-heard-says').textContent === 'The identity can read what was heard in the last 10 minutes, withheld words included, when they ask to. You can read the same here.', 'the card does not say the retention in force: ' + byId('sp-heard-says').textContent);
  const box = () => byId('sp-heard');
  assert(byId('sp-heard-show') && box() && box().hidden && asked.length === 0, 'heard speech was asked for, or shown, before the operator asked');
  byId('sp-heard-show').click();
  await until(() => box().querySelectorAll('.heard-row').length === 2);
  assert(asked.length === 1 && asked[0] === null, 'the first page was not asked for once, from the newest: ' + JSON.stringify(asked));
  let rows = [...box().querySelectorAll('.heard-row')];
  const held = rows[0].querySelector('[data-heard-text]');
  assert(held.textContent === 'withheld <b>banking</b> sentence' && held.hasAttribute('data-heard-withheld') && !held.querySelector('b'), 'the withheld words are not shown as text, whole: ' + held.outerHTML);
  assert(rows[0].textContent.includes('withheld (speaker not on the list)') && rows[0].textContent.includes('speaker identification unresolved') && rows[0].textContent.includes('2026-10-13T10:02:00Z'), 'the row does not say that it was withheld, why, by whom and when: ' + rows[0].textContent);
  assert(['at first: speaker identification pending', 'session vs-room', 'final 4', 'track track-a', 'speaker 0b6f0c9e-1111-4222-8333-444455556666'].every(w => rows[0].textContent.includes(w)), 'the row leaves out a field the identity is given: ' + rows[0].textContent);
  assert(!rows[1].querySelector('[data-heard-withheld]') && rows[1].querySelector('[data-heard-text]').textContent === 'long words', 'a delivered row is marked withheld, or its words element holds more than the words: ' + rows[1].innerHTML);
  assert(rows[1].querySelector('[data-heard-cut]') && rows[1].querySelector('[data-heard-cut]').textContent.includes('cut at the host'), 'a cut final does not say so on its own line: ' + rows[1].innerHTML);
  renderSettings();
  assert(box() && !box().hidden && box().querySelectorAll('.heard-row').length === 2, 'a redraw of Settings closed the rows shown');
  const older = byId('sp-heard-older');
  assert(older, 'the host said there are older rows and the page offers none');
  older.click();
  await until(() => box().querySelectorAll('.heard-row').length === 3);
  assert(asked.length === 2 && asked[1] === '7' && !byId('sp-heard-older'), 'the older page did not follow the cursor, or more is offered when the host named none: ' + JSON.stringify(asked));
  assert(box().textContent.includes('an earlier voice') && box().textContent.includes('banking'), 'the older rows replaced the newer ones');
  renderSettings();
  assert(box().querySelectorAll('.heard-row').length === 3, 'a redraw lost the older page');
  refuse = 'heard history withheld under SAFE: the ledger did not verify';
  byId('sp-heard-show').click();
  await until(() => box().querySelector('[data-heard-refused]'));
  assert(box().textContent.includes('heard history withheld under SAFE: the ledger did not verify') && !box().textContent.includes('banking'), 'a refusal is not shown in the host words: ' + box().textContent);
  plain = 'forbidden host\n';
  byId('sp-heard-show').click();
  await until(() => box().textContent.includes('forbidden host'));
  assert(box().textContent === 'What was heard cannot be shown: forbidden host', 'a refusal that is not JSON is not shown in the host words: ' + box().textContent);
});
</script>`

func TestTheSpeakersCardShowsWhatWasHeardWithheldWordsIncluded(t *testing.T) {
	m := substrateModules(t)
	m["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;'); export const copyText = async () => true;`)
	runPageInEngines(t, speakersHeardPage, m)
}

const voiceFilterPage = `<!doctype html>
<div id="voice-transcript" hidden></div>
<div id="voice-filter" hidden></div>
<button class="converse" id="converse" disabled hidden aria-pressed="false">&#127908;</button>
<script type="module">
import { converseForTest, speakerFilterLine, voiceEvent } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(() => {
  S.stats = { voice_engine: true, voice_state: 'plugin', speakers: { mode: 'only', uids: ['quinn-one'], revision: 2, withheld_finals: 3 } }; S.connected = true;
  const fl = document.getElementById('voice-filter');
  converseForTest('idle');
  assert(fl.hidden, 'nothing is said while nobody listens');
  converseForTest('live');
  assert(!fl.hidden && fl.textContent === 'Hearing only 1 listed speaker · 3 withheld', 'the restriction is named while listening: ' + fl.textContent);
  assert(speakerFilterLine({ mode: 'ignore', uids: ['a', 'b'], withheld_finals: 0 }) === 'Ignoring 2 listed speakers · 0 withheld', 'ignore reads as ignoring');
  voiceEvent({ type: 'transcript_withheld', session_id: 'vs-1', sequence: 8, reason: 'only the listed speakers are heard; this voice was not identified' });
  const tr = document.getElementById('voice-transcript');
  assert(!tr.hidden && tr.textContent.startsWith('A voice was withheld') && !tr.textContent.includes('secret'), 'a withheld voice is announced without its words: ' + tr.textContent);
  voiceEvent({ type: 'transcript_withheld', session_id: 'vs-1', sequence: 9, reason: 'speaker identification pending — awaiting corroborating speech; recent context is available through recall source=heard' });
  assert(!tr.hidden && tr.textContent.includes('identification pending') && tr.textContent.includes('recall source=heard') && tr.classList.contains('provisional') && !tr.classList.contains('failed'), 'pending speech remains visible as pending context, not a recognized identity or failed engine');
  S.stats.speakers = { mode: 'all', uids: [] };
  converseForTest('live');
  assert(fl.hidden, 'everyone heard says nothing');
});
</script>`

func TestTheVoicePanelSaysWhoIsHeard(t *testing.T) {
	runPageInEngines(t, voiceFilterPage, map[string][]byte{
		"/app.js":   []byte("export function toast(m) {}\n"),
		"/state.js": []byte("export const S = { stats: null, connected: false };\n"),
	})
}

const heardHistorySettingsPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, saveConfigSection, acceptSettingsConfig, heardHistoryReadback } from './views/settings.js';
import { assert, run } from './__harness.js';

run(() => {
  const byId = id => document.getElementById(id);
  const sets = () => frames.filter(f => f.type === 'config_set').map(f => f.config);
  S.identityExists = true;
  S.providersLoaded = true;
  S.providers = [];
  S.config = { llm: { provider: 'OpenAI', model: 'm', resolved_provider: 'OpenAI', resolved_model: 'm', timeout_seconds: 120 },
    speech: { stt: { provider: '' }, tts: { provider: '' }, services: [],
      speakers: { mode: 'all', uids: [], revision: 1 }, mode: { listen: 'interactive', speak: 'auto', revision: 1, name: 'interactive' },
      heard_history: { capacity: 256, retention_seconds: 1800, text_bytes: 8192, page_rows: 20 } } };
  S.openSettings('speech');
  const card = byId('sp-heard-history');
  assert(card, 'the Heard history card is on the speech page');
  assert(card.querySelector('h3') && card.querySelector('h3').title, 'the card heading carries its sentence');
  assert(byId('sp-heard-capacity').value === '256' && byId('sp-heard-retention').value === '1800' && byId('sp-heard-text').value === '8192' && byId('sp-heard-rows').value === '20', 'the numbers in force are drawn');
  const rb = byId('sp-heard-readback').textContent;
  assert(rb === 'In force: the last 256 finals, up to 8192 text bytes each, for 30 minutes; a read returns 20 rows unless the identity asks for another number.', 'the readback: ' + rb);
  byId('sp-heard-capacity').value = '64';
  byId('sp-heard-rows').value = '';
  saveConfigSection('speech');
  const sent = sets();
  assert(sent.length === 1, 'one config_set: ' + JSON.stringify(sent));
  const keys = Object.keys(sent[0]).filter(k => k.startsWith('speech.heard_history.')).sort();
  assert(JSON.stringify(keys) === '["speech.heard_history.capacity","speech.heard_history.page_rows"]', 'only the changed numbers are sent: ' + JSON.stringify(sent[0]));
  assert(sent[0]['speech.heard_history.capacity'] === 64 && sent[0]['speech.heard_history.page_rows'] === 0, 'the values: ' + JSON.stringify(sent[0]));
  S.config.speech.heard_history = { capacity: 64, retention_seconds: 1800, text_bytes: 8192, page_rows: 20 };
  assert(acceptSettingsConfig(frames.filter(f => f.type === 'config_set').at(-1).request_id), 'the save was not claimed');
  renderSettings();
  const said = byId('settings-stack').textContent;
  assert(!said.includes('Heard history ('), 'a heard history number was not verified: ' + said);
  assert(heardHistoryReadback({ capacity: 1, retention_seconds: 90, text_bytes: 300, page_rows: 1 }) === 'In force: the last 1 final, up to 300 text bytes each, for 90 seconds; a read returns 1 row unless the identity asks for another number.', 'odd numbers in words');
  byId('sp-heard-text').value = '1.5';
  saveConfigSection('speech');
  renderSettings();
  assert(byId('settings-stack').textContent.includes('Each heard history number is a whole number, or empty for its default.'), 'a fraction is refused on the page');
});
</script>`

func TestTheHeardHistoryCardSavesOnlyWhatChanged(t *testing.T) {
	runPageInEngines(t, heardHistorySettingsPage, substrateModules(t))
}
