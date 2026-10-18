//go:build !windows

package dashboard

import "testing"

const voiceReplyNotSpokenPage = `<!doctype html>
<div id="voice-transcript" hidden></div>
<script type="module">
import { assert, run } from './__harness.js';
import { speak, voiceEvent, bindTransport } from './voice.js';
import { S } from './state.js';
run(() => {
  const spoken = [], line = document.getElementById('voice-transcript');
  const ss = window.speechSynthesis;
  ss.speak = u => { spoken.push(u.text); };
  ss.cancel = () => {};
  const toasts = window.__toasts = [];
  const shown = () => line.hidden ? '' : line.textContent;

  voiceEvent({ type: 'reply_refused', session_id: 'vs-1', reason: 'the session ended before its reply' });
  assert(toasts.length === 1 && toasts[0] === 'This reply was not spoken: the session ended before its reply.', 'the refusal was not said: ' + JSON.stringify(toasts));
  assert(shown() === toasts[0] && line.classList.contains('failed') && !line.classList.contains('provisional'), 'the transcript line does not keep the reason: ' + line.outerHTML);
  voiceEvent({ type: 'reply_refused', session_id: 'vs-1', reason: 'a barge-in, close, abort or cancellation superseded this reply', superseded: true });
  assert(toasts.length === 1, 'a superseded reply was shown as an error: ' + JSON.stringify(toasts));
  voiceEvent({ type: 'transcript_partial', session_id: 'vs-1', text: 'and then' });
  assert(shown() === 'and then' && !line.classList.contains('failed'), 'the reason outlived the next words: ' + line.outerHTML);

  voiceEvent({ type: 'reply_fallback', session_id: 'vs-1', reason: 'cloud' });
  assert(toasts.length === 2 && toasts[1] === 'The speech engine did not speak this reply; the voice service chosen to speak replies spoke it.' && shown() === toasts[1], 'a service that took a refused reply was not named: ' + JSON.stringify(toasts));
  voiceEvent({ type: 'reply_fallback', session_id: 'vs-1', reason: 'browser' });
  assert(toasts.length === 3 && toasts[2] === "The voice service chosen to speak replies did not take this reply; the browser's own voice read it." && shown() === toasts[2], 'the browser voice standing in for a service was not named: ' + JSON.stringify(toasts));
  toasts.length = 1;

  bindTransport(() => {}, () => {});
  S.connected = true;
  S.stats = { voice_engine: true, voice_state: 'plugin', voice_listen: 'interactive', voice_speak: 'on', voice_mode_revision: 1 };
  speak('a typed answer with no session to speak it', null);
  assert(spoken.length === 0, 'the browser voice read a reply for the engine: ' + JSON.stringify(spoken));
  assert(toasts.length === 2 && toasts[1] === 'This reply was not spoken: no voice session of the speech engine was open for it.', 'the page did not say why: ' + JSON.stringify(toasts));
  speak('the engine speaks this', { session_id: 'vs-1', synthesis_id: 'vs-1-syn-1', route: 'plugin' });
  assert(spoken.length === 0 && toasts.length === 2, 'an engine-spoken reply was read or reported: ' + JSON.stringify([spoken, toasts]));
  S.stats = { voice_engine: true, voice_state: 'plugin', voice_listen: 'off', voice_speak: 'off', voice_mode_revision: 2 };
  speak('nothing is spoken in this mode', null);
  assert(spoken.length === 0 && toasts.length === 2, 'speak off was read or reported: ' + JSON.stringify([spoken, toasts]));

  S.stats = { voice_listen: 'interactive', voice_speak: 'on', voice_mode_revision: 3 };
  speak('read by the only voice there is', null);
  assert(spoken.length === 1 && spoken[0] === 'read by the only voice there is' && toasts.length === 2, 'the browser voice stopped being a voice: ' + JSON.stringify([spoken, toasts]));
});
</script>`

func TestAReplyNoVoiceSpokeIsSaidAndTheBrowserDoesNotStandInForTheEngine(t *testing.T) {
	runPageInEngines(t, voiceReplyNotSpokenPage, map[string][]byte{
		"/app.js": []byte("export function toast(m) { (window.__toasts = window.__toasts || []).push(m); }\n"),
	})
}
