//go:build !windows

package dashboard

import "testing"

// .
// .
// .
// .
// .
const voiceSpeakPage = `<!doctype html>
<script type="module">
import { assert, run } from './__harness.js';
import { speak } from './voice.js';
import { S } from './state.js';
run(() => {
  const spoken = [];
  // speechSynthesis is a getter-only Window property: stub its METHODS on
  // the real object (its presence is what makes speak() reach them).
  const ss = window.speechSynthesis;
  ss.speak = u => { spoken.push(u.text); };
  ss.cancel = () => {};
  S.voiceSpeak = true;
  speak('the engine speaks this', { session_id: 'vs-1', synthesis_id: 'vs-1-syn-1', route: 'plugin' });
  assert(spoken.length === 0, 'a plugin-routed reply is never spoken by the browser');
  speak('a plain chat reply', null);
  assert(spoken.length === 1 && spoken[0] === 'a plain chat reply', 'a response without plugin provenance keeps the browser fallback: ' + JSON.stringify(spoken));
  speak('a plugin reply after its session closed', { session_id: 'vs-gone', route: 'plugin' });
  assert(spoken.length === 1, 'an expired plugin reply cannot fall back to browser speech');
  S.voiceSpeak = false;
  speak('quiet', null);
  assert(spoken.length === 1, 'the fallback still honours voiceSpeak');
});
</script>`

func TestSpeakDecidesByProvenanceInBrowser(t *testing.T) {
	runPageInEngines(t, voiceSpeakPage, map[string][]byte{
		"/app.js": []byte("export function toast(m) {}\n"),
	})
}
