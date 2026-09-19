
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
    // An output lane has a speaker and no microphone: there is no input to
    // end, and an END frame would be the page inventing one. Its close is
    // the drain alone — the replies it owes play out, then it closes.
    if (!this.inputFinished) { this.inputFinished = true; if (this.mode !== 'output') this.emit(STREAM_END, null); }
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
  inputClosedByEngine() {
    // THE ENGINE ALREADY NAMED THE CUTOFF, so the page must not name
    // another: no END frame, no half-close at the browser's later sample
    // clock, which the engine would rightly refuse. Stop feeding the
    // microphone and leave the lane open — the last words are still
    // being finished and the reply still comes back over it. Idempotent.
    if (!this.active || this.finished) return false;
    this.finished = true;
    return true;
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
    // announced is the stream of the reply the engine last began: what a
    // newer reply replaces (announce/retire).
    this.announced = null;
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
  // release drops the context so the NEXT prime() has to make one, and
  // makes it AFTER the microphone is open — the capture-first rule
  // startDuplex names. A context made while the microphone was closed
  // (earbuds) is exactly the one that must not survive into a
  // conversation. The audio stops first, with its receipts: a source
  // scheduled into a context about to close must never be left believed
  // live. Nothing to release is not an error.
  release() {
    if (!this.ctx) return;
    this.stop('release');
    const c = this.ctx;
    this.ctx = null; this.at = 0;
    if (typeof c.close === 'function') { try { c.close(); } catch (e) {  } }
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

  // retire ends ONE stream where stop() ends them all: the reply it
  // carried has been replaced, so what the page still holds of it is
  // stopped, accounted at the instant it stopped, tombstoned so a late
  // frame cannot revive it, and reported stopped. The schedule pointer
  // goes back to now, because the reply that replaces it must not be
  // queued behind audio nobody will hear.
  //
  // A REPLY THE ENGINE FINISHED MAKING IS NOT A REPLY THE OPERATOR HAS
  // HEARD. Its frames sit here, scheduled; a newer reply arriving simply
  // queued behind them and both played (independent review GO143).
  retire(stream) {
    const s = stream >>> 0;
    if (!this.playingStream(s)) { this.tombstones.add(s); return 0; }
    const stopAt = this.ctx ? this.ctx.currentTime : 0;
    let n = 0;
    for (const e of this.sources) {
      if (e.stream !== s) continue;
      try { e.src.stop(); n++; } catch (err) {  }
      e.done = true;
    }
    const played = this.rendered(s, stopAt);
    const st = this.streams.get(s);
    if (st) st.live = 0;
    this.sources = this.sources.filter(e => e.stream !== s);
    this.stopped += n;
    this.at = 0;
    this.tombstones.add(s);
    if (!this.stream(s).terminal) this.terminate(s, played, 'stopped');
    return n;
  }
  // playingStream says this stream still has audio the page has not
  // played out: scheduled, or playing now.
  playingStream(stream) {
    const s = stream >>> 0;
    return this.sources.some(e => e.stream === s && !e.done);
  }
  // announce is the engine naming the stream of a NEW reply. Whatever the
  // page still holds of the reply before it is retired here — one place,
  // whether the words came by typing or by speech.
  announce(stream) {
    const s = stream >>> 0;
    if (this.announced !== null && this.announced !== s) this.retire(this.announced);
    this.announced = s;
  }
  // replaced is a new reply arriving with no stream named for it — an
  // engine whose start never reached this page. The last stream fed is
  // then the reply being replaced, and only then: once a start HAS named
  // a stream, announce owns this and guessing from the frames would
  // retire the new reply's own audio when it arrives first.
  replaced() {
    if (this.announced !== null || this.lastStream === null) return 0;
    return this.retire(this.lastStream);
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
    this.tombstones.clear(); this.lastStream = null; this.announced = null; this.streams = new Map(); this.sessionID = sessionID || '';
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
  // Noted BEFORE the lane hears it: the lane's own close is what asks for
  // the next one, and a refused output lane must not be asked for again in
  // the same breath.
  const refusedSpeaker = !!(st && st.state === 'refused' && head && head.mode === 'output');
  if (refusedSpeaker) speakerRefusedAt = Date.now();
  if (head) head.onState(st || {});
  // A new session starts with a clean playback fence:
  // the previous session's tombstones, and any stream id a fresh session
  // may reuse, must not drop this session's audio.
  if (st && st.state === 'open') { inputEndedWhy = ''; playback.reset(st.session_id, head ? head.rate : 0, head ? head.channels : 1); }
  if (st && st.state === 'input_complete') engineClosedInput(st);
  // A refused OUTPUT lane is not something the operator did or can fix: the
  // engine serves one session, and another page may be holding it. The
  // reply is still spoken, by the voice that speaks when the engine's is
  // not there; this page asks again later and says nothing about it.
  if (st && st.state === 'refused' && !refusedSpeaker) toast('The speech engine refused the session: ' + (st.reason || 'no reason given'));
  render();
}

// inputEndedWhy is why the microphone stopped when the ENGINE stopped it,
// kept so the mic can say so for as long as it is stopped rather than for
// as long as a toast lasts.
let inputEndedWhy = '';
// transcriptPlaceholder is the state sentence this page last wrote into
// the transcript line — "Listening…", or why listening ended — so it can
// be replaced or cleared without ever touching words the engine heard.
let transcriptPlaceholder = '';

// ENGINE-STOPPED, IN THE OPERATOR'S WORDS. "It stopped" and "it stopped
// because you reached your listening limit" are different sentences, and
// only one of them tells someone what to do next. A reason the page does
// not recognize is shown as the engine named it rather than swallowed.
function inputEndSentence(reason) {
  // What survives is what was ALREADY ACCEPTED: it finishes. A reply is
  // not promised here, because silence, meeting mode and SAFE all end
  // an input without one. The engine sends capture_limit, finish_input,
  // or nothing; anything else is named as it came.
  switch (reason) {
    case 'capture_limit':
      return 'That is as long as one conversation listens — the microphone stopped. What was already heard still finishes.';
    case 'finish_input':
      return 'Listening ended where the input was finished. What was already heard still finishes.';
    case '':
    case undefined:
    case null:
      return 'The speech engine stopped listening. What was already heard still finishes.';
    default:
      return 'The speech engine stopped listening (' + reason + '). What was already heard still finishes.';
  }
}

// engineClosedInput is the engine ending the input of its own accord.
// The microphone stops because the SESSION stopped accepting speech, not
// because a timer in this page guessed; the session, its playback and the
// final reply all stay alive, and no second half-close is sent.
function engineClosedInput(st) {
  // ONLY THE SESSION THIS PAGE HOLDS. A delayed completion belonging to a
  // session that is already over must never stop its replacement's
  // microphone.
  const id = (st && st.session_id) || '';
  if (id && playback.sessionID && id !== playback.sessionID) return;
  if (!resident || !resident.inputClosedByEngine()) return; // over, or already half-closed
  inputEndedWhy = inputEndSentence(st && st.reason);
  stopResidentCapture();
  toast(inputEndedWhy);
}

export function receiveFrame(buf) { playback.feed(buf); if (speakerLane) watchSpeakerPlayback(); }

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
  // A REPLY'S START IS THE MOMENT THE ONE BEFORE IT IS OVER HERE. The
  // engine names each reply's output stream as it begins it; the page
  // holds the audio, so what it still has of the previous reply is
  // retired now — stopped, tombstoned and reported stopped — instead of
  // playing on under the new one. The host fences what the engine is
  // still producing; only the page can stop what it was already sent.
  if (t === 'synthesis_start' && ev.stream) playback.announce(ev.stream);
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
export function inputEndSentenceForTest(reason) { return inputEndSentence(reason); }

function engineLane() { return !!(S.stats && S.stats.voice_engine && sendJSON); }

// ── THE VOICE MODE: ONE PAIR, AND IT IS THE HOST'S ───────────────────
//
// What the microphone does and whether replies are spoken is ONE value
// with one owner: the host's speech.mode pair, kept in config, carried on
// every status frame. This page keeps no mode of its own — it draws the
// pair it was given and changes it through the config door like any other
// setting, then waits for the frame that says the change took. The four
// names below are labels on pairs, not a fifth state.
const MODE_PAIRS = {
  interactive: { listen: 'interactive', speak: 'on' },
  earbuds: { listen: 'off', speak: 'on' },
  off: { listen: 'off', speak: 'off' },
  meeting: { listen: 'meeting', speak: 'off' },
};

// modeRev is the highest revision this page has ACCEPTED and modeHeld the
// pair that arrived with it. A page that has just changed the mode can be
// handed a status frame that left the host before the change: it carries a
// LOWER revision and the pair the operator has already moved off, and
// applying it would flip the control back under their hand. Lower is
// ignored; equal or higher is the truth.
let modeRev = 0;
let modeHeld = { listen: 'interactive', speak: 'auto' };
// offSeenRev is the highest revision at which this page has ACCEPTED
// listening off — a watermark, kept because the pair in force cannot
// answer the question an open in flight asks. Off at one revision and
// interactive at the next leave the pair reading interactive while a
// microphone asked for before either is still waiting on its permission
// prompt; that off was addressed to it, and the on is not a gesture.
let offSeenRev = 0;

// voiceMode is the ONE reader of the pair in force. A missing or unknown
// half is auto: listen resolves to interactive and speak to auto — the
// page's own per-reply rule, which is exactly the behaviour that was here
// before the pair existed.
export function voiceMode() {
  const st = S.stats || {};
  const rev = typeof st.voice_mode_revision === 'number' ? st.voice_mode_revision : 0;
  if (rev < modeRev) return { listen: modeHeld.listen, speak: modeHeld.speak, revision: modeRev };
  const listen = st.voice_listen === 'off' || st.voice_listen === 'meeting' ? st.voice_listen : 'interactive';
  const speak = st.voice_speak === 'on' || st.voice_speak === 'off' ? st.voice_speak : 'auto';
  modeRev = rev; modeHeld = { listen: listen, speak: speak };
  if (listen === 'off' && rev > offSeenRev) offSeenRev = rev;
  return { listen: listen, speak: speak, revision: rev };
}

// modeName is the operator's word for a pair — the same four names the
// host uses, and an honest phrase for the two pairs no name covers.
export function modeName(listen, speak) {
  const spoken = speak !== 'off';
  if (listen === 'interactive') return spoken ? 'interactive' : 'interactive, text replies';
  if (listen === 'meeting') return spoken ? 'meeting, spoken replies' : 'meeting';
  return spoken ? 'earbuds' : 'off';
}

// setVoiceMode changes the pair through the config door, BOTH HALVES every
// time: a change naming one half sets the other to auto, so a page that
// sent one would silently unset the other. The host accepts the pair whole
// or refuses it whole and answers with a status frame.
function setVoiceMode(name) {
  const pair = MODE_PAIRS[name];
  if (!pair || !sendJSON) return;
  // Silence is owed NOW, not at the next reply: entering a mode that does
  // not speak stops what is being spoken as the operator asks for it.
  if (pair.speak === 'off') hushForSpeakOff();
  sendJSON({ type: 'config_set', config: { 'speech.mode': { listen: pair.listen, speak: pair.speak } } });
}

// spokenHalf is the speak half this page has already acted on, so the
// silence a change owes is paid exactly once — by the tap that entered the
// mode, or by the status frame that brings a change made elsewhere (the
// identity's own, or another browser's).
let spokenHalf = 'auto';
function hushForSpeakOff() { spokenHalf = 'off'; hush(); }
function followSpeakHalf(speak) {
  const was = spokenHalf;
  spokenHalf = speak;
  if (speak === 'off' && was !== 'off') hush();
}

// followListenHalf is the OTHER half of a mode change made ANYWHERE — by
// the identity, in Settings, in another browser, by this page's own tap —
// landing on the microphone this page is holding. The speak half silences
// what is being spoken; this one is about the device: LISTENING TURNED OFF
// MEANS THE MICROPHONE STOPS, whoever turned it off. It ran with only the
// speak half followed, and a host that had committed listen=off left a
// live capture on every open page — the review that found it ran the
// device itself and watched the track stay live.
//
// What it never does is OPEN one. A status frame is not a gesture — no
// browser hands a page a microphone for one — so listen becoming
// interactive or meeting starts nothing here; the operator's tap does.
//
// And a remote change to MEETING leaves a live conversation lane exactly
// where it is: from that revision the host admits the session's finals as
// room speech, which is the host session's own act. Retiring the lane to
// reopen it under the other name would throw away the words in flight to
// change a label the host has already changed.
function followListenHalf(listen) {
  if (listen !== 'off') return;
  // A conversation still streaming is FINISHED, never aborted: the
  // microphone track ends, the lane drains, and the final reply for what
  // was already heard still plays — the same custody the tap's own
  // Finish → earbuds transition has. An abort would discard spoken words
  // and kill a reply in flight.
  if (resident && resident.streaming() && !resident.finished && !resident.finishing) { finishInput(); return; }
  // A conversation already draining owes that reply: nothing to do here.
  if (resident) return;
  // A latched cloud capture is listening that stays on, so off ends it the
  // way the tap's own send does: what was heard goes, the device is
  // released.
  if (latched && capturing) { latched = false; stopCapture(true); return; }
  // An explicit push-to-talk hold is the operator's hand on the button and
  // is not taken out of it: the release sends as always, and under off the
  // reply comes back as text: off means off.
}

// nextMode is THE CYCLE, as the tap sequence the control already had:
// interactive → earbuds → off → interactive. Meeting is not in it — the
// menu, Settings and the identity set it — and a tap in meeting comes back
// to interactive.
function nextMode(m) {
  if (m.listen === 'meeting') return 'interactive';
  if (m.listen === 'off') return m.speak === 'off' ? 'interactive' : 'off';
  return 'earbuds';
}

// laneMode is the mode a capture lane opens under: what a meeting hears is
// recorded and not addressed to the identity.
function laneMode(listen) { return listen === 'meeting' ? 'meeting' : 'conversation'; }

// ── THE MICROPHONE: THE BROWSER'S ELECTION, NAMED ────────────────────
//
// THE BROWSER ALREADY CHOOSES, AND KEEPS CHOOSING. No deviceId is asked
// for unless the operator has pinned one, so the election — and its
// following of the system default — stays exactly where it was. What a
// browser cannot do is say, where the operator is looking, WHICH device
// it elected: a laptop with no built-in microphone and a webcam attached
// after the page had loaded opened a session, heard nothing and closed,
// and nothing on the page could name the device that was listening.
// Three things follow, and no more: name what is listening, list what
// the browser will admit, and let one be pinned for THIS BROWSER when
// the election is wrong. Output devices stay entirely the browser's.
const MIC_KEY = 'aii.mic';
let micPinned = '';       // the deviceId the operator chose, '' for the browser's own
let micPinnedLabel = '';  // what it was called when they chose it, so an absent one has a name
let micInputs = [];       // [{id, label}] as the browser last listed them
let micDefaultLabel = ''; // what the browser calls its own default, where it says
let micLabel = '';        // the device carrying the live capture

function loadPinnedMic() {
  try {
    const saved = JSON.parse(localStorage.getItem(MIC_KEY) || 'null');
    if (saved && typeof saved.id === 'string') { micPinned = saved.id; micPinnedLabel = String(saved.label || ''); }
  } catch (e) {  }
}
loadPinnedMic();

function savePinnedMic() {
  try {
    if (micPinned) localStorage.setItem(MIC_KEY, JSON.stringify({ id: micPinned, label: micPinnedLabel }));
    else localStorage.removeItem(MIC_KEY);
  } catch (e) {  }
}

// audioConstraint asks for the browser's own device unless one is
// pinned, and then for that one EXACTLY — so a pin naming a device that
// is not here fails where we can say so, instead of being quietly
// answered with a different microphone.
function audioConstraint(ignorePin) {
  const audio = { echoCancellation: true, noiseSuppression: true, autoGainControl: true, channelCount: 1 };
  if (micPinned && !ignorePin) audio.deviceId = { exact: micPinned };
  return { audio };
}

// openMicStream acquires the microphone, falling back to the election
// when the pinned device is absent and saying which. The pin is KEPT:
// plugging that device back in uses it again without being asked twice.
async function openMicStream() {
  try {
    return await navigator.mediaDevices.getUserMedia(audioConstraint(false));
  } catch (err) {
    const name = err && err.name;
    if (!micPinned || (name !== 'OverconstrainedError' && name !== 'NotFoundError')) throw err;
    const stream = await navigator.mediaDevices.getUserMedia(audioConstraint(true));
    toast((micPinnedLabel || 'The microphone you chose') + ' is not connected — listening with the browser\'s default.');
    return stream;
  }
}

// noteCaptureDevice names the device that is actually listening, and
// watches for it leaving. A track that ENDS was taken away — stop() does
// not fire that — so a capture which would go on streaming silence is
// ended and said, rather than left looking alive.
function noteCaptureDevice(stream) {
  micLabel = '';
  const track = stream && stream.getAudioTracks ? stream.getAudioTracks()[0] : null;
  if (track) {
    micLabel = track.label || '';
    track.addEventListener('ended', () => {
      if (media !== stream) return; // already retired: nothing of ours is listening
      toast((micLabel || 'The microphone') + ' was disconnected — listening stopped.');
      if (resident || residentWanted) abortDuplex();
      else { held = false; latched = false; stopCapture(false); }
      refreshMicInputs();
      render();
    });
  }
  refreshMicInputs();
}

// refreshMicInputs asks the browser what inputs exist. Labels are the
// browser's to give — blank until the operator has allowed a microphone
// once — and the default/communications entries are aliases for a real
// device, so the devices themselves are listed while the alias's name is
// kept: it is the only place a browser says what it elected.
async function refreshMicInputs() {
  const md = navigator.mediaDevices;
  if (!md || !md.enumerateDevices) { micInputs = []; renderMicMenu(); return; }
  let list = [];
  try { list = await md.enumerateDevices(); } catch (e) { list = []; }
  const inputs = (list || []).filter(d => d && d.kind === 'audioinput');
  const alias = inputs.find(d => d.deviceId === 'default');
  micDefaultLabel = alias && alias.label ? String(alias.label).replace(/^default\s*[-\u2013\u2014:]\s*/i, '') : '';
  micInputs = inputs.filter(d => d.deviceId && d.deviceId !== 'default' && d.deviceId !== 'communications')
    .map(d => ({ id: d.deviceId, label: d.label || '' }));
  renderMicMenu();
}

// chooseMic pins an input for this browser, or gives the election back
// to the browser. A capture already running is NOT switched under the
// operator: the choice is for the next one, and says so.
function chooseMic(id) {
  const chosen = micInputs.find(d => d.id === id);
  micPinned = id || '';
  micPinnedLabel = chosen ? chosen.label : '';
  savePinnedMic();
  renderMicMenu();
  closeMicMenu();
  if (capturing || resident) {
    toast('The next time you talk: ' + (micPinned ? (micPinnedLabel || 'the microphone you chose') : 'the browser\'s default') + '.');
  }
}

// micInputsInto fills the menu's device list: the election first and
// chosen unless something is pinned, then every input the browser will
// admit by name. An unnamed list says why it is empty rather than
// offering anonymous devices.
function micInputsInto(box) {
  box.textContent = '';
  if (micLabel) {
    const live = document.createElement('div');
    live.className = 'mic-live';
    live.textContent = 'Listening with ' + micLabel;
    box.appendChild(live);
  }
  const row = (id, text, on, absent) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.setAttribute('role', 'menuitemradio');
    b.setAttribute('aria-checked', on ? 'true' : 'false');
    if (absent) b.disabled = true; else b.dataset.micInput = id;
    b.classList.toggle('on', !!on);
    b.textContent = text;
    box.appendChild(b);
  };
  row('', 'Browser default' + (micDefaultLabel ? ' — ' + micDefaultLabel : ''), !micPinned, false);
  const named = micInputs.filter(d => d.label);
  named.forEach(d => row(d.id, d.label, d.id === micPinned, false));
  if (micPinned && !named.some(d => d.id === micPinned)) {
    row(micPinned, (micPinnedLabel || 'The microphone you chose') + ' — not connected', true, true);
  }
  if (!named.length) {
    const note = document.createElement('div');
    note.className = 'mic-note';
    note.textContent = 'Your microphones are named here once you allow one.';
    box.appendChild(note);
  }
}

async function startCapture() {
  if (capturing) return;

  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    toast('The microphone needs a secure context — serve the dashboard over HTTPS, or reach it on localhost.');
    return;
  }
  // The revision this press asked under, and the fence naming it as the
  // capture in force: the permission prompt and the capture graph both make
  // the operator wait, and in that time listening can be turned off — by
  // the identity, in Settings, in another browser — or a later gesture can
  // ask for a capture of its own. Both awaits below are answered by the
  // same two questions.
  const askedRev = voiceMode().revision;
  const epoch = ++captureEpoch;
  let stream;
  try {
    stream = await openMicStream();
  } catch (err) {

    toast('No microphone: ' + (err && err.message ? err.message : err));
    return;
  }

  // A LATE DEVICE BINDS TO ITS OWN OPEN: a capture a later gesture asked
  // for is the live one, and this one releases only what it holds.
  if (epoch !== captureEpoch) { stopTracks(stream); return; }
  // The press was released, or an off was accepted after this one asked
  // for the device: the microphone is let go and the latch goes with it —
  // a latch is listening that stays on, and listening was turned off.
  if (!held || offAcceptedSince(askedRev)) { stopTracks(stream); held = false; latched = false; render(); return; }
  media = stream;
  noteCaptureDevice(media);
  ctx = new (window.AudioContext || window.webkitAudioContext)();
  const src = ctx.createMediaStreamSource(media);

  chunks = [];
  captured = 0;
  overflow = false;

  let lane = null;
  // A hold borrows the engine from the output lane: that lane is retired
  // first, the hold's lane waits on the queue behind its close — its audio
  // buffered meanwhile — and the output lane comes back when the hold's
  // own session has closed.
  if (engineLane() && speakerLane) retireSpeaker();
  if (engineLane()) lane = newLane(ctx.sampleRate, laneMode(voiceMode().listen));
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
  // A release during the capture graph's setup, an off accepted while it
  // loaded, or a page whose capture now belongs to a later gesture:
  // nothing may stay hot — neither the microphone nor the session lane
  // already opened for it — and a capture that is no longer the page's is
  // released without touching the one that is.
  const mine = media === stream;
  if (!mine || !held || offAcceptedSince(askedRev)) {
    if (lane) lane.abort();
    if (mine) { held = false; latched = false; teardown(); } else stopTracks(stream);
    render();
    return;
  }
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
  // microphone that does nothing. Under listen=meeting it is the host's
  // zero instead: what the room says is recorded and is not addressed to
  // the identity. The host holds its own value to the same rule; the page
  // agrees with it rather than asking for something it would refuse.
  view.setUint8(2, voiceMode().listen === 'meeting' ? 0 : 1);
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
  micLabel = '';
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
// captureEpoch names the capture in force FROM THE MOMENT IT IS ASKED
// FOR: every acquisition takes it before it waits for a device, and a
// teardown or a later gesture retires it. Every port callback carries the
// epoch it was opened under, so a late frame or acknowledged partial from
// a retired port is rejected whole — and so is a device that arrives for
// an open something newer replaced. Neither can ever feed the capture that
// replaced it. staleCapture counts what was rejected.
let captureEpoch = 0;
let staleCapture = 0;

async function openMicNode(ac, src, onFrame) {
  const frame = Math.max(160, Math.round(ac.sampleRate * 0.02));
  const epoch = captureEpoch; // the acquisition this graph belongs to: a teardown during setup retires it before it delivers
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

// ── THE ENGINE'S VOICE WITHOUT A MICROPHONE ─────────────────────────
//
// In earbuds the operator types and the replies are spoken. With a speech
// engine active the voice that speaks them is the engine's own — and an
// engine speaks only inside a session. So while that is the mode this page
// holds an OUTPUT lane open: a session with this page's speaker and no
// microphone at all. No device is opened and no permission is asked on
// this path; the host is told there is no input in so many words; and the
// reply to typed words arrives as the engine's audio on the lane, exactly
// as a spoken turn's does, with the same receipts.
//
// It is one more lane on the queue every lane rides, so the rule that
// already orders them orders this one: a lane opens when the one before it
// has been acknowledged closed. Leaving earbuds retires it — to talk, the
// conversation's lane waits behind its close; to silence, it is aborted —
// and a push-to-talk hold borrows the engine the same way and gives it
// back when it is done.
let speakerLane = null;
let speakerRefusedAt = 0;
let speakerWatch = null;
let cloudSpeaking = false;
// How long a refused output lane waits before asking again. The refusal
// is usually another page holding the engine's one session.
const SPEAKER_RETRY_MS = 15000;

function speakerWanted() {
  if (!engineLane() || S.connected === false) return false;
  const m = voiceMode();
  if (m.listen !== 'off' || m.speak !== 'on') return false;
  if (resident || residentWanted || held || capturing || latched) return false;
  return micState() !== 'safe';
}

// syncSpeaker makes the output lane match the mode: open while it is
// wanted, retired when it is not. It runs wherever the mode is read, and
// again when a lane closes — the conversation that finished into earbuds
// hands the engine on to it, and a hold gives it back.
function syncSpeaker() {
  if (!speakerWanted()) { if (speakerLane) retireSpeaker(); return; }
  if (speakerLane) return;
  if (speakerRefusedAt && Date.now() - speakerRefusedAt < SPEAKER_RETRY_MS) return;
  // Another lane of this page is still closing or still queued: this one
  // would only wait behind it, and the close that frees the queue comes
  // back through here.
  if (lanes.length) return;
  openSpeaker();
}

function openSpeaker() {
  // The speaker's own rate where a playback context already exists; the
  // host converts the engine's clock to whatever is declared here.
  const rate = (playback.ctx && playback.ctx.sampleRate) || 48000;
  const lane = newLane(rate, 'output');
  speakerLane = lane;
  const queued = lane.onClosed;
  lane.onClosed = l => {
    if (speakerLane === l) speakerLane = null;
    if (queued) queued(l);
    syncSpeaker();
  };
  // A playback context has to be woken by the operator's hand once. The
  // tap that entered earbuds usually did it; a page that loaded into the
  // mode gets it from the first key or click — and a typed message is one.
  if (typeof document !== 'undefined' && document.addEventListener) {
    const wake = () => { playback.prime(rate); };
    document.addEventListener('pointerdown', wake, { once: true, passive: true });
    document.addEventListener('keydown', wake, { once: true, passive: true });
  }
}

// retireSpeaker gives the engine back. An output lane that never opened
// simply leaves the queue; one that did is ABORTED — every way out of
// earbuds is the operator asking for something else now: to be heard, or
// to be left in silence.
function retireSpeaker() {
  const l = speakerLane;
  speakerLane = null;
  if (!l) return;
  if (l.state === 'waiting') { lanes = lanes.filter(x => x !== l); return; }
  playback.stop();
  l.abort();
}

// The stop control is shown while the engine is speaking on the output
// lane: there is no microphone to speak over it with, so the button is the
// only barge-in this mode has.
function watchSpeakerPlayback() {
  const b = $('hush-speaking');
  if (b) b.hidden = false;
  if (speakerWatch) return;
  speakerWatch = setInterval(() => {
    if (speakerLane && playback.playing()) return;
    clearInterval(speakerWatch); speakerWatch = null;
    const stop = $('hush-speaking');
    if (stop && !cloudSpeaking) stop.hidden = true;
  }, 250);
}

export function speakerLaneForTest() { return speakerLane; }

// startDuplex opens a resident conversation: acquire the microphone
// ONCE, stream it continuously into one session, and keep it live
// across the engine's replies. It never ties a turn to a pointer. The
// device is released on every exit — a denial, an abort that raced
// setup, or teardown — so no microphone is ever left open.
// The listen half the conversation opens under may be named by the tap
// that is changing it: the status frame carrying the new pair has not
// arrived yet, and the lane must be opened under the mode the operator
// just asked for, not the one they are leaving.
export async function startDuplex(want) {
  if (resident) return;
  // The operator wants to be heard: the output lane gives the engine up
  // now, and the conversation's lane opens when that close is acknowledged.
  if (speakerLane) retireSpeaker();
  const listen = want || voiceMode().listen;
  // The revision this open is asked under. Acquiring a device is not
  // instant — a permission prompt waits for the operator, a worklet loads
  // — and another actor can commit listen=off in that time. A device that
  // opened AFTER that must not stay open, so each await below is followed
  // by offAcceptedSince(askedRev). Comparing revisions is what keeps the
  // page's own off → interactive tap working: the off it was asked in is
  // not newer than itself.
  const askedRev = voiceMode().revision;
  // CONTINUOUS CAPTURE IS THE LISTEN HALF'S TO GIVE. With listening off
  // there is no conversation to open; the control says so and is one tap
  // from turning the microphone back on.
  if (listen === 'off') { toast('Listening is off — one tap on the microphone turns it back on.'); residentWanted = false; render(); return; }
  // Push-to-talk and a resident conversation share the capture state
  // (media/ctx/node); they are mutually exclusive so neither leaks the
  // other's microphone. Refuse to start over an active PTT hold.
  if (held || capturing) { toast('Finish the push-to-talk first.'); residentWanted = false; render(); return; }
  if (!engineLane()) { toast('A speech engine must be active for a spoken conversation.'); residentWanted = false; render(); return; }
  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    toast('The microphone needs a secure context — serve the dashboard over HTTPS, or reach it on localhost.');
    residentWanted = false; render(); return;
  }
  // A playback context from earbuds — made with no microphone open — is
  // in the way of the rule below: it is dropped HERE, before the device
  // is asked for, so the prime() after capture makes a new one under the
  // same gesture and on the voice path.
  playback.release();
  // This open is now the capture in force; whatever was being acquired
  // before it is retired and will release its own device.
  const epoch = ++captureEpoch;
  let stream;
  try {
    stream = await openMicStream();
  } catch (err) {
    toast('No microphone: ' + (err && err.message ? err.message : err));
    residentWanted = false; render(); return;
  }
  // A LATE DEVICE BINDS TO ITS OWN OPEN. Another gesture has since asked
  // for a capture of its own: this one releases the device it was handed
  // and touches nothing — not the page's capture, not the intent that
  // belongs to the open that replaced it.
  if (epoch !== captureEpoch) { stopTracks(stream); return; }
  // An abort that raced the permission prompt: release at once.
  if (!residentWanted) { stopTracks(stream); render(); return; }
  // Listening turned off while the prompt was up: the device that has just
  // opened is released here and no session is asked for — and listening
  // turned back on afterwards does not revive it.
  if (offAcceptedSince(askedRev)) { stopTracks(stream); residentWanted = false; render(); return; }
  media = stream;
  noteCaptureDevice(media);
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
    // The lane opens under the mode's listen half: a conversation (the
    // engine listens AND replies) in interactive, a meeting when the
    // operator is recording the room — where what is heard is not
    // addressed to the identity and no reply is spoken.
    newLane: () => newLane(rate, laneMode(listen)),
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
  // The capture graph finished for an open the page no longer holds — a
  // later gesture's capture is the live one — so this one releases its own
  // device and leaves that capture where it is.
  if (media !== stream) { stopTracks(stream); return; }
  // An abort that raced the worklet load — or an off accepted since this
  // open was asked for: the capture is released before it streams a sample.
  if (!residentWanted || offAcceptedSince(askedRev)) { abortDuplex(); return; }
  resident.start();
  render();
}

// offAcceptedSince: has listening been turned off at ANY revision newer
// than the one this open was asked under? Not what the pair says NOW — an
// off that was accepted and then followed by an on still cancels the open
// it caught in the middle, because turning listening back on is a setting
// and not a hand on the control, and no browser hands a page a microphone
// for a setting. A revision equal to or lower than the one the open was
// asked under is the mode it was asked in (or a frame that left the host
// before it) and says nothing about it: that is what keeps the page's own
// tap out of off working.
function offAcceptedSince(rev) {
  voiceMode(); // the latest frame is accepted before the question is answered
  return offSeenRev > rev;
}

// stopTracks releases a device this page holds. Nothing to stop is not an
// error — a cancelled open may be releasing a stream it never installed.
function stopTracks(stream) {
  if (stream && stream.getTracks) stream.getTracks().forEach(t => t.stop());
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
  // THE MODE'S SPEAK HALF IS THE FIRST WORD ON EVERY REPLY, before any
  // route, provenance or per-reply rule: off is off, and an operator who
  // turned speaking off is not asking to be read to by a different route.
  const mode = voiceMode();
  if (mode.speak === 'off') return;
  // A reply the resident engine speaks arrives with plugin provenance:
  // the page shows it and never speaks it itself —
  // decided by IDENTITY, not by whether a lane happens to be open. So an
  // expired plugin reply cannot fall back to browser speech, and an
  // unrelated response is not silenced just because a lane is live: a
  // response with no plugin provenance keeps the configured fallback.
  // Plugin provenance includes text-only refusals with no synthesis id:
  // losing that provenance must not revive them through another route.
  if (ref && ref.route === 'plugin') { playback.replaced(); return; }
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
  if (ref && ref.route === 'browser') { if (mode.speak === 'on' || S.voiceSpeak) browserSpeak(text); return; }
  if (S.stats && S.stats.reply_voice) { sayWithService(text, { text: text }); return; }
  // The browser's own voice keeps the older rule under AUTO: it reads a
  // reply back to an operator who spoke, and stays out of a typed
  // conversation. Under speak=on the operator has asked for replies aloud
  // however they were written, so the last-input rule no longer silences
  // it — that rule was a guess at what they wanted, and now they have said.
  if (mode.speak !== 'on' && !S.voiceSpeak) return;
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
  // The revision guard is about frames in flight on ONE connection: what
  // the next connection says about the mode is the truth, whatever number
  // it carries — and the off watermark is read on that same line of
  // numbers, so it starts again with them. Every capture is released just
  // below, so nothing in flight is left depending on it.
  modeRev = 0;
  offSeenRev = 0;
  held = false;
  latched = false;
  stopCapture(false);
  abortDuplex();
  speakerLane = null; speakerRefusedAt = 0;
  lanes = [];
  playback.stop();
}

// A REPLY BEING SPOKEN CAN BE STOPPED. The control exists only while
// there is something to stop, so the composer is otherwise as it was.
whenSpeaking(on => {
  cloudSpeaking = !!on;
  const b = $('hush-speaking');
  if (b) b.hidden = !on && !(speakerLane && playback.playing());
});

// micState is the microphone the host says this page offers — in order,
// SAFE pauses every one; a voice plugin's conversation; the configured
// speech-to-text service; and otherwise the way to set one up.
// The microphone and the stop square, drawn: an emoji microphone is a
// slanted handset that reads as a pencil at this size (found live).
const MIC_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><rect x="5.5" y="1.8" width="5" height="8.4" rx="2.5"/><path d="M3.2 7.6a4.8 4.8 0 0 0 9.6 0M8 12.4v2"/></svg>';
const STOP_ICON = '<svg class="stop" viewBox="0 0 16 16" aria-hidden="true"><rect x="4.5" y="4.5" width="7" height="7" rx="1.5"/></svg>';
// THE GLYPH IS THE MODE, drawn the same way: an ear where replies are
// spoken and the operator types, a crossed microphone where neither half
// is on, a room where the meeting is being recorded. A phone has no hover
// and no tooltip, so the mode has to be the control itself.
const EAR_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M4.9 6.9a3.1 3.1 0 1 1 6.2 0c0 1.7-1.5 2.3-2.1 3.1-.6.8-.3 2-1.5 2.4-1 .3-2-.3-2.1-1.3"/><circle cx="8" cy="6.9" r="1.1"/></svg>';
const MIC_OFF_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><rect x="5.5" y="1.8" width="5" height="8.4" rx="2.5"/><path d="M3.2 7.6a4.8 4.8 0 0 0 9.6 0M8 12.4v2"/><path d="M2.6 2.6l10.8 10.8"/></svg>';
const ROOM_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="5" r="2"/><path d="M4.6 12.3a3.4 3.4 0 0 1 6.8 0"/><circle cx="3.1" cy="6.6" r="1.3"/><path d="M1.2 11.6a2.4 2.4 0 0 1 1.9-2.4"/><circle cx="12.9" cy="6.6" r="1.3"/><path d="M14.8 11.6a2.4 2.4 0 0 0-1.9-2.4"/></svg>';
const GLYPHS = { mic: MIC_ICON, stop: STOP_ICON, ear: EAR_ICON, micoff: MIC_OFF_ICON, room: ROOM_ICON };

// modeGlyph names the drawing for a pair, and markMode puts the same
// state on the control as a class, so the mode is visible without a
// tooltip and the stylesheet can say what each one looks like.
function modeGlyph(m) {
  if (m.listen === 'meeting') return 'room';
  if (m.listen === 'interactive') return 'mic';
  return m.speak === 'off' ? 'micoff' : 'ear';
}
function markMode(el, glyph) {
  el.classList.toggle('earbuds', glyph === 'ear');
  el.classList.toggle('voice-off', glyph === 'micoff');
  el.classList.toggle('meeting', glyph === 'room');
}
function drawGlyph(el, glyph) {
  if (el.dataset.glyph !== glyph) { el.innerHTML = GLYPHS[glyph]; el.dataset.glyph = glyph; }
  markMode(el, glyph);
}

export function micState() {
  return (S.stats && S.stats.voice_state) || 'setup';
}

// The states that are NOT a mode: there is nothing to speak into, the
// service cannot be reached, or SAFE has paused every microphone. A cloud
// microphone has a mode instead, and micModeTitle names it.
const MIC_TITLES = {
  setup: 'Voice isn\'t set up — opens Speech settings',
  unreachable: 'Voice can\'t reach its service — opens Speech settings',
  safe: 'Voice is paused while this identity is in SAFE',
};

// ONE MICROPHONE, THE BEST ONE PRESENT. A voice plugin's conversation is
// #converse; every other state is this button: push-to-talk through the
// cloud service, a way into Speech settings when there is nothing to
// speak into or its service cannot be reached, or paused under SAFE.
export function render() {
  // The mode read first, in one place: a speak half the host has just
  // turned off silences what is being spoken NOW, and a listen half it has
  // just turned off stops this page's microphone — whoever turned them off.
  const m = voiceMode();
  followSpeakHalf(m.speak);
  followListenHalf(m.listen);
  syncSpeaker();
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
  // THE CONTROL'S GLYPH IS THE MODE, where there is a microphone to have
  // one. The other states mean something else entirely — nothing to speak
  // into, a service out of reach, SAFE — and keep the plain microphone
  // they had. A recording hold shows the microphone in any mode, because
  // the device is open; a meeting shows the room it is recording.
  const glyph = state !== 'cloud' || (recording && m.listen !== 'meeting') ? 'mic' : modeGlyph(m);
  drawGlyph(b, glyph);
  // With a voice that speaks replies, "voice" is not what is missing.
  const speaks = !!(S.stats && S.stats.reply_voice);
  b.title = inputEndedWhy && !recording ? inputEndedWhy
    : state === 'cloud' ? micModeTitle(m, recording)
    : state === 'setup' && speaks ? 'Voice input isn\'t set up — opens Speech settings'
    : MIC_TITLES[state] || MIC_TITLES.setup;
  b.setAttribute('aria-label', b.title);
}

// micModeTitle names the mode the operator is in and what the next tap
// does with it — including the two things only a title can say: that a
// hold talks in EVERY mode, and that a cloud meeting is hold-to-record
// segments, because without an engine there is nothing to stream to.
function micModeTitle(m, recording) {
  if (recording) return 'Tap to send what you said — the microphone then rests and replies are spoken';
  switch (modeGlyph(m)) {
    case 'room': return 'Meeting — hold to record the room, a segment at a time; tap to talk with the identity';
    case 'ear': return 'Earbuds — replies are spoken and you type; tap for silence, or hold to talk';
    case 'micoff': return 'Voice off — nothing is heard and nothing is spoken; tap to turn the microphone on, or hold to talk';
    default: return 'Interactive — tap to talk, or hold';
  }
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
  // THE MODE IN FORCE, NAMED, and the one mode the cycle does not reach.
  // Meeting is a decision about the room, not a step between talking and
  // typing, so it is chosen deliberately — here, in Settings, or by the
  // identity — and never landed on by tapping.
  const m = voiceMode();
  const mode = $('mic-mode'), meeting = $('mic-meeting');
  if (mode) {
    mode.hidden = !hears;
    mode.textContent = hears ? 'Mode: ' + modeName(m.listen, m.speak) : '';
  }
  if (meeting) {
    meeting.hidden = !hears;
    meeting.setAttribute('aria-checked', m.listen === 'meeting' ? 'true' : 'false');
    meeting.classList.toggle('on', m.listen === 'meeting');
  }
  const inputs = $('mic-inputs');
  if (inputs) {
    inputs.hidden = !hears;
    // Rebuilt only when something it shows has moved: a status frame
    // arrives every few seconds, and rebuilding under the operator's
    // hand would take the focus out of it mid-keyboard.
    const sig = hears ? [micPinned, micPinnedLabel, micLabel, micDefaultLabel,
      micInputs.map(d => d.id + '\u0000' + d.label).join('|')].join('\u0001') : '';
    if (!hears) { inputs.textContent = ''; delete inputs.dataset.sig; }
    else if (inputs.dataset.sig !== sig) { inputs.dataset.sig = sig; micInputsInto(inputs); }
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
    const m = voiceMode();
    c.classList.toggle('live', live);
    c.classList.toggle('waiting', waiting);
    // The glyph is the state, and the state is the mode: a microphone to
    // start, a stop square while the identity is listening — the next tap
    // finishes — an ear or a crossed microphone where the microphone
    // rests, and the room whenever a meeting is being recorded, because a
    // tap there does not finish anything, it comes back to talking.
    const glyph = m.listen === 'meeting' ? 'room' : live ? 'stop' : modeGlyph(m);
    drawGlyph(c, glyph);
    c.setAttribute('aria-pressed', live ? 'true' : 'false');
    // THE VISIBLE CONTROL SAYS WHY. In plugin mode the microphone button
    // is hidden and this one is what the operator sees, so an engine
    // that stopped listening is named HERE, in the same sentence the
    // page uses everywhere — and a tap now ends the conversation, since
    // there is no input left to finish.
    const ended = inputEndedWhy || inputEndSentence(null);
    c.title = m.listen === 'meeting'
      ? 'Meeting — the room is being recorded and replies are not spoken; tap to talk with the identity'
      : residentActive()
        ? (resident.finished
          ? ended + ' Tap to end the conversation.'
          : 'Stop listening — the identity gives its final reply, then replies are spoken and you type')
        : m.listen === 'off' && m.speak !== 'off'
          ? 'Earbuds — replies are spoken and you type; tap for silence'
          : m.listen === 'off'
            ? 'Voice off — nothing is heard and nothing is spoken; tap to start a spoken conversation'
            : 'Start a spoken conversation — the identity listens and replies while you speak';
    c.setAttribute('aria-label', c.title);
  }
  // Listening, said in words: the transcript line names the state until
  // the first words arrive — and names the end of listening the same
  // way. What this code wrote as a STATE is remembered so it can be
  // replaced or cleared; words the engine heard never are.
  const tr = $('voice-transcript');
  if (tr) {
    const text = tr.textContent || '';
    const placeholder = !text.trim() || text === transcriptPlaceholder;
    if (residentActive() && placeholder) {
      transcriptPlaceholder = resident.finished ? (inputEndedWhy || inputEndSentence(null)) : 'Listening…';
      tr.textContent = transcriptPlaceholder; tr.classList.add('provisional'); tr.hidden = false;
    } else if (!residentActive() && placeholder && text.trim()) {
      tr.textContent = ''; tr.hidden = true; transcriptPlaceholder = '';
    }
  }
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
    // A tap while the latch is on sends what was said (today) AND takes
    // the cycle's next step: the operator has spoken and is waiting to be
    // answered, which is earbuds.
    if (latched) { sendLatched(); setVoiceMode('earbuds'); return; }
    // The press opens the microphone in EVERY mode, because a hold is
    // push-to-talk everywhere and a hold cannot be told from a tap until
    // it is released. A release under the latch time sends nothing and
    // the fragment is dropped (pointerup).
    downAt = Date.now(); held = true; hush(); startCapture();
  });
  b.addEventListener('pointerup', e => {
    if (resident || residentWanted || latched || !held) return;
    e.preventDefault();
    if (Date.now() - downAt < LATCH_MS) {
      const m = voiceMode();
      // CONTINUOUS CAPTURE IS THE LISTEN HALF'S TO GIVE. The latch is
      // listening that stays on, so it belongs to interactive alone:
      // under off it is refused, and a cloud meeting has no engine to
      // stream to, so recording the room there is the hold and nothing
      // else. The tap takes the cycle's step instead, and what the press
      // captured is dropped rather than sent.
      if (m.listen !== 'interactive') {
        held = false; stopCapture(false);
        setVoiceMode(nextMode(m));
        render(); return;
      }
      latched = true; render(); return;
    }
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
    if (latched || held) { sendLatched(); setVoiceMode('earbuds'); return; }
    // A key is a tap and nothing else — there is no hold here — so
    // outside interactive it is the cycle's step alone.
    const m = voiceMode();
    if (m.listen !== 'interactive') { setVoiceMode(nextMode(m)); return; }
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
  const meeting = $('mic-meeting');
  if (meeting) meeting.addEventListener('click', e => {
    e.stopPropagation();
    closeMicMenu();
    setVoiceMode('meeting');
    // Under a resident engine a meeting is a LANE opened in meeting mode,
    // and a conversation lane already open is not that lane: it ends and
    // the meeting begins, under this same tap — the gesture a phone needs
    // to open a microphone at all.
    if (micState() === 'plugin') {
      abortDuplex();
      residentWanted = true; render(); startDuplex('meeting');
    }
  });
  const inputs = $('mic-inputs');
  if (inputs) inputs.addEventListener('click', e => {
    const btn = e.target && e.target.closest ? e.target.closest('[data-mic-input]') : null;
    if (!btn) return;
    e.stopPropagation();
    chooseMic(btn.dataset.micInput);
  });
  // The list follows the machine: a microphone plugged in while the page
  // is open appears in it, and one taken away leaves. Nothing switches
  // itself — the browser already follows the system default, and an
  // operator's pin is theirs until they change it.
  const md = navigator.mediaDevices;
  if (md && md.addEventListener) md.addEventListener('devicechange', () => { refreshMicInputs(); });
  refreshMicInputs();
  document.addEventListener('click', e => { if (!e.target.closest || !e.target.closest('#mic-menu')) closeMicMenu(); });
  menu.addEventListener('keydown', e => { if (e.key === 'Escape') { closeMicMenu(); more.focus(); } });
}

function wireConverse() {
  const c = $('converse');
  if (c) {
    c.removeAttribute('data-inert');
    c.addEventListener('click', () => {
      // THE SAME CYCLE, with the resident conversation's own acts hung on
      // the step each one is. In interactive a click while words are
      // still streaming FINISHES (the identity gives its final reply) and
      // after that ends the conversation — both land in earbuds, where
      // the operator types and is answered aloud.
      const m = voiceMode();
      if (m.listen === 'interactive') {
        // Interactive with nothing running is today's start, and the mode
        // is already the one it starts in: nothing to change.
        if (!resident && !residentWanted) { residentWanted = true; render(); startDuplex('interactive'); return; }
        if (resident && resident.streaming() && !resident.finished && !resident.finishing) finishInput();
        else abortDuplex();
        setVoiceMode('earbuds');
        // The playback context a phone needs, made under THIS tap: the
        // operator has just asked to hear replies without speaking, and
        // one made at the first reply frame would be born suspended.
        playback.prime();
        render(); return;
      }
      if (m.listen === 'off' && m.speak !== 'off') { abortDuplex(); setVoiceMode('off'); return; }
      // A MEETING IS A LANE, NOT ONLY A MODE, so leaving one retires the
      // lane FIRST. startDuplex opens nothing while a conversation is
      // resident (`if (resident) return`), so the meeting's own lane
      // outlived the change and went on recording the room under an
      // interactive mode. The replacement queues
      // behind this lane's closed acknowledgement — newLane opens a lane
      // only when it is the only one — so the new conversation begins
      // when the host says the meeting is over, and with its own live
      // microphone. The menu's meeting item already had this shape.
      if (m.listen === 'meeting') abortDuplex();
      // Off, or a meeting the operator is leaving: talking begins again.
      setVoiceMode('interactive');
      residentWanted = true; render(); startDuplex('interactive');
    });
  }
  renderConverse();
}
