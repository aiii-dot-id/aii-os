//go:build !windows

package dashboard

import "testing"

// .
// .
const voiceRefusalPage = `<!doctype html>
<script type="module">
import { assert, run } from './__harness.js';
import { speak } from './voice.js';
import { S } from './state.js';
run(async () => {
  const spoken = [];
  window.__speechServiceCalls = [];
  window.speechSynthesis.speak = u => spoken.push(u.text);
  window.speechSynthesis.cancel = () => {};
  S.voiceSpeak = true;
  for (const configured of [false, true]) {
    S.stats = { reply_voice: configured };
    for (const text of ['barge-in', 'Abort', 'ended session', 'unknown admission', 'engine refusal']) {
      speak(text, { session_id:'vs-ended', route:'plugin', text_only:true });
    }
    speak('native reply', { session_id:'vs-current', synthesis_id:'syn-current', route:'plugin' });
    await Promise.resolve();
    assert(spoken.length === 0 && window.__speechServiceCalls.length === 0,
      'fenced native content revived: browser=' + JSON.stringify(spoken) + ' service=' + JSON.stringify(window.__speechServiceCalls));
  }
  // Both positive controls must reach their real route selection; a no-op
  // speak() cannot make this test green.
  speak('ordinary configured reply', null);
  speak('already minted cloud reply', { route:'cloud', synthesis_id:'cloud-positive' });
  assert(window.__speechServiceCalls.length === 2, 'configured and minted service routes remain live');
  assert(window.__speechServiceCalls[0].text === 'ordinary configured reply' &&
    window.__speechServiceCalls[1].id === 'cloud-positive', 'wrong service arguments');
  S.stats = { reply_voice:false };
  speak('ordinary browser reply', null);
  assert(spoken.length === 1 && spoken[0] === 'ordinary browser reply', 'ordinary browser route remains live');
  S.voiceSpeak = false;
  speak('quiet ordinary reply', null);
  assert(spoken.length === 1, 'browser toggle remains respected');
});
</script>`

func TestRefusedNativeReplyCannotReviveInBrowser(t *testing.T) {
	runPageInEngines(t, voiceRefusalPage, map[string][]byte{
		"/app.js": []byte("export function toast(m) {}\n"),
		"/say.js": []byte(`
export function spokenAudio(ask) { window.__speechServiceCalls.push(ask); return Promise.resolve(); }
export function hushSpoken() {}
export function whenSpeaking(fn) {}
`),
	})
}
