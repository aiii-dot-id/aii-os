//go:build !windows

package dashboard

import "testing"

func TestAnEngineChangeKeepsTheOpenLaneUntilItRetiresInBrowser(t *testing.T) {
	page := micMarkup + `<script type="module">
import { render, wireMic, bindTransport, sessionState, speak, speakerLaneForTest, receiveFrame, encodeStreamFrame, voiceEvent, playbackForTest } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(async () => {
  const sent = [];
  bindTransport(() => {}, m => { sent.push(m); return 'req-' + sent.length; });
  let browserSpoke = 0, microphones = 0;
  if (window.speechSynthesis) window.speechSynthesis.speak = () => { browserSpoke++; };
  if (!navigator.mediaDevices) Object.defineProperty(navigator, 'mediaDevices', { value: {}, configurable: true });
  navigator.mediaDevices.getUserMedia = async () => { microphones++; throw new Error('no microphone here'); };
  const sources = [];
  window.AudioContext = class {
    constructor(o) { this.sampleRate = (o && o.sampleRate) || 48000; this.destination = {}; this.currentTime = 0; this.state = 'running'; }
    createBuffer(c, n, r) { const d = Array.from({ length: c }, () => new Float32Array(n)); return { duration: n / r, getChannelData: i => d[i] }; }
    createBufferSource() { const s = { connect() {}, start() {}, stop() { s.stopped = true; }, disconnect() {}, addEventListener() {}, onended: null, buffer: null }; sources.push(s); return s; }
    resume() { return Promise.resolve(); }
    close() {}
  };
  const engine = () => sent.filter(m => m.type === 'voice_session' && m.voice.action !== 'playback').map(m => m.voice.action + (m.voice.mode ? ':' + m.voice.mode : ''));
  const receipts = () => sent.filter(m => m.type === 'voice_session' && m.voice.action === 'playback').map(m => m.voice.playback);
  const line = () => { const v = document.getElementById('mic-voice'); return v.hidden ? '' : v.textContent; };
  const show = extra => {
    S.stats = Object.assign({ voice_state: 'plugin', voice_source: 'id.engine.a', voice_engine: true, reply_voice: '',
      voice_listen: 'off', voice_speak: 'on', voice_mode_revision: 1 }, extra);
    S.connected = true; render();
  };
  wireMic();

  show({});
  assert(engine().join(' ') === 'open:output', 'earbuds did not open the output lane: ' + engine().join(' '));
  sessionState({ state: 'open', session_id: 'vs-a' });
  assert(speakerLaneForTest() && speakerLaneForTest().isOpen, 'the output lane did not open');
  voiceEvent({ type: 'synthesis_start', session_id: 'vs-a', stream: 1 });
  speak('the first answer', { route: 'plugin', session_id: 'vs-a', synthesis_id: 'vs-a-syn-1' });
  receiveFrame(encodeStreamFrame(48000, 1, 1, 1, 0, new Int16Array(4800), 1));
  assert(sources.length === 1, 'fixture: the reply did not schedule');
  assert(line() === '', 'the menu names a carried voice before any change: ' + line());

  show({ voice_speaker: 'id.engine.a' });
  assert(engine().join(' ') === 'open:output', 'the change retired or reopened the lane halfway through the session: ' + engine().join(' '));
  assert(!sources[0].stopped, 'the change cut the reply that was playing');
  assert(microphones === 0, 'changing the speaker asked for a microphone');
  assert(line().includes('id.engine.a') && line().includes('next'), 'the menu does not say whose voice the replies are in until the next session: ' + JSON.stringify(line()));
  speak('the second answer', { route: 'plugin', session_id: 'vs-a', synthesis_id: 'vs-a-syn-2' });
  assert(browserSpoke === 0 && !(globalThis.said || []).length, 'another voice read a reply the open session speaks (browser ' + browserSpoke + ')');
  receiveFrame(encodeStreamFrame(48000, 1, 3, 2, 4800, null, 1));
  playbackForTest().ctx.currentTime = 10;
  sources[0].onended();
  const drained = receipts().filter(r => r.terminal);
  assert(drained.length === 1 && drained[0].session_id === 'vs-a' && drained[0].stream === 1 && drained[0].outcome === 'drained' && drained[0].rendered === 4800,
    'A\'s reply was not receipted drained under its own session: ' + JSON.stringify(receipts()));

  sessionState({ state: 'closed', session_id: 'vs-a' });
  show({});
  assert(engine().join(' ') === 'open:output open:output', 'the next session did not open when the old one retired: ' + engine().join(' '));
  assert(microphones === 0, 'the next lane asked for a microphone');
  assert(line() === '', 'the menu still names the retired session\'s engine: ' + line());
  assert(browserSpoke === 0, 'the browser\'s own voice spoke during the change');
});
</script>`
	runPageInEngines(t, page, routeModules)
}
