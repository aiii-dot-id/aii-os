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
// .
const voiceReceiptPage = `<!doctype html>
<script type="module">
import { assert, run } from './__harness.js';
import { encodeStreamFrame, Playback, voiceEvent, playbackForTest } from './voice.js';

function fakeContext(rate) {
  const ctx = { currentTime: 0, destination: {}, sources: [], rate, sampleRate: rate, state: 'suspended', resumed: 0,
    resume() { ctx.resumed++; ctx.state = 'running'; return Promise.resolve(); },
    createBuffer(ch, frames, r) { return { duration: frames / r, numberOfChannels: ch, length: frames, getChannelData() { return new Float32Array(frames); } }; },
    createBufferSource() { const s = { onended: null, connect() {}, start(at) { s.startedAt = at; }, stop() { s.stopped = true; } }; ctx.sources.push(s); return s; },
    close() {} };
  return ctx;
}

run(async () => {
  // THE PLAYBACK CONTEXT IS MADE IN THE GESTURE AND KEPT (found live
  // from a Pixel: the microphone worked, the reply played into
  // a suspended context): prime creates it once and resumes it; a frame
  // at another rate schedules into the SAME context, never a rebuild.
  const primed = new Playback({ report: () => {}, audioContext: fakeContext });
  assert(primed.prime(48000) && primed.ctx && primed.ctx.sampleRate === 48000 && primed.ctx.state === 'running' && primed.ctx.resumed === 1, 'prime makes the context and wakes it: ' + JSON.stringify(primed.ctx && { state: primed.ctx.state, resumed: primed.ctx.resumed }));
  const kept = primed.ctx;
  primed.reset('vs-1');
  primed.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(1600), 3));
  assert(primed.ctx === kept && primed.rate === 16000 && kept.sources.length === 1, 'a frame at the engine\'s rate schedules into the primed context, never a rebuilt one');
  primed.feed(encodeStreamFrame(24000, 1, 1, 2, 1600, new Int16Array(2400), 3));
  assert(primed.ctx === kept && kept.sources.length === 2, 'a rate change keeps the context too');
  kept.state = 'suspended';
  primed.feed(encodeStreamFrame(24000, 1, 1, 3, 4000, new Int16Array(2400), 3));
  assert(kept.state === 'running' && kept.resumed === 2, 'a frame wakes a context the browser suspended');
  assert(primed.prime() && primed.ctx === kept, 'priming again is idempotent');

  const reports = [];
  const pb = new Playback({ report: r => reports.push(r), audioContext: fakeContext });
  pb.reset('vs-9');
  pb.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(1600), 7));
  pb.feed(encodeStreamFrame(16000, 1, 1, 2, 1600, new Int16Array(1600), 7));
  assert(reports.length === 0, 'nothing is reported before the clock rendered anything');
  const ctx = pb.ctx;
  const t0 = pb.sources[0].startAt;
  ctx.currentTime = t0 + 0.05; // 50 ms into the first source
  pb.progress();
  assert(reports.length === 1 && reports[0].outcome === 'progress' && !reports[0].terminal && reports[0].rendered === 800
    && reports[0].session_id === 'vs-9' && reports[0].stream === 7 && reports[0].rate === 16000 && reports[0].channels === 1,
    'progress is what the clock has passed, under the session identity: ' + JSON.stringify(reports));
  pb.progress();
  assert(reports.length === 1, 'unchanged progress is not repeated');
  pb.feed(encodeStreamFrame(16000, 1, 3, 3, 3200, null, 7));
  assert(reports.length === 1, 'END with sources still live is not yet drained');
  ctx.currentTime = 5;
  ctx.sources[0].onended(); ctx.sources[1].onended();
  assert(reports.length === 2 && reports[1].terminal && reports[1].outcome === 'drained' && reports[1].rendered === 3200,
    'drained once every scheduled source ended: ' + JSON.stringify(reports[1]));
  ctx.sources[0].onended();
  assert(reports.length === 2, 'a repeated end changes nothing');
  assert(pb.rendered(7) === 3200 && pb.stats().reported === 2, 'the stream stands at its whole: ' + JSON.stringify(pb.stats()));

  // A second stream stopped mid-way: stopped with the clock's evidence and
  // tombstoned; a late frame of it reports nothing more.
  pb.feed(encodeStreamFrame(16000, 1, 1, 4, 0, new Int16Array(1600), 8));
  const t1 = pb.sources[0].startAt;
  ctx.currentTime = t1 + 0.025; // 400 samples in
  const n = pb.stop();
  assert(n === 1 && reports.length === 3 && reports[2].outcome === 'stopped' && reports[2].terminal && reports[2].rendered === 400 && reports[2].stream === 8,
    'a stop reports what the clock had rendered, once: ' + JSON.stringify(reports[2]));
  ctx.sources[ctx.sources.length - 1].onended();
  pb.feed(encodeStreamFrame(16000, 1, 1, 5, 1600, new Int16Array(1600), 8));
  assert(reports.length === 3 && pb.stats().scheduled === 4800, 'a stopped stream neither plays nor reports again');

  // A fenced stream that never played is receipted as stopped at zero, once.
  pb.tombstones.add(9);
  pb.feed(encodeStreamFrame(16000, 1, 1, 6, 0, new Int16Array(160), 9));
  pb.feed(encodeStreamFrame(16000, 1, 1, 7, 160, new Int16Array(160), 9));
  assert(reports.length === 4 && reports[3].stream === 9 && reports[3].rendered === 0 && reports[3].outcome === 'stopped' && reports[3].terminal,
    'a fenced stream that never played: stopped at zero, once: ' + JSON.stringify(reports[3]));

  // A new session: its identity travels, and a reused stream id is fresh.
  pb.reset('vs-10');
  pb.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(160), 7));
  pb.feed(encodeStreamFrame(16000, 1, 3, 2, 160, null, 7));
  ctx.currentTime = 9;
  ctx.sources[ctx.sources.length - 1].onended();
  assert(reports.length === 5 && reports[4].session_id === 'vs-10' && reports[4].outcome === 'drained' && reports[4].rendered === 160 && reports[4].stream === 7,
    'a new session reports under its own identity: ' + JSON.stringify(reports[4]));
  // An END with nothing scheduled is a drained stream at zero.
  pb.feed(encodeStreamFrame(16000, 1, 3, 3, 0, null, 11));
  assert(reports.length === 6 && reports[5].stream === 11 && reports[5].rendered === 0 && reports[5].outcome === 'drained', 'an empty reply drains at zero: ' + JSON.stringify(reports[5]));

  // THE STOP'S ORDER: the sources are
  // stopped BEFORE the terminal receipt is sent, and the receipt names what
  // the clock had passed at the stop instant — not a count taken while the
  // audio was still playing.
  {
    const trace = [];
    const p2 = new Playback({ report: r => trace.push(r.outcome + '_receipt:' + r.rendered), audioContext: fakeContext });
    p2.reset('vs-11');
    p2.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(1600), 3));
    const src = p2.sources[0].src;
    src.stop = () => { trace.push('source_stop'); src.stopped = true; };
    p2.ctx.currentTime = p2.sources[0].startAt + 0.025;
    // What the clock had passed at the stop instant, by the page's own
    // arithmetic — not a constant that a float ULP can make a lie.
    const expect = Math.floor((p2.ctx.currentTime - p2.sources[0].startAt) * 16000);
    p2.stop('operator');
    assert(trace.length === 2 && trace[0] === 'source_stop' && trace[1] === 'stopped_receipt:' + expect,
      'the audio stops first, then the receipt names the stop instant (' + expect + '): ' + JSON.stringify(trace));
    if (p2.timer) clearInterval(p2.timer);
  }

  // A REFUSED TERMINAL RECEIPT IS NOT RESOLUTION: the stream goes back to
  // unresolved and the same evidence is re-sent, bounded; what is still
  // refused when the tries run out is said out loud, never resolved quietly.
  {
    const sent = [];
    const said = [];
    const p3 = new Playback({ report: r => sent.push(r), audioContext: fakeContext, onUnresolved: (s, why) => said.push(s + ':' + why) });
    p3.retryBase = 10; p3.retryLimit = 2;
    p3.reset('vs-12');
    p3.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(160), 5));
    p3.ctx.currentTime = p3.sources[0].startAt + 0.01;
    p3.stop('operator');
    assert(sent.length === 1 && sent[0].terminal && sent[0].outcome === 'stopped', 'the stop reports once: ' + JSON.stringify(sent));
    const st = p3.streams.get(5);
    p3.receiptRefused('vs-12', 5, 'output still live');
    assert(st.terminal === false, 'a refused receipt leaves the stream UNRESOLVED');
    await new Promise(r => setTimeout(r, 60));
    assert(sent.length === 2 && sent[1].rendered === sent[0].rendered && sent[1].terminal, 'the same evidence is re-sent: ' + JSON.stringify(sent));
    assert(st.terminal === true, 'the re-sent report stands until it too is refused');
    // A refusal from ANOTHER session touches nothing, whatever stream
    // number it names.
    p3.receiptRefused('some-older-session', 5, 'delayed old refusal');
    assert(st.terminal === true && p3.stats().foreign === 1, 'a foreign session refusal changes no state: ' + JSON.stringify(p3.stats()));
    await new Promise(r => setTimeout(r, 40));
    assert(sent.length === 2, 'and schedules no retry: ' + sent.length);
    p3.receiptRefused('vs-12', 5, 'output still live');
    await new Promise(r => setTimeout(r, 80));
    assert(sent.length === 3, 'bounded retries continue: ' + sent.length);
    p3.receiptRefused('vs-12', 5, 'output still live');
    await new Promise(r => setTimeout(r, 80));
    assert(sent.length === 3 && said.length === 1 && /output still live/.test(said[0]) && p3.stats().unresolved === 1,
      'when the tries run out the stream stays unresolved and says so: ' + JSON.stringify([sent.length, said, p3.stats().unresolved]));
    if (p3.timer) clearInterval(p3.timer);
  }

  // The page answers the host's refusal through the event it actually
  // receives, naming the stream that owes another report.
  {
    const live = playbackForTest();
    live.reset('vs-13');
    live.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(160), 4));
    live.stop('operator');
    const st = live.streams.get(4);
    assert(st && st.terminal, 'the shipped page reports its stop');
    voiceEvent({ type: 'receipt_refused', session_id: 'vs-13', stream: 4, reason: 'output still live' });
    assert(st.terminal === false, 'the shipped page takes the refusal back to unresolved');
  }

  // THE REVIEW'S P1, end to end on the shipped module: a new worker's
  // session reuses stream number one; the OLD session's delayed refusal
  // arrives; the settled receipt stands and no retry is owed. And a reset
  // retires the timers the previous session left behind.
  {
    const live = playbackForTest();
    live.reset('old-worker-session', 24000, 1);
    live.terminate(1, 120, 'stopped');
    live.receiptRefused('old-worker-session', 1, 'output still live'); // a retry is owed...
    live.reset('new-worker-session', 24000, 1);                        // ...and the session ends
    assert(live.timers.size === 0, 'reset retires the previous session\'s timers');
    live.terminate(1, 240, 'drained');
    const current = live.streams.get(1);
    live.receiptRefused('old-worker-session', 1, 'delayed old refusal');
    assert(current.terminal === true && current.report.rendered === 240 && live.timers.size === 0,
      'an old session\'s refusal must not reopen a new session\'s receipt: ' + JSON.stringify([current.terminal, current.report, live.timers.size]));
    await new Promise(r => setTimeout(r, 60));
    assert(current.terminal === true, 'and no retry fires for it later');
  }
});
</script>`

func TestPlaybackReceiptsInBrowser(t *testing.T) {
	runPageInEngines(t, voiceReceiptPage, map[string][]byte{
		"/app.js": []byte("export function toast(m) { (window.__toasts = window.__toasts || []).push(m); }\n"),
	})
}

// .
// .
// .
const voiceHushOrderPage = `<!doctype html>
<div id="voice-transcript"></div>
<script type="module">
import { assert, run } from './__harness.js';
import { encodeStreamFrame, bindTransport, sessionState, receiveFrame, hush, playbackForTest, lanesForTest } from './voice.js';

run(() => {
  const trace = [];
  bindTransport(() => {}, m => trace.push(m.voice.action === 'playback' ? m.voice.playback.outcome + '_receipt' : m.voice.action + '_control'));
  // The page's own lane list, as a live session leaves it.
  lanesForTest().push({ isOpen: true, state: 'open', onState() {}, over() { return false; } });
  sessionState({ state: 'open', session_id: 'vs-1' });
  const pb = playbackForTest();
  receiveFrame(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(1600), 7));
  const before = trace.length;
  hush();
  const after = trace.slice(before);
  const fence = after.indexOf('interrupt_control');
  const receipt = after.findIndex(x => /_receipt$/.test(x));
  assert(fence >= 0 && receipt >= 0 && fence < receipt,
    'the engine fence is enqueued before the stop receipt: ' + JSON.stringify(after));
  if (pb.timer) clearInterval(pb.timer);
});
</script>`

func TestHushAsksForTheFenceBeforeItsReceiptInBrowser(t *testing.T) {
	runPageInEngines(t, voiceHushOrderPage, map[string][]byte{
		"/app.js": []byte("export function toast(m) { (window.__toasts = window.__toasts || []).push(m); }\n"),
	})
}
