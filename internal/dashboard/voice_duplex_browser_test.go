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
const voiceDuplexPage = `<!doctype html>
<button id="mic"></button><button id="voicemode"></button>
<script type="module">
import { assert, run } from './__harness.js';
import { decodeStreamFrame, StreamLane, PreRoll, Conversation } from './voice.js';

function lane(store) {
  const l = new StreamLane({ sendFrame: b => store.frames.push(decodeStreamFrame(b)), sendJSON: m => store.msgs.push(m), rate: 16000, channels: 1, mode: 'conversation' });
  store.lanes.push(l);
  l.open();
  return l;
}
const opens = s => s.msgs.filter(m => m.voice && m.voice.action === 'open').length;
const closes = s => s.msgs.filter(m => m.voice && m.voice.action === 'close').length;
const aborts = s => s.msgs.filter(m => m.voice && m.voice.action === 'abort').length;
const ends = s => s.frames.filter(f => f.kind === 3).length;
const pcms = s => s.frames.filter(f => f.kind === 1).length;

run(() => {
  // A. StreamLane.finishInput names the input end (finish_input) WITHOUT
  //    closing: an END frame goes to the wire, no close message, and the
  //    session stays open for the final reply.
  {
    const s = { frames: [], msgs: [], lanes: [] };
    const l = lane(s);
    l.onState({ state: 'open' });
    l.chunk(new Float32Array(320));
    l.finishInput();
    assert(ends(s) === 1 && pcms(s) === 1, 'finishInput sends one end after the audio: ' + JSON.stringify([ends(s), pcms(s)]));
    assert(closes(s) === 0, 'the input half-close must NOT close the session: ' + JSON.stringify(s.msgs));
    assert(l.ended === false && l.over() === false, 'the session stays open after the half-close');
    l.finishInput();
    assert(ends(s) === 1, 'the half-close is idempotent: one END');
    l.end();
    assert(ends(s) === 1 && closes(s) === 1, 'end() after the half-close closes without a second END');
  }

  // B. PreRoll is a bounded ring: five 300-sample frames past a 1000 bound
  //    keep roughly the last bound, never all 1500.
  {
    const pr = new PreRoll(1000);
    for (let i = 0; i < 5; i++) pr.push(new Float32Array(300).fill(0.25));
    assert(pr.samples() === 1200 && pr.samples() < 1500, 'the ring is bounded near its max: ' + pr.samples());
    const out = pr.drain();
    assert(out && out.length === 1200 && pr.samples() === 0, 'drain returns the ring and empties it');
    pr.push(new Float32Array(10)); pr.clear();
    assert(pr.samples() === 0 && pr.drain() === null, 'clear empties the ring');
  }

  // C. The resident conversation: ONE session, ONE continuous input stream,
  //    ONE finish_input. The mic streams continuously with no per-turn END;
  //    the engine's turns are its own. finish_input half-closes the input
  //    (no more mic audio) WITHOUT closing; the session stays open for the
  //    final reply; Abort closes.
  {
    const s = { frames: [], msgs: [], lanes: [] };
    let ended = 0;
    const conv = new Conversation({ newLane: () => lane(s), onEnded: () => { ended++; }, prerollSamples: 0 });
    conv.start();
    s.lanes[0].onState({ state: 'open' });
    conv.frame(new Float32Array(320));
    conv.frame(new Float32Array(320));   // continuous input stream — no per-turn boundary
    assert(opens(s) === 1, 'one session for the whole conversation: ' + opens(s));
    assert(ends(s) === 0 && closes(s) === 0, 'the mic streams continuously — no per-turn END, no close: ' + JSON.stringify(s.msgs));
    assert(pcms(s) === 2, 'both frames stream into the one input stream: ' + pcms(s));

    conv.finishInput();
    assert(ends(s) === 1 && closes(s) === 0, 'finish_input is a half-close, not a close: ' + JSON.stringify([ends(s), closes(s)]));
    assert(!conv.streaming(), 'the input is half-closed after finish_input');
    conv.frame(new Float32Array(320));
    assert(pcms(s) === 2, 'audio after finish_input is dropped, never sent past the cutoff: ' + pcms(s));
    assert(conv.active, 'the session stays OPEN for the final reply');
    assert(ended === 0, 'finish_input did not end the conversation');

    conv.abort();
    assert(aborts(s) === 1 && !conv.active, 'Abort closes the session: ' + JSON.stringify(s.msgs));
  }

  // D. Pre-roll keeps the opening words: audio captured BEFORE Start is
  //    flushed into the stream the moment the session opens.
  {
    const s = { frames: [], msgs: [], lanes: [] };
    const conv = new Conversation({ newLane: () => lane(s), prerollSamples: 16000, onEnded: () => {} });
    conv.frame(new Float32Array(320).fill(0.5));   // spoken before Start -> pre-roll
    conv.start();
    s.lanes[0].onState({ state: 'open' });
    assert(pcms(s) >= 1 && s.frames[0].kind === 1 && s.frames[0].pcm.length === 320, 'the lead-in reached the stream: ' + JSON.stringify(s.frames.map(f => [f.kind, f.pcm ? f.pcm.length : 0])));
    assert(s.frames[0].pcm[0] === 16383, 'the opening words are the ones spoken before Start: ' + s.frames[0].pcm[0]);
  }
});
</script>`

func TestVoiceResidentConversationInBrowser(t *testing.T) {
	runPageInEngines(t, voiceDuplexPage, map[string][]byte{
		"/app.js": []byte("export function toast(m) { (window.__toasts = window.__toasts || []).push(m); }\n"),
	})
}
