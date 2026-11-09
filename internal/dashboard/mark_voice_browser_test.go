//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

// THEIR MARK FOLLOWS THE VOICE ITSELF. What the mark's light reads
// (S.voiceNow) is the voice's own facts, driven here through the voice's
// own controls and entry points, and the voice asks for the mark to be
// drawn again (S.renderMarks) whenever one changes. Push-to-talk listens
// only once the microphone is capturing: not while permission is asked,
// not after it is refused, not after the press is released. A conversation
// listens while its input flows, and not after Finish input, while its
// session stays. The page plays a conversation's reply: speaking, after
// Finish input too, until the audio has played out or is hushed. The
// browser's own voice reads a reply: speaking, until it ends or is hushed.
// (TestMarkIsTheirs draws the mark from these facts on the real page.)
func TestTheMarkFollowsTheVoiceItself(t *testing.T) {
	page := micMarkup + `<button id="hush-speaking" hidden></button><script type="module">
import { render, wireMic, bindTransport, startDuplex, finishInput, residentActive, converseForTest, encodeStreamFrame, speak, hush, abortDuplex } from './voice.js';
import { S } from './state.js';
import { assert, run, until } from './__harness.js';
run(async () => {
  let draws = 0;
  S.renderMarks = () => { draws++; };
  const now = () => S.voiceNow();
  const host = bindTransport(() => {}, () => 'req');
  // A real track for the microphone, made before the page's audio is faked.
  const real = new (window.AudioContext || window.webkitAudioContext)();
  const mic = real.createMediaStreamDestination().stream;
  if (!navigator.mediaDevices) Object.defineProperty(navigator, 'mediaDevices', { value: {}, configurable: true });
  const sources = [];
  window.AudioContext = class {
    constructor(o) { this.sampleRate = (o && o.sampleRate) || 48000; this.destination = {}; this.currentTime = 0; this.state = 'running'; }
    createMediaStreamSource() { return { connect() {}, disconnect() {} }; }
    createScriptProcessor() { return { connect() {}, disconnect() {}, onaudioprocess: null }; }
    createBuffer(c, n, r) { const d = Array.from({ length: c }, () => new Float32Array(n)); return { duration: n / r, getChannelData: i => d[i] }; }
    createBufferSource() { const s = { ctx: this, connect() {}, start() {}, stop() {}, disconnect() {}, onended: null, buffer: null }; sources.push(s); return s; }
    resume() { return Promise.resolve(); }
    suspend() { return Promise.resolve(); }
    close() { return Promise.resolve(); }
  };
  const playedOut = () => { for (const s of sources.splice(0)) { s.ctx.currentTime += 60; if (s.onended) s.onended(); } };
  let stream = 0;
  const reply = () => host.receiveFrame(encodeStreamFrame(48000, 1, 1, 1, 0, new Int16Array(4800).fill(900), ++stream));
  const button = document.getElementById('mic');
  const press = type => button.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true }));
  // waits by design: a press becomes a hold only by outlasting the latch, so the duration is the gesture.
  const hold = () => new Promise(r => setTimeout(r, 450));
  S.stats = { voice_state: 'cloud' }; S.connected = true;
  wireMic(); render();

  // PUSH-TO-TALK, PERMISSION ASKED AND REFUSED.
  let asked = null;
  navigator.mediaDevices.getUserMedia = () => new Promise((ok, no) => { asked = { ok, no }; });
  press('pointerdown');
  await until(() => asked, 2000);
  assert(asked && !now().listening, 'the mark listens while permission is still being asked');
  asked.no(new DOMException('denied', 'NotAllowedError'));
  await new Promise(r => setTimeout(r, 0)); await new Promise(r => setTimeout(r, 0));
  assert(!now().listening, 'the mark listens after the microphone was refused');
  await hold(); press('pointerup');

  // PUSH-TO-TALK, GRANTED: listening while it captures, and not after.
  navigator.mediaDevices.getUserMedia = async () => mic;
  let d = draws;
  press('pointerdown');
  await until(() => now().listening, 2000);
  assert(now().listening && draws > d, 'a capturing push-to-talk is not listening, or the mark was not asked to draw: ' + JSON.stringify(now()) + ' ' + (draws - d));
  await hold();
  d = draws;
  press('pointerup');
  await until(() => !now().listening, 2000);
  assert(!now().listening && draws > d, 'a released push-to-talk still listens, or the mark was not asked to draw');

  // A KEY TAP LATCHES BEFORE THE MICROPHONE IS GRANTED: not listening while
  // it is asked for, nor once it is refused with the latch still set.
  asked = null;
  navigator.mediaDevices.getUserMedia = () => new Promise((ok, no) => { asked = { ok, no }; });
  const key = () => button.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true, cancelable: true }));
  key();
  await until(() => asked, 2000);
  assert(asked && !now().listening, 'a latched tap listens while permission is still being asked');
  asked.no(new DOMException('denied', 'NotAllowedError'));
  await new Promise(r => setTimeout(r, 0)); await new Promise(r => setTimeout(r, 0));
  assert(!now().listening, 'a latched tap listens after the microphone was refused');
  key(); // the next tap lets the latch go
  navigator.mediaDevices.getUserMedia = async () => mic;

  // A CONVERSATION: listening while its input flows.
  S.stats = { voice_engine: true, voice_state: 'plugin' }; render();
  converseForTest('waiting');
  d = draws;
  await startDuplex('interactive');
  host.sessionState({ state: 'open', session_id: 'vs-mark' });
  assert(residentActive() && now().listening && draws > d, 'a conversation\'s open microphone is not listening: ' + JSON.stringify(now()));

  // Its reply, through the page's playback: speaking, until it has played out.
  d = draws;
  reply();
  assert(now().speaking && draws > d, 'a conversation\'s reply playing is not speaking, or the mark was not asked to draw: ' + JSON.stringify(now()) + ', ' + sources.length + ' sources');
  d = draws;
  playedOut();
  assert(!now().speaking && draws > d, 'a reply that played out still speaks, or the mark was not asked to draw');

  // Finish input: not listening, the session stays, and its reply still speaks.
  d = draws;
  await finishInput();
  assert(!now().listening && residentActive() && draws > d, 'after Finish input the mark still listens, or the session ended: ' + JSON.stringify(now()));
  reply();
  assert(now().speaking, 'a reply after Finish input is not speaking');
  d = draws;
  hush();
  assert(!now().speaking && draws > d, 'a hushed reply still speaks, or the mark was not asked to draw');
  abortDuplex();

  // THE BROWSER'S OWN VOICE reads a reply: speaking, until it ends or is hushed.
  const synth = { speaking: false, u: null,
    speak(u) { this.u = u; this.speaking = true; if (u.onstart) u.onstart(); },
    cancel() { const u = this.u; this.u = null; if (this.speaking) { this.speaking = false; if (u && u.onend) u.onend(); } } };
  Object.defineProperty(window, 'speechSynthesis', { configurable: true, value: synth });
  window.SpeechSynthesisUtterance = class { constructor(text) { this.text = text; } };
  S.stats = { voice_state: 'setup' }; S.voiceSpeak = true; render();
  d = draws;
  speak('The grant letter is drafted.', { route: 'browser' });
  assert(now().speaking && draws > d, 'the browser\'s own voice reading a reply is not speaking: ' + JSON.stringify(now()));
  d = draws;
  synth.speaking = false; synth.u.onend();
  assert(!now().speaking && draws > d, 'the browser\'s voice ended and the mark still speaks, or was not asked to draw');
  speak('And the summary.', { route: 'browser' });
  assert(now().speaking, 'the second reply is not speaking');
  d = draws;
  hush();
  assert(!now().speaking && draws > d, 'a hushed browser voice still speaks, or the mark was not asked to draw');
});
</script>`
	modules := map[string][]byte{
		"/say.js": []byte("export function whenSpeaking(fn) { globalThis.cloudSpeaking = fn; }\n" +
			"export function spokenAudio() { return Promise.resolve(); }\n" +
			"export function hushSpoken() {}\n"),
	}
	for k, v := range micModules {
		modules[k] = v
	}
	runPageInEngines(t, page, modules)
}
