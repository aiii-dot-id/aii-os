//go:build !windows

package dashboard

import "testing"

// .
// .
// .
// .
// .
// .
const voiceHushPage = `<!doctype html>
<script type="module">
import { assert, run } from './__harness.js';
import { speak, hushFromHost } from './voice.js';
import { S } from './state.js';
run(async () => {
  const spoken = [];
  window.__speechServiceCalls = []; window.__hushes = 0; window.__cancels = 0;
  window.speechSynthesis.speak = u => spoken.push(u.text);
  window.speechSynthesis.cancel = () => { window.__cancels++; };
  S.voiceSpeak = true;
  S.stats = { reply_voice: true };
  speak('taken-over reply', { session_id:'vs-1', route:'cloud', synthesis_id:'fb-1', fallback:true });
  assert(window.__speechServiceCalls.length === 1 && window.__speechServiceCalls[0].id === 'fb-1', 'a taken-over reply plays by its id');
  hushFromHost({ session_id:'vs-1', synthesis_id:'fb-1', route:'cloud', reason:'the operator spoke' });
  assert(window.__hushes === 1 && window.__cancels === 1, 'the hush reached the service and the browser voice: ' + window.__hushes + '/' + window.__cancels);
  // The hush arrives at the element as an error; that is not a refusal to read back.
  window.__rejectNext = 'fb-1';
  speak('late error for the hushed reply', { session_id:'vs-1', route:'cloud', synthesis_id:'fb-1', fallback:true });
  await new Promise(r => setTimeout(r, 0)); await Promise.resolve();
  assert(spoken.length === 0, 'a hushed reply was read back by the browser: ' + JSON.stringify(spoken));
  // A service that refuses an ordinary reply still hands it to the browser voice.
  window.__rejectNext = 'ordinary-1';
  speak('an ordinary refusal', { route:'cloud', synthesis_id:'ordinary-1' });
  await new Promise(r => setTimeout(r, 0)); await Promise.resolve();
  assert(spoken.length === 1 && spoken[0] === 'an ordinary refusal', 'an ordinary refusal keeps the browser fallback: ' + JSON.stringify(spoken));
  // The browser route the host names for a taken-over reply.
  speak('browser fallback', { session_id:'vs-1', route:'browser', fallback:true });
  assert(spoken.length === 2 && spoken[1] === 'browser fallback', 'the browser route speaks: ' + JSON.stringify(spoken));
  S.voiceSpeak = false;
  speak('quiet browser fallback', { session_id:'vs-1', route:'browser', fallback:true });
  assert(spoken.length === 2, 'the browser route respects the toggle');
  // browserSpeak cancels before it speaks, so the count is relative.
  const hushesBefore = window.__hushes, cancelsBefore = window.__cancels;
  hushFromHost({ session_id:'vs-1', route:'browser', reason:'the session was aborted' });
  assert(window.__hushes === hushesBefore + 1 && window.__cancels === cancelsBefore + 1, 'a browser-route hush stops the browser voice: ' + window.__hushes + '/' + window.__cancels);
});
</script>`

func TestAHushFromTheHostStopsEveryVoice(t *testing.T) {
	runPageInEngines(t, voiceHushPage, map[string][]byte{
		"/app.js": []byte("export function toast(m) {}\n"),
		"/say.js": []byte(`
export function spokenAudio(ask) {
  window.__speechServiceCalls.push(ask);
  if (window.__rejectNext && ask && ask.id === window.__rejectNext) { window.__rejectNext = null; return Promise.reject(new Error('gone')); }
  return Promise.resolve();
}
export function hushSpoken() { window.__hushes++; }
export function whenSpeaking(fn) {}
`),
	})
}
