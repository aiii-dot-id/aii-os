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
const voiceEngineEndJourneyPage = `<!doctype html>
<button id="mic"></button><button id="converse"></button><div id="voice-transcript" hidden></div>
<script type="module">
import { assert, run } from './__harness.js';
import { bindTransport, startDuplex, residentActive, captureFenceForTest, converseForTest, decodeStreamFrame } from './voice.js';
import { S } from './state.js';

run(async () => {
  // An installed engine is the microphone: the host says so with voice_state.
  S.stats = Object.assign(S.stats || {}, { voice_engine: true, voice_state: 'plugin' });
  S.connected = true;
  const frames = [], msgs = [];
  const host = bindTransport(b => frames.push(b), m => msgs.push(m));
  // A synthetic microphone: a real MediaStream with a real audio track,
  // so stopping it is observable the way stopping hardware is.
  const ac = new (window.AudioContext || window.webkitAudioContext)();
  const dest = ac.createMediaStreamDestination();
  const track = dest.stream.getAudioTracks()[0];
  navigator.mediaDevices.getUserMedia = async () => dest.stream;

  converseForTest('waiting'); // the operator asked for a conversation
  await startDuplex();
  assert(residentActive(), 'the conversation started through the production entry point');
  assert(msgs.some(m => m.voice && m.voice.action === 'open'), 'the session was requested: ' + JSON.stringify(msgs));
  host.sessionState({ state: 'open', session_id: 'vs-live' });
  assert(track.readyState === 'live', 'the microphone is live while the session listens: ' + track.readyState);
  const epoch = captureFenceForTest().epoch();
  const mic = document.getElementById('mic');
  const converse = document.getElementById('converse');
  const transcript = document.getElementById('voice-transcript');
  const toasts = () => (window.__toasts || []).length;
  // THE VISIBLE CONTROL. With an engine installed the microphone button
  // is hidden; the conversation button and the transcript line are what
  // the operator sees, so that is where the state must read.
  assert(mic.hidden && !converse.hidden, 'in plugin mode the conversation control is the visible one');
  assert(/Stop listening/.test(converse.title) && converse.getAttribute('aria-label') === converse.title, 'while listening, the visible control offers to stop: ' + converse.title);
  assert(!transcript.hidden && transcript.textContent === 'Listening…', 'the transcript line says it is listening: ' + JSON.stringify(transcript.textContent));

  // 1. A COMPLETION FOR A SESSION THIS PAGE DOES NOT HOLD touches nothing.
  const t0 = toasts();
  host.sessionState({ state: 'input_complete', session_id: 'vs-old', reason: 'capture_limit' });
  assert(track.readyState === 'live', 'a late word about the previous session must not stop this microphone');
  assert(captureFenceForTest().epoch() === epoch && residentActive(), 'nothing of this session was retired');
  assert(toasts() === t0, 'and nothing was announced');
  assert(/Stop listening/.test(converse.title) && transcript.textContent === 'Listening…', 'the visible state did not change for a foreign session');

  // 2. THE MATCHING COMPLETION stops the microphone — and only the microphone.
  const before = frames.length, t1 = toasts();
  host.sessionState({ state: 'input_complete', session_id: 'vs-live', reason: 'capture_limit' });
  assert(track.readyState === 'ended', 'the microphone track is stopped: ' + track.readyState);
  assert(captureFenceForTest().epoch() > epoch, 'the capture graph is retired');
  assert(residentActive(), 'the session stays: what was heard still finishes over it');
  assert(!msgs.some(m => m.voice && (m.voice.action === 'abort' || m.voice.action === 'close')), 'no abort and no close: ' + JSON.stringify(msgs));
  assert(!frames.slice(before).some(b => decodeStreamFrame(b).kind === 3), 'no END frame follows the engine’s own cutoff');
  assert(/listens/.test(mic.title), 'the reason sits on the microphone: ' + mic.title);
  assert(/listens/.test(converse.title) && /Tap to end the conversation/.test(converse.title), 'the VISIBLE control names why, and what a tap now does: ' + converse.title);
  assert(converse.getAttribute('aria-label') === converse.title, 'and says the same to a screen reader');
  assert(!transcript.hidden && /listens/.test(transcript.textContent) && /still finishes/.test(transcript.textContent), 'the transcript line names the end of listening, promising only what was heard: ' + JSON.stringify(transcript.textContent));
  assert(!/reply is still coming/.test(converse.title + transcript.textContent), 'no unconditional promise of a reply');
  assert(toasts() === t1 + 1 && /listens/.test(window.__toasts[t1]), 'said once, in the operator’s words: ' + JSON.stringify(window.__toasts));

  // 3. THE SAME COMPLETION AGAIN — the event and a status reply can both
  //    carry it — is not announced again.
  host.sessionState({ state: 'input_complete', session_id: 'vs-live', reason: 'capture_limit' });
  assert(toasts() === t1 + 1, 'a duplicate completion is not announced twice');
});
</script>`

func TestEngineEndedInputJourneyInBrowser(t *testing.T) {
	runPageInEngines(t, voiceEngineEndJourneyPage, map[string][]byte{
		"/app.js": []byte("export function toast(m) { (window.__toasts = window.__toasts || []).push(m); }\n"),
	})
}
