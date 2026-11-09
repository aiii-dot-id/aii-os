//go:build !windows

package dashboard

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// OFF MEANS OFF, WITH AN ENGINE RUNNING TOO. The host reads a provider left empty
// as its installed engine, by design, so the provider list alone cannot say off
// there: hearing's Off also sets Talk's Off, which stops the microphone on every
// page, and the page reads that back as off. Spoken replies' Never is speaking's,
// which the host enforces too.
// This browser's voice cannot be chosen beside an engine, so it is not offered. What
// is in force comes from what is saved, and the month's usage stays in view.
const voiceOffHeadJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const $ = id => document.getElementById(id);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const shown = el => !!el && el.getClientRects().length > 0;
const say = m => sockets[sockets.length - 1].say(m);
const heard = { model_required: true, lists_models: true }, spoken = { model_required: true, voice_required: true, lists_models: true, lists_voices: true };
const ENGINE = 'id.test.voice';
const services = [
  { name: ENGINE, title: 'Room voice', plugin: true, added: true, speech: { stt: {}, tts: {} }, settings: [] },
  { name: 'OpenAI', endpoint: 'https://api.openai.test/v1', speech: { stt: heard, tts: spoken }, added: true, has_key: true, chats: true },
];
const speech = (over = {}) => ({ stt: { provider: 'OpenAI', model: 'm-hear', monthly_minutes: 300 }, tts: { provider: 'OpenAI', model: 'm-say', voice: 'v-1', monthly_characters: 0 }, services,
  mode: { listen: 'interactive', speak: 'auto', name: 'interactive', revision: 2, set: true },
  speakers: { mode: 'all', uids: [], unidentified: 'deliver', revision: 1 }, heard_history: { capacity: 256, retention_seconds: 1800, text_bytes: 8192, page_rows: 20 }, voices: [], operator_speakers: [],
  spent: [{ provider: 'OpenAI', direction: 'stt', requests: 22, seconds: 1860 }], resets: '1 December', ...over });
__FRAME__
const status = over => say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 6, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0, ...over } });
const pick = (id, v) => { $(id).value = v; $(id).dispatchEvent(new Event('change', { bubbles: true })); };
const talk = v => $('sp-mode-listen-' + v);
const options = id => [...$(id).options].map(o => o.textContent);
const hearingSaid = () => $('sp-readback-stt').textContent;
const lastSet = () => sent.filter(f => f.type === 'config_set' && f.config && f.config['speech.mode']).at(-1);
const save = () => $('speech-form').querySelector('[data-save="speech"]').click();
const heardLanes = () => sent.filter(f => f.type === 'voice_session' && f.voice && f.voice.action === 'open' && f.voice.mode !== 'output').length;
const anyLane = () => sent.filter(f => f.type === 'voice_session' && f.voice && f.voice.action === 'open').length;
const opened = async sp => {
  await until(() => sockets.length > 0);
  sockets[0].open();
  status({});
  say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 6 } });
  say({ type: 'providers', providers: [{ name: 'OpenAI', chat: true, models: ['m'], default_model: 'm', has_key: true }] });
  frame(sp);
  await until(() => typeof S.go === 'function');
  location.hash = '#/settings/voice';
  await until(() => $('speech-form'), 10000);
};
`

const voiceOffJS = voiceOffHeadJS + `async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  status({});
  say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 6 } });
  say({ type: 'providers', providers: [{ name: 'OpenAI', chat: true, models: ['m'], default_model: 'm', has_key: true }] });
  frame(speech());
  await until(() => typeof S.go === 'function');
  location.hash = '#/settings/voice';
  await until(() => $('speech-form'), 10000);
  // Hearing keeps its Off. This browser's voice cannot be chosen beside an engine.
  check(options('sp-provider-stt')[0] === 'Off', 'hearing does not offer Off: ' + options('sp-provider-stt'));
  check(!options('sp-provider-tts').includes('This browser'), 'their voice offers a browser the host would not use: ' + options('sp-provider-tts'));
  check(hearingSaid() === 'Listening with OpenAI.', 'what is in force: ' + hearingSaid());
  // What speaks now is what is saved; a choice not yet saved is said as that.
  pick('sp-provider-tts', ENGINE);
  await until(() => $('sp-readback-tts').textContent.includes('not saved'), 3000).catch(() => {});
  check($('sp-readback-tts').textContent === 'Replies spoken by OpenAI. Room voice is chosen and not saved; Save to put it in force.', 'a choice not saved is named as in use: ' + $('sp-readback-tts').textContent);
  pick('sp-provider-tts', 'OpenAI');
  // A month's charge stays in view, whatever is chosen now.
  const charged = () => shown($('sp-spent-stt')) && $('sp-spent-stt').textContent.includes('OpenAI 31m 0s over 22 utterances');
  check(charged(), 'the charge is not shown with the service chosen');
  pick('sp-provider-stt', ENGINE);
  await until(() => $('sp-provider-stt').value === ENGINE, 3000).catch(() => {});
  check(charged(), 'the charge went with an engine chosen');
  // Off sets Talk's Off, and leaving it asks to be heard again.
  pick('sp-provider-stt', '');
  await until(() => talk('off')?.checked, 3000).catch(() => {});
  check(talk('off')?.checked, 'Off did not set Talk off');
  check(charged(), 'the charge went with Off chosen');
  check(hearingSaid().endsWith('Off is chosen and not saved; Save to put it in force.'), 'Off is not said to be unsaved: ' + hearingSaid());
  talk('interactive').click();
  await until(() => $('sp-provider-stt').value === 'OpenAI', 3000).catch(() => {});
  check($('sp-provider-stt').value === 'OpenAI', 'Talk turned on did not return hearing to what is saved: ' + JSON.stringify($('sp-provider-stt').value));
  pick('sp-provider-stt', '');
  await until(() => talk('off')?.checked, 3000).catch(() => {});
  pick('sp-provider-stt', 'OpenAI');
  await until(() => talk('interactive')?.checked, 3000).catch(() => {});
  check(talk('interactive')?.checked, 'choosing a service from Off left Talk off');
  pick('sp-provider-stt', '');
  await until(() => talk('off')?.checked, 3000).catch(() => {});
  // Saved as off: the empty provider and Talk's Off, as one change.
  save();
  await until(() => lastSet(), 3000).catch(() => {});
  const set = lastSet();
  check(set && set.config['speech.stt.provider'] === '' && set.config['speech.mode'].listen === 'off', 'Off was not sent as off: ' + JSON.stringify(set && set.config));
  // Read back as off: the host names its engine as the default, and Talk as off.
  frame(speech({ stt: { provider: ENGINE, plugin: true, default: true, model: '', language: '', monthly_minutes: 300 }, mode: { listen: 'off', speak: 'auto', name: 'earbuds', revision: 3, set: true } }), { request_id: set && set.request_id });
  status({ voice_state: 'plugin', voice_source: ENGINE, voice_engine: true, voice_listen: 'off', voice_speak: 'auto', voice_mode_revision: 3 });
  await until(() => document.querySelector('[data-said="speech"]'), 3000).catch(() => {});
  const said = document.querySelector('[data-said="speech"]');
  check(said && said.classList.contains('good'), 'the save the host accepted is not said as saved: ' + (said && said.textContent));
  check($('sp-provider-stt').value === '' && $('sp-provider-stt').selectedOptions[0].textContent === 'Off', 'hearing does not read Off: ' + $('sp-provider-stt').selectedOptions[0]?.textContent);
  check(hearingSaid() === 'You type. Nothing is heard.', 'hearing does not say it is off: ' + hearingSaid());
  check(talk('off')?.checked, 'Talk does not read Off');
  check(!$('sp-provider-stt').title.includes('Talk button opens this page'), 'its title promises what an engine does not do: ' + $('sp-provider-stt').title);
  check(heardLanes() === 0, 'something opened to hear with hearing off: ' + heardLanes());
  // Speaking's off, with the same engine: nothing opens, nothing is read.
  $('sp-mode-speak-off').click();
  const before = lastSet();
  save();
  await until(() => lastSet() !== before, 3000).catch(() => {});
  const silent = lastSet();
  check(silent && silent !== before && silent.config['speech.mode'].speak === 'off' && silent.config['speech.mode'].listen === 'off', 'Never was not sent: ' + JSON.stringify(silent && silent.config['speech.mode']));
  frame(speech({ stt: { provider: ENGINE, plugin: true, default: true, model: '', language: '', monthly_minutes: 300 }, mode: { listen: 'off', speak: 'off', name: 'off', revision: 4, set: true } }), { request_id: silent && silent.request_id });
  status({ voice_state: 'plugin', voice_source: ENGINE, voice_engine: true, voice_listen: 'off', voice_speak: 'off', voice_mode_revision: 4 });
  let read = 0;
  if (window.speechSynthesis) window.speechSynthesis.speak = () => { read++; };
  const lanes = anyLane(), frames = sent.length;
  const { speak } = await import('/voice/speech.js');
  speak('a typed reply');
  speak('a reply the engine would speak', { route: 'plugin' });
  check(read === 0 && anyLane() === lanes && !sent.slice(frames).some(f => f.type === 'say' || f.type === 'voice_session'), 'something was spoken with speaking off: read ' + read + ', ' + JSON.stringify(sent.slice(frames)));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestOffMeansOffWithAnEngineRunning(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, strings.Replace(voiceOffJS, "__FRAME__", voiceFrameJS, 1), homeStyles)
}

// A SAVE OF SOMETHING ELSE LEAVES THE AUTOMATIC ENGINE AUTOMATIC. The host names its
// engine in place of a provider left empty, and the picker shows it. Saving only
// Never must not write that name down: another engine could not then take over.
// Choosing the engine on purpose does write it.
const voiceAutomaticJS = voiceOffHeadJS + `const automatic = { provider: ENGINE, plugin: true, default: true, model: '', language: '', voice: '', monthly_minutes: 0, monthly_characters: 0 };
async function run() {
  const chosen = over => speech({ stt: automatic, tts: automatic, spent: [], ...over });
  await opened(chosen());
  check($('sp-provider-tts').value === ENGINE && $('sp-provider-stt').value === ENGINE, 'the picker does not show the engine holding the default');
  $('sp-mode-speak-off').click();
  save();
  await until(() => lastSet(), 3000).catch(() => {});
  const set = lastSet();
  check(set && set.config['speech.tts.provider'] === '' && set.config['speech.stt.provider'] === '', 'an unrelated save wrote the automatic engine down: ' + JSON.stringify(set && [set.config['speech.stt.provider'], set.config['speech.tts.provider']]));
  frame(chosen({ mode: { listen: 'interactive', speak: 'off', name: 'interactive, text replies', revision: 3, set: true } }), { request_id: set && set.request_id });
  await until(() => document.querySelector('[data-said="speech"]'), 3000).catch(() => {});
  check(document.querySelector('[data-said="speech"]')?.classList.contains('good'), 'that save is not said as saved: ' + document.querySelector('[data-said="speech"]')?.textContent);
  check(!$('sp-browser-gone'), 'a provider left automatic is said to have been a browser: ' + $('sp-browser-gone')?.textContent);
  // On purpose, hearing and speaking both: away and back, then Save.
  for (const id of ['sp-provider-tts', 'sp-provider-stt']) {
    pick(id, 'OpenAI');
    await until(() => $(id).value === 'OpenAI', 3000).catch(() => {});
    pick(id, ENGINE);
    await until(() => $(id).value === ENGINE, 3000).catch(() => {});
  }
  let before = lastSet();
  save();
  await until(() => lastSet() !== before, 3000).catch(() => {});
  const named = lastSet();
  check(named !== before && named.config['speech.tts.provider'] === ENGINE && named.config['speech.stt.provider'] === ENGINE, 'choosing the engine on purpose did not name it: ' + JSON.stringify([named.config['speech.stt.provider'], named.config['speech.tts.provider']]));
  // The host says it is chosen now, not the default, and a later unrelated save keeps it so.
  const kept = { ...automatic, default: false };
  frame(chosen({ stt: kept, tts: kept, mode: { listen: 'interactive', speak: 'off', name: 'interactive, text replies', revision: 4, set: true } }), { request_id: named.request_id });
  await until(() => document.querySelector('[data-said="speech"].good'), 3000).catch(() => {});
  check(document.querySelector('[data-said="speech"]')?.classList.contains('good'), 'the engine chosen on purpose is not said as saved: ' + document.querySelector('[data-said="speech"]')?.textContent);
  $('sp-mode-speak-on').click();
  before = lastSet();
  save();
  await until(() => lastSet() !== before, 3000).catch(() => {});
  const later = lastSet();
  check(later !== before && later.config['speech.tts.provider'] === ENGINE && later.config['speech.stt.provider'] === ENGINE, 'a later save lost the engine chosen on purpose: ' + JSON.stringify([later.config['speech.stt.provider'], later.config['speech.tts.provider']]));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

// TALK TURNED ON FROM OFF LEAVES THE AUTOMATIC ENGINE AUTOMATIC. With the engine
// holding the default and hearing Off, turning Talk on is all that changed: hearing
// reads the engine, and Save leaves the stored provider empty.
const voiceTalkOnJS = voiceOffHeadJS + `const automatic = { provider: ENGINE, plugin: true, default: true, model: '', language: '', voice: '', monthly_minutes: 0, monthly_characters: 0 };
async function run() {
  const off = { listen: 'off', speak: 'auto', name: 'earbuds', revision: 3, set: true };
  await opened(speech({ stt: automatic, tts: automatic, spent: [], mode: off }));
  check($('sp-provider-stt').value === '' && talk('off')?.checked, 'hearing does not read Off');
  talk('interactive').click();
  await until(() => $('sp-provider-stt').value === ENGINE, 3000).catch(() => {});
  check($('sp-provider-stt').value === ENGINE, 'hearing does not read the engine with Talk on: ' + JSON.stringify($('sp-provider-stt').value));
  check(talk('interactive')?.checked, 'Talk did not stay on');
  // Talk back to Off before Save: hearing reads Off again.
  talk('off').click();
  await until(() => $('sp-provider-stt').value === '', 3000).catch(() => {});
  check($('sp-provider-stt').value === '', 'Talk back to Off does not read Off: ' + JSON.stringify($('sp-provider-stt').value));
  talk('interactive').click();
  await until(() => $('sp-provider-stt').value === ENGINE, 3000).catch(() => {});
  // Another engine takes the default before Save: hearing reads it, and Save still names none.
  const other = { name: 'id.test.other', title: 'Other voice', plugin: true, added: true, speech: { stt: {}, tts: {} }, settings: [] };
  const otherHolds = { ...automatic, provider: other.name };
  frame(speech({ services: [other, ...services], stt: otherHolds, tts: otherHolds, spent: [], mode: off }));
  await until(() => $('sp-provider-stt').value === other.name, 3000).catch(() => {});
  check($('sp-provider-stt').value === other.name, 'hearing still reads the engine that held the default: ' + JSON.stringify($('sp-provider-stt').value));
  save();
  await until(() => lastSet(), 3000).catch(() => {});
  const set = lastSet();
  check(set && set.config['speech.stt.provider'] === '' && set.config['speech.mode'].listen === 'interactive', 'turning Talk on wrote an engine down: ' + JSON.stringify(set && [set.config['speech.stt.provider'], set.config['speech.mode']]));
  frame(speech({ stt: automatic, tts: automatic, spent: [], mode: { ...off, listen: 'interactive', name: 'interactive', revision: 4 } }), { request_id: set && set.request_id });
  await until(() => document.querySelector('[data-said="speech"]'), 3000).catch(() => {});
  check(document.querySelector('[data-said="speech"]')?.classList.contains('good'), 'that save is not said as saved: ' + document.querySelector('[data-said="speech"]')?.textContent);
  check($('sp-readback-stt').textContent.startsWith('Listening with Room voice'), 'what is in force: ' + $('sp-readback-stt').textContent);
  // Talk turned off from elsewhere after that save: hearing reads Off.
  frame(speech({ stt: automatic, tts: automatic, spent: [], mode: { ...off, revision: 5 } }));
  await until(() => $('sp-provider-stt').value === '', 3000).catch(() => {});
  check($('sp-provider-stt').value === '', 'after the save, Talk off does not read Off: ' + JSON.stringify($('sp-provider-stt').value));
  frame(speech({ stt: automatic, tts: automatic, spent: [], mode: { ...off, listen: 'interactive', name: 'interactive', revision: 6 } }));
  await until(() => $('sp-provider-stt').value === ENGINE, 3000).catch(() => {});
  // Off again, on again: the same, and choosing the engine on purpose still names it.
  pick('sp-provider-stt', '');
  await until(() => talk('off')?.checked, 3000).catch(() => {});
  talk('interactive').click();
  await until(() => $('sp-provider-stt').value === ENGINE, 3000).catch(() => {});
  pick('sp-provider-stt', 'OpenAI');
  await until(() => $('sp-provider-stt').value === 'OpenAI', 3000).catch(() => {});
  pick('sp-provider-stt', ENGINE);
  await until(() => $('sp-provider-stt').value === ENGINE, 3000).catch(() => {});
  const before = lastSet();
  save();
  await until(() => lastSet() !== before, 3000).catch(() => {});
  check(lastSet() !== before && lastSet().config['speech.stt.provider'] === ENGINE, 'choosing the engine after Talk did not name it: ' + JSON.stringify(lastSet().config['speech.stt.provider']));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestTalkTurnedOnFromOffLeavesTheAutomaticEngineAutomatic(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, strings.Replace(voiceTalkOnJS, "__FRAME__", voiceFrameJS, 1), homeStyles)
}

func TestASaveOfSomethingElseLeavesTheAutomaticEngineAutomatic(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, strings.Replace(voiceAutomaticJS, "__FRAME__", voiceFrameJS, 1), homeStyles)
}

// AN ENGINE THAT ARRIVES BEFORE SAVE CHANGES WHAT CAN BE CHOSEN. This browser's voice
// cannot be chosen beside it, so a pending choice of it goes, the page says so, and
// the picker returns to what is saved. A pending Off stays, and is Talk's Off now,
// even if Talk still reads otherwise when Save is pressed. Other unsaved edits stay.
const voiceEngineArrivesJS = voiceOffHeadJS + `const before = services.filter(s => !s.plugin);
const gone = 'This browser cannot be chosen while Room voice is running.';
async function run() {
  await opened(speech({ services: before }));
  check(options('sp-provider-tts').includes('This browser'), 'with no engine this browser is not offered');
  pick('sp-provider-tts', '');
  await until(() => $('sp-provider-tts').value === '', 3000).catch(() => {});
  pick('sp-provider-stt', '');
  await until(() => $('sp-provider-stt').value === '', 3000).catch(() => {});
  $('sp-speakers-mode').value = 'only';
  $('sp-speakers-mode').dispatchEvent(new Event('change', { bubbles: true }));
  check(talk('off')?.checked, 'a chosen Off with no engine did not set Talk off');
  frame(speech());
  await until(() => options('sp-provider-tts').includes('Room voice'), 3000).catch(() => {});
  check(!options('sp-provider-tts').includes('This browser') && $('sp-provider-tts').value === 'OpenAI', 'a pending browser survived the engine: ' + options('sp-provider-tts') + ' at ' + JSON.stringify($('sp-provider-tts').value));
  check(shown($('sp-browser-gone')) && $('sp-browser-gone').textContent === gone, 'the page does not say the browser went: ' + $('sp-browser-gone')?.textContent);
  check($('sp-provider-stt').value === '', 'a pending Off did not stay');
  check(talk('off')?.checked, 'a pending Off does not hold Talk off with an engine running');
  check($('sp-speakers-mode').value === 'only', 'another unsaved edit went with it');
  // Talk moved by something that draws nothing: Save still sends off.
  talk('interactive').checked = true;
  save();
  await until(() => lastSet(), 3000).catch(() => {});
  const set = lastSet();
  check(set && set.config['speech.tts.provider'] === 'OpenAI', 'Save sent the browser the host would not use: ' + JSON.stringify(set && set.config['speech.tts.provider']));
  check(set && set.config['speech.stt.provider'] === '' && set.config['speech.mode'].listen === 'off', 'Save did not send Off as off: ' + JSON.stringify(set && [set.config['speech.stt.provider'], set.config['speech.mode']]));
  // Said once: Save has been pressed, and it is not said again.
  frame(speech({ stt: { provider: ENGINE, plugin: true, default: true, model: '', language: '', monthly_minutes: 300 }, mode: { listen: 'off', speak: 'auto', name: 'earbuds', revision: 3, set: true } }), { request_id: set && set.request_id });
  await until(() => !$('sp-browser-gone'), 3000).catch(() => {});
  check(!$('sp-browser-gone'), 'it is still said after Save: ' + $('sp-browser-gone')?.textContent);
  // Or until the picker moves.
  frame(speech({ services: before, mode: { listen: 'interactive', speak: 'auto', name: 'interactive', revision: 4, set: true } }));
  await until(() => options('sp-provider-tts').includes('This browser'), 3000).catch(() => {});
  pick('sp-provider-tts', '');
  await until(() => $('sp-provider-tts').value === '', 3000).catch(() => {});
  frame(speech({ mode: { listen: 'interactive', speak: 'auto', name: 'interactive', revision: 4, set: true } }));
  await until(() => $('sp-browser-gone'), 3000).catch(() => {});
  check(shown($('sp-browser-gone')), 'an engine arriving a second time is not said');
  pick('sp-provider-tts', ENGINE);
  await until(() => $('sp-provider-tts').value === ENGINE, 3000).catch(() => {});
  check(!$('sp-browser-gone'), 'it is still said after the picker moved');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestAnEngineThatArrivesBeforeSaveChangesWhatCanBeChosen(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, strings.Replace(voiceEngineArrivesJS, "__FRAME__", voiceFrameJS, 1), homeStyles)
}

// NEVER SAYS WHAT IT DOES. With speaking off the page does not say a voice speaks,
// whichever is chosen, and says so again as soon as speaking is back.
const voiceNeverJS = voiceOffHeadJS + `const never = { listen: 'interactive', speak: 'off', name: 'interactive, text replies', revision: 3, set: true };
const line = () => $('sp-readback-tts').textContent, note = () => $('sp-note-tts')?.textContent || '';
async function run() {
  await opened(speech({ mode: never }));
  check(line() === 'Replies are not spoken.', 'Never with a service chosen says: ' + line());
  check(!note().includes('Speaks every reply') && !note().includes('browser\'s own voice does'), 'Never still says what speaks: ' + note());
  frame(speech({ mode: never, tts: { provider: '' }, services: services.filter(s => !s.plugin) }));
  await until(() => $('sp-provider-tts').value === '', 3000).catch(() => {});
  check(line() === 'Replies are not spoken.', 'Never with this browser says: ' + line());
  frame(speech({ mode: { ...never, speak: 'on', revision: 4 } }));
  await until(() => line().startsWith('Replies spoken by'), 3000).catch(() => {});
  check(line() === 'Replies spoken by OpenAI.' && note().includes('Speaks every reply'), 'Always does not say what speaks: ' + line() + ' / ' + note());
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

// AN ENGINE FIRST NAMED IN SAVE'S ANSWER. This browser chosen with no engine known
// cannot then be what speaks: the rest is saved, and the page says what happened in
// place of a plain success, as it does not for a provider left automatic. A hearing
// Off is still off.
const voiceSaveNamesEngineJS = voiceOffHeadJS + `const before = services.filter(s => !s.plugin);
const named = over => ({ provider: ENGINE, plugin: true, default: true, model: '', language: '', voice: '', monthly_minutes: 300, monthly_characters: 0, ...over });
async function run() {
  await opened(speech({ services: before, spent: [] }));
  pick('sp-provider-tts', '');
  await until(() => $('sp-provider-tts').value === '', 3000).catch(() => {});
  pick('sp-provider-stt', '');
  await until(() => talk('off')?.checked, 3000).catch(() => {});
  // Talk moved by something that draws nothing: Save still sends off.
  talk('interactive').checked = true;
  save();
  await until(() => lastSet(), 3000).catch(() => {});
  const set = lastSet();
  check(set && set.config['speech.tts.provider'] === '' && set.config['speech.stt.provider'] === '' && set.config['speech.mode'].listen === 'off', 'the choices were not sent as chosen: ' + JSON.stringify(set && [set.config['speech.stt.provider'], set.config['speech.tts.provider'], set.config['speech.mode']]));
  // A frame naming the engine lands while Save waits: this browser leaves the picker
  // at once, and the answer will say why, once.
  const answered = speech({ stt: named(), tts: named(), spent: [], mode: { listen: 'off', speak: 'auto', name: 'earbuds', revision: 3, set: true } });
  if (__BETWEEN__) {
    frame(answered);
    await until(() => options('sp-provider-stt').includes('Room voice'), 3000).catch(() => {});
    check(!options('sp-provider-tts').includes('This browser'), 'this browser is still offered beside an engine: ' + options('sp-provider-tts'));
    check(!$('sp-browser-gone'), 'it is said while Save still waits: ' + $('sp-browser-gone')?.textContent);
  } else {
    check($('sp-provider-tts').value === '' && options('sp-provider-tts').includes('This browser'), 'with no engine known yet, the picker does not show what was sent: ' + $('sp-provider-tts').value + ' of ' + options('sp-provider-tts'));
    check(!$('sp-browser-gone'), 'it is said before any engine is known: ' + $('sp-browser-gone')?.textContent);
  }
  frame(answered, { request_id: set && set.request_id });
  await until(() => document.querySelector('[data-said="speech"]'), 3000).catch(() => {});
  const said = document.querySelector('[data-said="speech"]');
  check(said && said.classList.contains('bad') && said.textContent.startsWith('This browser cannot be chosen while Room voice is running.'), 'the answer does not say what happened: ' + (said && said.className + ' / ' + said.textContent));
  check(!$('sp-browser-gone'), 'it is said twice: ' + $('sp-browser-gone')?.textContent);
  check($('sp-readback-tts').textContent.startsWith('Replies spoken by Room voice'), 'what speaks: ' + $('sp-readback-tts').textContent);
  check($('sp-provider-stt').value === '' && $('sp-readback-stt').textContent === 'You type. Nothing is heard.', 'hearing is not off: ' + $('sp-readback-stt').textContent);
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestAnEngineFirstNamedInSavesAnswerIsSaid(t *testing.T) {
	for name, between := range map[string]string{"with a frame naming it first": "true", "first named in the answer": "false"} {
		t.Run(name, func(t *testing.T) {
			page := strings.Replace(voiceSaveNamesEngineJS, "__FRAME__", voiceFrameJS, 1)
			runRealPage(t, statusFakeSocketJS, strings.Replace(page, "__BETWEEN__", between, 1), homeStyles)
		})
	}
}

// A BROWSER DROPPED WHILE SAVE WAITS IS SAID ONCE. The answer says it only when that
// Save asked for this browser and the host took it. A browser asked for after Save went
// out, or a Save that was refused, is said beside the picker.
const voiceBrowserDroppedHeadJS = voiceOffHeadJS + `const before = services.filter(s => !s.plugin);
const named = { provider: ENGINE, plugin: true, default: true, model: '', language: '', voice: '', monthly_minutes: 300, monthly_characters: 0 };
const sentence = 'This browser cannot be chosen while Room voice is running.';
const speakOther = () => [...document.querySelectorAll('input[name="sp-mode-speak"]')].find(r => !r.checked);
const engineFrame = speak => speech({ stt: named, tts: named, spent: [], mode: { listen: 'interactive', speak, name: 'interactive', revision: 3, set: true } });
const engineKnown = () => until(() => options('sp-provider-stt').includes('Room voice'), 3000).catch(() => {});
const allSaid = () => document.getElementById('speech-form').textContent.split(sentence).length - 1;
async function run() {
  await opened(speech({ services: before, spent: [] }));
  __ORDER__
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

const voiceBrowserAfterSaveJS = `const other = speakOther();
  other.click();
  save();
  await until(() => lastSet(), 3000).catch(() => {});
  const set = lastSet();
  check(set && set.config['speech.tts.provider'] === 'OpenAI', 'that Save asked for something else: ' + JSON.stringify(set && set.config['speech.tts.provider']));
  pick('sp-provider-tts', '');
  await until(() => $('sp-provider-tts').value === '', 3000).catch(() => {});
  // The saved voice stays what it was named, and the engine arrives beside it.
  const arrived = speech({ spent: [], mode: { listen: 'interactive', speak: other.value, name: 'interactive', revision: 3, set: true } });
  arrived.stt = { ...arrived.stt, language: set && set.config['speech.stt.language'] };
  frame(arrived);
  await engineKnown();
  check(!options('sp-provider-tts').includes('This browser'), 'this browser is still offered beside an engine: ' + options('sp-provider-tts'));
  check($('sp-browser-gone')?.textContent === sentence, 'the browser went unsaid: ' + $('sp-browser-gone')?.textContent);
  frame(arrived, { request_id: set && set.request_id });
  await until(() => document.querySelector('[data-said="speech"]'), 3000).catch(() => {});
  const said = document.querySelector('[data-said="speech"]');
  check(said && said.classList.contains('good'), 'that Save was answered as: ' + (said && said.className + ': ' + said.textContent));
  check(allSaid() === 1, 'said ' + allSaid() + ' times after the answer');`

const voiceBrowserRefusedJS = `pick('sp-provider-tts', '');
  await until(() => $('sp-provider-tts').value === '', 3000).catch(() => {});
  save();
  await until(() => lastSet(), 3000).catch(() => {});
  const set = lastSet();
  check(set && set.config['speech.tts.provider'] === '', 'this browser was not asked for: ' + JSON.stringify(set && set.config));
  frame(engineFrame('auto'));
  await engineKnown();
  check(!options('sp-provider-tts').includes('This browser'), 'this browser is still offered beside an engine: ' + options('sp-provider-tts'));
  check(allSaid() === 0, 'said while its own Save waits: ' + allSaid());
  say({ type: 'error', request_id: set && set.request_id, message: 'fixture: refused' });
  await until(() => document.querySelector('[data-said="speech"]'), 3000).catch(() => {});
  const said = document.querySelector('[data-said="speech"]');
  check(said && said.classList.contains('bad') && said.textContent.includes('fixture: refused'), 'the refusal: ' + (said && said.textContent));
  check($('sp-browser-gone')?.textContent === sentence, 'the browser went unsaid after a refusal: ' + $('sp-browser-gone')?.textContent);
  check(allSaid() === 1, 'said ' + allSaid() + ' times after the refusal');`

func TestABrowserDroppedWhileSaveWaitsIsSaidOnce(t *testing.T) {
	for name, order := range map[string]string{"asked for after that Save": voiceBrowserAfterSaveJS, "its Save refused": voiceBrowserRefusedJS} {
		t.Run(name, func(t *testing.T) {
			page := strings.Replace(voiceBrowserDroppedHeadJS, "__FRAME__", voiceFrameJS, 1)
			runRealPage(t, statusFakeSocketJS, strings.Replace(page, "__ORDER__", order, 1), homeStyles)
		})
	}
}

// A NEWER TALK ON SURVIVES A FRAME. Talk turned on and not yet saved stays on when an
// engine arrives with the host still reporting Off, and when the answer to an older
// Save reports the Off it was asked for.
const voiceTalkHeldJS = voiceOffHeadJS + `const before = services.filter(s => !s.plugin);
const automatic = { provider: ENGINE, plugin: true, default: true, model: '', language: '', voice: '', monthly_minutes: 0, monthly_characters: 0 };
const none = { provider: '', model: '', language: '', voice: '', monthly_minutes: 0, monthly_characters: 0 };
const off = { listen: 'off', speak: 'auto', name: 'earbuds', revision: 3, set: true };
const on = { ...off, listen: 'interactive', name: 'interactive', revision: 4 };
async function run() {
  // No engine, Talk off saved: Talk on, then the first engine, the host still saying off.
  await opened(speech({ services: before, stt: none, tts: none, spent: [], mode: off }));
  talk('interactive').click();
  frame(speech({ stt: automatic, tts: automatic, spent: [], mode: off }));
  await until(() => options('sp-provider-stt').includes('Room voice'), 3000).catch(() => {});
  check(talk('interactive')?.checked, 'an engine arriving took back a newer Talk on');
  check($('sp-provider-stt').value === ENGINE, 'hearing does not read the engine with Talk on: ' + JSON.stringify($('sp-provider-stt').value));
  save();
  await until(() => lastSet(), 3000).catch(() => {});
  let set = lastSet();
  check(set && set.config['speech.mode'].listen === 'interactive' && set.config['speech.stt.provider'] === '', 'that Talk on was not saved: ' + JSON.stringify(set && [set.config['speech.stt.provider'], set.config['speech.mode']]));
  frame(speech({ stt: automatic, tts: automatic, spent: [], mode: on }), { request_id: set && set.request_id });
  await until(() => document.querySelector('[data-said="speech"]'), 3000).catch(() => {});
  // Talk off saved, Talk on while that waits, then its answer says off.
  talk('off').click();
  await until(() => talk('off')?.checked, 3000).catch(() => {});
  const earlier = lastSet();
  save();
  await until(() => lastSet() !== earlier, 3000).catch(() => {});
  set = lastSet();
  check(set !== earlier && set.config['speech.mode'].listen === 'off', 'Talk off was not sent: ' + JSON.stringify(set.config['speech.mode']));
  talk('interactive').click();
  frame(speech({ stt: automatic, tts: automatic, spent: [], mode: { ...off, revision: 5 } }), { request_id: set.request_id });
  await until(() => document.querySelector('[data-said="speech"]'), 3000).catch(() => {});
  check(talk('interactive')?.checked, 'the answer to an older Save took back a newer Talk on');
  check($('sp-provider-stt').value === ENGINE, 'hearing does not read the engine with the newer Talk on: ' + JSON.stringify($('sp-provider-stt').value));
  const later = lastSet();
  save();
  await until(() => lastSet() !== later, 3000).catch(() => {});
  check(lastSet() !== later && lastSet().config['speech.mode'].listen === 'interactive' && lastSet().config['speech.stt.provider'] === '', 'the newer Talk on was not saved: ' + JSON.stringify([lastSet().config['speech.stt.provider'], lastSet().config['speech.mode']]));
  frame(speech({ stt: automatic, tts: automatic, spent: [], mode: { ...on, revision: 6 } }), { request_id: lastSet().request_id });
  await until(() => $('sp-provider-stt').value === ENGINE && !talk('off')?.checked, 3000).catch(() => {});
  // The same with hearing already reading empty: off is saved, another Save of off goes
  // out, Talk is turned on while it waits, and its answer says off.
  talk('off').click();
  const offAgain = lastSet();
  save();
  await until(() => lastSet() !== offAgain, 3000).catch(() => {});
  const offSent = lastSet();
  frame(speech({ stt: automatic, tts: automatic, spent: [], mode: { ...off, revision: 7 } }), { request_id: offSent.request_id });
  await until(() => $('sp-provider-stt').value === '' && talk('off')?.checked, 3000).catch(() => {});
  check($('sp-provider-stt').value === '', 'off saved does not read empty: ' + JSON.stringify($('sp-provider-stt').value));
  const speakOther = [...document.querySelectorAll('input[name="sp-mode-speak"]')].find(r => !r.checked);
  speakOther.click();
  const heldOff = lastSet();
  save();
  await until(() => lastSet() !== heldOff, 3000).catch(() => {});
  const held = lastSet();
  check(held !== heldOff && held.config['speech.mode'].listen === 'off', 'the second Save did not send off: ' + JSON.stringify(held.config['speech.mode']));
  talk('interactive').click();
  frame(speech({ stt: automatic, tts: automatic, spent: [], mode: { ...off, speak: speakOther.value, revision: 8 } }), { request_id: held.request_id });
  await until(() => $('sp-provider-stt').value === ENGINE, 3000).catch(() => {});
  check(talk('interactive')?.checked, 'that answer took back Talk on beside an empty hearing');
  check($('sp-provider-stt').value === ENGINE, 'hearing does not read the engine: ' + JSON.stringify($('sp-provider-stt').value));
  const lastOne = lastSet();
  save();
  await until(() => lastSet() !== lastOne, 3000).catch(() => {});
  check(lastSet() !== lastOne && lastSet().config['speech.mode'].listen === 'interactive', 'that Talk on was not saved: ' + JSON.stringify(lastSet().config['speech.mode']));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestANewerTalkOnSurvivesAFrame(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, strings.Replace(voiceTalkHeldJS, "__FRAME__", voiceFrameJS, 1), homeStyles)
}

// AN ENGINE THAT LEAVES BEFORE SAVE. Automatic stays automatic and asks for nothing
// new; a chosen Off is still off; this browser is offered again.
const voiceEngineLeavesJS = voiceOffHeadJS + `const before = services.filter(s => !s.plugin);
const automatic = { provider: ENGINE, plugin: true, default: true, model: '', language: '', voice: '', monthly_minutes: 0, monthly_characters: 0 };
const none = { provider: '', model: '', language: '', voice: '', monthly_minutes: 0, monthly_characters: 0 };
const off = { listen: 'off', speak: 'auto', name: 'earbuds', revision: 3, set: true };
async function run() {
  await opened(speech({ stt: automatic, tts: automatic, spent: [], mode: off }));
  talk('interactive').click();
  await until(() => $('sp-provider-stt').value === ENGINE, 3000).catch(() => {});
  frame(speech({ services: before, stt: none, tts: none, spent: [], mode: off }));
  await until(() => options('sp-provider-tts').includes('This browser'), 3000).catch(() => {});
  check($('sp-provider-stt').value === '' && $('sp-provider-tts').value === '', 'the pickers do not read empty with the engine gone');
  check(talk('interactive')?.checked, 'Talk was moved by an engine leaving');
  check(!$('sp-browser-gone'), 'the browser is said to be gone with no engine: ' + $('sp-browser-gone')?.textContent);
  save();
  await until(() => lastSet(), 3000).catch(() => {});
  let set = lastSet();
  check(set && set.config['speech.stt.provider'] === '' && set.config['speech.tts.provider'] === '' && set.config['speech.mode'].listen === 'interactive', 'what was sent with the engine gone: ' + JSON.stringify(set && [set.config['speech.stt.provider'], set.config['speech.tts.provider'], set.config['speech.mode']]));
  frame(speech({ services: before, stt: none, tts: none, spent: [], mode: { ...off, listen: 'interactive', name: 'interactive', revision: 4 } }), { request_id: set && set.request_id });
  await until(() => document.querySelector('[data-said="speech"]'), 3000).catch(() => {});
  check(document.querySelector('[data-said="speech"]')?.classList.contains('good'), 'that save is not said as saved: ' + document.querySelector('[data-said="speech"]')?.textContent);
  // A chosen Off, and the engine goes: still off.
  frame(speech({ stt: automatic, tts: automatic, spent: [], mode: { ...off, listen: 'interactive', name: 'interactive', revision: 5 } }));
  await until(() => options('sp-provider-tts').includes('Room voice'), 3000).catch(() => {});
  pick('sp-provider-stt', '');
  await until(() => talk('off')?.checked, 3000).catch(() => {});
  frame(speech({ services: before, stt: none, tts: none, spent: [], mode: { ...off, listen: 'interactive', name: 'interactive', revision: 6 } }));
  await until(() => options('sp-provider-tts').includes('This browser'), 3000).catch(() => {});
  check($('sp-provider-stt').value === '' && talk('off')?.checked, 'a chosen Off did not stay with the engine gone');
  const earlier = lastSet();
  save();
  await until(() => lastSet() !== earlier, 3000).catch(() => {});
  set = lastSet();
  check(set !== earlier && set.config['speech.stt.provider'] === '' && set.config['speech.mode'].listen === 'off', 'a chosen Off was not sent as off: ' + JSON.stringify(set && [set.config['speech.stt.provider'], set.config['speech.mode']]));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestAnEngineThatLeavesBeforeSave(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, strings.Replace(voiceEngineLeavesJS, "__FRAME__", voiceFrameJS, 1), homeStyles)
}

func TestNeverSaysWhatItDoes(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, strings.Replace(voiceNeverJS, "__FRAME__", voiceFrameJS, 1), homeStyles)
}

// voiceFrameJS is settings_voice_browser_test.go's config frame, with room for the
// request it answers.
const voiceFrameJS = `const frame = (sp, extra) => say({ ...extra, type: 'config', config: { llm: { provider: 'OpenAI', model: 'm', resolved_provider: 'OpenAI', resolved_model: 'm', timeout_seconds: 120 }, speech: sp, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [], dashboard: {} } });`

// A MOVE WITHIN SETTINGS SURVIVES A REDRAW BEFORE ITS HASHCHANGE. An address names
// a Settings section at once, and its hashchange comes a task later. A frame that
// redraws Settings in between used to write the section still shown over the new
// address, and the move was lost. Moves the page makes itself still follow.
const settingsMoveJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const $ = id => document.getElementById(id);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const say = m => sockets[sockets.length - 1].say(m);
const shown = el => !!el && el.getClientRects().length > 0;
const PROVIDERS = { type: 'providers', providers: [{ name: 'OpenAI', chat: true, models: ['m'], default_model: 'm', has_key: true }] };
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 6, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 6 } });
  say(PROVIDERS);
  say({ type: 'config', config: { llm: { provider: 'OpenAI', model: 'm', resolved_provider: 'OpenAI', resolved_model: 'm', timeout_seconds: 120 }, speech: { stt: {}, tts: {}, services: [] }, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [] } });
  await until(() => typeof S.go === 'function');
  location.hash = '#/settings/voice';
  await until(() => shown($('speech-form')), 10000);
  // The move, then a frame that redraws Settings, in one task.
  location.hash = '#/settings/substrate';
  say(PROVIDERS);
  await until(() => shown(document.querySelector('[data-section="substrate"]')), 3000).catch(() => {});
  check(location.hash === '#/settings/model', 'the move was lost to a redraw: the address is ' + location.hash);
  check(shown(document.querySelector('[data-section="substrate"]')) && !shown($('speech-form')), 'Model is not what is shown');
  // Moving back the same way.
  location.hash = '#/settings/speech';
  say(PROVIDERS);
  await until(() => shown($('speech-form')), 3000).catch(() => {});
  check(location.hash === '#/settings/voice', 'the old name did not land on its new address: ' + location.hash);
  // A move the page makes itself still takes the address with it.
  S.openUpdates();
  await until(() => location.hash === '#/settings/app', 3000).catch(() => {});
  check(location.hash === '#/settings/app', 'the address did not follow the page\'s own move: ' + location.hash);
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

// THE PAGE'S OWN MOVE IS NEWER THAN AN ADDRESS STILL TO BE ANSWERED. An address is
// moved, and before its hashchange runs the page itself opens a section: that section
// stands, and the hashchange that follows does not take it away. A redraw is not a
// move, and leaves the address for its hashchange (the test above).
const settingsOwnMoveJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const say = m => sockets[sockets.length - 1].say(m);
const shown = el => !!el && el.getClientRects().length > 0;
const section = id => document.querySelector('#settings-stack [data-section="' + id + '"], #settings-stack[data-section="' + id + '"]');
// Resolves once the hashchange asked for has run, and the handlers after it.
const landed = () => new Promise(done => addEventListener('hashchange', () => setTimeout(done), { once: true }));
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 6, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 6 } });
  say({ type: 'config', config: { llm: { provider: 'OpenAI', model: 'm', resolved_provider: 'OpenAI', resolved_model: 'm', timeout_seconds: 120 }, speech: { stt: {}, tts: {}, services: [] }, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [], dashboard: {} } });
  await until(() => typeof S.go === 'function');
  location.hash = '#/settings/voice';
  await until(() => S.view === 'settings' && shown(document.getElementById('speech-form')), 10000);
  for (const [name, move, id, address] of [
    ['to Backups', () => S.openSettings('backups'), 'backups', '#/settings/backups'],
    ['to the app', () => S.openUpdates(), 'updates', '#/settings/app'],
  ]) {
    location.hash = '#/settings/voice';
    await until(() => location.hash === '#/settings/voice' && shown(document.getElementById('speech-form')), 5000);
    const done = landed();
    location.hash = '#/settings/model';
    move();
    await done;
    check(location.hash === address, 'after moving ' + name + ' the address is ' + location.hash);
    check(section(id) && !section('substrate'), 'after moving ' + name + ' Settings shows ' + (document.querySelector('#settings-stack [data-section]')?.dataset.section || 'nothing'));
  }
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestThePagesOwnMoveIsNewerThanAnAddressStillToBeAnswered(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, settingsOwnMoveJS, homeStyles)
}

func TestAMoveWithinSettingsSurvivesARedrawBeforeItsHashchange(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, settingsMoveJS, homeStyles)
}

// VOICE'S STYLESHEET OPENS WITH SETTINGS. Held, Voice is not drawn unstyled; refused,
// Settings says it did not open; a later opening fetches it again and draws Voice
// styled, with the operator's own custom.css still last.
func TestVoicesStylesheetOpensWithSettings(t *testing.T) {
	runDashboardInEachEngine(t, func(t *testing.T) *Server {
		s := New("127.0.0.1", 0, &WSHandler{})
		release := make(chan struct{})
		var releasing sync.Once
		open := func() { releasing.Do(func() { close(release) }) }
		t.Cleanup(open)
		// Firefox runs no more of a page while a sheet it asked for is held (measured),
		// so there the sheet is refused at once.
		if strings.Contains(t.Name(), "gecko") {
			open()
		}
		var asked atomic.Int32
		inner := s.server.Handler
		s.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/__fail-the-sheet":
				open()
				w.WriteHeader(http.StatusNoContent)
				return
			case "/views/settings/voice.css":
				if asked.Add(1) == 1 {
					select {
					case <-release:
					case <-r.Context().Done():
					}
					http.Error(w, "held, then refused", http.StatusServiceUnavailable)
					return
				}
			}
			inner.ServeHTTP(w, r)
		})
		return s
	}, "/", shellDriver+`
const voice = () => document.querySelector('#settings-stack [data-section="speech"]');
const failed = () => document.getElementById('part-failure');
const voiceSheet = () => document.querySelector('link[href="./views/settings/voice.css"]');
const styled = () => [...document.styleSheets].some(s => s.href && s.href.endsWith('/views/settings/voice.css') && s.cssRules.length > 0);
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  location.hash = '#/settings/voice';
  await until(() => voiceSheet() || failed());
  if (!voiceSheet() && !failed()) return fail('opening Settings did not ask for Voice\'s sheet');
  // waits by design: what must not be drawn gives nothing to wait on
  await new Promise(r => setTimeout(r, 400));
  if (voice()) return fail('Voice was drawn with its sheet still held');
  await fetch('/__fail-the-sheet');
  await until(() => failed());
  if (!failed()) return fail('a sheet that failed was not said');
  if (voice()) return fail('Voice was drawn with its sheet failed');
  if (voiceSheet()) return fail('the failed sheet was left in the page');
  location.hash = '#/home';
  await until(() => viewOn() === 'view-home');
  location.hash = '#/settings/voice';
  await until(() => voice(), 8000);
  if (!voice()) return fail('a later opening did not draw Voice: at ' + location.hash + ' on ' + viewOn());
  if (!styled()) return fail('Voice was drawn without its sheet');
  const custom = document.querySelector('link[href="./custom.css"]');
  if (!custom || !(voiceSheet().compareDocumentPosition(custom) & Node.DOCUMENT_POSITION_FOLLOWING)) return fail('the operator\'s custom.css is not last');
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`)
}
