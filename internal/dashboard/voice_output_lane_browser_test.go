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
// .
// .
// .
// .
func TestEarbudsWithAnEngineHoldsAnOutputLaneAndNeverAsksForAMicrophone(t *testing.T) {
	page := micMarkup + `<button id="hush-speaking" hidden></button><script type="module">
import { render, wireMic, bindTransport, sessionState, receiveFrame, encodeStreamFrame, speak, hush, playbackForTest, speakerLaneForTest, connectionLost } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(async () => {
  const sent = [];
  bindTransport(() => {}, m => { sent.push(m); return 'req-' + sent.length; });
  let asked = 0;
  if (!navigator.mediaDevices) Object.defineProperty(navigator, 'mediaDevices', { value: {}, configurable: true });
  navigator.mediaDevices.getUserMedia = async () => { asked++; throw new DOMException('denied', 'NotAllowedError'); };
  let spoken = 0;
  if (window.speechSynthesis) window.speechSynthesis.speak = () => { spoken++; };
  window.AudioContext = class {
    constructor(o) { this.sampleRate = (o && o.sampleRate) || 48000; this.destination = {}; this.currentTime = 0; this.state = 'running'; }
    createBuffer(c, n, r) { const d = Array.from({ length: c }, () => new Float32Array(n)); return { duration: n / r, getChannelData: i => d[i] }; }
    createBufferSource() { return { connect() {}, start() {}, stop() {}, disconnect() {}, addEventListener() {}, onended: null, buffer: null }; }
    resume() { return Promise.resolve(); }
    close() {}
  };
  const opens = () => sent.filter(m => m.type === 'voice_session' && m.voice.action === 'open');
  const acts = a => sent.filter(m => m.type === 'voice_session' && m.voice.action === a).length;
  const show = (listen, speak, rev, more) => {
    S.stats = Object.assign({ voice_state: 'plugin', voice_engine: true, voice_listen: listen, voice_speak: speak, voice_mode_revision: rev }, more || {});
    S.connected = true; render();
  };
  wireMic();

  // Interactive: no output lane — a conversation is the operator's to open.
  show('interactive', 'on', 1);
  assert(opens().length === 0 && !speakerLaneForTest(), 'an output lane was opened outside earbuds');

  // EARBUDS: the lane opens by itself, as an OUTPUT lane, and no device is asked for.
  show('off', 'on', 2);
  assert(opens().length === 1, 'earbuds with an engine did not open a lane: ' + JSON.stringify(sent));
  const open = opens()[0].voice;
  assert(open.mode === 'output' && open.channels === 1 && open.rate >= 8000, 'the lane was not opened as an output lane: ' + JSON.stringify(open));
  assert(asked === 0, 'the page asked the browser for a microphone to open a lane that has none');
  show('off', 'on', 2);
  assert(opens().length === 1, 'every status frame opened another lane');
  sessionState({ state: 'open', session_id: 'vs-7' });
  assert(speakerLaneForTest() && speakerLaneForTest().isOpen, 'the output lane did not open');

  // THE ENGINE SPEAKS THE REPLY; nothing else does. It arrives with the
  // engine's provenance, its audio arrives on the lane, and the stop
  // control is there while it plays.
  speak('the answer to what was typed', { route: 'plugin', synthesis_id: 'vs-7-syn-1', session_id: 'vs-7' });
  assert(spoken === 0, 'the browser\'s own voice read a reply the engine is speaking');
  const pcm = new Int16Array(4800).fill(1200);
  receiveFrame(encodeStreamFrame(48000, 1, 1, 1, 0, pcm, 1));
  assert(playbackForTest().stats().received === 4800, 'the engine\'s audio was not played off the lane: ' + JSON.stringify(playbackForTest().stats()));
  assert(document.getElementById('hush-speaking').hidden === false, 'there is no way to stop the engine\'s voice in a mode with no microphone');
  hush();
  assert(acts('interrupt') === 1, 'the stop control did not reach the engine as an interruption');
  assert(asked === 0, 'a microphone was asked for along the way');

  // OFF: silence is owed now — the lane is aborted, not drained.
  show('off', 'off', 3);
  assert(acts('abort') === 1 && !speakerLaneForTest(), 'speak off did not retire the output lane: ' + JSON.stringify(sent.slice(-3)));
  sessionState({ state: 'closed', session_id: 'vs-7' });
  assert(opens().length === 1, 'a lane was reopened in a mode that does not speak');

  // Back to earbuds: a new lane, and again no microphone.
  show('off', 'on', 4);
  assert(opens().length === 2 && opens()[1].voice.mode === 'output', 'earbuds did not come back with its lane');

  // A REFUSED OUTPUT LANE IS NOT THE OPERATOR'S PROBLEM: no toast, and no
  // hammering — another page may be holding the engine's one session.
  globalThis.toasts = [];
  sessionState({ state: 'refused', reason: 'session vs-3 is still open' });
  assert((globalThis.toasts || []).length === 0, 'a refused output lane was reported to the operator: ' + (globalThis.toasts || []).join(' | '));
  show('off', 'on', 4); show('off', 'on', 4);
  assert(opens().length === 2, 'a refused output lane was asked for again at once');

  // A lost connection takes the lane with it; the next one starts clean.
  connectionLost();
  assert(!speakerLaneForTest(), 'a lane survived its connection');
  assert(asked === 0, 'a microphone was asked for');
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
func TestAHoldInEarbudsBorrowsTheEngineFromTheOutputLaneAndGivesItBack(t *testing.T) {
	page := micMarkup + `<button id="hush-speaking" hidden></button><script type="module">
import { render, wireMic, bindTransport, sessionState, speakerLaneForTest } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(async () => {
  const sent = [], frames = [];
  bindTransport(f => frames.push(new Uint8Array(f)), m => { sent.push(m); return 'req-' + sent.length; });
  let asked = 0, node = null;
  const stream = { getTracks: () => [{ stop() {} }], getAudioTracks: () => [{ label: 'test', stop() {}, getSettings: () => ({}), addEventListener() {} }] };
  if (!navigator.mediaDevices) Object.defineProperty(navigator, 'mediaDevices', { value: {}, configurable: true });
  navigator.mediaDevices.getUserMedia = async () => { asked++; return stream; };
  window.AudioContext = class {
    constructor() { this.sampleRate = 16000; this.destination = {}; this.currentTime = 0; this.state = 'running'; }
    createMediaStreamSource() { return { connect() {} }; }
    createScriptProcessor() { node = { connect() {}, disconnect() {}, onaudioprocess: null }; return node; }
    createBuffer(c, n, r) { const d = Array.from({ length: c }, () => new Float32Array(n)); return { duration: n / r, getChannelData: i => d[i] }; }
    createBufferSource() { return { connect() {}, start() {}, stop() {}, disconnect() {}, addEventListener() {}, onended: null, buffer: null }; }
    resume() { return Promise.resolve(); }
    close() {}
  };
  const tick = () => new Promise(r => setTimeout(r, 0));
  const until = async (ok, what) => { for (let i = 0; i < 400 && !ok(); i++) await tick(); assert(ok(), what); };
  const talk = () => node.onaudioprocess({ inputBuffer: { getChannelData: () => new Float32Array(4800).fill(0.25) } });
  const voice = () => sent.filter(m => m.type === 'voice_session').map(m => m.voice.action + (m.voice.mode ? ':' + m.voice.mode : ''));
  const count = v => voice().filter(x => x === v).length;
  const mic = document.getElementById('mic');
  const press = type => mic.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true }));
  const show = rev => {
    S.stats = { voice_state: 'cloud', voice_engine: true, voice_listen: 'off', voice_speak: 'on', voice_mode_revision: rev };
    S.connected = true; render();
  };
  wireMic();

  show(1);
  sessionState({ state: 'open', session_id: 'vs-1' });
  assert(voice().join(' ') === 'open:output' && asked === 0, 'earbuds did not hold its output lane: ' + voice().join(' '));

  // THE HOLD. The output lane is retired; the hold's lane is NOT asked for
  // while the engine still holds the output session.
  node = null;
  press('pointerdown');
  await until(() => node && node.onaudioprocess, 'the hold never opened the microphone');
  talk();
  await until(() => count('abort') === 1, 'the hold did not retire the output lane: ' + voice().join(' '));
  assert(!speakerLaneForTest(), 'the output lane is still held during a hold');
  assert(voice().filter(v => v.indexOf('open:') === 0).length === 1,
    'the hold\'s lane was asked for while the engine still held the output session: ' + voice().join(' '));
  const before = frames.length;

  // THE ACKNOWLEDGEMENT opens the hold's lane, and what was said while it
  // waited is sent, not lost.
  sessionState({ state: 'closed', session_id: 'vs-1' });
  await until(() => count('open:conversation') === 1, 'the hold\'s lane never opened after the close was acknowledged: ' + voice().join(' '));
  assert(count('open:output') === 1, 'the output lane came back while the operator was still holding: ' + voice().join(' '));
  sessionState({ state: 'open', session_id: 'vs-2' });
  await until(() => frames.length > before, 'what was said while the lane waited was lost');

  // RELEASE: the hold finishes its input and the HOST closes that session
  // when its reply has been given — until then the engine is still the
  // hold's, and no output lane is asked for beside it.
  await new Promise(r => setTimeout(r, 450));
  const sentBefore = frames.length;
  press('pointerup');
  await until(() => frames.length > sentBefore, 'the release never finished the hold\'s input');
  render();
  assert(count('open:output') === 1, 'an output lane was asked for while the hold\'s session was still open: ' + voice().join(' '));
  sessionState({ state: 'closed', session_id: 'vs-2' });
  await until(() => count('open:output') === 2, 'earbuds did not get its output lane back after the hold: ' + voice().join(' '));
  assert(asked === 1, 'the microphone was asked for ' + asked + ' times; the hold is the only thing here that has one');
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
func TestANewReplysStartRetiresWhatThePageStillHoldsOfTheOldOne(t *testing.T) {
	page := micMarkup + `<button id="hush-speaking" hidden></button><script type="module">
import { render, wireMic, bindTransport, sessionState, receiveFrame, voiceEvent, encodeStreamFrame, playbackForTest, connectionLost } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(async () => {
  const sent = [], sources = [];
  bindTransport(() => {}, m => { sent.push(m); return 'r' + sent.length; });
  window.AudioContext = class {
    constructor(o) { this.sampleRate = (o && o.sampleRate) || 48000; this.destination = {}; this.currentTime = 0; this.state = 'running'; }
    createBuffer(c, n, r) { const d = Array.from({ length: c }, () => new Float32Array(n)); return { duration: n / r, getChannelData: i => d[i] }; }
    createBufferSource() { const s = { connect() {}, start() {}, stop() { this.stopped = true; }, disconnect() {}, addEventListener() {}, onended: null, stopped: false }; sources.push(s); return s; }
    resume() { return Promise.resolve(); }
    close() {}
  };
  S.stats = { voice_engine: true, voice_state: 'plugin', voice_listen: 'off', voice_speak: 'on', voice_mode_revision: 1 };
  S.connected = true;
  wireMic(); render();
  sessionState({ state: 'open', session_id: 'vs-9' });
  const reports = () => sent.filter(m => m.type === 'voice_session' && m.voice.action === 'playback').map(m => m.voice.playback);

  // The first reply: named, then a second of audio and its END. Nothing
  // has played — the clock never moves in this double.
  voiceEvent({ type: 'synthesis_start', session_id: 'vs-9', stream: 1 });
  receiveFrame(encodeStreamFrame(48000, 1, 1, 1, 0, new Int16Array(48000), 1));
  receiveFrame(encodeStreamFrame(48000, 1, 3, 2, 48000, null, 1));
  assert(sources.length === 1 && !sources[0].stopped, 'fixture: the first reply is not queued');
  assert(reports().filter(r => r.terminal).length === 0, 'the first reply was reported terminal before anything replaced it');

  // The second reply begins. What the page still holds of the first is
  // retired and reported stopped, and the new audio plays.
  voiceEvent({ type: 'synthesis_start', session_id: 'vs-9', stream: 2 });
  assert(sources[0].stopped, 'the old reply kept its place in the queue under the new one');
  const term = reports().filter(r => r.terminal && r.stream === 1);
  assert(term.length === 1 && term[0].outcome === 'stopped' && term[0].rendered === 0,
    'the retired stream owes the engine a terminal receipt saying what was heard: ' + JSON.stringify(reports()));
  receiveFrame(encodeStreamFrame(48000, 1, 1, 1, 0, new Int16Array(4800), 2));
  assert(sources.length === 2 && !sources[1].stopped, 'the new reply did not play');

  // A late frame of the retired stream is not revived, and the retired
  // stream is not reported twice.
  receiveFrame(encodeStreamFrame(48000, 1, 1, 3, 48000, new Int16Array(4800), 1));
  assert(sources.length === 2, 'a frame of the retired reply was scheduled after its stop');
  assert(reports().filter(r => r.terminal && r.stream === 1).length === 1,
    'the retired stream was reported terminal twice: ' + JSON.stringify(reports()));
  S.connected = false; connectionLost();
});
</script>`
	runPageInEngines(t, page, micModules)
}
