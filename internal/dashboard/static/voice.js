
import { S } from './state.js';
import { $ } from './util.js';
import { toast } from './app.js';
import { spokenAudio, hushSpoken, whenSpeaking } from './say.js';

const FRAME_VERSION = 1;
const HEADER_BYTES = 8;

let media = null;
let ctx = null;
let node = null;
let chunks = [];
let captured = 0;
let overflow = false;
let capturing = false;
let sendFrame = null;

let held = false;
// latched is a tap: recording stays on after a press shorter than
// LATCH_MS, until the next press or Space/Enter sends it.
let latched = false;
let downAt = 0;
const LATCH_MS = 400;
let stopping = false; // a push-to-talk release flushing its held tail before the cutoff

// The resident conversation. The microphone is acquired ONCE
// (worklet capture) and stays live across the engine's replies;
// `resident` is the running Conversation and `residentWanted` the
// operator's intent held across the async getUserMedia/worklet setup,
// so an abort that races setup releases the device instead of leaking
// it.
let resident = null;
let residentWanted = false;

let sendJSON = null;

export function bindTransport(fn, jsonFn) {
  sendFrame = fn; sendJSON = jsonFn || null;
  return { receiveFrame, sessionState, voiceEvent, hushFromHost };
}

export const STREAM_VERSION = 2;
export const STREAM_HEADER = 24;
export const STREAM_PCM = 1, STREAM_GAP = 2, STREAM_END = 3;

export function encodeStreamFrame(rate, channels, kind, seq, start, pcm, stream = 0) {
  const n = pcm ? pcm.length : 0;
  const buf = new ArrayBuffer(STREAM_HEADER + n * 2);
  const view = new DataView(buf);
  view.setUint8(0, STREAM_VERSION);
  view.setUint8(1, channels);
  view.setUint8(2, kind);
  view.setUint8(3, 0);
  view.setUint32(4, rate, true);
  view.setUint32(8, seq, true);
  view.setBigInt64(12, BigInt(start), true);
  view.setUint32(20, stream >>> 0, true);
  for (let i = 0; i < n; i++) view.setInt16(STREAM_HEADER + i * 2, pcm[i], true);
  return buf;
}

export function decodeStreamFrame(buf) {
  const view = new DataView(buf);
  if (buf.byteLength < STREAM_HEADER || view.getUint8(0) !== STREAM_VERSION) return null;
  const channels = view.getUint8(1), kind = view.getUint8(2);
  const rate = view.getUint32(4, true), seq = view.getUint32(8, true);
  const start = Number(view.getBigInt64(12, true));
  const stream = view.getUint32(20, true);
  const n = (buf.byteLength - STREAM_HEADER) >> 1;
  const pcm = new Int16Array(n);
  for (let i = 0; i < n; i++) pcm[i] = view.getInt16(STREAM_HEADER + i * 2, true);
  return { rate, channels, kind, seq, start, stream, pcm };
}

function toInt16(f32) {
  const out = new Int16Array(f32.length);
  for (let i = 0; i < f32.length; i++) {
    let v = f32[i];
    if (v > 1) v = 1; else if (v < -1) v = -1;
    out[i] = v < 0 ? v * 0x8000 : v * 0x7fff;
  }
  return out;
}

export const STREAM_QUEUE = 64;

export class StreamLane {
  constructor(o) {
    this.sendFrame = o.sendFrame; this.sendJSON = o.sendJSON;
    this.rate = o.rate; this.channels = o.channels || 1; this.mode = o.mode || 'meeting';
    this.seq = 0; this.samples = 0; this.isOpen = false; this.pending = []; this.gap = false; this.dropped = 0;
    this.state = 'waiting'; this.ended = false; this.closeRequested = false; this.closeSent = false; this.aborted = false;
    this.inputFinished = false; // the one END has been named
    this.onClosed = null;
  }
  open() {
    this.state = 'opening';
    this.sendJSON({ type: 'voice_session', voice: { action: 'open', mode: this.mode, rate: this.rate, channels: this.channels } });
  }
  onState(st) {
    this.state = st.state;
    if (st.state === 'open') {
      this.isOpen = true;
      this.flush();
      if (this.aborted) this.sendAbort();
      else if (this.closeRequested) this.sendClose();
      return;
    }
    if (st.state === 'closed' || st.state === 'refused' || st.state === 'failed') {
      this.isOpen = false;
      this.pending = [];
      if (this.onClosed) this.onClosed(this);
    }
  }

  send(e) {
    if (e.gap) this.sendFrame(encodeStreamFrame(this.rate, this.channels, STREAM_GAP, ++this.seq, e.start, null));
    this.sendFrame(encodeStreamFrame(this.rate, this.channels, e.kind, ++this.seq, e.start, e.pcm));
  }
  flush() {
    for (const e of this.pending) this.send(e);
    this.pending = [];
  }

  enqueue(e) {
    while (this.pending.length >= STREAM_QUEUE) {
      const i = this.pending.findIndex(x => x.kind !== STREAM_END);
      if (i < 0) break;
      const gone = this.pending.splice(i, 1)[0];
      if (gone.kind === STREAM_PCM) this.dropped++;
      if (i < this.pending.length) this.pending[i].gap = true; else this.gap = true;
    }
    if (this.gap) { e.gap = true; this.gap = false; }
    this.pending.push(e);
  }
  emit(kind, pcm) {
    const e = { kind, start: this.samples, pcm, gap: false };
    if (!this.isOpen) { this.enqueue(e); return; }
    if (this.gap) { e.gap = true; this.gap = false; }
    this.send(e);
  }
  chunk(f32) {
    if (this.ended) return;
    this.emit(STREAM_PCM, toInt16(f32));
    this.samples += f32.length;
  }

  end() {
    if (this.ended) return;
    this.ended = true;
    if (!this.inputFinished) { this.inputFinished = true; this.emit(STREAM_END, null); }
    this.closeRequested = true;
    if (this.isOpen) this.sendClose();
  }
  sendClose() {
    if (this.closeSent) return;
    this.closeSent = true;
    this.sendJSON({ type: 'voice_session', voice: { action: 'close' } });
  }

  // finishInput: the session's ONE input half-close.
  // An END frame maps to the engine's finish_input — a PERMANENT
  // half-close (the engine fixes an immutable cutoff, drains the exact
  // tail, then refuses further PCM), NOT a per-turn commit. The session
  // stays open so the host can admit the final reply; close is a
  // separate act (Abort).
  finishInput() {
    if (this.ended || this.closeRequested || this.inputFinished) return;
    this.inputFinished = true;
    this.emit(STREAM_END, null);
  }

  abort() {
    this.ended = true; this.aborted = true; this.pending = [];
    if (this.isOpen) this.sendAbort();
  }
  sendAbort() {
    if (this.closeSent) return;
    this.closeSent = true;
    this.sendJSON({ type: 'voice_session', voice: { action: 'abort' } });
  }
  over() { return this.state === 'closed' || this.state === 'refused' || this.state === 'failed'; }
}

// PreRoll is a bounded ring of captured lead-in: the audio just before
// a resident conversation opens its stream, kept so the opening words
// are not clipped by the setup latency. It holds roughly `max` samples
// and drops whole oldest frames past that — bounded, never growing
// without end.
export class PreRoll {
  constructor(max) { this.max = Math.max(0, max | 0); this.frames = []; this.total = 0; }
  push(f32) {
    if (this.max <= 0 || !f32 || !f32.length) return;
    this.frames.push(f32); this.total += f32.length;
    while (this.frames.length > 1 && this.total - this.frames[0].length >= this.max) {
      this.total -= this.frames.shift().length;
    }
  }
  drain() {
    if (!this.frames.length) return null;
    const out = new Float32Array(this.total);
    let o = 0; for (const f of this.frames) { out.set(f, o); o += f.length; }
    this.frames = []; this.total = 0;
    return out;
  }
  clear() { this.frames = []; this.total = 0; }
  samples() { return this.total; }
}

// Conversation is the resident conversation over ONE streaming lane.
// Start opens one session and the microphone streams into it as a
// single CONTINUOUS input stream — through the engine's replies too, so
// a spoken interruption keeps its opening words. Turns are the engine's
// semantic events (the host answers each one while the mic stays open);
// the browser never feeds a turn back as input. finishInput is the ONE
// explicit input half-close (finish_input): it names the final cutoff
// and stops the input WITHOUT closing, so the host can admit the final
// reply; Abort tears it down.
export class Conversation {
  constructor(o) {
    this.newLane = o.newLane;
    this.onEnded = o.onEnded || null;
    this.pre = new PreRoll(o.prerollSamples || 0);
    this.lane = null;
    this.active = false;
    this.finished = false;
    this.finishing = false; // a flush is in flight ahead of the half-close
  }
  start() {
    if (this.active) return;
    this.active = true;
    this.lane = this.newLane();
    const base = this.lane.onClosed;
    this.lane.onClosed = l => { if (base) base(l); this._closed(l); };
    const seed = this.pre.drain();
    if (seed && seed.length) this.lane.chunk(seed);
  }
  frame(f32) {
    if (!this.active) { this.pre.push(f32); return; }
    if (this.finished) return; // input half-closed: no more microphone audio
    if (this.lane && !this.lane.ended) this.lane.chunk(f32);
  }
  finishInput() {
    // The session's ONE input half-close (finish_input): stop feeding
    // the microphone and name the final cutoff, WITHOUT closing — the
    // host admits the final reply while the session stays open; Abort
    // closes. Idempotent.
    if (!this.active || this.finished || !this.lane || this.lane.ended) return;
    this.finished = true;
    this.lane.finishInput();
  }
  _closed(lane) {
    if (!this.active) return;
    this.active = false;
    this.lane = null;
    if (this.onEnded) this.onEnded(lane ? lane.state : 'closed');
  }
  abort() {
    this.active = false;
    if (this.lane && !this.lane.ended) { try { this.lane.abort(); } catch (e) {  } }
    this.lane = null;
    this.pre.clear();
  }
  streaming() { return this.active && !this.finished && !!this.lane && !this.lane.ended; }
}

export class Playback {
  // The page's playback and its EVIDENCE:
  // per output stream, what was scheduled, what the audio clock has passed
  // (rendered — a client report, never acoustic proof), whether END was
  // received, and the one terminal report: "drained" when END is received
  // and every scheduled source has ended, "stopped" when the page's own
  // stop completed. Reports name the ORIGINATING session, so a late report
  // can never be attributed to a replacement. Missing evidence is never a
  // successful drain: the engine waits for the terminal report.
  constructor(o) {
    o = o || {};
    this.report = o.report || null; // one report to the host
    this.newContext = o.audioContext || (rate => new (window.AudioContext || window.webkitAudioContext)({ sampleRate: rate }));
    this.sessionID = '';
    this.ctx = null; this.at = 0; this.rate = 0; this.channels = 1;
    this.received = 0; this.scheduled = 0; this.stopped = 0; this.reported = 0;
    this.sources = []; this.lastStream = null;
    // Tombstones are the generations whose delivery must never (re-)enter
    // playback: every stream a cancel stopped. Bound to the engine's
    // stream id (monotonic, never reused), they PERSIST for the session —
    // a newer reply lifts nothing for an older one, and a stopped stream
    // never revives: not on a reused id, not after its own end, not once a
    // newer reply has begun.
    this.tombstones = new Set();
    this.streams = new Map();
    this.timer = null;
    // A terminal receipt the ENGINE refused leaves its stream UNRESOLVED,
    // never resolved-in-silence: it is re-sent, bounded, and what is still
    // unresolved when the tries run out is said out loud (onUnresolved).
    this.retries = new Map();
    this.unresolved = new Map();
    this.timers = new Set();
    this.foreign = 0; // refusals that named another session: applied to nothing
    this.onUnresolved = o.onUnresolved || null;
    this.retryBase = 150;
    this.retryLimit = 6;
  }
  stream(id) {
    let st = this.streams.get(id);
    if (!st) { st = { scheduled: 0, played: 0, ended: false, live: 0, terminal: false, outcome: '', last: -1, report: null }; this.streams.set(id, st); }
    return st;
  }
  // prime creates the playback context under the microphone click's
  // activation — AFTER the capture is open (startDuplex says why) —
  // and resumes it. A mobile browser (Chrome on Android, WebKit on
  // iOS) leaves a context created outside a gesture suspended, and
  // every reply is then scheduled into silence. The context is made
  // ONCE and kept: an AudioBuffer carries its own rate and the graph
  // resamples it, so the engine's rate never rebuilds the context — a
  // rebuild would be a context made outside the gesture again.
  prime(rate) {
    if (!this.ctx) {
      try { this.ctx = this.newContext(rate || 48000); } catch (e) { return false; }
      this.at = 0;
    }
    this.resume();
    return !!this.ctx;
  }
  // resume wakes a suspended context — after a gesture, after the page
  // returns to the foreground; best effort, never a throw.
  resume() {
    const c = this.ctx;
    if (!c || c.state !== 'suspended' || typeof c.resume !== 'function') return;
    try { const p = c.resume(); if (p && typeof p.catch === 'function') p.catch(() => {}); } catch (e) { /* the next gesture tries again */ }
  }
  feed(buf) {
    const f = decodeStreamFrame(buf);
    if (!f) return;
    if (f.kind === STREAM_PCM) this.received += f.pcm.length / (f.channels || 1);
    if (this.tombstones.has(f.stream)) {
      // A fenced stream that never played: receipted as stopped at zero,
      // once — the engine still awaits its terminal evidence.
      if (!this.streams.has(f.stream)) this.terminate(f.stream, 0, 'stopped');
      return;
    }
    if (f.kind === STREAM_END) { const st = this.stream(f.stream); st.ended = true; this.settle(f.stream); return; }
    if (f.kind !== STREAM_PCM || f.pcm.length === 0) return;
    if (!this.ctx) { this.ctx = this.newContext(f.rate); this.at = 0; }
    this.rate = f.rate; // the buffer carries its own rate; the graph resamples to the context's
    this.resume();
    this.channels = f.channels || 1;
    this.lastStream = f.stream;
    const frames = f.pcm.length / f.channels;
    const ab = this.ctx.createBuffer(f.channels, frames, f.rate);
    for (let c = 0; c < f.channels; c++) {
      const ch = ab.getChannelData(c);
      for (let i = 0; i < frames; i++) ch[i] = f.pcm[i * f.channels + c] / 0x8000;
    }
    const src = this.ctx.createBufferSource();
    src.buffer = ab; src.connect(this.ctx.destination);
    const now = this.ctx.currentTime + 0.02;
    if (this.at < now) this.at = now;
    const startAt = this.at;
    src.start(startAt);
    this.at += ab.duration;
    this.scheduled += frames;
    const st = this.stream(f.stream);
    st.scheduled += frames; st.live++;
    const entry = { src, until: this.at, stream: f.stream, startAt, frames, done: false };
    this.sources.push(entry);
    src.onended = () => { this.ended(entry); };
    if (!this.timer && this.report) {
      // Progress, bounded: at most four reports a second per live stream,
      // only when the count moved; the timer ends with the last source.
      this.timer = setInterval(() => { this.progress(); if (!this.sources.length) { clearInterval(this.timer); this.timer = null; } }, 250);
    }
  }
  // ended: the clock reached the source's end — its frames were rendered
  // whole (a stopped source was accounted at the stop, and is not here).
  ended(entry) {
    if (entry.done) return;
    entry.done = true;
    this.sources = this.sources.filter(e => e !== entry);
    const st = this.streams.get(entry.stream);
    if (!st) return;
    st.live = Math.max(0, st.live - 1);
    if (!st.terminal) st.played += entry.frames;
    this.settle(entry.stream);
  }
  // rendered is the client's evidence for one stream at an instant — by
  // default now: the sources the clock has ended, plus the played part of
  // the ones in flight. `at` names an earlier instant (the moment a stop
  // took effect), so a receipt can report exactly what had played THEN.
  rendered(stream, at) {
    const st = this.streams.get(stream);
    if (!st) return 0;
    let n = st.played;
    const now = at === undefined ? (this.ctx ? this.ctx.currentTime : 0) : at;
    for (const e of this.sources) {
      if (e.stream !== stream || e.done) continue;
      n += Math.max(0, Math.min(e.frames, Math.floor((now - e.startAt) * this.rate)));
    }
    return Math.min(n, st.scheduled);
  }
  // settle makes the DRAINED terminal report: END received and every
  // scheduled source ended, once.
  settle(stream) {
    const st = this.streams.get(stream);
    if (!st || st.terminal || !st.ended || st.live > 0) return;
    this.terminate(stream, st.played, 'drained');
  }
  // terminate records the stream's one terminal report and sends it. The
  // report is KEPT: if the engine refuses it, the same evidence is re-sent
  // rather than the stream being quietly abandoned as resolved.
  terminate(stream, rendered, outcome) {
    const st = this.stream(stream);
    st.terminal = true; st.outcome = outcome; st.played = rendered;
    st.report = { rendered, outcome };
    this.unresolved.delete(stream);
    this.send(stream, rendered, true, outcome);
  }
  // receiptRefused is the host's word that a terminal report did not stand
  // (most often: the engine had not yet fenced the output the page stopped).
  // The stream goes back to UNRESOLVED and the same report is re-sent after
  // a growing pause; when the tries run out it stays unresolved, and says so.
  //
  // A REFUSAL BELONGS TO THE SESSION THAT MADE THE REPORT (a voice-platform
  // P1). Output stream ids are the engine's and a fresh worker
  // begins them again, so a stream NUMBER identifies nothing on its own: an
  // old session's delayed refusal, routed by number alone, reopened a new
  // session's settled receipt and scheduled a retry no one owed. It is
  // checked before ANY state changes, and the retry it schedules is bound
  // to that session and that exact stream object — a reset, or a stream
  // replaced under the same number, retires it.
  receiptRefused(sessionID, stream, reason) {
    if (!sessionID || sessionID !== this.sessionID) { this.foreign++; return; }
    const st = this.streams.get(stream);
    if (!st || !st.report) return;
    st.terminal = false;
    const tries = (this.retries.get(stream) || 0) + 1;
    this.retries.set(stream, tries);
    if (tries > this.retryLimit) {
      this.unresolved.set(stream, reason || 'refused');
      if (this.onUnresolved) this.onUnresolved(stream, reason || '');
      return;
    }
    const wait = Math.min(2000, this.retryBase * Math.pow(2, tries - 1));
    const timer = setTimeout(() => {
      this.timers.delete(timer);
      if (this.sessionID !== sessionID) return; // the session this owed is over
      const cur = this.streams.get(stream);
      if (cur !== st || cur.terminal || !cur.report) return;
      this.terminate(stream, cur.report.rendered, cur.report.outcome);
    }, wait);
    this.timers.add(timer);
  }
  progress() {
    for (const [id, st] of this.streams) {
      if (st.terminal || st.live === 0) continue;
      const r = this.rendered(id);
      if (r !== st.last) { st.last = r; this.send(id, r, false, 'progress'); }
    }
  }
  send(stream, rendered, terminal, outcome) {
    if (!this.report) return;
    this.reported++;
    this.report({ session_id: this.sessionID, stream: stream >>> 0, rendered, rate: this.rate, channels: this.channels, terminal, outcome });
  }

  stop(cause) {
    // An operator/engine cancel tombstones what it stopped so no late or
    // reused frame revives it; a rate change abandons the old context
    // without tombstoning (a new-rate reply is a new stream anyway).
    //
    // THE AUDIO STOPS FIRST, THE RECEIPT GOES LAST (the voice platform's
    // review). A terminal "stopped" receipt used to be sent while its
    // sources were still playing — a count that was not yet true, in front
    // of a fence the engine had not been asked for. Now: mark the instant,
    // stop the sources, account what had played AT that instant, and only
    // then report it.
    const fence = cause !== 'rate-change';
    const stopAt = this.ctx ? this.ctx.currentTime : 0;
    let n = 0;
    const streams = new Set();
    for (const e of this.sources) streams.add(e.stream);
    for (const e of this.sources) {
      try { e.src.stop(); n++; } catch (err) {  }
    }
    const played = new Map();
    for (const s of streams) played.set(s, this.rendered(s, stopAt));
    for (const e of this.sources) {
      e.done = true;
      const st = this.streams.get(e.stream);
      if (st) st.live = 0;
    }
    this.sources = [];
    this.stopped += n;
    this.at = 0;
    if (fence) {
      for (const s of streams) this.tombstones.add(s >>> 0);
      if (this.lastStream !== null) this.tombstones.add(this.lastStream >>> 0);
    }
    for (const s of streams) {
      const st = this.stream(s);
      if (st.terminal) continue;
      this.terminate(s, played.get(s) || 0, 'stopped');
    }
    return n;
  }
  // reset begins a session: a clean fence, clean stream evidence, and the
  // session identity every report of it will name.
  reset(sessionID, rate, channels) {
    // Every timer the last session left owes nothing to this one.
    for (const t of this.timers) clearTimeout(t);
    this.timers.clear();
    if (this.timer) { clearInterval(this.timer); this.timer = null; }
    this.tombstones.clear(); this.lastStream = null; this.streams = new Map(); this.sessionID = sessionID || '';
    this.retries = new Map(); this.unresolved = new Map();
    if (rate) this.rate = rate;
    if (channels) this.channels = channels;
  }
  playing() { return !!this.ctx && this.ctx.currentTime < this.at; }
  stats() { return { received: this.received, scheduled: this.scheduled, stopped: this.stopped, playing: this.playing(), at: this.at, rate: this.rate, tombstoned: this.tombstones.size, reported: this.reported, streams: this.streams.size, unresolved: this.unresolved.size, foreign: this.foreign }; }
}

let lanes = [];
// The page's playback reports travel the session's own channel (the
// voice_session message), naming the session they belong to.
const playback = new Playback({
  report: r => { if (sendJSON) sendJSON({ type: 'voice_session', voice: { action: 'playback', playback: r } }); },
  // What the engine would not accept, and the page could not resolve, is
  // said out loud: a session whose reply has no accepted render evidence
  // cannot report a clean drain, and nobody should believe it did.
  onUnresolved: (stream, reason) => toast('The engine did not accept the playback receipt for this reply' + (reason ? ' (' + reason + ')' : '') + '; its drain cannot be reported as clean.'),
});
// A page brought back to the foreground wakes its playback context: a
// mobile browser suspends it in the background and the next reply would
// otherwise play into silence.
if (typeof document !== 'undefined' && document.addEventListener) {
  document.addEventListener('visibilitychange', () => { if (!document.hidden) playback.resume(); });
}

function captureLane() { return lanes.length ? lanes[lanes.length - 1] : null; }

function newLane(rate, mode) {
  const l = new StreamLane({ sendFrame, sendJSON, rate, channels: 1, mode: mode || 'conversation' });
  l.onClosed = closedLane => {
    lanes = lanes.filter(x => x !== closedLane);
    const next = lanes[0];
    if (next && next.state === 'waiting') next.open();
  };
  lanes.push(l);
  if (lanes.length === 1) l.open();
  return l;
}

export function sessionState(st) {
  const head = lanes[0];
  if (head) head.onState(st || {});
  // A new session starts with a clean playback fence:
  // the previous session's tombstones, and any stream id a fresh session
  // may reuse, must not drop this session's audio.
  if (st && st.state === 'open') playback.reset(st.session_id, head ? head.rate : 0, head ? head.channels : 1);
  if (st && st.state === 'refused') toast('The speech engine refused the session: ' + (st.reason || 'no reason given'));
  render();
}

export function receiveFrame(buf) { playback.feed(buf); }

// voiceEvent renders one engine observation on the page: a live
// transcript line (provisional or final), a failure banner, or a clean
// close that clears the line. Other event types (turn, synthesis,
// playback, uid, an unknown future type) reach the page but are not
// rendered here yet — passed over, never an error. Recognition is
// participant evidence for display, not an operator command.
export function voiceEvent(ev) {
  ev = ev || {};
  const t = ev.type || '';
  // The engine fenced its current reply because the operator spoke
  // (interruption_requested): what the page still holds of that reply is
  // stopped and tombstoned now — a fenced reply is not played out of a
  // queue, and the stop's own receipt follows it.
  if (t === 'interruption_requested') playback.stop();
  // A receipt the host or the engine refused: the stream is not resolved,
  // and the page owes it another report (bounded).
  if (t === 'receipt_refused') { playback.receiptRefused(ev.session_id || '', (ev.stream || 0) >>> 0, ev.reason || ''); return; }
  const el = $('voice-transcript');
  if (!el) return;
  if (t === 'transcript_final' || t === 'transcript_partial') {
    const text = (ev.text || '').trim();
    // The operator's own words: a final transcript is a message
    // they sent — ws.js posts it to the thread and shows the thinking
    // dots (it holds addMsg and setThinking); here the transient line is
    // cleared so the same words are not shown twice. voice.js imports no
    // view module: it loads on the bare voice test harnesses too.
    if (t === 'transcript_final' && ev.operator && text) {
      el.textContent = ''; el.classList.remove('provisional', 'failed'); el.hidden = true;
      return;
    }
    el.textContent = text ? ((ev.speaker ? ev.speaker + ': ' : '') + text) : '';
    el.classList.toggle('provisional', t === 'transcript_partial');
    el.classList.remove('failed');
    el.hidden = !text;
    return;
  }
  // Words the speaker policy withheld: the operator sees that a voice
  // was withheld and why, never what it said.
  if (t === 'transcript_withheld') {
    el.textContent = 'A voice was withheld' + (ev.reason ? ' — ' + ev.reason : '') + '.';
    el.classList.add('provisional'); el.classList.remove('failed');
    el.hidden = false;
    renderConverse();
    return;
  }
  // 'failure' is the ENGINE's own event, 'failed' the host's word that the
  // session's critical stream ended badly. Both are the same news to the
  // operator and both carry the reason they were given.
  if (t === 'failed' || t === 'failure') {
    el.textContent = 'The speech engine failed' + (ev.reason ? ': ' + ev.reason : '') + '.';
    el.classList.remove('provisional');
    el.classList.add('failed');
    el.hidden = false;
    return;
  }
  if (t === 'closed') {
    el.textContent = '';
    el.classList.remove('provisional', 'failed');
    el.hidden = true;
  }
}

export function playbackForTest() { return playback; }
// converseForTest puts the microphone control into one of its states
// without a microphone: 'idle', 'waiting' (the session is opening) or
// 'live' (the identity is listening). A test seam, like playbackForTest.
export function converseForTest(state) {
  residentWanted = state !== 'idle';
  resident = state === 'live' ? { streaming() { return true; }, frame() {}, start() {} } : null;
  renderConverse();
}
export function captureFenceForTest() { return { epoch: () => captureEpoch, retire: () => teardown(), stale: () => staleCapture }; }
export function lanesForTest() { return lanes; }

function engineLane() { return !!(S.stats && S.stats.voice_engine && sendJSON); }

async function startCapture() {
  if (capturing) return;

  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    toast('The microphone needs a secure context — serve the dashboard over HTTPS, or reach it on localhost.');
    return;
  }
  try {
    media = await navigator.mediaDevices.getUserMedia({
      audio: {

        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true,
        channelCount: 1,
      },
    });
  } catch (err) {

    toast('No microphone: ' + (err && err.message ? err.message : err));
    return;
  }

  if (!held) { media.getTracks().forEach(t => t.stop()); media = null; return; }
  ctx = new (window.AudioContext || window.webkitAudioContext)();
  const src = ctx.createMediaStreamSource(media);

  chunks = [];
  captured = 0;
  overflow = false;

  let lane = null;
  if (engineLane()) lane = newLane(ctx.sampleRate);
  // The same bounded worklet capture as the resident conversation (the
  // ScriptProcessor where a worklet is unavailable), so a release can
  // flush the held tail BEFORE it names the input cutoff.
  await openMicNode(ctx, src, buf => {
    if (!capturing || overflow) return;
    if (lane) { lane.chunk(buf); return; }

    const limit = frameLimit();
    if (limit && (captured + buf.length) * 2 + HEADER_BYTES > limit) {
      overflow = true;

      setTimeout(() => { held = false; latched = false; stopCapture(true); render(); }, 0);
      return;
    }
    captured += buf.length;
    chunks.push(buf);
  });
  // A release during the capture graph's setup: nothing may stay hot —
  // neither the microphone nor the session lane already opened for it.
  if (!held) { if (lane) lane.abort(); teardown(); render(); return; }
  capturing = true;
  render();
}

function frameLimit() {
  const n = S.stats && S.stats.voice_max_frame_bytes;
  return typeof n === 'number' && n > 0 ? n : 0;
}

async function stopCapture(send) {
  if (!capturing || stopping) return;
  // A release that SENDS flushes the held tail first — through the same
  // frame path, into the lane or the utterance — then names the cutoff at
  // the admitted sample clock. An abort flushes nothing.
  let tail = { samples: 0, confirmed: true, reason: '' };
  if (send) { stopping = true; tail = await flushCapture(); stopping = false; }
  if (!capturing) return; // torn down while flushing (disconnect, page hidden)
  capturing = false;
  captured = 0;
  const rate = ctx ? ctx.sampleRate : 0;
  const collected = chunks;
  chunks = [];
  teardown();
  const lane = captureLane();
  if (lane && !lane.ended) {
    // A sending release names the session's one input cutoff and does
    // NOT close: the host awaits the final transcript, admits its eligible
    // reply, and only then requests the drain-close. Abort stays immediate.
    if (send) { lane.finishInput(); reportTail(tail); } else lane.abort();
    render();
    return;
  }
  render();
  if (!send || !collected.length || !rate) return;

  let total = 0;
  for (const c of collected) total += c.length;

  if (total < rate * 0.2) return;
  if (overflow) toast('That is as much as one utterance can carry — sending what I heard.');

  const frame = new ArrayBuffer(HEADER_BYTES + total * 2);
  const view = new DataView(frame);
  view.setUint8(0, FRAME_VERSION);
  view.setUint8(1, 1);
  // 1 = CONVERSATION: the operator pressing the microphone is opening one,
  // and spoken words that waited silently for the next turn would be a
  // microphone that does nothing. Meeting stays the host's zero.
  view.setUint8(2, 1);
  view.setUint8(3, 0);
  view.setUint32(4, rate, true);
  let off = HEADER_BYTES;
  for (const c of collected) {
    for (let i = 0; i < c.length; i++) {

      let v = c[i];
      if (v > 1) v = 1; else if (v < -1) v = -1;
      view.setInt16(off, v < 0 ? v * 0x8000 : v * 0x7fff, true);
      off += 2;
    }
  }
  if (sendFrame) sendFrame(frame);
  reportTail(tail); // the whole-utterance path owes the same honesty about its tail
}

function teardown() {
  // Retire the capture FIRST: whatever its port still
  // delivers is rejected, and the port itself is closed.
  captureEpoch++;
  if (node) {
    if (node.port) { try { node.port.onmessage = null; node.port.close(); } catch (e) {  } }
    try { node.disconnect(); } catch (e) {  }
    node = null;
  }
  captureKind = '';
  for (const [id, w] of flushWaiters) {
    flushWaiters.delete(id);
    w({ samples: 0, confirmed: false, reason: 'the capture was torn down before it acknowledged the flush' });
  }
  if (ctx) { try { ctx.close(); } catch (e) {  } ctx = null; }
  if (media) { media.getTracks().forEach(t => t.stop()); media = null; }
}

// openMicNode wires the capture graph: bounded AudioWorklet frames
// where available (the duplex path), the deprecated ScriptProcessor as
// a fallback. Both deliver Float32 mono frames to onFrame; `node` owns
// the node so teardown() releases it.
// captureKind names the live capture graph ('worklet' | 'scriptprocessor');
// flushWaiters holds the flush acknowledgements the worklet still owes.
let captureKind = '';
let flushSeq = 0;
const flushWaiters = new Map();
// captureEpoch names the LIVE capture: every port callback
// carries the epoch it was opened under, and teardown retires it, so a
// late frame or acknowledged partial from a retired port is rejected
// whole — it can never feed the capture that replaced it. staleCapture
// counts what was rejected.
let captureEpoch = 0;
let staleCapture = 0;

async function openMicNode(ac, src, onFrame) {
  const frame = Math.max(160, Math.round(ac.sampleRate * 0.02));
  const epoch = ++captureEpoch; // a teardown during setup retires this capture before it delivers
  if (ac.audioWorklet && typeof AudioWorkletNode !== 'undefined') {
    try {
      await ac.audioWorklet.addModule(new URL('voice-capture.worklet.js', import.meta.url));
      const wn = new AudioWorkletNode(ac, 'voice-capture', { numberOfInputs: 1, numberOfOutputs: 0, processorOptions: { frame } });
      wn.port.onmessage = e => captureMessage(e.data, onFrame, epoch);
      src.connect(wn);
      node = wn; captureKind = 'worklet';
      return 'worklet';
    } catch (e) {  }
  }
  const sp = ac.createScriptProcessor(4096, 1, 1);
  sp.onaudioprocess = e => { if (epoch !== captureEpoch) { staleCapture++; return; } onFrame(new Float32Array(e.inputBuffer.getChannelData(0))); };
  src.connect(sp); sp.connect(ac.destination);
  node = sp; captureKind = 'scriptprocessor';
  return 'scriptprocessor';
}

// captureMessage routes one worklet message: a frame (an ArrayBuffer) to
// the capture, or a flush acknowledgement — whose held partial is admitted
// through the SAME frame path BEFORE the waiting flush resolves.
// A message from a RETIRED capture (its epoch is not the live one) is
// rejected whole, and so is an acknowledgement nobody awaits (its flush
// timed out or was torn down): the cutoff was already named without that
// partial, and audio after the cutoff is not admitted anywhere.
export function captureMessage(data, onFrame, epoch) {
  if (!data) return;
  if (epoch !== undefined && epoch !== captureEpoch) { staleCapture++; return; }
  if (data instanceof ArrayBuffer) { onFrame(new Float32Array(data)); return; }
  if (data.ack === undefined) return;
  const w = flushWaiters.get(data.ack);
  if (!w) { staleCapture++; return; }
  flushWaiters.delete(data.ack);
  if (data.buffer && data.samples > 0) onFrame(new Float32Array(data.buffer));
  w({ samples: data.samples | 0, confirmed: true, reason: '' });
}

// flushPort asks a capture port for its held partial and waits, bounded,
// for the acknowledgement; the partial is admitted (captureMessage) before
// the promise resolves, so the caller's sample clock is exact when it
// names the cutoff. A port that does not answer within the bound resolves
// UNCONFIRMED with its reason — reported, never counted as complete.
export function flushPort(port, timeoutMs) {
  const bound = timeoutMs || 300;
  const id = ++flushSeq;
  return new Promise(resolve => {
    const timer = setTimeout(() => {
      if (flushWaiters.delete(id)) resolve({ samples: 0, confirmed: false, reason: 'the capture did not acknowledge the flush within ' + bound + ' ms' });
    }, bound);
    flushWaiters.set(id, r => { clearTimeout(timer); resolve(r); });
    try { port.postMessage({ flush: id }); } catch (e) {
      if (flushWaiters.delete(id)) { clearTimeout(timer); resolve({ samples: 0, confirmed: false, reason: 'the capture port refused the flush: ' + (e && e.message ? e.message : e) }); }
    }
  });
}

// flushCapture flushes the LIVE capture. The ScriptProcessor fallback holds
// no partial the page can reach, so it cannot flush: that is reported as
// unconfirmed, not hidden. No capture at all has nothing to flush.
function flushCapture() {
  if (captureKind === 'worklet' && node && node.port) return flushPort(node.port, 300);
  if (captureKind === 'scriptprocessor') return Promise.resolve({ samples: 0, confirmed: false, reason: 'the ScriptProcessor fallback cannot flush its held tail' });
  return Promise.resolve({ samples: 0, confirmed: true, reason: '' });
}

// reportTail puts the flush outcome where the operator can see it: an
// unconfirmed tail is said so, never counted as complete capture.
function reportTail(tail) {
  S.captureTail = { confirmed: !!tail.confirmed, samples: tail.samples | 0, reason: tail.reason || '' };
  if (!tail.confirmed) toast('The end of what you said could not be confirmed (' + (tail.reason || 'no acknowledgement') + '); the input ended at the last sample the engine was given.');
}

// finishWithTail is the ordering law of the input half-close: flush and
// admit the capture's held tail (through the same frame path), THEN name
// the session's one input cutoff at the admitted sample clock — exactly
// once. A tail the capture could not confirm is REPORTED in the result;
// the cutoff still names exactly what was admitted, nothing is invented.
export async function finishWithTail(conv, flush) {
  if (!conv || !conv.active || conv.finished) return { samples: 0, confirmed: true, reason: '', finished: false };
  const tail = await flush();
  conv.finishInput();
  return Object.assign({ finished: true }, tail);
}

// reportCaptureSettings records what the browser SAYS it applied — echo
// cancellation, noise suppression, gain — as SETTINGS requested, never
// as a claim that acoustic quality was tested. Reference
// availability is a browser setting, not a measured result; the plane
// does not grade it.
function reportCaptureSettings(stream) {
  try {
    const tr = stream.getAudioTracks ? stream.getAudioTracks()[0] : null;
    const s = tr && tr.getSettings ? tr.getSettings() : {};
    S.captureSettings = {
      echoCancellation: !!s.echoCancellation,
      noiseSuppression: !!s.noiseSuppression,
      autoGainControl: !!s.autoGainControl,
      sampleRate: s.sampleRate || (ctx ? ctx.sampleRate : 0),
      tested: false,
    };
  } catch (e) { S.captureSettings = { tested: false }; }
}

// startDuplex opens a resident conversation: acquire the microphone
// ONCE, stream it continuously into one session, and keep it live
// across the engine's replies. It never ties a turn to a pointer. The
// device is released on every exit — a denial, an abort that raced
// setup, or teardown — so no microphone is ever left open.
export async function startDuplex() {
  if (resident) return;
  // Push-to-talk and a resident conversation share the capture state
  // (media/ctx/node); they are mutually exclusive so neither leaks the
  // other's microphone. Refuse to start over an active PTT hold.
  if (held || capturing) { toast('Finish the push-to-talk first.'); residentWanted = false; render(); return; }
  if (!engineLane()) { toast('A speech engine must be active for a spoken conversation.'); residentWanted = false; render(); return; }
  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    toast('The microphone needs a secure context — serve the dashboard over HTTPS, or reach it on localhost.');
    residentWanted = false; render(); return;
  }
  let stream;
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true, channelCount: 1 } });
  } catch (err) {
    toast('No microphone: ' + (err && err.message ? err.message : err));
    residentWanted = false; render(); return;
  }
  // An abort that raced the permission prompt: release at once.
  if (!residentWanted) { stream.getTracks().forEach(t => t.stop()); render(); return; }
  media = stream;
  ctx = new (window.AudioContext || window.webkitAudioContext)();
  reportCaptureSettings(media);
  // The playback context is made HERE — after the microphone is open,
  // never before it. CAPTURE FIRST is load-bearing on Android (found
  // live from a Pixel): a playback context made before the
  // microphone puts the audio path into media mode and the echo
  // canceller never engages, so the phone hears every reply as the
  // operator's words; opened after capture, the path is the voice path
  // and the canceller holds. Made now, under the same user activation
  // the microphone got, rather than at the first reply frame, so a
  // mobile browser does not leave it suspended.
  playback.prime(ctx.sampleRate);
  const src = ctx.createMediaStreamSource(media);
  const rate = ctx.sampleRate;
  resident = new Conversation({
    // A resident conversation is always conversation-mode (the engine
    // listens AND replies), independent of the meeting/conversation
    // toggle that governs push-to-talk.
    newLane: () => newLane(rate, 'conversation'),
    prerollSamples: Math.round(rate * 0.3),
    onEnded: () => { abortDuplex(); },
  });
  // The callback is bound to THIS conversation: a frame from
  // a capture that outlived its abort never feeds the replacement.
  const conv = resident;
  try {
    await openMicNode(ctx, src, f32 => { if (resident === conv) conv.frame(f32); });
  } catch (e) {
    toast('The microphone could not start: ' + (e && e.message ? e.message : e));
    abortDuplex(); return;
  }
  // An abort that raced the worklet load.
  if (!residentWanted) { abortDuplex(); return; }
  resident.start();
  render();
}

// finishInput is the operator's ONE explicit input half-close: name the
// final cutoff (finish_input) and STOP capturing — no more microphone
// audio — WITHOUT closing the session, so the host can process the final
// transcript and admit the eligible final reply. Abort closes the
// session. A semantic ENGINE turn is NOT this: an observed turn updates
// the UI (voice_event) and lets the host answer; it must never
// half-close the input.
export async function finishInput() {
  if (!resident || resident.finished || resident.finishing) return;
  resident.finishing = true;
  const tail = await finishWithTail(resident, flushCapture);
  reportTail(tail);
  stopResidentCapture();
}

// stopResidentCapture stops the microphone but keeps the resident
// session and its playback alive: after Finish input there is no more
// mic audio, yet the final reply still plays and the session stays open
// until Abort or the engine's word.
function stopResidentCapture() {
  capturing = false;
  teardown();
}

// abortDuplex ends the resident conversation and releases the
// microphone — the Abort control, and the path every teardown takes.
// Nothing is left open and no reply keeps playing after the operator
// ends the talk.
export function abortDuplex() {
  residentWanted = false;
  if (resident) { resident.abort(); resident = null; }
  teardown();
  playback.stop();
  render();
}

export function residentActive() { return !!resident; }

export function speak(text, ref) {
  // A reply the resident engine speaks arrives with plugin provenance:
  // the page shows it and never speaks it itself —
  // decided by IDENTITY, not by whether a lane happens to be open. So an
  // expired plugin reply cannot fall back to browser speech, and an
  // unrelated response is not silenced just because a lane is live: a
  // response with no plugin provenance keeps the configured fallback.
  // Plugin provenance includes text-only refusals with no synthesis id:
  // losing that provenance must not revive them through another route.
  if (ref && ref.route === 'plugin') return;
  if (!text) return;
  // A CONFIGURED VOICE SPEAKS EVERY REPLY, whether or not the operator
  // used the microphone. Speaking was coupled to the microphone because
  // the browser's own voice was all there was, and reading every typed
  // answer aloud in it would be a nuisance; a service the operator chose
  // in Settings → Speech is not a nuisance, it is the answer they asked
  // to hear. The microphone belongs to the other half of speech.
  // A reply the host is ALREADY speaking arrives with the id to play it
  // by: it has been in the making since the words existed.
  if (ref && ref.route === 'cloud' && ref.synthesis_id) { sayWithService(text, { id: ref.synthesis_id }); return; }
  // A reply the host handed to the browser's own voice after the engine
  // refused it: read when the operator wants replies read, and stopped
  // by the host's hush like any other.
  if (ref && ref.route === 'browser') { if (S.voiceSpeak) browserSpeak(text); return; }
  if (S.stats && S.stats.reply_voice) { sayWithService(text, { text: text }); return; }
  // The browser's own voice keeps the older rule: it reads a reply back
  // to an operator who spoke, and stays out of a typed conversation.
  if (!S.voiceSpeak) return;
  browserSpeak(text);
}

function browserSpeak(text) {
  if (!window.speechSynthesis || !text) return;
  window.speechSynthesis.cancel();
  const u = new SpeechSynthesisUtterance(text);
  // The browser's own voice can be stopped the same way a service's can.
  const stop = $('hush-speaking');
  if (stop) stop.hidden = false;
  u.onend = u.onerror = () => { if (stop) stop.hidden = true; };
  window.speechSynthesis.speak(u);
}

// hushedReplies names the replies the host hushed: a refusal that lands
// for one of them afterwards is the hush arriving as an error, not a
// service refusing — nothing reads it back.
const hushedReplies = new Set();

function sayWithService(text, ask) {
  spokenAudio(ask).catch(err => {
    if (ask && ask.id && hushedReplies.has(ask.id)) return;
    // THE REPLY IS STILL SPOKEN. The operator hears the answer and reads
    // why it was not the voice they chose.
    toast('The speaking service refused this reply: ' + ((err && err.message) || err));
    browserSpeak(text);
  });
}

// hushFromHost is the host's word that a reply another voice took over for
// a session is over — the session's fence moved. The service's audio and
// the browser's own voice both stop, and a refusal that arrives for the
// stopped reply afterwards reads nothing back.
export function hushFromHost(h) {
  if (h && h.synthesis_id) hushedReplies.add(h.synthesis_id);
  hushSpoken();
  if (window.speechSynthesis) window.speechSynthesis.cancel();
  const stop = $('hush-speaking');
  if (stop) stop.hidden = true;
}

export function hush() {
  hushSpoken();
  if (window.speechSynthesis) window.speechSynthesis.cancel();

  // THE ENGINE'S FENCE IS ASKED FOR FIRST (the voice platform's review):
  // the stop's terminal receipt names output the engine still
  // believes is live unless its cancel is already on the wire. The request
  // is a single frame; the audio stops in the same breath, and the receipt
  // — which stop() sends last — arrives behind the fence.
  const head = lanes[0];
  if (head && head.isOpen && sendJSON) sendJSON({ type: 'voice_session', voice: { action: 'interrupt' } });
  playback.stop();
}

export function connectionLost() {
  held = false;
  latched = false;
  stopCapture(false);
  abortDuplex();
  lanes = [];
  playback.stop();
}

// A REPLY BEING SPOKEN CAN BE STOPPED. The control exists only while
// there is something to stop, so the composer is otherwise as it was.
whenSpeaking(on => {
  const b = $('hush-speaking');
  if (b) b.hidden = !on;
});

// micState is the microphone the host says this page offers — in order,
// SAFE pauses every one; a voice plugin's conversation; the configured
// speech-to-text service; and otherwise the way to set one up.
// The microphone and the stop square, drawn: an emoji microphone is a
// slanted handset that reads as a pencil at this size (found live).
const MIC_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><rect x="5.5" y="1.8" width="5" height="8.4" rx="2.5"/><path d="M3.2 7.6a4.8 4.8 0 0 0 9.6 0M8 12.4v2"/></svg>';
const STOP_ICON = '<svg class="stop" viewBox="0 0 16 16" aria-hidden="true"><rect x="4.5" y="4.5" width="7" height="7" rx="1.5"/></svg>';

export function micState() {
  return (S.stats && S.stats.voice_state) || 'setup';
}

const MIC_TITLES = {
  cloud: 'Talk — tap, or hold',
  setup: 'Voice isn\'t set up — opens Speech settings',
  unreachable: 'Voice can\'t reach its service — opens Speech settings',
  safe: 'Voice is paused while this identity is in SAFE',
};

// ONE MICROPHONE, THE BEST ONE PRESENT. A voice plugin's conversation is
// #converse; every other state is this button: push-to-talk through the
// cloud service, a way into Speech settings when there is nothing to
// speak into or its service cannot be reached, or paused under SAFE.
export function render() {
  renderConverse();
  renderMicMenu();
  const b = $('mic');
  if (!b) return;
  const state = micState(), recording = capturing || latched;
  b.hidden = state === 'plugin';
  b.disabled = state === 'safe' || (state === 'cloud' && (!S.connected || residentActive()));
  b.classList.toggle('faint', state === 'setup' || state === 'unreachable');
  b.classList.toggle('live', recording);
  b.setAttribute('aria-pressed', recording ? 'true' : 'false');
  // With a voice that speaks replies, "voice" is not what is missing.
  const speaks = !!(S.stats && S.stats.reply_voice);
  b.title = recording && state === 'cloud' ? 'Tap to send what you said'
    : state === 'setup' && speaks ? 'Voice input isn\'t set up — opens Speech settings'
    : MIC_TITLES[state] || MIC_TITLES.setup;
  b.setAttribute('aria-label', b.title);
}

// The chevron beside a working microphone keeps Speech settings one click
// away, and names what is listening.
function renderMicMenu() {
  const more = $('mic-more'), source = $('mic-source'), voice = $('mic-voice');
  if (!more) return;
  const state = micState();
  // THE MENU IS VOICE, BOTH HALVES OF IT. An identity that speaks its
  // replies but has no microphone set up still has a voice to name and a
  // way into its settings.
  const speaks = (S.stats && S.stats.reply_voice) || '';
  const hears = state === 'plugin' || state === 'cloud';
  more.hidden = !hears && !speaks;
  if (more.hidden) closeMicMenu();
  if (voice) {
    voice.hidden = !speaks;
    voice.textContent = speaks ? 'Replies spoken by ' + speaks : '';
  }
  if (source) {
    source.hidden = !hears;
    source.textContent = hears
      ? (state === 'plugin' ? 'Voice plugin' : 'Cloud speech') + (S.stats && S.stats.voice_source ? ' — ' + S.stats.voice_source : '')
      : '';
  }
}

function closeMicMenu() {
  const menu = $('mic-menu'), more = $('mic-more');
  if (menu) menu.hidden = true;
  if (more) more.setAttribute('aria-expanded', 'false');
}

// Settings registers the way in; a page without it — a bare voice
// harness — has nowhere to send the operator, and does nothing.
function openSpeechSettings() {
  closeMicMenu();
  if (S.openSettings) S.openSettings('speech', 'sp-provider-stt');
}

// renderConverse reflects the resident conversation on #converse: a tap
// starts it, a tap while words stream finishes, and one after ends it.
function renderConverse() {
  const c = $('converse');
  if (c) {
    const available = micState() === 'plugin';
    c.hidden = !available;
    c.disabled = !available || !S.connected;
    const live = residentActive(), waiting = !live && residentWanted;
    c.classList.toggle('live', live);
    c.classList.toggle('waiting', waiting);
    // The glyph is the state: a microphone to start, a stop square while
    // the identity is listening — the next tap finishes.
    const glyph = live ? 'stop' : 'mic';
    if (c.dataset.glyph !== glyph) { c.innerHTML = live ? STOP_ICON : MIC_ICON; c.dataset.glyph = glyph; }
    c.setAttribute('aria-pressed', live ? 'true' : 'false');
    c.title = residentActive()
      ? 'Stop listening — the identity gives its final reply'
      : 'Start a spoken conversation — the identity listens and replies while you speak';
    c.setAttribute('aria-label', c.title);
  }
  // Listening, said in words: the transcript line names the state until
  // the first words arrive.
  const tr = $('voice-transcript');
  if (tr && residentActive() && !(tr.textContent || '').trim()) { tr.textContent = 'Listening…'; tr.classList.add('provisional'); tr.hidden = false; }
  if (tr && !residentActive() && (tr.textContent || '') === 'Listening…') { tr.textContent = ''; tr.hidden = true; }
  // Who is heard, said while the identity listens: the restriction in
  // force and what it withheld — never the words.
  const fl = $('voice-filter');
  if (fl) {
    const p = S.stats && S.stats.speakers;
    const on = !!(p && p.mode && p.mode !== 'all' && residentActive());
    fl.hidden = !on;
    if (on) fl.textContent = speakerFilterLine(p);
  }
}

export function speakerFilterLine(p) {
  const n = (p.uids || []).length, s = n === 1 ? '' : 's';
  const who = p.mode === 'only' ? 'Hearing only ' + n + ' listed speaker' + s : 'Ignoring ' + n + ' listed speaker' + s;
  return who + ' · ' + (p.withheld_finals || 0) + ' withheld';
}

export function wireMic() {
  // The control hides because the speaking stopped, not because it was
  // the thing that stopped it: a reply that ends on its own, or one a
  // newer reply supersedes, must leave the composer the same way.
  const stop = $('hush-speaking');
  if (stop) stop.onclick = () => hush();
  wireConverse();
  wireMicMenu();
  const b = $('mic');
  if (!b) return;
  b.removeAttribute('data-inert');

  // TAP OR HOLD. A hold records until release; a press shorter than
  // LATCH_MS latches recording on until the next press — on a phone a tap
  // is the gesture, and it released under the 0.2 s floor and was thrown
  // away, so the button looked dead. The first press can also meet the
  // permission prompt mid-hold. Push-to-talk stands down while a resident
  // conversation holds the microphone (or is being acquired).
  b.addEventListener('pointerdown', e => {
    if (resident || residentWanted) return;
    e.preventDefault();
    const state = micState();
    if (state === 'setup' || state === 'unreachable') { openSpeechSettings(); return; }
    if (state !== 'cloud') return;
    if (latched) { sendLatched(); return; }
    downAt = Date.now(); held = true; hush(); startCapture();
  });
  b.addEventListener('pointerup', e => {
    if (resident || residentWanted || latched || !held) return;
    e.preventDefault();
    if (Date.now() - downAt < LATCH_MS) { latched = true; render(); return; }
    held = false; stopCapture(true);
  });
  const abandon = () => { if (resident || residentWanted || latched || !held) return; held = false; stopCapture(false); };
  b.addEventListener('pointerleave', abandon);
  b.addEventListener('pointercancel', abandon);
  b.addEventListener('keydown', e => {
    if (e.key !== ' ' && e.key !== 'Enter') return;
    e.preventDefault();
    if (e.repeat || resident || residentWanted) return;
    const state = micState();
    if (state === 'setup' || state === 'unreachable') { openSpeechSettings(); return; }
    if (state !== 'cloud') return;
    if (latched || held) { sendLatched(); return; }
    latched = true; held = true; hush(); startCapture(); render();
  });

  window.addEventListener('pagehide', () => { held = false; latched = false; stopCapture(false); abortDuplex(); });
  render();
}

function sendLatched() {
  latched = false; held = false;
  stopCapture(true);
  render();
}

function wireMicMenu() {
  const more = $('mic-more'), menu = $('mic-menu'), settings = $('mic-settings');
  if (!more || !menu) return;
  more.removeAttribute('data-inert');
  more.addEventListener('click', e => {
    e.stopPropagation();
    const open = menu.hidden;
    menu.hidden = !open;
    more.setAttribute('aria-expanded', open ? 'true' : 'false');
    if (open && settings) settings.focus();
  });
  if (settings) settings.addEventListener('click', openSpeechSettings);
  document.addEventListener('click', e => { if (!e.target.closest || !e.target.closest('#mic-menu')) closeMicMenu(); });
  menu.addEventListener('keydown', e => { if (e.key === 'Escape') { closeMicMenu(); more.focus(); } });
}

function wireConverse() {
  const c = $('converse');
  if (c) {
    c.removeAttribute('data-inert');
    c.addEventListener('click', () => {
      // A second click while words are still streaming FINISHES (the
      // identity gives its final reply); after that, or before any
      // words, it ends the conversation.
      if (resident || residentWanted) {
        if (resident && resident.streaming() && !resident.finished && !resident.finishing) { finishInput(); render(); return; }
        abortDuplex(); return;
      }
      residentWanted = true; render(); startDuplex();
    });
  }
  renderConverse();
}
