//go:build !windows

// .
// .

package dashboard

import "testing"

// .
// .
// .
// .
// .
func TestTheMicrophoneGlyphIsTheModeTheHostNames(t *testing.T) {
	page := micMarkup + `<script type="module">
import { render, wireMic, modeName } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(() => {
  const opened = [];
  S.openSettings = (section, focus) => opened.push(section + '/' + focus);
  const mic = document.getElementById('mic'), conv = document.getElementById('converse');
  const menuMode = document.getElementById('mic-mode'), meeting = document.getElementById('mic-meeting');
  const show = (state, listen, speak, rev) => {
    S.stats = { voice_state: state, voice_listen: listen, voice_speak: speak, voice_mode_revision: rev };
    S.connected = true; render();
  };
  wireMic();

  // THE FOUR MODES, each drawn as itself.
  show('cloud', 'interactive', 'on', 1);
  assert(mic.dataset.glyph === 'mic' && !mic.classList.contains('earbuds') && !mic.classList.contains('voice-off') && !mic.classList.contains('meeting'),
    'interactive is the microphone, unmarked: ' + mic.outerHTML);
  assert(mic.title === 'Interactive — tap to talk, or hold', 'interactive does not name itself: ' + mic.title);
  assert(mic.getAttribute('aria-label') === mic.title, 'the label and the title disagree');
  assert(menuMode.textContent === 'Mode: interactive', 'the menu does not name the mode in force: ' + menuMode.textContent);

  show('cloud', 'off', 'on', 2);
  assert(mic.dataset.glyph === 'ear' && mic.classList.contains('earbuds'), 'earbuds is not an ear: ' + mic.outerHTML);
  assert(mic.title.indexOf('Earbuds') === 0 && mic.title.indexOf('hold to talk') > 0, 'earbuds does not say what the next tap — or a hold — does: ' + mic.title);

  show('cloud', 'off', 'off', 3);
  assert(mic.dataset.glyph === 'micoff' && mic.classList.contains('voice-off') && !mic.classList.contains('earbuds'),
    'off is not a crossed microphone: ' + mic.outerHTML);
  assert(mic.title.indexOf('Voice off') === 0, 'off does not name itself: ' + mic.title);

  show('cloud', 'meeting', 'off', 4);
  assert(mic.dataset.glyph === 'room' && mic.classList.contains('meeting'), 'a meeting is not the room: ' + mic.outerHTML);
  assert(mic.title.indexOf('hold to record the room') > 0, 'a cloud meeting does not say it records in held segments: ' + mic.title);
  assert(menuMode.textContent === 'Mode: meeting' && meeting.getAttribute('aria-checked') === 'true',
    'the menu does not show the meeting it is in: ' + menuMode.textContent + ' / ' + meeting.outerHTML);

  // A STATUS FRAME OLDER THAN THE MODE THIS PAGE ACCEPTED IS NOT NEWS. A
  // page that has just changed the mode is handed frames that left the
  // host before the change; applying one flips the control back under the
  // operator's hand.
  show('cloud', 'interactive', 'on', 3);
  assert(mic.dataset.glyph === 'room' && menuMode.textContent === 'Mode: meeting',
    'a lower-revision frame moved the control back: ' + mic.dataset.glyph);
  show('cloud', 'interactive', 'on', 5);
  assert(mic.dataset.glyph === 'mic' && menuMode.textContent === 'Mode: interactive', 'the next frame was not followed: ' + mic.dataset.glyph);

  // A missing pair is auto, which is the behaviour that was here before
  // the pair existed: interactive, and the page's own rule about speaking.
  S.stats = { voice_state: 'cloud', voice_mode_revision: 6 }; render();
  assert(mic.dataset.glyph === 'mic' && menuMode.textContent === 'Mode: interactive', 'auto is not interactive: ' + mic.dataset.glyph);

  // THE STATES THAT ARE NOT A MODE are what they were.
  show('setup', 'off', 'off', 7);
  assert(mic.dataset.glyph === 'mic' && mic.classList.contains('faint') && mic.title === 'Voice isn\'t set up — opens Speech settings',
    'a microphone with nothing to speak into was drawn as a mode: ' + mic.outerHTML);
  opened.length = 0;
  mic.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
  assert(opened.join() === 'speech/sp-provider-stt', 'setup no longer opens Speech settings: ' + opened.join());
  show('safe', 'interactive', 'on', 8);
  assert(mic.disabled && mic.dataset.glyph === 'mic', 'SAFE no longer pauses the microphone: ' + mic.outerHTML);

  // The plugin's control carries the same mode.
  show('plugin', 'off', 'on', 9);
  assert(!conv.hidden && conv.dataset.glyph === 'ear' && conv.classList.contains('earbuds'), 'the conversation control is not the mode: ' + conv.outerHTML);
  assert(conv.title.indexOf('Earbuds') === 0, 'the conversation control does not name the mode: ' + conv.title);
  show('plugin', 'meeting', 'off', 10);
  assert(conv.dataset.glyph === 'room' && conv.title.indexOf('Meeting') === 0, 'a plugin meeting is not the room: ' + conv.outerHTML);

  // The two pairs no name covers are said as they are, never as a name
  // that would be a lie.
  assert(modeName('interactive', 'on') === 'interactive' && modeName('off', 'on') === 'earbuds' &&
    modeName('off', 'off') === 'off' && modeName('meeting', 'off') === 'meeting' &&
    modeName('meeting', 'on') === 'meeting, spoken replies' && modeName('interactive', 'off') === 'interactive, text replies',
    'a pair is named wrongly: ' + modeName('meeting', 'on'));
});
</script>`
	runPageInEngines(t, page, micModules)
}

// .
// .
// .
// .
// .
// .
func TestTheMicrophoneCycleSendsThePairWholeAndTheHoldLeavesIt(t *testing.T) {
	page := micMarkup + `<script type="module">
import { render, wireMic, bindTransport, playbackForTest } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(async () => {
  const frames = [], sent = [];
  bindTransport(f => frames.push(new Uint8Array(f)), m => { sent.push(m); return 'req-' + sent.length; });
  let node = null, closed = 0;
  const stream = { getTracks: () => [{ stop() {} }], getAudioTracks: () => [{ label: 'test', stop() {}, getSettings: () => ({}), addEventListener() {} }] };
  if (!navigator.mediaDevices) Object.defineProperty(navigator, 'mediaDevices', { value: {}, configurable: true });
  navigator.mediaDevices.getUserMedia = async () => stream;
  window.AudioContext = class {
    constructor() { this.sampleRate = 16000; this.destination = {}; this.currentTime = 0; this.state = 'running'; }
    createMediaStreamSource() { return { connect() {} }; }
    createScriptProcessor() { node = { connect() {}, disconnect() {}, onaudioprocess: null }; return node; }
    close() { closed++; }
  };
  const tick = () => new Promise(r => setTimeout(r, 0));
  const until = async (ok, what) => { for (let i = 0; i < 200 && !ok(); i++) await tick(); assert(ok(), what); };
  const talk = () => node.onaudioprocess({ inputBuffer: { getChannelData: () => new Float32Array(4800).fill(0.25) } });
  const mic = document.getElementById('mic');
  const press = type => mic.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true }));
  const tap = () => { press('pointerdown'); press('pointerup'); };
  const modes = () => sent.filter(m => m.type === 'config_set' && m.config && m.config['speech.mode'])
    .map(m => m.config['speech.mode']).map(p => p.listen + '/' + p.speak);
  const show = (listen, speak, rev) => { S.stats = { voice_state: 'cloud', voice_listen: listen, voice_speak: speak, voice_mode_revision: rev }; S.connected = true; render(); };

  show('interactive', 'on', 1);
  wireMic(); render();

  // 1. An idle tap in interactive latches listening, and changes nothing:
  //    the mode the operator is in is the one they asked for.
  node = null;
  tap();
  await until(() => node && node.onaudioprocess, 'the tap never opened the microphone');
  assert(mic.classList.contains('live'), 'a tap no longer latches: ' + mic.outerHTML);
  assert(modes().length === 0, 'an idle tap changed the mode: ' + modes().join(' '));
  talk();

  // 2. A tap while latched sends what was said AND asks for earbuds.
  press('pointerdown');
  await until(() => frames.length === 1, 'the second tap did not send what was said');
  assert(frames[0][2] === 1, 'the utterance was not marked a conversation (mode byte ' + frames[0][2] + ')');
  assert(modes().join(' ') === 'off/on', 'the send did not ask for earbuds: ' + modes().join(' '));
  press('pointerup');
  show('off', 'on', 2);

  // 3. A tap in earbuds asks for silence, and sends no audio.
  tap();
  await until(() => modes().length === 2, 'the tap in earbuds changed nothing');
  assert(modes()[1] === 'off/off', 'earbuds does not lead to off: ' + modes().join(' '));
  await tick(); await tick();
  assert(frames.length === 1, 'the tap that changed the mode sent audio');
  show('off', 'off', 3);

  // 4. A tap in off turns the microphone back on.
  tap();
  await until(() => modes().length === 3, 'the tap in off changed nothing');
  assert(modes()[2] === 'interactive/on', 'off does not lead back to interactive: ' + modes().join(' '));
  assert(modes().join(' ') === 'off/on off/off interactive/on', 'the cycle is not interactive → earbuds → off → interactive: ' + modes().join(' '));
  assert(sent.filter(m => m.type === 'config_set').every(m => {
    const p = m.config['speech.mode'];
    return p && typeof p.listen === 'string' && typeof p.speak === 'string' && Object.keys(p).length === 2;
  }), 'a change travelled with one half: ' + JSON.stringify(sent.filter(m => m.type === 'config_set')));

  // 5. A HOLD IS PUSH-TO-TALK IN EVERY MODE and leaves the mode where it
  //    was: in earbuds it records while held, sends on release, and asks
  //    for nothing.
  show('off', 'on', 4);
  node = null;
  press('pointerdown');
  await until(() => node && node.onaudioprocess, 'the hold in earbuds never opened the microphone');
  talk();
  await new Promise(r => setTimeout(r, 450));
  press('pointerup');
  await until(() => frames.length === 2, 'the hold in earbuds sent nothing');
  assert(modes().length === 3, 'the hold changed the mode: ' + modes().join(' '));
  assert(!mic.classList.contains('live'), 'the hold kept recording after its release');

  // 6. THE MENU IS WHERE A MEETING IS DECIDED, and the pair travels whole.
  document.getElementById('mic-meeting').click();
  await until(() => modes().length === 4, 'the meeting item sent nothing');
  assert(modes()[3] === 'meeting/off', 'the meeting item did not send the meeting pair: ' + modes().join(' '));
  show('meeting', 'off', 5);

  // 7. A cloud meeting records the room in held segments, and the host is
  //    told it is a meeting: the whole-utterance mode byte is its zero.
  node = null;
  press('pointerdown');
  await until(() => node && node.onaudioprocess, 'the hold in a meeting never opened the microphone');
  talk();
  await new Promise(r => setTimeout(r, 450));
  press('pointerup');
  await until(() => frames.length === 3, 'the meeting segment was never sent');
  assert(frames[2][2] === 0, 'a meeting utterance was sent as a conversation (mode byte ' + frames[2][2] + ')');
  assert(modes().length === 4, 'recording the room changed the mode: ' + modes().join(' '));

  // 8. A tap in a meeting comes back to talking with the identity.
  tap();
  await until(() => modes().length === 5, 'the tap in a meeting changed nothing');
  assert(modes()[4] === 'interactive/on', 'a meeting does not tap back to interactive: ' + modes().join(' '));

  // THE PLAYBACK CONTEXT IS DROPPED, NOT REUSED, when a conversation is
  // about to open one after the microphone (the capture-first rule): a
  // context made while the microphone was closed must not survive it.
  const pb = playbackForTest();
  pb.prime(16000);
  assert(pb.ctx, 'prime made no playback context');
  const was = closed;
  pb.release();
  assert(!pb.ctx && closed === was + 1, 'release kept the context the next capture has to replace');
  pb.release();
  assert(!pb.ctx && closed === was + 1, 'releasing nothing was not nothing');
});
</script>`
	runPageInEngines(t, page, micModules)
}

// .
// .
// .
const voiceModeSettingsPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, saveConfigSection, voiceModeReadback } from './views/settings.js';
import { assert, run } from './__harness.js';

run(() => {
  const byId = id => document.getElementById(id);
  const sets = () => frames.filter(f => f.type === 'config_set').map(f => f.config);
  S.identityExists = true;
  S.providersLoaded = true;
  S.providers = [];
  S.config = { llm: { provider: 'OpenAI', model: 'm', resolved_provider: 'OpenAI', resolved_model: 'm', timeout_seconds: 120 },
    speech: { stt: { provider: '' }, tts: { provider: '' }, services: [],
      mode: { listen: 'meeting', speak: 'off', revision: 3, name: 'meeting', set: true } } };
  S.openSettings('speech');
  assert(byId('sp-voice-mode'), 'the Voice mode card is not on the speech page');
  assert(byId('sp-mode-listen').value === 'meeting' && byId('sp-mode-speak').value === 'off', 'the pair in force is not drawn: ' +
    byId('sp-mode-listen').value + '/' + byId('sp-mode-speak').value);
  const rb = byId('sp-mode-readback').textContent;
  assert(rb.indexOf('meeting') === 0 && rb.includes('listen: meeting, speak: off') && rb.includes('revision 3'),
    'the readback does not name the mode, the pair and the revision: ' + rb);

  byId('sp-mode-listen').value = 'interactive';
  byId('sp-mode-speak').value = 'on';
  saveConfigSection('speech_mode');
  const sent = sets();
  assert(sent.length === 1 && sent[0]['speech.mode'], 'one config_set with the pair: ' + JSON.stringify(sent));
  const pair = sent[0]['speech.mode'];
  assert(pair.listen === 'interactive' && pair.speak === 'on' && Object.keys(pair).length === 2,
    'the save did not send both halves and nothing else: ' + JSON.stringify(pair));

  // Nothing chosen is said as nothing chosen, and either half can be
  // handed back to the default.
  assert(voiceModeReadback({ listen: 'interactive', speak: 'auto', revision: 0, name: 'interactive', set: false }).includes('nothing chosen'),
    'an unset mode is not named as the default in force: ' + voiceModeReadback({ listen: 'interactive', speak: 'auto', revision: 0, name: 'interactive', set: false }));
  S.config.speech.mode = { listen: 'interactive', speak: 'auto', revision: 0, name: 'interactive', set: false };
  renderSettings();
  assert(byId('sp-mode-speak').value === '', 'automatic is not offered as what it is: ' + byId('sp-mode-speak').value);
});
</script>`

func TestTheVoiceModeCardSendsThePairWhole(t *testing.T) {
	runPageInEngines(t, voiceModeSettingsPage, substrateModules(t))
}

// .
// .
// .
// .
// .
// .
func TestSpeakingObeysTheModesSpeakHalf(t *testing.T) {
	page := micMarkup + `<script type="module">
import { render, wireMic, speak, bindTransport } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(async () => {
  const spoken = [], stopped = [], sent = [];
  Object.defineProperty(window, 'speechSynthesis', {
    value: { speak: u => spoken.push(u.text), cancel: () => stopped.push('cancel') }, configurable: true, writable: true });
  globalThis.SpeechSynthesisUtterance = function (text) { this.text = text; };
  bindTransport(() => {}, m => { sent.push(m); return 'req-' + sent.length; });
  if (!navigator.mediaDevices) Object.defineProperty(navigator, 'mediaDevices', { value: {}, configurable: true });
  navigator.mediaDevices.getUserMedia = async () => ({ getTracks: () => [{ stop() {} }] });
  window.AudioContext = class {
    constructor() { this.sampleRate = 16000; this.destination = {}; this.currentTime = 0; }
    createMediaStreamSource() { return { connect() {} }; }
    createScriptProcessor() { return { connect() {}, disconnect() {}, onaudioprocess: null }; }
    close() {}
  };
  const modes = () => sent.filter(m => m.type === 'config_set' && m.config && m.config['speech.mode'])
    .map(m => m.config['speech.mode']).map(p => p.listen + '/' + p.speak);
  const show = (listen, half, rev) => { S.stats = { voice_state: 'cloud', voice_listen: listen, voice_speak: half, voice_mode_revision: rev }; S.connected = true; render(); };
  const mic = document.getElementById('mic');
  wireMic();

  // AUTO is the older rule, untouched: the browser's own voice reads a
  // reply back to an operator who spoke and stays out of a typed
  // conversation.
  S.voiceSpeak = false;
  show('interactive', '', 1);
  speak('one');
  assert(spoken.length === 0, 'auto read a reply back to an operator who typed: ' + spoken.join());
  S.voiceSpeak = true;
  speak('two');
  assert(spoken.join() === 'two', 'auto did not read a reply back to an operator who spoke: ' + spoken.join());

  // ON is the operator saying it: replies are read however they were
  // written, so the last-input rule no longer silences them.
  S.voiceSpeak = false;
  show('off', 'on', 2);
  speak('three');
  assert(spoken.join() === 'two,three', 'speak=on stayed out of a typed conversation: ' + spoken.join());

  // OFF is silent, whatever the operator last did and whichever route the
  // reply came back by — and what was being spoken stops as it arrives.
  S.voiceSpeak = true;
  const hushes = stopped.length;
  show('off', 'off', 3);
  assert(stopped.length > hushes, 'a mode that does not speak did not silence what was being spoken');
  speak('four');
  speak('five', { route: 'browser' });
  S.stats.reply_voice = 'ElevenLabs';
  speak('six');
  assert(spoken.join() === 'two,three', 'speak=off spoke anyway: ' + spoken.join());
  delete S.stats.reply_voice;

  // A second frame carrying the same off says nothing new: the silence is
  // owed once.
  const quiet = stopped.length;
  show('off', 'off', 4);
  assert(stopped.length === quiet, 'every status frame hushed again: ' + (stopped.length - quiet));

  // And the tap that enters off pays it without waiting for the frame.
  show('off', 'on', 5);
  const before = stopped.length;
  mic.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, cancelable: true }));
  mic.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, cancelable: true }));
  assert(modes().join(' ') === 'off/off', 'the tap in earbuds did not ask for silence: ' + modes().join(' '));
  assert(stopped.length > before, 'the tap into off left a reply speaking until the host answered');
});
</script>`
	runPageInEngines(t, page, micModules)
}
