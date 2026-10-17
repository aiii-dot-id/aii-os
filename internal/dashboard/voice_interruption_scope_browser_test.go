//go:build !windows

package dashboard

import "testing"

const voiceInterruptionScopePage = `<!doctype html>
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
  const pcm = n => new Int16Array(n);
  const echo = sid => ({ session_id: sid, type: 'interruption_requested', stream: 1 });
  const vad = sid => ({ session_id: sid, type: 'interruption_requested' });
  const terminal = s => sent.filter(r => r.stream === s && r.terminal);
  const of = (live, s) => live.sources.filter(e => e.stream === s);

  function begin(sid) {
    sent = [];
    const live = playbackForTest();
    live.reset(sid, 16000, 1);
    live.ctx = fakeContext(16000);
    voiceEvent({ type: 'synthesis_start', session_id: sid, stream: 1 });
    for (let i = 0; i < 3; i++) live.feed(encodeStreamFrame(16000, 1, 1, i + 1, i * 1600, pcm(1600), 1));
    live.feed(encodeStreamFrame(16000, 1, 3, 4, 4800, null, 1));
    const [whole, part, unplayed] = live.sources;
    live.ctx.currentTime = part.startAt + 0.03125;
    whole.src.onended();
    const played = 1600 + Math.floor((live.ctx.currentTime - part.startAt) * 16000);
    assert(played > 1600 && played < 3200, sid + ': fixture is not part-played: ' + played);
    return { live, played, part, unplayed };
  }
  function predecessorStopped(label, b) {
    const t1 = terminal(1);
    assert(t1.length === 1 && t1[0].outcome === 'stopped' && t1[0].rendered === b.played,
      label + 'the fenced reply is receipted once, stopped, with what had played (' + b.played + '): ' + JSON.stringify(sent.filter(r => r.stream === 1)));
    assert(b.part.src.stopped && b.unplayed.src.stopped && b.live.tombstones.has(1) && of(b.live, 1).length === 0,
      label + 'the fenced reply\'s buffered audio did not stop');
  }
  function successorLives(label, live) {
    assert(!live.tombstones.has(2), label + 'the new reply was tombstoned: ' + JSON.stringify([...live.tombstones]));
    const two = of(live, 2);
    assert(two.length > 0 && two.every(e => !e.done && !e.src.stopped), label + 'the new reply\'s audio was stopped: ' + JSON.stringify(two.map(e => [e.done, !!e.src.stopped])));
    assert(terminal(2).length === 0, label + 'the new reply was receipted before it played: ' + JSON.stringify(terminal(2)));
  }
  function successorInOrder(label, live) {
    const two = of(live, 2);
    const due = live.ctx.currentTime + 0.02 + 1e-9;
    assert(two[0].startAt <= due, label + 'the new reply waits behind audio nobody will hear: starts ' + two[0].startAt + ', due by ' + due);
    for (let i = 1; i < two.length; i++) {
      assert(Math.abs(two[i].startAt - two[i - 1].until) < 1e-9, label + 'the new reply plays out of order: ' + JSON.stringify(two.map(e => [e.startAt, e.until])));
    }
  }
  function drains(label, live, want) {
    live.ctx.currentTime = 1e6;
    for (const e of of(live, 2)) e.src.onended();
    const t2 = terminal(2);
    assert(t2.length === 1 && t2[0].outcome === 'drained' && t2[0].rendered === want,
      label + 'the new reply did not drain whole (' + want + '): ' + JSON.stringify(sent.filter(r => r.stream === 2)));
  }

  {
    const label = 'new audio first: ', sid = 'delayed-echo';
    const b = begin(sid), live = b.live;
    live.feed(encodeStreamFrame(16000, 1, 1, 1, 0, pcm(1600), 2));
    voiceEvent(echo(sid));
    successorLives(label, live);
    predecessorStopped(label, b);
    successorInOrder(label, live);
    voiceEvent({ type: 'synthesis_start', session_id: sid, stream: 2 });
    live.feed(encodeStreamFrame(16000, 1, 1, 2, 1600, pcm(1600), 2));
    live.feed(encodeStreamFrame(16000, 1, 3, 3, 3200, null, 2));
    successorLives(label, live);
    successorInOrder(label, live);
    voiceEvent(echo(sid));
    voiceEvent(echo(sid));
    successorLives(label + 'repeated echo: ', live);
    assert(terminal(1).length === 1, label + 'a repeated echo receipted the old reply again: ' + JSON.stringify(sent.filter(r => r.stream === 1)));
    drains(label, live, 3200);
    live.reset('after-' + sid);
  }

  {
    const label = 'echo first: ', sid = 'echo-first';
    const b = begin(sid), live = b.live;
    voiceEvent(echo(sid));
    predecessorStopped(label, b);
    voiceEvent({ type: 'synthesis_start', session_id: sid, stream: 2 });
    live.feed(encodeStreamFrame(16000, 1, 1, 1, 0, pcm(1600), 2));
    live.feed(encodeStreamFrame(16000, 1, 3, 2, 1600, null, 2));
    successorLives(label, live);
    successorInOrder(label, live);
    assert(terminal(1).length === 1, label + 'the start retired the old reply a second time: ' + JSON.stringify(sent.filter(r => r.stream === 1)));
    drains(label, live, 1600);
    live.reset('after-' + sid);
  }

  {
    const label = 'barge-in: ', sid = 'barge-in';
    const b = begin(sid), live = b.live;
    voiceEvent(echo(sid));
    voiceEvent({ type: 'synthesis_start', session_id: sid, stream: 2 });
    live.feed(encodeStreamFrame(16000, 1, 1, 1, 0, pcm(1600), 2));
    live.feed(encodeStreamFrame(16000, 1, 1, 2, 1600, pcm(1600), 2));
    live.feed(encodeStreamFrame(16000, 1, 1, 3, 0, pcm(1600), 3));
    const two = of(live, 2), three = of(live, 3);
    voiceEvent({ type: 'speech_start', session_id: sid });
    assert(two.every(e => e.src.stopped) && three.every(e => e.src.stopped) && live.sources.length === 0
      && live.tombstones.has(2) && live.tombstones.has(3),
      label + 'speech did not stop everything the page holds: ' + JSON.stringify(live.sources.map(e => e.stream)));
    for (const s of [2, 3]) {
      const t = terminal(s);
      assert(t.length === 1 && t[0].outcome === 'stopped', label + 'stream ' + s + ' speech stopped is not receipted stopped once: ' + JSON.stringify(t));
    }
    voiceEvent({ type: 'synthesis_start', session_id: sid, stream: 4 });
    live.feed(encodeStreamFrame(16000, 1, 1, 1, 0, pcm(1600), 4));
    live.feed(encodeStreamFrame(16000, 1, 1, 1, 0, pcm(1600), 5));
    const four = of(live, 4), five = of(live, 5);
    assert(four.length === 1 && five.length === 1, label + 'fixture: the next replies did not schedule');
    voiceEvent(vad(sid));
    assert(four[0].src.stopped && five[0].src.stopped && live.sources.length === 0 && live.tombstones.has(4) && live.tombstones.has(5),
      label + 'the engine\'s VAD fence did not stop everything the page holds: ' + JSON.stringify(live.sources.map(e => e.stream)));
    for (const s of [4, 5]) {
      const t = terminal(s);
      assert(t.length === 1 && t[0].outcome === 'stopped', label + 'stream ' + s + ' the VAD fence stopped is not receipted stopped once: ' + JSON.stringify(t));
    }
    live.reset('after-' + sid);
  }

  {
    const label = 'another session: ', sid = 'held';
    const b = begin(sid), live = b.live;
    voiceEvent(echo('retired-session'));
    voiceEvent(vad('retired-session'));
    assert(!b.part.src.stopped && !live.tombstones.has(1) && terminal(1).length === 0,
      label + 'a stop for another session reached this one');
    live.reset('after-' + sid);
  }
});
</script>`

func TestAHostStopIsScopedToItsStreamInBrowser(t *testing.T) {
	runPageInEngines(t, voiceInterruptionScopePage, map[string][]byte{
		"/app.js": []byte("export function toast(m) { (window.__toasts = window.__toasts || []).push(m); }\n"),
	})
}
