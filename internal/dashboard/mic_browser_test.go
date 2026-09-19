//go:build !windows

// .
// .

package dashboard

import "testing"

const micMarkup = `<!doctype html>
<div id="voice-transcript" hidden></div>
<button class="converse" id="converse" disabled hidden aria-pressed="false">&#127908;</button>
<button class="mic" id="mic" type="button" hidden aria-pressed="false">&#127908;</button>
<button class="mic-more" id="mic-more" type="button" hidden aria-expanded="false"></button>
<div class="mic-menu" id="mic-menu" hidden><div id="mic-voice" hidden></div><div id="mic-mode" hidden></div><div id="mic-source"></div><div class="mic-inputs" id="mic-inputs" hidden></div><button type="button" role="menuitemradio" aria-checked="false" id="mic-meeting" hidden>Meeting</button><button type="button" id="mic-settings">Speech settings</button></div>
`

var micModules = map[string][]byte{
	"/app.js":   []byte("export function toast(m) { (globalThis.toasts ||= []).push(m); }\n"),
	"/state.js": []byte("export const S = { stats: null, connected: false };\n"),
}

// .
// .
// .
// .
func TestTheMicrophoneIsTheOneItsStateNames(t *testing.T) {
	page := micMarkup + `<script type="module">
import { render, wireMic } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(() => {
  const opened = [];
  S.openSettings = (section, focus) => opened.push(section + '/' + focus);
  const mic = document.getElementById('mic'), conv = document.getElementById('converse');
  const more = document.getElementById('mic-more'), menu = document.getElementById('mic-menu');
  const show = (state, connected, speaks) => { S.stats = { voice_state: state, voice_source: state === 'plugin' ? 'id.test.voice' : 'ElevenLabs · scribe_v2', reply_voice: speaks || '' }; S.connected = connected; render(); };
  wireMic();
  assert(mic.title === 'Voice isn\'t set up — opens Speech settings' && mic.classList.contains('faint'), 'before the host says anything the microphone offers setup: ' + mic.outerHTML);

  show('plugin', true);
  assert(!conv.hidden && mic.hidden, 'a voice plugin is the conversation microphone, and push-to-talk leaves');
  assert(!more.hidden && document.getElementById('mic-source').textContent === 'Voice plugin — id.test.voice', 'the chevron names the plugin: ' + document.getElementById('mic-source').textContent);

  show('cloud', true);
  assert(conv.hidden && !mic.hidden && !mic.disabled && !mic.classList.contains('faint'), 'cloud speech is push-to-talk: ' + mic.outerHTML);
  assert(mic.title === 'Interactive — tap to talk, or hold', 'push-to-talk names its mode and says how: ' + mic.title);
  assert(!more.hidden && document.getElementById('mic-source').textContent === 'Cloud speech — ElevenLabs · scribe_v2', 'the chevron names the service');
  show('cloud', false);
  assert(mic.disabled, 'push-to-talk waits for the connection');

  // THE MENU IS VOICE, BOTH HALVES OF IT: an identity that speaks its
  // replies still has a voice to name and a way into its settings, even
  // with no microphone set up at all.
  show('setup', true, 'ElevenLabs');
  assert(!more.hidden, 'a speaking identity with no microphone had no way into its voice settings');
  assert(document.getElementById('mic-voice').textContent === 'Replies spoken by ElevenLabs',
    'the voice that speaks replies is not named: ' + document.getElementById('mic-voice').outerHTML);
  assert(document.getElementById('mic-source').hidden, 'a microphone that is not set up was named as one');
  assert(mic.title === 'Voice input isn\'t set up — opens Speech settings', 'the microphone said voice was missing beside a voice that speaks: ' + mic.title);
  show('cloud', true, 'ElevenLabs');
  assert(document.getElementById('mic-source').textContent === 'Cloud speech — ElevenLabs · scribe_v2' &&
    !document.getElementById('mic-voice').hidden, 'both halves are named when both are there');

  for (const [state, title] of [['setup', 'Voice isn\'t set up — opens Speech settings'], ['unreachable', 'Voice can\'t reach its service — opens Speech settings']]) {
    show(state, false);
    assert(!mic.hidden && !mic.disabled && mic.classList.contains('faint') && mic.title === title, state + ' is a faint way into settings, even offline: ' + mic.outerHTML);
    assert(more.hidden, state + ' has no chevron: the microphone itself leads to settings');
    opened.length = 0;
    mic.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
    mic.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    assert(opened.join() === 'speech/sp-provider-stt,speech/sp-provider-stt', state + ': a press and Enter open Speech settings: ' + opened.join());
  }

  show('safe', true);
  assert(!mic.hidden && mic.disabled && conv.hidden && more.hidden, 'SAFE pauses every microphone');
  assert(mic.title === 'Voice is paused while this identity is in SAFE', 'and says why: ' + mic.title);

  show('cloud', true);
  opened.length = 0;
  more.click();
  assert(!menu.hidden && more.getAttribute('aria-expanded') === 'true', 'the chevron opens its menu');
  document.getElementById('mic-settings').click();
  assert(opened.join() === 'speech/sp-provider-stt' && menu.hidden, 'Speech settings is one click away, and the menu closes: ' + opened.join());
});
</script>`
	runPageInEngines(t, page, micModules)
}

// .
// .
// .
func TestPushToTalkLatchesOnATapAndSendsOnRelease(t *testing.T) {
	page := micMarkup + `<script type="module">
import { render, wireMic, bindTransport } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(async () => {
  const frames = [];
  bindTransport(f => frames.push(new Uint8Array(f)), () => {});
  let node = null;
  const stream = { getTracks: () => [{ stop() {} }] };
  if (!navigator.mediaDevices) Object.defineProperty(navigator, 'mediaDevices', { value: {}, configurable: true });
  navigator.mediaDevices.getUserMedia = async () => stream;
  window.AudioContext = class {
    constructor() { this.sampleRate = 16000; this.destination = {}; }
    createMediaStreamSource() { return { connect() {} }; }
    createScriptProcessor() { node = { connect() {}, disconnect() {}, onaudioprocess: null }; return node; }
    close() {}
  };
  const tick = () => new Promise(r => setTimeout(r, 0));
  const until = async (ok, what) => { for (let i = 0; i < 200 && !ok(); i++) await tick(); assert(ok(), what); };
  const speak = () => node.onaudioprocess({ inputBuffer: { getChannelData: () => new Float32Array(4800).fill(0.25) } });
  const mic = document.getElementById('mic');
  const press = type => mic.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true }));
  S.stats = { voice_state: 'cloud' }; S.connected = true;
  wireMic(); render();

  // A tap latches.
  node = null;
  press('pointerdown');
  press('pointerup');
  await until(() => node && node.onaudioprocess, 'the tap never opened the microphone');
  assert(mic.classList.contains('live') && mic.getAttribute('aria-pressed') === 'true', 'a tap keeps recording: ' + mic.outerHTML);
  press('pointerleave');
  speak();
  assert(frames.length === 0, 'leaving a latched microphone sent nothing yet');
  press('pointerdown');
  await until(() => frames.length === 1, 'the second press did not send what was said');
  assert(frames[0][2] === 1, 'push-to-talk opens a conversation (mode byte ' + frames[0][2] + ')');
  press('pointerup');
  assert(!mic.classList.contains('live'), 'after sending the microphone is idle');

  // A hold sends on release.
  node = null;
  press('pointerdown');
  await until(() => node && node.onaudioprocess, 'the hold never opened the microphone');
  speak();
  await new Promise(r => setTimeout(r, 450));
  press('pointerup');
  await until(() => frames.length === 2, 'releasing a hold did not send');

  // A hold that leaves the button is abandoned.
  node = null;
  press('pointerdown');
  await until(() => node && node.onaudioprocess, 'the third press never opened the microphone');
  speak();
  await new Promise(r => setTimeout(r, 450));
  press('pointerleave');
  press('pointerup');
  await tick(); await tick();
  assert(frames.length === 2 && !mic.classList.contains('live'), 'a hold that left the button sent anyway, or kept recording');

  // Space starts, and Space sends.
  node = null;
  mic.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true, cancelable: true }));
  await until(() => node && node.onaudioprocess, 'Space never opened the microphone');
  speak();
  mic.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true, cancelable: true }));
  await until(() => frames.length === 3, 'the second Space did not send');
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
// .
// .
// .
// .
// .
func TestTheMicrophoneMenuNamesTheElectionAndTakesAChoice(t *testing.T) {
	page := micMarkup + `<script type="module">
import { render, wireMic, bindTransport } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(async () => {
  try { localStorage.removeItem('aii.mic'); } catch (e) {  }
  const asked = [];           // the deviceId each getUserMedia asked for, '' = the browser's own
  let devices = [
    { kind: 'audioinput', deviceId: 'default', label: 'Default - Built-in Microphone' },
    { kind: 'audioinput', deviceId: 'builtin', label: 'Built-in Microphone' },
    { kind: 'audiooutput', deviceId: 'spk', label: 'Speakers' },
  ];
  let ended = null;           // the live track's own 'ended' listener
  const want = c => (c && c.audio && c.audio.deviceId && c.audio.deviceId.exact) || '';
  if (!navigator.mediaDevices || !navigator.mediaDevices.addEventListener) {
    Object.defineProperty(navigator, 'mediaDevices', { value: new EventTarget(), configurable: true });
  }
  navigator.mediaDevices.enumerateDevices = async () => devices.slice();
  navigator.mediaDevices.getUserMedia = async c => {
    const id = want(c);
    asked.push(id);
    if (id && !devices.some(d => d.deviceId === id)) { const e = new Error('no such device'); e.name = 'OverconstrainedError'; throw e; }
    const d = devices.find(x => x.deviceId === (id || 'builtin'));
    const track = { label: d ? d.label : '', stop() {}, getSettings: () => ({}), addEventListener(t, fn) { if (t === 'ended') ended = fn; } };
    return { getAudioTracks: () => [track], getTracks: () => [track] };
  };
  let node = null;
  window.AudioContext = class {
    constructor() { this.sampleRate = 16000; this.destination = {}; }
    createMediaStreamSource() { return { connect() {} }; }
    createScriptProcessor() { node = { connect() {}, disconnect() {}, onaudioprocess: null }; return node; }
    close() {}
  };
  bindTransport(() => {}, () => {});
  const tick = () => new Promise(r => setTimeout(r, 0));
  const until = async (ok, what) => { for (let i = 0; i < 200 && !ok(); i++) await tick(); assert(ok(), what); };
  const mic = document.getElementById('mic'), more = document.getElementById('mic-more');
  const list = document.getElementById('mic-inputs');
  const press = type => mic.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true }));
  const rows = () => Array.from(list.querySelectorAll('button')).map(b => b.textContent);
  const rowFor = text => Array.from(list.querySelectorAll('button')).find(b => b.textContent.indexOf(text) === 0);
  const talk = async () => { const n = asked.length; press('pointerdown'); press('pointerup'); await until(() => asked.length > n, 'the microphone never opened'); await until(() => node && node.onaudioprocess, 'the capture graph never opened'); };
  const done = async () => { press('pointerdown'); press('pointerup'); await tick(); await tick(); };

  S.stats = { voice_state: 'cloud', voice_source: 'ElevenLabs' }; S.connected = true;
  wireMic(); render();
  more.click();

  // 1. The election is named and chosen: the browser's own default row
  //    carries the name the browser gives it, and the real devices are
  //    listed beside it — outputs are not.
  await until(() => rows().length >= 2, 'the menu never listed the inputs: ' + list.innerHTML);
  assert(!list.hidden, 'the input list is hidden beside a working microphone');
  const def = rowFor('Browser default');
  assert(def && def.getAttribute('aria-checked') === 'true', 'the browser\'s election is not the chosen row: ' + rows().join(' / '));
  assert(def.textContent === 'Browser default — Built-in Microphone', 'the election is not named: ' + def.textContent);
  assert(rowFor('Built-in Microphone') !== undefined, 'a real input is missing: ' + rows().join(' / '));
  assert(!rows().some(r => r.indexOf('Speakers') === 0), 'an output device was offered as a microphone: ' + rows().join(' / '));

  // 2. With nothing pinned the page asks for NO device: the browser
  //    elects, exactly as it did before this control existed.
  await talk();
  assert(asked[0] === '', 'the page overrode the browser\'s election: asked for ' + JSON.stringify(asked[0]));
  await until(() => (list.textContent || '').indexOf('Listening with Built-in Microphone') >= 0,
    'the device that is listening is not named: ' + list.textContent);

  // 3. A device taken away ends the capture and says so — it does not
  //    go on streaming silence.
  assert(typeof ended === 'function', 'nothing watched the live track');
  ended();
  await until(() => !mic.classList.contains('live'), 'the capture outlived its device');
  assert((globalThis.toasts || []).some(m => m.indexOf('was disconnected') > 0), 'the disconnection was silent: ' + JSON.stringify(globalThis.toasts));

  // 4. A microphone plugged in while the page is open appears.
  devices = devices.concat([{ kind: 'audioinput', deviceId: 'webcam', label: 'Webcam Microphone' }]);
  navigator.mediaDevices.dispatchEvent(new Event('devicechange'));
  await until(() => rowFor('Webcam Microphone') !== undefined, 'a microphone attached while the page was open never appeared: ' + rows().join(' / '));

  // 5. Choosing it pins it for THIS BROWSER, and the next capture asks
  //    for exactly that device.
  rowFor('Webcam Microphone').click();
  await until(() => rowFor('Webcam Microphone') && rowFor('Webcam Microphone').getAttribute('aria-checked') === 'true', 'the choice was not marked: ' + list.innerHTML);
  assert(JSON.parse(localStorage.getItem('aii.mic')).id === 'webcam', 'the choice did not survive as this browser\'s: ' + localStorage.getItem('aii.mic'));
  assert(rowFor('Browser default').getAttribute('aria-checked') === 'false', 'the election is still marked chosen beside a pin');
  await talk();
  assert(asked[1] === 'webcam', 'the pinned device was not asked for: ' + JSON.stringify(asked));
  await done();

  // 6. A pin whose device is gone falls back to the election, out loud,
  //    and keeps the pin for when it returns.
  devices = devices.filter(d => d.deviceId !== 'webcam');
  navigator.mediaDevices.dispatchEvent(new Event('devicechange'));
  await talk();
  assert(asked[2] === 'webcam' && asked[3] === '', 'an absent pin did not fall back to the browser: ' + JSON.stringify(asked));
  assert((globalThis.toasts || []).some(m => m.indexOf('is not connected') > 0), 'the fallback was silent: ' + JSON.stringify(globalThis.toasts));
  assert(JSON.parse(localStorage.getItem('aii.mic')).id === 'webcam', 'an absent device dropped the operator\'s choice');
  await until(() => rowFor('Webcam Microphone') !== undefined, 'the pinned device is not shown while it is away: ' + rows().join(' / '));
  assert(rowFor('Webcam Microphone').textContent.indexOf('not connected') > 0 && rowFor('Webcam Microphone').disabled,
    'an absent pin is not named as absent: ' + rowFor('Webcam Microphone').textContent);
  await done();

  // 7. Browser default gives the election back.
  rowFor('Browser default').click();
  assert(localStorage.getItem('aii.mic') === null, 'the pin outlived the choice to drop it');
  await talk();
  assert(asked[4] === '', 'after choosing the browser default the page still asked for a device: ' + JSON.stringify(asked));
  await done();
  try { localStorage.removeItem('aii.mic'); } catch (e) {  }
});
</script>`
	runPageInEngines(t, page, micModules)
}
