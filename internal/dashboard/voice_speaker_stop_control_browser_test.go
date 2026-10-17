//go:build !windows

package dashboard

import "testing"

func TestTheSpeakerStopControlEndsWithItsAudioOrItsLaneInBrowser(t *testing.T) {
	page := micMarkup + `<button id="hush-speaking" hidden></button><script type="module">
import { render, wireMic, bindTransport, sessionState, receiveFrame, encodeStreamFrame, speakerLaneForTest, connectionLost, abortDuplex } from './voice.js';
import { S } from './state.js';
import { assert, run, until } from './__harness.js';
run(async () => {
  const sent = [];
  bindTransport(() => {}, m => { sent.push(m); return 'req-' + sent.length; });
  if (!navigator.mediaDevices) Object.defineProperty(navigator, 'mediaDevices', { value: {}, configurable: true });
  navigator.mediaDevices.getUserMedia = async () => { throw new DOMException('denied', 'NotAllowedError'); };
  const sources = [];
  let ctx = null;
  window.AudioContext = class {
    constructor(o) { this.sampleRate = (o && o.sampleRate) || 48000; this.destination = {}; this.currentTime = 0; this.state = 'running'; ctx = this; }
    createBuffer(c, n, r) { const d = Array.from({ length: c }, () => new Float32Array(n)); return { duration: n / r, getChannelData: i => d[i] }; }
    createBufferSource() { const s = { connect() {}, start() {}, stop() {}, disconnect() {}, addEventListener() {}, onended: null, buffer: null }; sources.push(s); return s; }
    resume() { return Promise.resolve(); }
    close() { return Promise.resolve(); }
  };
  const stop = document.getElementById('hush-speaking');
  const show = (listen, speak, rev, more) => {
    S.stats = Object.assign({ voice_state: 'plugin', voice_engine: true, voice_listen: listen, voice_speak: speak, voice_mode_revision: rev }, more || {});
    S.connected = true; render();
  };
  let stream = 0;
  const play = () => { receiveFrame(encodeStreamFrame(48000, 1, 1, 1, 0, new Int16Array(4800).fill(900), ++stream)); };
  const playedOut = () => { if (ctx) ctx.currentTime += 60; for (const s of sources.splice(0)) if (s.onended) s.onended(); };
  const opened = why => { sessionState({ state: 'open', session_id: 'vs-' + sent.length }); assert(speakerLaneForTest() && speakerLaneForTest().isOpen, 'the output lane did not open ' + why + ': ' + JSON.stringify(sent.slice(-4)) + ' ' + JSON.stringify(speakerLaneForTest() && speakerLaneForTest().state)); };
  const hiddenSoon = async why => { await until(() => stop.hidden, 2000); assert(stop.hidden, 'the stop control stayed after ' + why); };
  wireMic();
  show('off', 'on', 1);
  opened('#1');

  play();
  assert(!stop.hidden, 'no stop control while the engine speaks on the output lane');
  playedOut();
  await hiddenSoon('the audio played out');

  receiveFrame(encodeStreamFrame(48000, 1, 3, 2, 0, new Int16Array(0), stream));
  // waits by design: the control must keep its visibility across multiple 250 ms update intervals.
  await new Promise(r => setTimeout(r, 600));
  assert(stop.hidden, 'the stop control came back for an END frame that arrived after the audio had played out');
  play();
  assert(!stop.hidden, 'no stop control for the reply the operator is about to stop');
  stop.click();
  await hiddenSoon('the operator stopped the reply');
  receiveFrame(encodeStreamFrame(48000, 1, 1, 2, 0, new Int16Array(4800).fill(900), stream));
  // waits by design: the control must keep its visibility across multiple 250 ms update intervals.
  await new Promise(r => setTimeout(r, 600));
  assert(stop.hidden, 'the stop control came back for a late frame of the reply the operator stopped');

  play();
  globalThis.cloudSpeaking(true);
  for (const s of sources.splice(0)) if (s.onended) s.onended();
  globalThis.cloudSpeaking(false);
  if (ctx) ctx.currentTime += 60;
  await hiddenSoon('the other route ended after the lane\'s last source had');

  play();
  globalThis.cloudSpeaking(true);
  playedOut();
  // waits by design: the control must keep its visibility across multiple 250 ms update intervals.
  await new Promise(r => setTimeout(r, 600));
  assert(!stop.hidden, 'the stop control went while a reply was still being spoken');
  globalThis.cloudSpeaking(false);
  assert(stop.hidden, 'the stop control stayed after the spoken reply ended');

  play();
  assert(!stop.hidden, 'no stop control for the second reply');
  sessionState({ state: 'closed', session_id: 'vs-closing' });
  assert(speakerLaneForTest(), 'the mode still wants a lane and none came back');
  // waits by design: the control must keep its visibility across multiple 250 ms update intervals.
  await new Promise(r => setTimeout(r, 600));
  assert(!stop.hidden, 'the stop control went while the audio was still playing');
  playedOut();
  await hiddenSoon('the audio played out on the lane that came back');
  opened('#2');

  play();
  connectionLost();
  await hiddenSoon('the connection was lost');

  show('off', 'on', 2);
  opened('#3');
  play();
  show('off', 'on', 2, { voice_engine: false });
  assert(!speakerLaneForTest(), 'the lane outlived its engine');
  await hiddenSoon('the engine was taken away');
  sessionState({ state: 'closed', session_id: 'vs-aborted' }); // the abort is acknowledged

  show('off', 'on', 3);
  opened('#4');
  play();
  show('off', 'off', 4);
  await hiddenSoon('speaking was turned off');
  sessionState({ state: 'closed', session_id: 'vs-aborted' });

  show('off', 'on', 5);
  opened('#5');
  play();
  assert(!stop.hidden, 'no stop control before the abort');
  abortDuplex();
  await hiddenSoon('the abort');

  play();
  assert(!stop.hidden && speakerLaneForTest(), 'no stop control before the refusal');
  sessionState({ state: 'refused', reason: 'another page holds the session' });
  assert(!speakerLaneForTest(), 'a refused lane was asked for again at once');
  await hiddenSoon('the lane was refused');
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
