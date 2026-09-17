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
<div class="mic-menu" id="mic-menu" hidden><div id="mic-voice" hidden></div><div id="mic-source"></div><button type="button" id="mic-settings">Speech settings</button></div>
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
  assert(mic.title === 'Talk — tap, or hold', 'push-to-talk says how: ' + mic.title);
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
