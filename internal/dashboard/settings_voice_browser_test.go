//go:build !windows

package dashboard

import "testing"

// VOICE IS THE BOARD SettingsVoice (spec step 15). On the shipped page and its
// socket: its heading, then Talk, Hearing you and their voice, the rest of voice
// under one disclosure, and Save. Talk's two choices are radio groups that a
// person can name, and they leave as one pair. A service with a kept key folds
// the key and its ceiling behind Change; one with none shows them. With no service
// chosen, no model or voice is asked for. A frame does not close what is open.
// Hearing offers no browser engine: nothing in the page can hear in a browser.
// What is said of typed replies follows the voice chosen: a chosen voice reads
// every reply, and the browser's own reads only what answers speech.
const settingsVoiceJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const $ = id => document.getElementById(id);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const pause = ms => new Promise(r => setTimeout(r, ms));
const shown = el => !!el && el.getClientRects().length > 0;
const say = m => sockets[sockets.length - 1].say(m);
const heard = { model_required: true, lists_models: true }, spoken = { model_required: true, voice_required: true, lists_models: true, lists_voices: true };
const services = [
  { name: 'OpenAI', endpoint: 'https://api.openai.test/v1', speech: { stt: heard, tts: spoken }, added: true, has_key: true, chats: true },
  { name: 'Deepgram', endpoint: 'https://deepgram.test', speech: { stt: heard, tts: heard }, added: false, has_key: false },
];
const speech = (over = {}) => ({ stt: { provider: 'OpenAI', model: 'm-hear', monthly_minutes: 300 }, tts: { provider: '' }, services, mode: { listen: 'meeting', speak: 'on', name: 'meeting', revision: 2, set: true },
  speakers: { mode: 'all', uids: [], unidentified: 'deliver', revision: 1 }, heard_history: { capacity: 256, retention_seconds: 1800, text_bytes: 8192, page_rows: 20 }, voices: [], operator_speakers: [], ...over });
const frame = sp => say({ type: 'config', config: { llm: { provider: 'OpenAI', model: 'm', resolved_provider: 'OpenAI', resolved_model: 'm', timeout_seconds: 120 }, speech: sp, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [], dashboard: {} } });
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 6, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 6 } });
  say({ type: 'providers', providers: [{ name: 'OpenAI', chat: true, models: ['m'], default_model: 'm', has_key: true }] });
  frame(speech());
  await until(() => typeof S.go === 'function');
  location.hash = '#/settings/voice';
  await until(() => $('speech-form'), 10000);
  const form = $('speech-form');
  // The board's order and words.
  check(form.querySelector('h2').textContent === 'Voice', 'the page does not say Voice: ' + form.querySelector('h2').textContent);
  check(form.querySelector('.settings-voice__intro').textContent === 'How you talk with Mira, and how Mira speaks. Changes apply the next time you press Talk.', 'the sentence under the heading: ' + form.querySelector('.settings-voice__intro').textContent);
  const parts = [...form.querySelectorAll(':scope > section h3')].map(h => h.textContent).join();
  check(parts === "Talk,Hearing you,Mira's voice", 'the parts are not the board\'s: ' + parts);
  const more = $('sp-more');
  check(more && more.parentElement === form && !more.open, 'the rest of voice is not one closed disclosure');
  check(more.querySelector('summary').textContent === 'More about voice · who is heard, your voice, what was heard, echo', 'the disclosure says: ' + more.querySelector('summary').textContent);
  for (const id of ['sp-speakers', 'sp-my-voice', 'sp-heard-history', 'sp-echo-mode']) check(more.contains($(id)), id + ' is not under the disclosure');
  check(getComputedStyle(more.querySelector('summary')).display !== 'flex', 'the disclosure has lost its marker');
  check(parseFloat(getComputedStyle(more).borderTopWidth) > 0 && parseFloat(getComputedStyle(more).borderBottomWidth) > 0, 'the disclosure is not between two rules');
  check(form.lastElementChild.querySelector('[data-save="speech"]') && form.querySelectorAll('[data-save]').length === 1 && form.querySelector('[data-save="speech"]').textContent === 'Save', 'Voice does not end in its one Save');
  check(form.lastElementChild.textContent.includes('Talk and spoken replies apply at once; the rest at your next Talk.'), 'Save does not say when things apply');
  // Talk's choices have names, and leave as one pair.
  const radio = (name, words) => [...$('speech-form').querySelectorAll('input[name="' + name + '"]')].find(r => r.closest('label').textContent.startsWith(words));
  const legends = [...$('sp-voice-mode').querySelectorAll('fieldset > legend')].map(l => l.textContent).join();
  check(legends === 'When you press Talk,Spoken replies', 'the two groups are not named: ' + legends);
  check(radio('sp-mode-listen', 'Record the room')?.checked && radio('sp-mode-speak', 'Always')?.checked, 'the pair in force is not drawn');
  check(radio('sp-mode-listen', 'You speak to Mira') && radio('sp-mode-listen', 'Off') && radio('sp-mode-speak', 'When you spoke') && radio('sp-mode-speak', 'Never'), 'a choice of the board is missing');
  // The menu holds neither group, and the page does not say it does.
  check(!$('speech-form').textContent.includes('menu on Home'), 'the page says the Talk menu holds these choices');
  const whenSpoke = () => radio('sp-mode-speak', 'When you spoke').closest('label').textContent, note = () => $('sp-note-tts').textContent;
  // This browser reads only what answers speech.
  check(whenSpoke().includes('Replies to what you type are not.'), 'with this browser as the voice, typed replies are not said to stay silent: ' + whenSpoke());
  check(!note().includes('Speaks every reply') && note().startsWith('With nothing chosen'), 'with this browser as the voice, every reply is promised: ' + note());
  radio('sp-mode-listen', 'Off').click(); radio('sp-mode-speak', 'Never').click();
  // Hearing offers services and a server, never a browser engine.
  const offered = id => [...$(id).options].map(o => o.textContent).join();
  check(offered('sp-provider-stt') === 'Off,OpenAI,Deepgram,A server you run', 'hearing offers: ' + offered('sp-provider-stt'));
  check(offered('sp-provider-tts') === 'This browser,OpenAI,Deepgram,A server you run', 'their voice offers: ' + offered('sp-provider-tts'));
  check(!$('sp-part-stt').parentElement.textContent.includes('This browser'), 'hearing speaks of a browser engine');
  // A kept key is folded behind Change, and Change opens it.
  const fields = () => $('sp-key-fields-stt');
  check($('sp-key-label-stt').textContent === 'OpenAI key · kept' && !shown(fields()) && $('sp-key-change-stt').textContent === 'Change', 'a kept key is not folded: ' + $('sp-key-label-stt').textContent);
  $('sp-key-change-stt').click();
  check(shown(fields()) && shown($('sp-key-stt')) && shown($('sp-ceiling-stt')) && $('sp-key-change-stt').getAttribute('aria-expanded') === 'true', 'Change does not open the key and the ceiling');
  // With no service chosen, nothing asks for a model or a voice, or for a key.
  check(!shown($('sp-model-tts')) && !shown($('sp-voice-tts')) && !shown($('sp-sample-tts')) && !shown($('sp-key-row-tts')), 'with no service chosen a model, a voice or a key is asked for');
  check($('sp-readback-tts').textContent === 'Replies spoken by this browser.', 'their voice says: ' + $('sp-readback-tts').textContent);
  // A service with no key kept shows where it goes, with no Change.
  const pick = (id, v) => { $(id).value = v; $(id).dispatchEvent(new Event('change', { bubbles: true })); };
  pick('sp-provider-stt', 'Deepgram');
  await until(() => $('sp-key-label-stt').textContent === 'Deepgram key · none kept', 3000).catch(() => {});
  check($('sp-key-label-stt').textContent === 'Deepgram key · none kept' && shown($('sp-key-stt')) && !$('sp-key-change-stt'), 'a service with no key does not show where it goes: ' + $('sp-key-label-stt').textContent);
  check($('sp-readback-stt').textContent.includes('Deepgram is chosen and not saved'), 'a chosen service is not said to be unsaved: ' + $('sp-readback-stt').textContent);
  pick('sp-provider-stt', 'OpenAI');
  await until(() => $('sp-key-label-stt').textContent === 'OpenAI key · kept', 3000).catch(() => {});
  // A voice chosen reads every reply, and the page does not say otherwise.
  pick('sp-provider-tts', 'OpenAI');
  await until(() => note().includes('Speaks every reply'), 3000).catch(() => {});
  check(note().startsWith('Speaks every reply'), 'a voice chosen does not promise every reply: ' + note());
  check(whenSpoke().includes('Replies to what you say are read aloud.') && !whenSpoke().includes('type are not'), 'a voice chosen is said to stay silent on typed replies: ' + whenSpoke());
  pick('sp-provider-tts', '');
  await until(() => !note().includes('Speaks every reply'), 3000).catch(() => {});
  // A frame does not close what is open, or take the unsaved choices.
  $('sp-more').open = true; await pause(30);
  frame(speech({ heard_history: { capacity: 128, retention_seconds: 1800, text_bytes: 8192, page_rows: 20 },
    voices: [{ uuid: '7e0b1f2c-4c31-4b7a-9f0e-3a1d2c5b6e70', label: 'Speaker 1', enrolled: true, heard: true }, { uuid: '1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d', label: 'Ann', enrolled: true, heard: true, mine: true }] }));
  await until(() => $('sp-heard-capacity')?.value === '128', 3000).catch(() => {});
  check($('sp-more').open, 'a frame closed the disclosure');
  // As the board: the choices under it are no wider than it draws them. On a
  // phone each speaker's id has its own line, so the rows match.
  for (const id of ['sp-speakers-mode', 'sp-echo-mode']) check($(id).getBoundingClientRect().width <= 520.5, id + ' is wider than the board: ' + $(id).getBoundingClientRect().width);
  const voiceRows = [...$('sp-my-voice').querySelectorAll('[data-my-voice-row]')];
  check(voiceRows.length === 2, 'the speakers are not drawn: ' + voiceRows.length);
  for (const row of voiceRows) {
    const label = row.querySelector('[data-my-voice-label]').getBoundingClientRect(), id = row.querySelector('.settings-voice__id').getBoundingClientRect();
    check(window.innerWidth < 768 ? id.top >= label.bottom - 1 : id.top < label.bottom, 'a speaker\'s id at ' + window.innerWidth + ' px: label ends ' + label.bottom + ', id starts ' + id.top);
  }
  // Open, what is under it runs straight on to Save, with no empty stretch.
  const before = $('sp-echo-readback').getBoundingClientRect().bottom, saveAt = $('speech-form').querySelector('[data-save="speech"]').getBoundingClientRect().top;
  check(saveAt - before < 80, 'an empty stretch stands above Save: ' + Math.round(saveAt - before) + ' px');
  const stretched = [...$('speech-form').querySelectorAll('#sp-speakers .settings-voice__field')].filter(e => !e.hidden && e.getBoundingClientRect().height > 180).length;
  check(!stretched, 'a field is stretched under the disclosure');
  check(shown($('sp-key-fields-stt')), 'a frame folded the key they opened');
  check(radio('sp-mode-listen', 'Off')?.checked && radio('sp-mode-speak', 'Never')?.checked, 'a frame took the unsaved choices');
  // Save sends both halves as one pair.
  $('speech-form').querySelector('[data-save="speech"]').click();
  await pause(50);
  const pair = sent.filter(f => f.type === 'config_set').map(f => f.config && f.config['speech.mode']).filter(Boolean).at(-1);
  check(pair && pair.listen === 'off' && pair.speak === 'off', 'the pair was not sent whole: ' + JSON.stringify(pair));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestVoiceIsTheBoard(t *testing.T) {
	for _, size := range [][2]int{{1280, 800}, {390, 844}} {
		runRealPageAtSize(t, statusFakeSocketJS, settingsVoiceJS, homeStyles, size[0], size[1])
	}
}
