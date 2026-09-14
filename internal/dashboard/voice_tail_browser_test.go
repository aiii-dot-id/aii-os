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
const voiceTailPage = `<!doctype html>
<script type="module">
import { assert, run } from './__harness.js';
import { decodeStreamFrame, StreamLane, Conversation, finishWithTail, flushPort, captureMessage } from './voice.js';

run(async () => {
  // 1. The worklet: a partial is HELD until the frame fills; a flush posts
  //    the held partial WITH its acknowledgement in one message, then the
  //    capture stops, so no sample can exist after the ack.
  const { CaptureProcessor } = await import('./voice-capture.worklet.js');
  const p = new CaptureProcessor({ processorOptions: { frame: 64 } });
  const posted = [];
  p.port.postMessage = m => posted.push(m);
  p.process([[new Float32Array(63).fill(1)]]);
  assert(posted.length === 0 && p.n === 63, 'a partial frame is held, not posted: ' + posted.length + '/' + p.n);
  p.process([[new Float32Array([2, 3])]]);
  assert(posted.length === 1 && posted[0] instanceof ArrayBuffer && new Float32Array(posted[0]).length === 64 && p.n === 1,
    'a full frame is posted and the remainder held: ' + posted.length + '/' + p.n);
  p.port.onmessage({ data: { flush: 9 } });
  const ack = posted[1];
  assert(posted.length === 2 && ack && ack.ack === 9 && ack.samples === 1 && new Float32Array(ack.buffer)[0] === 3,
    'flush posts the held partial WITH its acknowledgement: ' + JSON.stringify(ack && { ack: ack.ack, samples: ack.samples }));
  p.process([[new Float32Array(64).fill(7)]]);
  assert(posted.length === 2, 'after the flush the capture is stopped: nothing more is posted');
  p.port.onmessage({ data: { flush: 10 } });
  assert(posted[2].samples === 0 && posted[2].buffer === null, 'an empty flush acknowledges zero samples');

  // 2. The page-side protocol: the ack's partial is admitted through the
  //    frame path BEFORE the flush resolves; a capture that never answers
  //    resolves unconfirmed within the bound.
  const got = [];
  const port = { postMessage(m) { setTimeout(() => captureMessage({ ack: m.flush, samples: 100, buffer: new Float32Array(100).fill(0.5).buffer }, f32 => got.push(f32.length)), 0); } };
  const r = await flushPort(port, 500);
  assert(r.confirmed && r.samples === 100 && got.length === 1 && got[0] === 100, 'the partial is admitted before the flush resolves: ' + JSON.stringify([r, got]));
  const t0 = Date.now();
  const r2 = await flushPort({ postMessage() {} }, 60);
  assert(!r2.confirmed && /acknowledge/.test(r2.reason) && Date.now() - t0 < 1000, 'a capture that never acknowledges is reported unconfirmed, within the bound: ' + JSON.stringify(r2));

  // 3. The ordering law: flush + admit the tail, THEN one END at the
  //    admitted sample clock, no close; exactly once.
  const mk = s => () => { const l = new StreamLane({ sendFrame: b => s.frames.push(decodeStreamFrame(b)), sendJSON: m => s.msgs.push(m), rate: 16000, channels: 1, mode: 'conversation' }); s.lanes.push(l); l.open(); return l; };
  const s = { frames: [], msgs: [], lanes: [] };
  const conv = new Conversation({ newLane: mk(s), prerollSamples: 0, onEnded: () => {} });
  conv.start(); s.lanes[0].onState({ state: 'open' });
  conv.frame(new Float32Array(320));
  const res = await finishWithTail(conv, async () => { conv.frame(new Float32Array(37).fill(0.5)); return { samples: 37, confirmed: true, reason: '' }; });
  const kinds = s.frames.map(f => f.kind).join(',');
  assert(res.finished && res.confirmed && kinds === '1,1,3', 'the tail is admitted, THEN the one END: ' + kinds);
  assert(s.frames[2].start === 357, 'END names the admitted sample clock INCLUDING the tail: ' + s.frames[2].start);
  assert(s.frames[1].pcm.length === 37 && s.frames[1].pcm[0] === 16383, 'the admitted tail is the flushed partial');
  assert(!s.msgs.some(m => m.voice && m.voice.action === 'close'), 'the half-close does not close');
  const again = await finishWithTail(conv, async () => ({ samples: 5, confirmed: true, reason: '' }));
  assert(again.finished === false && s.frames.length === 3, 'the half-close happens exactly once');

  // 4. A failed flush is REPORTED, and END still names exactly what was
  //    admitted — never a sample invented, never a tail silently counted.
  const s2 = { frames: [], msgs: [], lanes: [] };
  const conv2 = new Conversation({ newLane: mk(s2), prerollSamples: 0, onEnded: () => {} });
  conv2.start(); s2.lanes[0].onState({ state: 'open' });
  conv2.frame(new Float32Array(320));
  const r4 = await finishWithTail(conv2, async () => ({ samples: 0, confirmed: false, reason: 'the capture did not acknowledge' }));
  assert(r4.finished && !r4.confirmed && s2.frames.map(f => f.kind).join(',') === '1,3' && s2.frames[1].start === 320,
    'a failed flush is reported and END names exactly the admitted clock: ' + JSON.stringify(r4) + ' end=' + s2.frames[1].start);
});
</script>`

func TestMicrophoneTailIsFlushedBeforeTheHalfCloseInBrowser(t *testing.T) {
	runPageInEngines(t, voiceTailPage, map[string][]byte{
		"/app.js": []byte("export function toast(m) { (window.__toasts = window.__toasts || []).push(m); }\n"),
	})
}
