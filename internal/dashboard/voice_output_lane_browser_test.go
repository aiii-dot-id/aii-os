//go:build !windows

package dashboard

import "testing"

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

  show('interactive', 'on', 1);
  assert(opens().length === 0 && !speakerLaneForTest(), 'an output lane was opened outside earbuds');

  show('off', 'on', 2);
  assert(opens().length === 1, 'earbuds with an engine did not open a lane: ' + JSON.stringify(sent));
  const open = opens()[0].voice;
  assert(open.mode === 'output' && open.channels === 1 && open.rate >= 8000, 'the lane was not opened as an output lane: ' + JSON.stringify(open));
  assert(asked === 0, 'the page asked the browser for a microphone to open a lane that has none');
  show('off', 'on', 2);
  assert(opens().length === 1, 'every status frame opened another lane');
  sessionState({ state: 'open', session_id: 'vs-7' });
  assert(speakerLaneForTest() && speakerLaneForTest().isOpen, 'the output lane did not open');

  speak('the answer to what was typed', { route: 'plugin', synthesis_id: 'vs-7-syn-1', session_id: 'vs-7' });
  assert(spoken === 0, 'the browser\'s own voice read a reply the engine is speaking');
  const pcm = new Int16Array(4800).fill(1200);
  receiveFrame(encodeStreamFrame(48000, 1, 1, 1, 0, pcm, 1));
  assert(playbackForTest().stats().received === 4800, 'the engine\'s audio was not played off the lane: ' + JSON.stringify(playbackForTest().stats()));
  assert(document.getElementById('hush-speaking').hidden === false, 'there is no way to stop the engine\'s voice in a mode with no microphone');
  hush();
  assert(acts('interrupt') === 1, 'the stop control did not reach the engine as an interruption');
  assert(asked === 0, 'a microphone was asked for along the way');

  show('off', 'off', 3);
  assert(acts('abort') === 1 && !speakerLaneForTest(), 'speak off did not retire the output lane: ' + JSON.stringify(sent.slice(-3)));
  sessionState({ state: 'closed', session_id: 'vs-7' });
  assert(opens().length === 1, 'a lane was reopened in a mode that does not speak');

  show('off', 'on', 4);
  assert(opens().length === 2 && opens()[1].voice.mode === 'output', 'earbuds did not come back with its lane');

  globalThis.toasts = [];
  sessionState({ state: 'refused', reason: 'session vs-3 is still open' });
  assert((globalThis.toasts || []).length === 0, 'a refused output lane was reported to the operator: ' + (globalThis.toasts || []).join(' | '));
  show('off', 'on', 4); show('off', 'on', 4);
  assert(opens().length === 2, 'a refused output lane was asked for again at once');

  connectionLost();
  assert(!speakerLaneForTest(), 'a lane survived its connection');
  assert(asked === 0, 'a microphone was asked for');
});
</script>`
	runPageInEngines(t, page, micModules)
}

func TestConversationWaitsForTheOldPlaybackRouteBeforeOpeningMicrophone(t *testing.T) {
	page := micMarkup + `<script type="module">
import { render, wireMic, bindTransport, playbackForTest, sessionState, Playback, encodeStreamFrame, lanesForTest } from './voice.js';
import { S } from './state.js';
import { assert, run, until } from './__harness.js';
run(async () => {
  const order = [], sent = [];
  bindTransport(() => {}, m => { sent.push(m); return 'req-' + sent.length; });
  let finishClose, finishMic;
  const micTrack = {
    label: 'test', stop() {}, addEventListener() {},
    getCapabilities: () => ({ echoCancellation: [true, false, 'all'] }),
    applyConstraints: async c => { assert(c.echoCancellation.exact === 'all', 'full echo cancellation was not requested'); order.push('aec-all'); },
    getSettings: () => ({ echoCancellation: 'all' }),
  };
  const stream = { getTracks: () => [micTrack], getAudioTracks: () => [micTrack] };
  if (!navigator.mediaDevices) Object.defineProperty(navigator, 'mediaDevices', { value: {}, configurable: true });
  navigator.mediaDevices.getUserMedia = () => {
    order.push('microphone');
    return new Promise(resolve => { finishMic = () => resolve(stream); });
  };
  window.AudioContext = class {
    constructor() { order.push('context'); this.sampleRate = 48000; this.destination = {}; this.currentTime = 0; this.state = 'running'; }
    close() { order.push('close-begun'); return new Promise(resolve => { finishClose = () => { order.push('close-finished'); resolve(); }; }); }
    createMediaStreamSource() { return { connect() {} }; }
    createScriptProcessor() { return { connect() {}, disconnect() {}, onaudioprocess: null }; }
  };
  S.stats = { voice_state: 'plugin', voice_engine: true, voice_listen: 'off', voice_speak: 'on', voice_mode_revision: 1 };
  S.connected = true;
  wireMic(); render();
  sessionState({ state: 'open', session_id: 'vs-output' });
  assert(playbackForTest().prime(48000), 'the earbuds playback route did not open');
  S.stats = { voice_state: 'plugin', voice_engine: true, voice_listen: 'off', voice_speak: 'off', voice_mode_revision: 2 };
  render();
  document.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
  assert(order.join(' ') === 'context', 'a stale earbuds wake reopened playback after its lane ended: ' + order.join(' '));
  document.getElementById('converse').click();
  assert(order.join(' ') === 'context close-begun', 'the microphone opened before the old playback route closed: ' + order.join(' '));
  assert(!playbackForTest().prime(48000), 'a second output route opened during retirement');
  document.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true }));
  assert(order.join(' ') === 'context close-begun', 'a stale wake recreated playback during retirement: ' + order.join(' '));
  finishClose();
  await until(() => order.includes('microphone'), 'the microphone never opened after playback closed');
  assert(order.slice(0, 3).join() === 'context,close-begun,close-finished' && order[3] === 'microphone',
    'capture-first order was broken: ' + order.join(' '));
  assert(!playbackForTest().prime(48000), 'playback reopened while microphone permission was pending');
  playbackForTest().feed(encodeStreamFrame(48000, 1, 1, 1, 0, new Int16Array(480), 99));
  assert(order.length === 4, 'a late output frame recreated playback before microphone capture: ' + order.join(' '));
  finishMic();
  await until(() => order.includes('aec-all') && order.filter(x => x === 'context').length === 3 && lanesForTest().length === 2,
    'capture permission did not resume the conversation');
  assert(order[4] === 'aec-all' && order[5] === 'context' && order[6] === 'context' && lanesForTest().length === 2,
    'full echo cancellation and new playback were not ordered after capture: ' + order.join(' '));
  assert(sent.some(m => m.type === 'voice_session' && m.voice.action === 'abort'), 'the output lane was not retired');
  sessionState({ state: 'closed', session_id: 'vs-output' });
  await until(() => sent.some(m => m.type === 'voice_session' && m.voice.action === 'open' && m.voice.mode === 'conversation'),
    'the conversation did not open after the output session retired');
  const opened = sent.find(m => m.type === 'voice_session' && m.voice.action === 'open' && m.voice.mode === 'conversation').voice;
  assert(opened.processing && opened.processing.echo_cancellation === true && opened.processing.tested === undefined,
    'the browser did not report its actual capture setting: ' + JSON.stringify(opened));
  const stuck = new Playback({ audioContext: () => ({ state: 'running', close: () => Promise.reject(new Error('device held')) }) });
  assert(stuck.prime(48000), 'stuck test route did not open');
  let refused = false;
  try { await stuck.release(); } catch (e) { refused = e.message === 'device held'; }
  assert(refused && !stuck.prime(48000), 'failed close silently reopened playback');
});
</script>`
	runPageInEngines(t, page, micModules)
}

func TestAHoldBesideAChosenHearingServiceIsTheServicesAndTheOutputLaneComesBack(t *testing.T) {
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
  const utterances = () => frames.filter(f => f[0] === 1), streamed = () => frames.filter(f => f[0] === 2);
  const mic = document.getElementById('mic');
  const press = type => mic.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true }));
  const show = rev => {
    S.stats = { voice_state: 'cloud', voice_engine: true, voice_listen: 'off', voice_speak: 'on', voice_mode_revision: rev };
    S.connected = true; render();
  };
  wireMic();

  show(1);
  sessionState({ state: 'open', session_id: 'vs-1' });
  assert(voice().join(' ') === 'open:output' && asked === 0, 'earbuds did not hold the engine\'s output lane: ' + voice().join(' '));

  node = null;
  press('pointerdown');
  await until(() => node && node.onaudioprocess, 'the hold never opened the microphone');
  talk();
  await until(() => count('abort') === 1, 'the output lane did not step aside for the hold: ' + voice().join(' '));
  assert(!speakerLaneForTest(), 'the output lane is still held during a hold');
  sessionState({ state: 'closed', session_id: 'vs-1' });
  await tick();
  assert(count('open:conversation') === 0 && streamed().length === 0,
    'the hold\'s words were sent to the engine beside a chosen hearing service: ' + voice().join(' ') + ' / ' + streamed().length + ' lane frames');
  assert(count('open:output') === 1, 'the output lane came back while the operator was still holding: ' + voice().join(' '));

  // waits by design: holding the press past its latch threshold is the gesture under test.
  await new Promise(r => setTimeout(r, 450));
  press('pointerup');
  await until(() => utterances().length === 1, 'the release did not send what was said to the service: ' + frames.length + ' frames');
  assert(utterances()[0].length === 8 + 4800 * 2, 'the utterance does not carry what was said: ' + utterances()[0].length + ' bytes');
  assert(count('open:conversation') === 0 && streamed().length === 0, 'the engine was given the hold\'s words: ' + voice().join(' '));
  await until(() => count('open:output') === 2, 'earbuds did not get its output lane back after the hold: ' + voice().join(' '));
  assert(asked === 1, 'the microphone was asked for ' + asked + ' times; the hold is the only thing here that has one');
});
</script>`
	runPageInEngines(t, page, micModules)
}

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

  voiceEvent({ type: 'synthesis_start', session_id: 'vs-9', stream: 1 });
  receiveFrame(encodeStreamFrame(48000, 1, 1, 1, 0, new Int16Array(48000), 1));
  receiveFrame(encodeStreamFrame(48000, 1, 3, 2, 48000, null, 1));
  assert(sources.length === 1 && !sources[0].stopped, 'fixture: the first reply is not queued');
  assert(reports().filter(r => r.terminal).length === 0, 'the first reply was reported terminal before anything replaced it');

  voiceEvent({ type: 'synthesis_start', session_id: 'vs-9', stream: 2 });
  assert(sources[0].stopped, 'the old reply kept its place in the queue under the new one');
  const term = reports().filter(r => r.terminal && r.stream === 1);
  assert(term.length === 1 && term[0].outcome === 'stopped' && term[0].rendered === 0,
    'the retired stream owes the engine a terminal receipt saying what was heard: ' + JSON.stringify(reports()));
  receiveFrame(encodeStreamFrame(48000, 1, 1, 1, 0, new Int16Array(4800), 2));
  assert(sources.length === 2 && !sources[1].stopped, 'the new reply did not play');

  receiveFrame(encodeStreamFrame(48000, 1, 1, 3, 48000, new Int16Array(4800), 1));
  assert(sources.length === 2, 'a frame of the retired reply was scheduled after its stop');
  assert(reports().filter(r => r.terminal && r.stream === 1).length === 1,
    'the retired stream was reported terminal twice: ' + JSON.stringify(reports()));
  S.connected = false; connectionLost();
});
</script>`
	runPageInEngines(t, page, micModules)
}

func TestDisconnectParksPlaybackAndAReplyWakesTheSameContext(t *testing.T) {
	page := micMarkup + `<script type="module">
import {S} from './state.js';
import {bindTransport,connectionLost,playbackForTest,encodeStreamFrame} from './voice.js';
import {run,until} from './__harness.js';
run(async assert=>{
 bindTransport(()=>{},()=> 'test-request');
 S.connected=false;
 const playback=playbackForTest();let settleSuspend,suspends=0,resumes=0;
 const context={state:'running',currentTime:0,destination:{},
  suspend(){suspends++;return new Promise(resolve=>{settleSuspend=()=>{context.state='suspended';resolve()}})},
  resume(){resumes++;context.state='running';return Promise.resolve()},
  close(){throw Error('disconnect closed the gesture-unlocked context')},
  createBuffer(ch,n,rate){return {duration:n/rate,getChannelData(){return new Float32Array(n)}}},
  createBufferSource(){return {connect(){},start(){},stop(){},onended:null}}
 };
 playback.ctx=context;
 connectionLost();connectionLost();
 assert(suspends===1 && playback.ctx===context,'disconnect did not park exactly one existing context');
 settleSuspend();await until(()=>context.state==='suspended'&&!playback.suspending,'idle speaker did not suspend');
 assert(resumes===0,'a quiet reconnect woke audio without a reply');
 playback.feed(encodeStreamFrame(48000,1,1,1,0,new Int16Array(128),51));
 assert(playback.ctx===context&&context.state==='running'&&resumes===1,'a reply did not reuse and resume the primed context');
 connectionLost();
 playback.feed(encodeStreamFrame(48000,1,1,1,0,new Int16Array(128),52));
 assert(resumes===1,'a frame raced ahead of the pending suspension');
 settleSuspend();await until(()=>resumes===2&&context.state==='running','late suspension silenced the new reply');
  assert(playback.ctx===context,'waking rebuilt the mobile audio context');
  connectionLost();
  playback.feed(encodeStreamFrame(48000,1,1,1,0,new Int16Array(128),53));
  connectionLost(); // this later loss retires the queued wake as well
  settleSuspend();await until(()=>!playback.suspending,'final suspension did not settle');
  assert(resumes===2&&context.state==='suspended','an obsolete reply woke audio after another disconnect');
  playback.stop();
});</script>`
	runPageInEngines(t, page, micModules)
}
