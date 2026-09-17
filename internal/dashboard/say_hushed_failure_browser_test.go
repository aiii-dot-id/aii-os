//go:build !windows

package dashboard

import "testing"

// .
// .
// .
// .
// .
// .
// .
const hushedFailurePage = `<!doctype html><button id="hush-speaking" hidden></button><script type="module">
import { speak, hush } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(async () => {
  const said = [];
  window.speechSynthesis.speak = u => said.push(u.text);
  window.speechSynthesis.cancel = () => {};
  S.stats = { reply_voice: 'Fixture' };
  let refuse;
  window.fetch = () => new Promise((resolve, reject) => { refuse = reject; });
  speak('This was cancelled', null);
  hush();
  refuse(new Error('late network failure'));
  await new Promise(r => setTimeout(r, 30));
  assert(said.length === 0, 'a cancelled reply restarted in the browser voice: ' + JSON.stringify(said));
  // THE CONTROL HALF: the same failure with nobody hushing still falls
  // back to the browser voice, so the silence above is the hush's doing.
  speak('This was refused', null);
  refuse(new Error('late refusal'));
  await new Promise(r => setTimeout(r, 30));
  assert(said.length === 1 && said[0] === 'This was refused', 'an unhushed refusal lost its fallback: ' + JSON.stringify(said));
});
</script>`

func TestACancelledReplyDoesNotRestartWhenItsRequestFailsLate(t *testing.T) {
	runPageInEngines(t, hushedFailurePage, map[string][]byte{"/app.js": []byte("export function toast(m) {}\n")})
}
