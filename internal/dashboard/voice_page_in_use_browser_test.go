//go:build !windows

package dashboard

import "testing"

const voicePageInUsePage = `<!doctype html>
<button id="mic"></button><button id="converse"></button><div id="voice-transcript" hidden></div>
<script type="module">
import { assert, run } from './__harness.js';
import { bindTransport, render, sessionState, speak, operatorGestureForTest, speakerLaneForTest } from './voice.js';
import { S } from './state.js';
run(() => {
  const sent = [], toasts = window.__toasts = [];
  const ss = window.speechSynthesis, spoken = [];
  ss.speak = u => { spoken.push(u.text); };
  ss.cancel = () => {};
  bindTransport(() => {}, m => sent.push(m));
  const opens = () => sent.filter(m => m.type === 'voice_session' && m.voice && m.voice.action === 'open').map(m => m.voice);
  S.connected = true;
  const earbuds = rev => { S.stats = { voice_engine: true, voice_state: 'plugin', voice_listen: 'off', voice_speak: 'on', voice_mode_revision: rev }; render(); };
  const held = 'The speech engine couldn\'t start speaking. Another page holds the voice session (the page at 10.0.0.9).';

  earbuds(1);
  assert(opens().length === 1 && opens()[0].mode === 'output' && !('gesture' in opens()[0]), 'a page that only follows the mode claimed the hand, or did not ask: ' + JSON.stringify(opens()));

  sessionState({ state: 'refused', reason: held });
  assert(!speakerLaneForTest(), 'a refused lane was kept');
  render();
  assert(opens().length === 1, 'a refused lane was asked for again in the same breath: ' + opens().length);
  assert(toasts.length === 0, 'a page that only followed the mode reported its refusal: ' + JSON.stringify(toasts));
  speak('an answer no session spoke', null);
  assert(spoken.length === 0, 'the browser voice read a reply for the engine: ' + JSON.stringify(spoken));
  assert(toasts.length === 1 && toasts[0] === 'This reply was not spoken: no voice session of the speech engine was open for it. ' + held, 'the page did not say which page holds the voice: ' + JSON.stringify(toasts));

  operatorGestureForTest();
  assert(opens().length === 2 && opens()[1].mode === 'output' && opens()[1].gesture === true, 'the hand did not ask for the lane, or did not say it was the hand: ' + JSON.stringify(opens()));
  sessionState({ state: 'refused', reason: held });
  assert(toasts.length === 2 && toasts[1] === held, 'a refusal of what the hand asked for was not said: ' + JSON.stringify(toasts));

  operatorGestureForTest();
  assert(opens().length === 3 && opens()[2].gesture === true, 'the hand did not ask a third time: ' + JSON.stringify(opens()));
  sessionState({ state: 'open', session_id: 'vs-9' });
  speak('an answer the host bound to no session', null);
  assert(toasts.length === 3 && toasts[2] === 'This reply was not spoken: no voice session of the speech engine was open for it.', 'an old refusal was said of a page that holds the voice: ' + JSON.stringify(toasts));

  sessionState({ state: 'closed', session_id: 'vs-9' });
  assert(opens().length === 4 && opens()[3].mode === 'output' && !('gesture' in opens()[3]), 'a lane closed under the page was asked for again in the name of a spent gesture, or not at all: ' + JSON.stringify(opens()));
  sessionState({ state: 'refused', reason: held });
  assert(toasts.length === 3, 'a lane that asked again by itself reported its refusal: ' + JSON.stringify(toasts));
  const now = Date.now;
  Date.now = () => now() + 16000;
  render();
  assert(opens().length === 5 && !('gesture' in opens()[4]), 'the clock did not ask again, or claimed the hand: ' + JSON.stringify(opens()));
  sessionState({ state: 'open', session_id: 'vs-10' });
  speak('another answer the host bound to no session', null);
  assert(toasts.length === 4 && toasts[3] === 'This reply was not spoken: no voice session of the speech engine was open for it.', 'a refusal outlived the opening of the lane that was refused: ' + JSON.stringify(toasts));
  Date.now = now;

  operatorGestureForTest();
  assert(opens().length === 5, 'a gesture asked for a lane the page already holds: ' + JSON.stringify(opens()));
  sessionState({ state: 'closed', session_id: 'vs-10' });
  assert(opens().length === 6 && !('gesture' in opens()[5]), 'a lane closed under the page was asked for again in the name of a hand that had asked for nothing: ' + JSON.stringify(opens()));
  sessionState({ state: 'open', session_id: 'vs-11' });

  const silent = rev => { S.stats = { voice_engine: true, voice_state: 'plugin', voice_listen: 'off', voice_speak: 'off', voice_mode_revision: rev }; render(); };
  silent(2);
  sessionState({ state: 'closed', session_id: 'vs-11' });
  assert(opens().length === 6 && !speakerLaneForTest(), 'a lane was asked for with speaking off: ' + JSON.stringify(opens()));
  operatorGestureForTest();
  earbuds(3);
  assert(opens().length === 7 && opens()[6].gesture === true, 'the hand that turned speaking on did not say so: ' + JSON.stringify(opens()));
  sessionState({ state: 'open', session_id: 'vs-12' });
  silent(4);
  sessionState({ state: 'closed', session_id: 'vs-12' });
  earbuds(5);
  assert(opens().length === 8 && !('gesture' in opens()[7]), 'a spent gesture was claimed by a second open: ' + JSON.stringify(opens()));
});
</script>`

func TestThePageInUseAsksForTheVoiceByTheOperatorsHand(t *testing.T) {
	runPageInEngines(t, voicePageInUsePage, map[string][]byte{
		"/app.js": []byte("export function toast(m) { (window.__toasts = window.__toasts || []).push(m); }\n"),
	})
}
