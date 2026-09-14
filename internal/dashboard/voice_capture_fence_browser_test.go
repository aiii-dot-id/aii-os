//go:build !windows

package dashboard

import "testing"

// .
// .
// .
// .
// .
// .
const voiceCaptureFencePage = `<!doctype html>
<script type="module">
import { assert, run } from './__harness.js';
import { captureMessage, flushPort, captureFenceForTest, voiceEvent, playbackForTest, encodeStreamFrame } from './voice.js';

run(async () => {
  const fence = captureFenceForTest();
  const e0 = fence.epoch();
  const got = [];
  captureMessage(new Float32Array(3).buffer, f => got.push(f.length), e0);
  assert(got.length === 1 && got[0] === 3, 'a live capture frame is admitted');

  // A flush in flight when the capture is retired (abort + replacement):
  // it resolves unconfirmed, and the retired epoch is gone.
  let flushId = null;
  const pending = flushPort({ postMessage(m) { flushId = m.flush; } }, 500);
  fence.retire();
  const r = await pending;
  assert(!r.confirmed && /torn down/.test(r.reason), 'the retired capture flush resolves unconfirmed: ' + JSON.stringify(r));
  assert(fence.epoch() !== e0, 'teardown retires the capture epoch');

  // Late messages from the retired port — a frame, then the acknowledged
  // partial — reach nothing.
  const staleBefore = fence.stale();
  captureMessage(new Float32Array(5).buffer, f => got.push(f.length), e0);
  captureMessage({ ack: flushId, samples: 7, buffer: new Float32Array(7).buffer }, f => got.push(f.length), e0);
  assert(got.length === 1, 'a retired capture feeds nothing: ' + got.join(','));
  assert(fence.stale() === staleBefore + 2, 'the rejections are counted: ' + (fence.stale() - staleBefore));

  // A current-epoch acknowledgement nobody awaits (its flush timed out and
  // the cutoff was named without it) admits no partial either.
  captureMessage({ ack: 9999, samples: 4, buffer: new Float32Array(4).buffer }, f => got.push(f.length), fence.epoch());
  assert(got.length === 1, 'an unawaited ack admits nothing after the cutoff');

  // The replacement capture is fed normally, and its own flush confirms.
  captureMessage(new Float32Array(2).buffer, f => got.push(f.length), fence.epoch());
  assert(got.length === 2 && got[1] === 2, 'the replacement capture is fed');
  const live = fence.epoch();
  const port2 = { postMessage(m) { setTimeout(() => captureMessage({ ack: m.flush, samples: 9, buffer: new Float32Array(9).buffer }, f => got.push(f.length), live), 0); } };
  const r2 = await flushPort(port2, 500);
  assert(r2.confirmed && r2.samples === 9 && got.length === 3 && got[2] === 9, 'the live capture flush admits its partial then confirms: ' + JSON.stringify([r2, got]));

  // The engine's VAD fence: interruption_requested stops what the page
  // still holds of the fenced reply and tombstones its stream.
  const pb = playbackForTest();
  pb.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(16000), 11));
  assert(pb.stats().scheduled === 16000, 'a reply is playing from the page queue');
  voiceEvent({ type: 'interruption_requested', session_id: 'vs-1' });
  const st = pb.stats();
  assert(st.stopped === 1 && st.tombstoned >= 1 && !st.playing, 'the fenced reply is stopped and tombstoned: ' + JSON.stringify(st));
  pb.feed(encodeStreamFrame(16000, 1, 1, 2, 16000, new Int16Array(8000), 11));
  assert(pb.stats().scheduled === 16000, 'a late frame of the fenced stream is dropped');
});
</script>`

func TestCaptureInstanceFenceInBrowser(t *testing.T) {
	runPageInEngines(t, voiceCaptureFencePage, map[string][]byte{
		"/app.js": []byte("export function toast(m) { (window.__toasts = window.__toasts || []).push(m); }\n"),
	})
}
