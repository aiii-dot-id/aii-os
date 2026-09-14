//go:build !windows

// .
// .

package dashboard

import "testing"

// .
// .
// .
// .
const voiceEventPage = `<!doctype html>
<div id="voice-transcript" hidden></div>
<script type="module">
import { assert, run } from './__harness.js';
import { voiceEvent } from './voice.js';

run(() => {
  const el = document.getElementById('voice-transcript');

  voiceEvent({ type: 'transcript_partial', text: 'hel', speaker: 'Ada' });
  assert(!el.hidden && el.textContent === 'Ada: hel' && el.classList.contains('provisional'),
    'a provisional transcript shows, marked provisional: ' + el.textContent + ' hidden=' + el.hidden);

  voiceEvent({ type: 'transcript_final', text: 'hello there', speaker: 'Ada', final: true });
  assert(!el.hidden && el.textContent === 'Ada: hello there' && !el.classList.contains('provisional'),
    'the final transcript replaces it and is no longer provisional: ' + el.textContent);

  voiceEvent({ type: 'synthesis_end' });
  assert(el.textContent === 'Ada: hello there',
    'an unrendered event type is passed over without error, leaving the line');

  voiceEvent({ type: 'failed', reason: 'engine died' });
  assert(!el.hidden && el.classList.contains('failed') && el.textContent.indexOf('engine died') >= 0,
    'a failure is shown, not a clean idle: ' + el.textContent);

  voiceEvent({ type: 'closed' });
  assert(el.hidden && el.textContent === '' && !el.classList.contains('failed'),
    'a clean close clears the line');
});
</script>`

func TestVoiceEventRendersInBrowser(t *testing.T) {
	runPageInEngines(t, voiceEventPage, map[string][]byte{
		"/app.js": []byte("export function toast(m) {}\n"),
	})
}
