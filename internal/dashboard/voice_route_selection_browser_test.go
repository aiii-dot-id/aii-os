//go:build !windows

package dashboard

import "testing"

var routeModules = map[string][]byte{
	"/app.js":   micModules["/app.js"],
	"/state.js": micModules["/state.js"],
	"/say.js": []byte("export function spokenAudio(ask) { (globalThis.said ||= []).push(ask); return Promise.resolve(); }\n" +
		"export function hushSpoken() {}\nexport function whenSpeaking() {}\n"),
}

func TestPushToTalkBesideAChosenHearingServiceNeverReachesTheEngine(t *testing.T) {
	page := micMarkup + `<script type="module">
import { render, wireMic, bindTransport } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(async () => {
  const sent = [], frames = [];
  bindTransport(f => frames.push(new Uint8Array(f)), m => { sent.push(m); return 'req-' + sent.length; });
  let node = null;
  const stream = { getTracks: () => [{ stop() {} }], getAudioTracks: () => [{ label: 'test', stop() {}, getSettings: () => ({}), addEventListener() {} }] };
  if (!navigator.mediaDevices) Object.defineProperty(navigator, 'mediaDevices', { value: {}, configurable: true });
  navigator.mediaDevices.getUserMedia = async () => stream;
  window.AudioContext = class {
    constructor() { this.sampleRate = 16000; this.destination = {}; this.currentTime = 0; this.state = 'running'; }
    createMediaStreamSource() { return { connect() {} }; }
    createScriptProcessor() { node = { connect() {}, disconnect() {}, onaudioprocess: null }; return node; }
    close() {}
  };
  const tick = () => new Promise(r => setTimeout(r, 0));
  const until = async (ok, what) => { for (let i = 0; i < 400 && !ok(); i++) await tick(); assert(ok(), what); };
  const talk = () => node.onaudioprocess({ inputBuffer: { getChannelData: () => new Float32Array(4800).fill(0.25) } });
  const engine = () => sent.filter(m => m.type === 'voice_session').map(m => m.voice.action + (m.voice.mode ? ':' + m.voice.mode : ''));
  const mic = document.getElementById('mic');
  const press = type => mic.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true }));
  S.stats = { voice_state: 'cloud', voice_source: 'Deepgram · nova-3', voice_engine: true, reply_voice: 'ElevenLabs',
    voice_listen: 'interactive', voice_speak: 'on', voice_mode_revision: 1 };
  S.connected = true;
  wireMic(); render();
  assert(engine().length === 0, 'the page asked the engine for a session nobody chose it for: ' + engine().join(' '));

  press('pointerdown');
  await until(() => node && node.onaudioprocess, 'the hold never opened the microphone');
  talk();
  assert(engine().length === 0, 'the hold asked the engine for a session beside a chosen hearing service: ' + engine().join(' '));
  // waits by design: holding the press past its latch threshold is the gesture under test.
  await new Promise(r => setTimeout(r, 450));
  press('pointerup');
  await until(() => frames.length === 1, 'the release did not send what was said: ' + frames.length + ' frames');
  assert(frames[0][0] === 1 && frames[0].length === 8 + 4800 * 2,
    'what was said did not go to the service as one utterance (frame version ' + frames[0][0] + ', ' + frames[0].length + ' bytes)');
  assert(engine().length === 0, 'the hold opened a session on the engine beside a chosen hearing service: ' + engine().join(' '));
});
</script>`
	runPageInEngines(t, page, routeModules)
}

func TestEarbudsOpenTheEnginesOutputLaneOnlyWhereTheEngineSpeaks(t *testing.T) {
	page := micMarkup + `<script type="module">
import { render, wireMic, bindTransport, sessionState, speak, speakerLaneForTest } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(async () => {
  const sent = [];
  bindTransport(() => {}, m => { sent.push(m); return 'req-' + sent.length; });
  let browserSpoke = 0;
  if (window.speechSynthesis) window.speechSynthesis.speak = () => { browserSpoke++; };
  window.AudioContext = class {
    constructor(o) { this.sampleRate = (o && o.sampleRate) || 48000; this.destination = {}; this.currentTime = 0; this.state = 'running'; }
    createBuffer(c, n, r) { const d = Array.from({ length: c }, () => new Float32Array(n)); return { duration: n / r, getChannelData: i => d[i] }; }
    createBufferSource() { return { connect() {}, start() {}, stop() {}, disconnect() {}, addEventListener() {}, onended: null, buffer: null }; }
    resume() { return Promise.resolve(); }
    close() {}
  };
  const engine = () => sent.filter(m => m.type === 'voice_session').map(m => m.voice.action + (m.voice.mode ? ':' + m.voice.mode : ''));
  const show = (speaks, rev) => {
    S.stats = { voice_state: 'plugin', voice_source: 'id.test.voice', voice_engine: true, reply_voice: speaks,
      voice_listen: 'off', voice_speak: 'on', voice_mode_revision: rev };
    S.connected = true; render();
  };
  wireMic();

  show('ElevenLabs', 1);
  assert(engine().length === 0 && !speakerLaneForTest(),
    'earbuds opened the engine\'s output lane beside a chosen speaking service: ' + engine().join(' '));
  speak('the answer to what was typed', null);
  assert((globalThis.said || []).length === 1 && globalThis.said[0].text === 'the answer to what was typed',
    'the typed reply was not handed to the chosen speaking service: ' + JSON.stringify(globalThis.said || []));
  assert(browserSpoke === 0, 'the browser\'s own voice read a reply a service was chosen to speak');

  show('', 2);
  assert(engine().join(' ') === 'open:output', 'with speaking left to the engine earbuds hold no output lane: ' + engine().join(' '));
  sessionState({ state: 'open', session_id: 'vs-out' });
  assert(speakerLaneForTest() && speakerLaneForTest().isOpen, 'the output lane did not open');

  show('ElevenLabs', 3);
  assert(engine().join(' ') === 'open:output abort' && !speakerLaneForTest(),
    'the output lane outlived the choice of a speaking service: ' + engine().join(' '));
  sessionState({ state: 'closed', session_id: 'vs-out' });
  show('ElevenLabs', 3);
  assert(engine().filter(a => a === 'open:output').length === 1, 'the output lane came back beside a chosen speaking service: ' + engine().join(' '));
});
</script>`
	runPageInEngines(t, page, routeModules)
}
