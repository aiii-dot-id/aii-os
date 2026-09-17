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
const voiceStreamPage = `<!doctype html>
<button id="mic"></button>
<script type="module">
import { assert, run } from './__harness.js';
import { encodeStreamFrame, decodeStreamFrame, StreamLane, Playback, STREAM_QUEUE } from './voice.js';

run(() => {
  const pcm = new Int16Array([1, -1, 32767, -32768]);
  const d = decodeStreamFrame(encodeStreamFrame(16000, 1, 1, 7, 320, pcm));
  assert(d && d.rate === 16000 && d.channels === 1 && d.kind === 1 && d.seq === 7 && d.start === 320
    && d.pcm.length === 4 && d.pcm[2] === 32767 && d.pcm[3] === -32768,
    'the codec does not round-trip: ' + JSON.stringify(d));

  // 1. RELEASE BEFORE OPEN: the operator lets go before the host has
  //    opened the session. Nothing is sent yet — and nothing is lost:
  //    the open flushes the audio and its exact end, then the drain.
  {
    const frames = [], msgs = [];
    const lane = new StreamLane({ sendFrame: b => frames.push(b), sendJSON: m => msgs.push(m), rate: 16000, channels: 1, mode: 'meeting' });
    lane.open();
    lane.chunk(new Float32Array(320));
    lane.chunk(new Float32Array(320));
    lane.end();
    assert(frames.length === 0, 'nothing is sent before the host opens the session');
    assert(msgs.length === 1 && msgs[0].voice.action === 'open', 'the close must wait for the open: ' + JSON.stringify(msgs));
    lane.onState({ state: 'open', session_id: 'vs-1' });
    assert(frames.length === 3, 'the open flushes two frames and the end: ' + frames.length);
    const end = decodeStreamFrame(frames[2]);
    assert(end.kind === 3 && end.start === 640, 'the end names the exact sample: ' + JSON.stringify(end));
    assert(msgs.length === 2 && msgs[1].voice.action === 'close', 'the drain follows the flush: ' + JSON.stringify(msgs));
    let closed = false; lane.onClosed = () => { closed = true; };
    lane.onState({ state: 'closed' });
    assert(closed && lane.over(), 'the lane ends on the host’s word');
  }

  // 2. THE QUEUE IS BOUNDED WHOLE. 128 chunks before the open leave at
  //    most STREAM_QUEUE entries; the loss is one declaration, carried
  //    by the oldest surviving frame; the end is never dropped; the
  //    sequence counts what goes to the wire.
  {
    const frames = [], msgs = [];
    const lane = new StreamLane({ sendFrame: b => frames.push(b), sendJSON: m => msgs.push(m), rate: 16000, channels: 1, mode: 'meeting' });
    lane.open();
    for (let i = 0; i < 128; i++) lane.chunk(new Float32Array(320));
    assert(lane.pending.length <= STREAM_QUEUE, 'the queue grew past its bound: ' + lane.pending.length);
    lane.end();
    assert(lane.pending.length <= STREAM_QUEUE, 'the end must not grow the queue past its bound: ' + lane.pending.length);
    assert(lane.pending[lane.pending.length - 1].kind === 3, 'the end is kept under saturation');
    assert(lane.pending.filter(e => e.gap).length === 1 && lane.pending[0].gap, 'one declaration, on the oldest surviving frame');
    assert(lane.dropped === 128 - (STREAM_QUEUE - 1), 'dropped audio is counted: ' + lane.dropped);
    lane.onState({ state: 'open' });
    const sent = frames.map(decodeStreamFrame);
    assert(sent.length === STREAM_QUEUE + 1, 'the bound plus one declaration went to the wire: ' + sent.length);
    assert(sent[0].kind === 2 && sent[1].kind === 1 && sent[0].start === sent[1].start, 'the gap is declared where audio resumes: ' + JSON.stringify([sent[0], sent[1].start]));
    assert(sent.filter(f => f.kind === 2).length === 1, 'exactly one declaration');
    assert(sent.every((f, i) => f.seq === i + 1), 'the sequence counts the wire');
    const end = sent[sent.length - 1];
    assert(end.kind === 3 && end.start === 128 * 320, 'the end names the whole clock, dropped audio included: ' + JSON.stringify(end));
    assert(msgs.length === 2 && msgs[1].voice.action === 'close', 'the drain follows the flush');
  }

  // 3. INTERRUPTION stops the engine's scheduled audio at once and
  //    fences the stream it stopped: a late frame of that stream is
  //    TOMBSTONES it: that stream never re-enters playback — not on a
  //    reused id, not after its own end, not after a newer reply has
  // begun. A genuinely new reply still
  //    plays at once, without waiting on the old stream.
  {
    const pb = new Playback();
    pb.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(16000), 7));
    pb.feed(encodeStreamFrame(16000, 1, 1, 2, 16000, new Int16Array(16000), 7));
    assert(pb.stats().scheduled === 32000 && pb.sources.length === 2, 'two seconds scheduled: ' + JSON.stringify(pb.stats()));
    const n = pb.stop();
    assert(n === 2 && pb.sources.length === 0 && !pb.playing(), 'the interruption stops every scheduled source: ' + n);
    pb.feed(encodeStreamFrame(16000, 1, 1, 3, 32000, new Int16Array(16000), 7));
    assert(pb.stats().scheduled === 32000 && pb.stats().received === 48000, 'a late frame of the cancelled stream is fenced: ' + JSON.stringify(pb.stats()));
    pb.feed(encodeStreamFrame(16000, 1, 3, 4, 48000, null, 7));
    pb.feed(encodeStreamFrame(16000, 1, 1, 5, 0, new Int16Array(8000), 7));
    assert(pb.stats().scheduled === 32000, 'the cancelled stream cannot revive after its own end on a reused id: ' + JSON.stringify(pb.stats()));
    pb.feed(encodeStreamFrame(16000, 1, 1, 6, 0, new Int16Array(8000), 8));
    assert(pb.stats().scheduled === 40000 && pb.sources.length === 1 && pb.sources[0].stream === 8, 'a new reply plays at once while the old stream stays fenced: ' + JSON.stringify(pb.stats()));
    pb.feed(encodeStreamFrame(16000, 1, 1, 7, 8000, new Int16Array(8000), 7));
    assert(pb.stats().scheduled === 40000, 'the cancelled stream cannot re-enter even after a newer reply began: ' + JSON.stringify(pb.stats()));
    // A NEW session (sessionState open -> reset) clears the fence, so a
    // fresh session may reuse an id the last one tombstoned.
    pb.reset();
    pb.feed(encodeStreamFrame(16000, 1, 1, 8, 0, new Int16Array(8000), 7));
    assert(pb.stats().scheduled === 48000 && pb.sources[pb.sources.length - 1].stream === 7, 'a new session clears the fence — a reused id plays again: ' + JSON.stringify(pb.stats()));
  }

  // 4. END IS NOT COMPLETION: a reply that follows an END plays after the
  //    previous one, never over it.
  {
    const pb = new Playback();
    pb.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(16000), 1));
    const firstEnd = pb.at;
    pb.feed(encodeStreamFrame(16000, 1, 3, 2, 16000, null, 1));
    pb.feed(encodeStreamFrame(16000, 1, 1, 3, 0, new Int16Array(16000), 2));
    const second = pb.sources[pb.sources.length - 1];
    assert(pb.sources.length === 2 && second.startAt >= firstEnd - 1e-6, 'the second reply starts at ' + second.startAt + ', before the first ends at ' + firstEnd);
    assert(pb.playing(), 'the timeline is preserved until the audio has played');
  }
});
</script>`

func TestVoiceStreamingLaneInBrowser(t *testing.T) {
	runPageInEngines(t, voiceStreamPage, map[string][]byte{
		"/app.js": []byte("export function toast(m) { (window.__toasts = window.__toasts || []).push(m); }\n"),
	})
}
