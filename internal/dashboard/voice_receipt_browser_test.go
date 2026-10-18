//go:build !windows

package dashboard

import "testing"

const voiceReceiptPage = `<!doctype html>
<script type="module">
import { assert, run, until } from './__harness.js';
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

  pb.feed(encodeStreamFrame(16000, 1, 1, 4, 0, new Int16Array(1600), 8));
  const t1 = pb.sources[0].startAt;
  ctx.currentTime = t1 + 0.025; // 400 samples in
  const n = pb.stop();
  assert(n === 1 && reports.length === 3 && reports[2].outcome === 'stopped' && reports[2].terminal && reports[2].rendered === 400 && reports[2].stream === 8,
    'a stop reports what the clock had rendered, once: ' + JSON.stringify(reports[2]));
  ctx.sources[ctx.sources.length - 1].onended();
  pb.feed(encodeStreamFrame(16000, 1, 1, 5, 1600, new Int16Array(1600), 8));
  assert(reports.length === 3 && pb.stats().scheduled === 4800, 'a stopped stream neither plays nor reports again');

  pb.tombstones.add(9);
  pb.feed(encodeStreamFrame(16000, 1, 1, 6, 0, new Int16Array(160), 9));
  pb.feed(encodeStreamFrame(16000, 1, 1, 7, 160, new Int16Array(160), 9));
  assert(reports.length === 4 && reports[3].stream === 9 && reports[3].rendered === 0 && reports[3].outcome === 'stopped' && reports[3].terminal,
    'a fenced stream that never played: stopped at zero, once: ' + JSON.stringify(reports[3]));

  pb.reset('vs-10');
  pb.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(160), 7));
  pb.feed(encodeStreamFrame(16000, 1, 3, 2, 160, null, 7));
  ctx.currentTime = 9;
  ctx.sources[ctx.sources.length - 1].onended();
  assert(reports.length === 5 && reports[4].session_id === 'vs-10' && reports[4].outcome === 'drained' && reports[4].rendered === 160 && reports[4].stream === 7,
    'a new session reports under its own identity: ' + JSON.stringify(reports[4]));
  pb.feed(encodeStreamFrame(16000, 1, 3, 3, 0, null, 11));
  assert(reports.length === 6 && reports[5].stream === 11 && reports[5].rendered === 0 && reports[5].outcome === 'drained', 'an empty reply drains at zero: ' + JSON.stringify(reports[5]));

  for (const how of ['stop', 'retire', 'rate-change']) {
    const sent = [];
    const p4 = new Playback({ report: r => sent.push(r), audioContext: fakeContext });
    p4.reset('vs-14');
    p4.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(1600), 6));
    p4.ctx.currentTime = 5;
    p4.ctx.sources[0].onended(); // the buffer finished: everything held has played
    const terminals = () => sent.filter(r => r.terminal);
    assert(terminals().length === 0, how + ': no terminal receipt before END or a stop: ' + JSON.stringify(sent));
    if (how === 'stop') p4.stop('operator');
    else if (how === 'retire') p4.retire(6);
    else p4.stop('rate-change');
    if (how === 'rate-change') {
      assert(terminals().length === 0, 'a rate change settles no stream that may still drain: ' + JSON.stringify(sent));
    } else {
      assert(terminals().length === 1 && terminals()[0].outcome === 'stopped' && terminals()[0].rendered === 1600 && terminals()[0].stream === 6,
        how + ' after the buffer finished receipts what played: ' + JSON.stringify(sent));
    }
    p4.feed(encodeStreamFrame(16000, 1, 3, 2, 1600, null, 6)); // the END, late
    const t = terminals();
    assert(t.length === 1 && p4.streams.get(6).terminal && t[0].rendered === 1600
      && t[0].outcome === (how === 'rate-change' ? 'drained' : 'stopped'),
      how + ': one terminal receipt, whatever the late END: ' + JSON.stringify(sent));
    if (p4.timer) clearInterval(p4.timer);
  }
  {
    const sent = [];
    const p5 = new Playback({ report: r => sent.push(r), audioContext: fakeContext });
    p5.retryBase = 100000;
    p5.reset('vs-15');
    p5.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(160), 12));
    p5.feed(encodeStreamFrame(16000, 1, 3, 2, 160, null, 12));
    p5.ctx.currentTime = 5;
    p5.ctx.sources[0].onended();
    assert(sent.filter(r => r.terminal).length === 1, 'drained once: ' + JSON.stringify(sent));
    p5.receiptRefused('vs-15', 12, 'output still live');
    p5.stop('operator');
    assert(sent.filter(r => r.terminal).length === 1 && p5.streams.get(12).terminal === false,
      'a stop does not re-send a refused receipt ahead of its retry: ' + JSON.stringify(sent));
    for (const t of p5.timers) clearTimeout(t);
    if (p5.timer) clearInterval(p5.timer);
  }

  for (const reused of [true, false]) {
    const sent = [];
    const p6 = new Playback({ report: r => sent.push(r), audioContext: fakeContext });
    p6.reset('old-session');
    p6.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(1600), 1));
    p6.feed(encodeStreamFrame(16000, 1, 3, 2, 1600, null, 1)); // the old reply is whole, still buffered
    const old = p6.sources[0];
    p6.reset('new-session'); // the successor opens before the old audio has played
    const id = reused ? 1 : 2;
    p6.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(1600), id));
    const next = p6.sources[p6.sources.length - 1];
    assert(!old.src.stopped, 'the old tail plays on after the successor opens');
    p6.ctx.currentTime = old.until; // the old audio has played; the successor's not yet
    old.src.onended();
    p6.progress();
    const forNew = () => sent.filter(r => r.session_id === 'new-session');
    assert(forNew().every(r => r.rendered === 0 && !r.terminal) && p6.rendered(id) === 0,
      (reused ? 'reused' : 'distinct') + ' stream: the old audio was credited to the successor: ' + JSON.stringify([sent, p6.rendered(id)]));
    assert(sent.every(r => r.session_id !== 'old-session'), 'the ended session reported after its end: ' + JSON.stringify(sent));
    if (reused) {
      p6.ctx.currentTime = next.startAt;
      p6.stop('operator');
      const t = forNew().filter(r => r.terminal);
      assert(t.length === 1 && t[0].outcome === 'stopped' && t[0].rendered === 0 && t[0].stream === 1,
        'the successor\'s stop reports what IT played: ' + JSON.stringify(sent));
    } else {
      p6.feed(encodeStreamFrame(16000, 1, 3, 2, 1600, null, id));
      p6.ctx.currentTime = next.until;
      next.src.onended();
      const t = forNew().filter(r => r.terminal);
      assert(t.length === 1 && t[0].outcome === 'drained' && t[0].rendered === 1600,
        'the successor drains on its own audio: ' + JSON.stringify(sent));
    }
    if (p6.timer) clearInterval(p6.timer);
  }

  {
    const trace = [];
    const p2 = new Playback({ report: r => trace.push(r.outcome + '_receipt:' + r.rendered), audioContext: fakeContext });
    p2.reset('vs-11');
    p2.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(1600), 3));
    const src = p2.sources[0].src;
    src.stop = () => { trace.push('source_stop'); src.stopped = true; };
    p2.ctx.currentTime = p2.sources[0].startAt + 0.025;
    const expect = Math.floor((p2.ctx.currentTime - p2.sources[0].startAt) * 16000);
    p2.stop('operator');
    assert(trace.length === 2 && trace[0] === 'source_stop' && trace[1] === 'stopped_receipt:' + expect,
      'the audio stops first, then the receipt names the stop instant (' + expect + '): ' + JSON.stringify(trace));
    if (p2.timer) clearInterval(p2.timer);
  }

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
    await until(() => sent.length === 2, 'the refused report was never re-sent');
    assert(sent.length === 2 && sent[1].rendered === sent[0].rendered && sent[1].terminal, 'the same evidence is re-sent: ' + JSON.stringify(sent));
    assert(st.terminal === true, 'the re-sent report stands until it too is refused');
    p3.receiptRefused('some-older-session', 5, 'delayed old refusal');
    assert(st.terminal === true && p3.stats().foreign === 1, 'a foreign session refusal changes no state: ' + JSON.stringify(p3.stats()));
    await new Promise(r => setTimeout(r, 40));
    assert(sent.length === 2, 'and schedules no retry: ' + sent.length);
    p3.receiptRefused('vs-12', 5, 'output still live');
    await until(() => sent.length === 3, 'the next bounded retry never came');
    assert(sent.length === 3, 'bounded retries continue: ' + sent.length);
    p3.receiptRefused('vs-12', 5, 'output still live');
    // waits by design: no retry may fire after the terminal refusal; wait beyond the retry interval to verify that absence.
    await new Promise(r => setTimeout(r, 80));
    assert(sent.length === 3 && said.length === 1 && /output still live/.test(said[0]) && p3.stats().unresolved === 1,
      'when the tries run out the stream stays unresolved and says so: ' + JSON.stringify([sent.length, said, p3.stats().unresolved]));
    if (p3.timer) clearInterval(p3.timer);
  }

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

  {
    const live = playbackForTest();
    live.reset('buffered-session');
    live.ctx = fakeContext(16000);
    live.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(1600), 21));
    live.feed(encodeStreamFrame(16000, 1, 3, 2, 1600, null, 21));
    const src = live.sources[0];
    live.ctx.currentTime = src.startAt + 0.025;
    const rendered = live.rendered(21);
    for (const type of ['speech_start', 'interruption_requested', 'synthesis_start']) {
      voiceEvent({type, session_id:'another-session', stream:99});
      assert(!src.src.stopped && !live.streams.get(21).terminal, 'a foreign '+type+' retired this page\'s reply');
    }
    voiceEvent({type:'speech_start', session_id:'buffered-session'});
    const receipt = live.streams.get(21).report;
    assert(src.src.stopped && live.streams.get(21).terminal && receipt.outcome === 'stopped'
      && receipt.rendered === rendered && rendered > 0 && rendered < 1600,
      'speech did not stop and truthfully receipt the buffered tail: '+JSON.stringify(receipt));
    const scheduled = live.stats().scheduled;
    live.feed(encodeStreamFrame(16000, 1, 1, 3, 1600, new Int16Array(160), 21));
    assert(live.stats().scheduled === scheduled, 'a late stopped tail was played again');
    voiceEvent({type:'synthesis_start', session_id:'buffered-session', stream:22});
    live.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(160), 22));
    assert(live.sources.length === 1 && live.stats().scheduled === scheduled+160, 'the following reply cannot play');
    live.reset('after-buffered-proof');
  }

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
    // waits by design: no retry may fire after the terminal refusal; wait beyond the retry interval to verify that absence.
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

const voiceHushOrderPage = `<!doctype html>
<div id="voice-transcript"></div>
<script type="module">
import { assert, run, until } from './__harness.js';
import { encodeStreamFrame, bindTransport, sessionState, receiveFrame, hush, playbackForTest, lanesForTest } from './voice.js';

run(() => {
  const trace = [];
  bindTransport(() => {}, m => trace.push(m.voice.action === 'playback' ? m.voice.playback.outcome + '_receipt' : m.voice.action + '_control'));
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

const voiceReplaceReceiptPage = `<!doctype html>
<script type="module">
import { assert, run } from './__harness.js';
import { encodeStreamFrame, bindTransport, voiceEvent, playbackForTest } from './voice.js';

function fakeContext(rate) {
  const ctx = { currentTime: 0, destination: {}, sources: [], rate, sampleRate: rate, state: 'running',
    resume() { return Promise.resolve(); },
    createBuffer(ch, frames, r) { return { duration: frames / r, numberOfChannels: ch, length: frames, getChannelData() { return new Float32Array(frames); } }; },
    createBufferSource() { const s = { onended: null, connect() {}, start(at) { s.startedAt = at; }, stop() { s.stopped = true; } }; ctx.sources.push(s); return s; },
    close() {} };
  return ctx;
}

run(() => {
  let sent = [];
  bindTransport(() => {}, m => { if (m.type === 'voice_session' && m.voice.action === 'playback') sent.push(m.voice.playback); });
  for (const stopFirst of [false, true]) {
    const label = stopFirst ? 'after the stop observation: ' : 'announced alone: ';
    const sid = stopFirst ? 'replace-after-stop' : 'replace-announced';
    sent = [];
    const live = playbackForTest();
    live.reset(sid, 16000, 1);
    live.ctx = fakeContext(16000);
    voiceEvent({ type: 'synthesis_start', session_id: sid, synthesis_id: 'first', stream: 31 });
    for (let i = 0; i < 3; i++) live.feed(encodeStreamFrame(16000, 1, 1, i + 1, i * 1600, new Int16Array(1600), 31));
    live.feed(encodeStreamFrame(16000, 1, 3, 4, 4800, null, 31));
    const [whole, part, unplayed] = live.sources;
    live.ctx.currentTime = part.startAt + 0.03125;
    whole.src.onended();
    const played = 1600 + Math.floor((live.ctx.currentTime - part.startAt) * 16000);
    assert(played > 1600 && played < 3200, label + 'fixture is not part-played: ' + played);
    live.progress();
    assert(sent.length === 1 && !sent[0].terminal && sent[0].rendered === played,
      label + 'progress names what the clock passed: ' + JSON.stringify(sent));
    if (stopFirst) voiceEvent({ type: 'interruption_requested', session_id: sid, synthesis_id: 'first', reason: 'stop_playback' });
    voiceEvent({ type: 'synthesis_start', session_id: sid, synthesis_id: 'second', stream: 32 });
    const old = sent.filter(r => r.stream === 31);
    const terminal = old.filter(r => r.terminal);
    assert(terminal.length === 1 && terminal[0].outcome === 'stopped' && terminal[0].rendered === played,
      label + 'the replaced reply is receipted once, stopped, with what had played (' + played + '): ' + JSON.stringify(old));
    for (let i = 1; i < old.length; i++) {
      assert(old[i].rendered >= old[i - 1].rendered, label + 'receipts went backward: ' + JSON.stringify(old));
    }
    assert(part.src.stopped && unplayed.src.stopped && live.streams.get(31).report.rendered === played,
      label + 'the replaced audio stops, and the kept report is the true one: ' + JSON.stringify(live.streams.get(31).report));
    live.feed(encodeStreamFrame(16000, 1, 1, 1, 0, new Int16Array(1600), 32));
    live.feed(encodeStreamFrame(16000, 1, 3, 2, 1600, null, 32));
    live.feed(encodeStreamFrame(16000, 1, 1, 5, 4800, new Int16Array(1600), 31));
    assert(live.sources.length === 1 && live.sources[0].stream === 32 && live.sources[0].startAt < part.startAt + 0.1,
      label + 'the replacement did not play next: ' + JSON.stringify(live.sources.map(e => [e.stream, e.startAt])));
    live.ctx.currentTime = live.sources[0].until;
    live.sources[0].src.onended();
    const next = sent.filter(r => r.stream === 32 && r.terminal);
    assert(next.length === 1 && next[0].outcome === 'drained' && next[0].rendered === 1600,
      label + 'the replacement drains whole: ' + JSON.stringify(sent));
    assert(sent.filter(r => r.stream === 31).length === old.length, label + 'the old reply reported again: ' + JSON.stringify(sent));
    live.reset('after-' + sid);
  }
});
</script>`

func TestAReplacedReplyIsReceiptedWithWhatItPlayedInBrowser(t *testing.T) {
	runPageInEngines(t, voiceReplaceReceiptPage, map[string][]byte{
		"/app.js": []byte("export function toast(m) { (window.__toasts = window.__toasts || []).push(m); }\n"),
	})
}
