//go:build !windows

package dashboard

import "testing"

const voiceInputCompletePage = `<!doctype html>
<button id="mic"></button>
<script type="module">
import { assert, run } from './__harness.js';
import { Conversation, StreamLane, decodeStreamFrame, inputEndSentenceForTest } from './voice.js';

run(() => {
  const mk = () => {
    const frames = [], msgs = [];
    const lane = new StreamLane({ sendFrame: b => frames.push(b), sendJSON: m => msgs.push(m), rate: 16000, channels: 1, mode: 'conversation' });
    lane.open();
    lane.onState({ state: 'open', session_id: 'vs-1' });
    const conv = new Conversation({ newLane: () => lane });
    conv.start();
    return { conv, lane, frames, msgs };
  };

  {
    const { conv, frames, msgs } = mk();
    conv.frame(new Float32Array(320));
    const before = frames.length;
    assert(before === 1, 'the microphone was feeding the lane: ' + before);
    assert(conv.inputClosedByEngine() === true, 'the engine ending the input is news the first time');
    assert(frames.length === before, 'no END frame may follow the engine’s own cutoff: ' + (frames.length - before) + ' extra');
    assert(!msgs.some(m => m.voice && m.voice.action === 'abort'), 'an engine-ended input is not an abort: ' + JSON.stringify(msgs));
    assert(!msgs.some(m => m.voice && m.voice.action === 'close'), 'the session stays open for the last words and the reply');

    conv.frame(new Float32Array(320));
    conv.frame(new Float32Array(320));
    assert(frames.length === before, 'no microphone audio after the engine stopped accepting it');
    assert(conv.streaming() === false, 'the page no longer considers itself streaming');
  }

  {
    const { conv } = mk();
    assert(conv.inputClosedByEngine() === true, 'the first is news');
    assert(conv.inputClosedByEngine() === false, 'a duplicate is not');
  }

  {
    const { conv, frames } = mk();
    conv.inputClosedByEngine();
    const before = frames.length;
    conv.finishInput();
    assert(frames.length === before, 'the operator’s Finish cannot name a cutoff the engine already fixed');
  }

  {
    const limit = inputEndSentenceForTest('capture_limit');
    assert(/listens/.test(limit) && /still/.test(limit), 'the limit must say what happened and what survives: ' + limit);
    assert(!/capture_limit/.test(limit), 'the operator does not read the engine’s token: ' + limit);
    const unknown = inputEndSentenceForTest('quota_exhausted');
    assert(/quota_exhausted/.test(unknown), 'an unfamiliar reason is still named: ' + unknown);
    const none = inputEndSentenceForTest('');
    assert(none && !/undefined|null/.test(none), 'no reason is still a sentence: ' + none);
  }
});
</script>`

func TestEngineEndedInputInBrowser(t *testing.T) {
	runPageInEngines(t, voiceInputCompletePage, map[string][]byte{
		"/app.js": []byte("export function toast(m) { (window.__toasts = window.__toasts || []).push(m); }\n"),
	})
}
