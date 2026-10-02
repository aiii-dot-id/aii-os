
import { S } from './state.js';
import { $ } from './util.js';
import { toast } from './app.js';
import { spokenAudio, hushSpoken, whenSpeaking } from './say.js';
import { echoMode, nativeEcho, browserEcho, referenceRoles } from './voice-echo.js';

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
let latched = false;
let downAt = 0;
const LATCH_MS = 400;
let stopping = false;

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
    this.processing = o.processing || null;
    this.seq = 0; this.samples = 0; this.isOpen = false; this.pending = []; this.gap = false; this.dropped = 0;
    this.state = 'waiting'; this.ended = false; this.closeRequested = false; this.closeSent = false; this.aborted = false;
    this.inputFinished = false;
    this.onClosed = null;
  }
  open() {
    this.state = 'opening';
    const voice = { action: 'open', mode: this.mode, rate: this.rate, channels: this.channels };
    if (this.processing && this.mode !== 'output') voice.processing = this.processing;
    this.sendJSON({ type: 'voice_session', voice });
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
    if (f32.length % this.channels) throw new Error('Incomplete interleaved audio frame');
    this.emit(STREAM_PCM, toInt16(f32));
    this.samples += f32.length / this.channels;
  }

  end() {
    if (this.ended) return;
    this.ended = true;
    if (!this.inputFinished) { this.inputFinished = true; if (this.mode !== 'output') this.emit(STREAM_END, null); }
    this.closeRequested = true;
    if (this.isOpen) this.sendClose();
  }
  sendClose() {
    if (this.closeSent) return;
    this.closeSent = true;
    this.sendJSON({ type: 'voice_session', voice: { action: 'close' } });
  }

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

export class Conversation {
  constructor(o) {
    this.newLane = o.newLane;
    this.onEnded = o.onEnded || null;
    this.pre = new PreRoll(o.prerollSamples || 0);
    this.lane = null;
    this.active = false;
    this.finished = false;
    this.finishing = false;
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
    if (this.finished) return;
    if (this.lane && !this.lane.ended) this.lane.chunk(f32);
  }
  finishInput() {
    if (!this.active || this.finished || !this.lane || this.lane.ended) return;
    this.finished = true;
    this.lane.finishInput();
  }
  inputClosedByEngine() {
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
  constructor(o) {
    o = o || {};
    this.report = o.report || null;
    this.newContext = o.audioContext || (rate => new (window.AudioContext || window.webkitAudioContext)({ sampleRate: rate }));
    this.sessionID = '';
    this.ctx = null; this.closing = null; this.suspending = null; this.wakeEpoch = 0; this.captureOpening = 0; this.at = 0; this.rate = 0; this.channels = 1;
    this.received = 0; this.scheduled = 0; this.stopped = 0; this.reported = 0;
    this.sources = []; this.lastStream = null;
    this.announced = null;
    this.tombstones = new Set();
    this.streams = new Map();
    this.timer = null;
    this.retries = new Map();
    this.unresolved = new Map();
    this.timers = new Set();
    this.foreign = 0;
    this.onUnresolved = o.onUnresolved || null;
    this.retryBase = 150;
    this.retryLimit = 6;
  }
  stream(id) {
    let st = this.streams.get(id);
    if (!st) { st = { scheduled: 0, played: 0, ended: false, live: 0, terminal: false, outcome: '', last: -1, report: null }; this.streams.set(id, st); }
    return st;
  }
  prime(rate) {
    if (this.closing || this.captureOpening) return false;
    if (!this.ctx) {
      try { this.ctx = this.newContext(rate || 48000); } catch (e) { return false; }
      this.at = 0;
    }
    this.resume();
    return !!this.ctx;
  }
  useCaptureContext(context) {
    if (this.ctx || this.closing) throw new Error('Previous playback context is still owned');
    this.ctx = context; this.at = 0;
    this.reference = context.createGain();
    this.reference.channelCount = 1;
    this.reference.channelCountMode = 'explicit';
    this.resume();
    return this.reference;
  }
  release() {
    if (this.closing) return this.closing;
    if (!this.ctx) return Promise.resolve();
    this.stop('release');
    const c = this.ctx;
    this.ctx = null; this.reference = null; this.at = 0;
    try {
      this.closing = Promise.resolve(typeof c.close === 'function' ? c.close() : null)
        .then(() => { this.closing = null; });
    } catch (e) { this.closing = Promise.reject(e); }
    return this.closing;
  }
  holdForCapture(epoch) { this.captureOpening = epoch; }
  captureReady(epoch) { if (this.captureOpening === epoch) this.captureOpening = 0; }
  park() {
    this.wakeEpoch++;
    const c = this.ctx;
    if (!c || c.state !== 'running' || this.suspending || typeof c.suspend !== 'function') return;
    try {
      const pending = Promise.resolve(c.suspend()).catch(() => {});
      this.suspending = pending;
      pending.then(() => { if (this.suspending === pending) this.suspending = null; });
    } catch (e) { }
  }
  resume() {
    const c = this.ctx;
    if (c && this.suspending) {
      const epoch = this.wakeEpoch;
      this.suspending.then(() => { if (this.ctx === c && this.wakeEpoch === epoch) this.resume(); });
      return;
    }
    if (!c || c.state !== 'suspended' || typeof c.resume !== 'function') return;
    try { const p = c.resume(); if (p && typeof p.catch === 'function') p.catch(() => {}); } catch (e) { }
  }
  feed(buf) {
    const f = decodeStreamFrame(buf);
    if (!f) return;
    if (f.kind === STREAM_PCM) this.received += f.pcm.length / (f.channels || 1);
    if (this.closing || this.captureOpening) {
      this.tombstones.add(f.stream);
      if (!this.streams.has(f.stream)) this.terminate(f.stream, 0, 'stopped');
      return;
    }
    if (this.tombstones.has(f.stream)) {
      if (!this.streams.has(f.stream)) this.terminate(f.stream, 0, 'stopped');
      return;
    }
    if (f.kind === STREAM_END) { const st = this.stream(f.stream); st.ended = true; this.settle(f.stream); return; }
    if (f.kind !== STREAM_PCM || f.pcm.length === 0) return;
    if (!this.ctx) { this.ctx = this.newContext(f.rate); this.at = 0; }
    this.rate = f.rate;
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
    if (this.reference) src.connect(this.reference);
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
      this.timer = setInterval(() => { this.progress(); if (!this.sources.length) { clearInterval(this.timer); this.timer = null; } }, 250);
    }
  }
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
  settle(stream) {
    const st = this.streams.get(stream);
    if (!st || st.terminal || !st.ended || st.live > 0) return;
    this.terminate(stream, st.played, 'drained');
  }
  terminate(stream, rendered, outcome) {
    const st = this.stream(stream);
    st.terminal = true; st.outcome = outcome; st.played = rendered;
    st.report = { rendered, outcome };
    this.unresolved.delete(stream);
    this.send(stream, rendered, true, outcome);
  }
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
      if (this.sessionID !== sessionID) return;
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

  retire(stream) {
    const s = stream >>> 0;
    if (!this.playingStream(s)) { this.tombstones.add(s); return 0; }
    const stopAt = this.ctx ? this.ctx.currentTime : 0;
    let n = 0;
    for (const e of this.sources) {
      if (e.stream !== s) continue;
      try { e.src.stop(); n++; } catch (err) {  }
    }
    const played = this.rendered(s, stopAt);
    for (const e of this.sources) if (e.stream === s) e.done = true;
    const st = this.streams.get(s);
    if (st) st.live = 0;
    this.sources = this.sources.filter(e => e.stream !== s);
    this.stopped += n;
    this.at = this.reflow(stopAt);
    this.tombstones.add(s);
    if (!this.stream(s).terminal) this.terminate(s, played, 'stopped');
    return n;
  }
  reflow(now) {
    let at = 0;
    const lead = now + 0.02;
    for (const e of this.sources) {
      if (e.done) continue;
      const start = Math.max(at, lead);
      if (e.startAt > start) {
        const dur = e.until - e.startAt;
        const old = e.src;
        old.onended = null;
        try { old.stop(); } catch (err) {  }
        const src = this.ctx.createBufferSource();
        src.buffer = old.buffer; src.connect(this.ctx.destination);
        if (this.reference) src.connect(this.reference);
        src.start(start);
        src.onended = () => { this.ended(e); };
        e.src = src; e.startAt = start; e.until = start + dur;
      }
      at = Math.max(at, e.until);
    }
    return at;
  }
  playingStream(stream) {
    const s = stream >>> 0;
    return this.sources.some(e => e.stream === s && !e.done);
  }
  announce(stream) {
    const s = stream >>> 0;
    if (this.announced !== null && this.announced !== s) this.retire(this.announced);
    this.announced = s;
  }
  replaced() {
    if (this.announced !== null || this.lastStream === null) return 0;
    return this.retire(this.lastStream);
  }

  stop(cause) {
    this.wakeEpoch++;
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
  reset(sessionID, rate, channels) {
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
const playback = new Playback({
  report: r => { if (sendJSON) sendJSON({ type: 'voice_session', voice: { action: 'playback', playback: r } }); },
  onUnresolved: (stream, reason) => toast('The engine did not accept the playback receipt for this reply' + (reason ? ' (' + reason + ')' : '') + '; its drain cannot be reported as clean.'),
});
if (typeof document !== 'undefined' && document.addEventListener) {
  document.addEventListener('visibilitychange', () => { if (!document.hidden) playback.resume(); });
}

function captureLane() { return lanes.length ? lanes[lanes.length - 1] : null; }

function newLane(rate, mode, paired = false) {
  const c = S.captureSettings;
  const processing = c && mode !== 'output' ? {
    echo_cancellation: c.echoCancellation,
    noise_suppression: c.noiseSuppression,
    auto_gain_control: c.autoGainControl,
    sample_rate: c.sampleRate || null,
    ...(paired ? { channel_roles: referenceRoles } : {}),
  } : null;
  const l = new StreamLane({ sendFrame, sendJSON, rate, channels: paired ? 2 : 1, mode: mode || 'conversation', processing });
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
  const refusedSpeaker = !!(st && st.state === 'refused' && head && head.mode === 'output');
  if (refusedSpeaker) speakerRefusedAt = Date.now();
  if (head) head.onState(st || {});
  if (st && st.state === 'open') { inputEndedWhy = ''; playback.reset(st.session_id, head ? head.rate : 0, head ? head.channels : 1); }
  if (st && st.state === 'input_complete') engineClosedInput(st);
  if (st && st.state === 'refused' && !refusedSpeaker) toast('The speech engine refused the session: ' + (st.reason || 'no reason given'));
  render();
}

let inputEndedWhy = '';
let transcriptPlaceholder = '';

function inputEndSentence(reason) {
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

function engineClosedInput(st) {
  const id = (st && st.session_id) || '';
  if (id && playback.sessionID && id !== playback.sessionID) return;
  if (!resident || !resident.inputClosedByEngine()) return;
  inputEndedWhy = inputEndSentence(st && st.reason);
  stopResidentCapture();
  toast(inputEndedWhy);
}

export function receiveFrame(buf) { playback.feed(buf); if (speakerLane) watchSpeakerPlayback(); }

export function voiceEvent(ev) {
  ev = ev || {};
  const t = ev.type || '';
  const ownSession = !ev.session_id || ev.session_id === playback.sessionID;
  if (ownSession && t === 'interruption_requested' && ev.stream) playback.retire(ev.stream);
  else if (ownSession && (t === 'speech_start' || t === 'interruption_requested')) playback.stop();
  if (ownSession && t === 'synthesis_start' && ev.stream) playback.announce(ev.stream);
  if (t === 'receipt_refused') { playback.receiptRefused(ev.session_id || '', (ev.stream || 0) >>> 0, ev.reason || ''); return; }
  const el = $('voice-transcript');
  if (!el) return;
  if (t === 'transcript_final' || t === 'transcript_partial') {
    const text = (ev.text || '').trim();
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
  if (t === 'transcript_withheld') {
    el.textContent = 'A voice was withheld' + (ev.reason ? ' — ' + ev.reason : '') + '.';
    el.classList.add('provisional'); el.classList.remove('failed');
    el.hidden = false;
    renderConverse();
    return;
  }
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
export function converseForTest(state) {
  residentWanted = state !== 'idle';
  resident = state === 'live' ? { streaming() { return true; }, frame() {}, start() {} } : null;
  renderConverse();
}
export function captureFenceForTest() { return { epoch: () => captureEpoch, retire: () => teardown(), stale: () => staleCapture }; }
export function lanesForTest() { return lanes; }
export function inputEndSentenceForTest(reason) { return inputEndSentence(reason); }

function engineLane() { return !!(S.stats && S.stats.voice_engine && sendJSON); }

function engineHears() { return engineLane() && micState() === 'plugin'; }
function engineSpeaks() { return engineLane() && !(S.stats && S.stats.reply_voice); }

const MODE_PAIRS = {
  interactive: { listen: 'interactive', speak: 'on' },
  earbuds: { listen: 'off', speak: 'on' },
  off: { listen: 'off', speak: 'off' },
  meeting: { listen: 'meeting', speak: 'off' },
};

let modeRev = 0;
let modeHeld = { listen: 'interactive', speak: 'auto' };
let offSeenRev = 0;

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

export function modeName(listen, speak) {
  const spoken = speak !== 'off';
  if (listen === 'interactive') return spoken ? 'interactive' : 'interactive, text replies';
  if (listen === 'meeting') return spoken ? 'meeting, spoken replies' : 'meeting';
  return spoken ? 'earbuds' : 'off';
}

function setVoiceMode(name) {
  const pair = MODE_PAIRS[name];
  if (!pair || !sendJSON) return;
  if (pair.listen !== 'off' || pair.speak !== 'on') {
    speakerStoppedAtRevision = voiceMode().revision;
    if (speakerLane) retireSpeaker();
  }
  if (pair.speak === 'off') hushForSpeakOff();
  sendJSON({ type: 'config_set', config: { 'speech.mode': { listen: pair.listen, speak: pair.speak } } });
}

let spokenHalf = 'auto';
function hushForSpeakOff() { spokenHalf = 'off'; hush(); }
function followSpeakHalf(speak) {
  const was = spokenHalf;
  spokenHalf = speak;
  if (speak === 'off' && was !== 'off') hush();
}

let listenFollowedRevision = -1;
function followListenHalf(listen, speak, revision) {
  const changed = revision !== listenFollowedRevision;
  listenFollowedRevision = revision;
  if (listen !== 'off') return;
  if (speak === 'off' && (resident || residentWanted)) {
    if (changed) abortDuplex();
    return;
  }
  if (resident && resident.streaming() && !resident.finished && !resident.finishing) { finishInput(); return; }
  if (resident) return;
  if (latched && capturing) { latched = false; stopCapture(true); return; }
}

function nextMode(m) {
  if (m.listen === 'meeting') return 'interactive';
  if (m.listen === 'off') return m.speak === 'off' ? 'interactive' : 'off';
  return 'earbuds';
}

function laneMode(listen) { return listen === 'meeting' ? 'meeting' : 'conversation'; }

const MIC_KEY = 'aii.mic';
let micPinned = '';
let micPinnedLabel = '';
let micInputs = [];
let micDefaultLabel = '';
let micLabel = '';

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

function audioConstraint(ignorePin, selected = 'browser') {
  const audio = { echoCancellation: browserEcho(selected) ? true : { exact: false }, noiseSuppression: true, autoGainControl: true, channelCount: 1 };
  if (micPinned && !ignorePin) audio.deviceId = { exact: micPinned };
  return { audio };
}

async function openMicStream(selected = 'browser') {
  let stream;
  try {
    stream = await navigator.mediaDevices.getUserMedia(audioConstraint(false, selected));
  } catch (err) {
    const name = err && err.name;
    if (!micPinned || (name !== 'OverconstrainedError' && name !== 'NotFoundError')) throw err;
    stream = await navigator.mediaDevices.getUserMedia(audioConstraint(true, selected));
    toast((micPinnedLabel || 'The microphone you chose') + ' is not connected — listening with the browser\'s default.');
  }
  const track = stream.getAudioTracks && stream.getAudioTracks()[0];
  try {
    const caps = track && track.getCapabilities && track.getCapabilities();
    if (browserEcho(selected) && caps && Array.isArray(caps.echoCancellation) && caps.echoCancellation.includes('all') &&
        typeof track.applyConstraints === 'function') {
      await track.applyConstraints({ echoCancellation: { exact: 'all' } });
    }
  } catch (e) { }
  return stream;
}

function noteCaptureDevice(stream) {
  micLabel = '';
  const track = stream && stream.getAudioTracks ? stream.getAudioTracks()[0] : null;
  if (track) {
    micLabel = track.label || '';
    track.addEventListener('ended', () => {
      if (media !== stream) return;
      toast((micLabel || 'The microphone') + ' was disconnected — listening stopped.');
      if (resident || residentWanted) abortDuplex();
      else { held = false; latched = false; stopCapture(false); }
      refreshMicInputs();
      render();
    });
  }
  refreshMicInputs();
}

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
  const askedRev = voiceMode().revision;
  const epoch = ++captureEpoch;
  let stream;
  try {
    stream = await openMicStream();
  } catch (err) {
    if (epoch !== captureEpoch) return;
    toast('No microphone: ' + (err && err.message ? err.message : err));
    return;
  }

  if (epoch !== captureEpoch) { stopTracks(stream); return; }
  if (!held || offAcceptedSince(askedRev)) { stopTracks(stream); held = false; latched = false; render(); return; }
  media = stream;
  noteCaptureDevice(media);
  ctx = new (window.AudioContext || window.webkitAudioContext)();
  reportCaptureSettings(media);
  const src = ctx.createMediaStreamSource(media);

  chunks = [];
  captured = 0;
  overflow = false;

  let lane = null;
  if (engineHears() && speakerLane) retireSpeaker();
  if (engineHears()) lane = newLane(ctx.sampleRate, laneMode(voiceMode().listen));
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
  let tail = { samples: 0, confirmed: true, reason: '' };
  if (send) { stopping = true; tail = await flushCapture(); stopping = false; }
  if (!capturing) return;
  capturing = false;
  captured = 0;
  const rate = ctx ? ctx.sampleRate : 0;
  const collected = chunks;
  chunks = [];
  teardown();
  const lane = captureLane();
  if (lane && !lane.ended) {
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
  reportTail(tail);
}

function teardown() {
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
  if (ctx) { if (playback.reference) { try { playback.reference.disconnect(); } catch (e) {} }
    if (ctx !== playback.ctx) { try { ctx.close(); } catch (e) {} } ctx = null; }
  if (media) { media.getTracks().forEach(t => t.stop()); media = null; }
  micLabel = '';
}

let captureKind = '';
let flushSeq = 0;
const flushWaiters = new Map();
let captureEpoch = 0;
let staleCapture = 0;

async function openMicNode(ac, src, onFrame, reference = null) {
  const frame = Math.max(160, Math.round(ac.sampleRate * 0.02));
  const epoch = captureEpoch;
  if (ac.audioWorklet && typeof AudioWorkletNode !== 'undefined') {
    try {
      await ac.audioWorklet.addModule(new URL('voice-capture.worklet.js', import.meta.url));
      if (epoch !== captureEpoch) throw new Error('Capture superseded');
      const wn = new AudioWorkletNode(ac, 'voice-capture', { numberOfInputs: reference ? 2 : 1, numberOfOutputs: 0, channelCount: 1, channelCountMode: 'explicit', processorOptions: { frame, reference: !!reference } });
      wn.port.onmessage = e => captureMessage(e.data, onFrame, epoch);
      src.connect(wn);
      if (reference) reference.connect(wn, 0, 1);
      node = wn; captureKind = 'worklet';
      return 'worklet';
    } catch (e) { if (reference) throw e; }
  }
  if (reference) throw new Error('Native echo processing requires AudioWorklet');
  const sp = ac.createScriptProcessor(4096, 1, 1);
  sp.onaudioprocess = e => { if (epoch !== captureEpoch) { staleCapture++; return; } onFrame(new Float32Array(e.inputBuffer.getChannelData(0))); };
  src.connect(sp); sp.connect(ac.destination);
  node = sp; captureKind = 'scriptprocessor';
  return 'scriptprocessor';
}

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

function flushCapture() {
  if (captureKind === 'worklet' && node && node.port) return flushPort(node.port, 300);
  if (captureKind === 'scriptprocessor') return Promise.resolve({ samples: 0, confirmed: false, reason: 'the ScriptProcessor fallback cannot flush its held tail' });
  return Promise.resolve({ samples: 0, confirmed: true, reason: '' });
}

function reportTail(tail) {
  S.captureTail = { confirmed: !!tail.confirmed, samples: tail.samples | 0, reason: tail.reason || '' };
  if (!tail.confirmed) toast('The end of what you said could not be confirmed (' + (tail.reason || 'no acknowledgement') + '); the input ended at the last sample the engine was given.');
}

export async function finishWithTail(conv, flush) {
  if (!conv || !conv.active || conv.finished) return { samples: 0, confirmed: true, reason: '', finished: false };
  const tail = await flush();
  conv.finishInput();
  return Object.assign({ finished: true }, tail);
}

function reportCaptureSettings(stream) {
  try {
    const tr = stream.getAudioTracks ? stream.getAudioTracks()[0] : null;
    const s = tr && tr.getSettings ? tr.getSettings() : {};
    S.captureSettings = {
      echoCancellation: s.echoCancellation === 'all' || s.echoCancellation === 'remote-only' ? true :
        (typeof s.echoCancellation === 'boolean' ? s.echoCancellation : null),
      noiseSuppression: typeof s.noiseSuppression === 'boolean' ? s.noiseSuppression : null,
      autoGainControl: typeof s.autoGainControl === 'boolean' ? s.autoGainControl : null,
      sampleRate: Number.isInteger(s.sampleRate) && s.sampleRate > 0 ? s.sampleRate : null,
      tested: false,
    };
  } catch (e) { S.captureSettings = { tested: false }; }
}

let speakerLane = null;
let speakerRefusedAt = 0;
let speakerStoppedAtRevision = -1;
let speakerWatch = null;
let cloudSpeaking = false;
const SPEAKER_RETRY_MS = 15000;

function speakerWanted() {
  if (!engineSpeaks() || S.connected === false) return false;
  const m = voiceMode();
  if (m.revision <= speakerStoppedAtRevision) return false;
  if (m.listen !== 'off' || m.speak !== 'on') return false;
  if (resident || residentWanted || held || capturing || latched) return false;
  return micState() !== 'safe';
}

function syncSpeaker() {
  if (!speakerWanted()) { if (speakerLane) retireSpeaker(); return; }
  if (speakerLane) return;
  if (speakerRefusedAt && Date.now() - speakerRefusedAt < SPEAKER_RETRY_MS) return;
  if (lanes.length) return;
  openSpeaker();
}

function openSpeaker() {
  const rate = (playback.ctx && playback.ctx.sampleRate) || 48000;
  const lane = newLane(rate, 'output');
  speakerLane = lane;
  const queued = lane.onClosed;
  lane.onClosed = l => {
    if (speakerLane === l) speakerLane = null;
    if (queued) queued(l);
    syncSpeaker();
  };
  if (typeof document !== 'undefined' && document.addEventListener) {
    const wake = () => { if (speakerLane === lane && speakerWanted()) playback.prime(rate); };
    document.addEventListener('pointerdown', wake, { once: true, passive: true });
    document.addEventListener('keydown', wake, { once: true, passive: true });
  }
}

function retireSpeaker() {
  const l = speakerLane;
  speakerLane = null;
  if (!l) return;
  if (l.state === 'waiting') { lanes = lanes.filter(x => x !== l); return; }
  playback.stop();
  l.abort();
}

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

export async function startDuplex(want) {
  if (resident) return;
  if (speakerLane) retireSpeaker();
  const listen = want || voiceMode().listen;
  const selectedEcho = echoMode();
  const paired = nativeEcho(selectedEcho);
  const askedRev = voiceMode().revision;
  if (listen === 'off') { toast('Listening is off — one tap on the microphone turns it back on.'); residentWanted = false; render(); return; }
  if (held || capturing) { toast('Finish the push-to-talk first.'); residentWanted = false; render(); return; }
  if (!engineHears()) { toast('A speech engine must be active for a spoken conversation.'); residentWanted = false; render(); return; }
  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    toast('The microphone needs a secure context — serve the dashboard over HTTPS, or reach it on localhost.');
    residentWanted = false; render(); return;
  }
  const epoch = ++captureEpoch;
  try { await playback.release(); }
  catch (e) {
    if (epoch === captureEpoch) {
      residentWanted = false;
      toast('The previous speaker route did not close; listening cannot start safely.');
      render();
    }
    return;
  }
  if (epoch !== captureEpoch || !residentWanted || offAcceptedSince(askedRev)) return;
  playback.holdForCapture(epoch);
  let stream;
  try {
    stream = await openMicStream(selectedEcho);
  } catch (err) {
    playback.captureReady(epoch);
    if (epoch !== captureEpoch) return;
    toast('No microphone: ' + (err && err.message ? err.message : err));
    residentWanted = false; render(); return;
  }
  if (epoch !== captureEpoch) { stopTracks(stream); playback.captureReady(epoch); return; }
  if (!residentWanted) { stopTracks(stream); playback.captureReady(epoch); render(); return; }
  if (offAcceptedSince(askedRev)) { stopTracks(stream); playback.captureReady(epoch); residentWanted = false; render(); return; }
  media = stream;
  noteCaptureDevice(media);
  try { ctx = new (window.AudioContext || window.webkitAudioContext)(); }
  catch (e) {
    playback.captureReady(epoch);
    stopTracks(stream);
    media = null;
    residentWanted = false;
    toast('The microphone audio route could not open: ' + (e && e.message ? e.message : e));
    render();
    return;
  }
  reportCaptureSettings(media);
  playback.captureReady(epoch);
  const reference = paired ? playback.useCaptureContext(ctx) : null;
  if (!paired) playback.prime(ctx.sampleRate);
  const src = ctx.createMediaStreamSource(media);
  const rate = ctx.sampleRate;
  resident = new Conversation({
    newLane: () => newLane(rate, laneMode(listen), paired),
    prerollSamples: Math.round(rate * 0.3) * (paired ? 2 : 1),
    onEnded: () => { abortDuplex(); },
  });
  const conv = resident;
  try {
    await openMicNode(ctx, src, f32 => { if (resident === conv) conv.frame(f32); }, reference);
  } catch (e) {
    if (media !== stream) { stopTracks(stream); return; }
    toast('The microphone could not start: ' + (e && e.message ? e.message : e));
    abortDuplex(); return;
  }
  if (media !== stream) { stopTracks(stream); return; }
  if (!residentWanted || offAcceptedSince(askedRev)) { abortDuplex(); return; }
  resident.start();
  render();
}

function offAcceptedSince(rev) {
  voiceMode();
  return offSeenRev > rev;
}

function stopTracks(stream) {
  if (stream && stream.getTracks) stream.getTracks().forEach(t => t.stop());
}

export async function finishInput() {
  if (!resident || resident.finished || resident.finishing) return;
  const conv = resident;
  conv.finishing = true;
  const tail = await finishWithTail(conv, flushCapture);
  if (resident !== conv) return;
  reportTail(tail);
  stopResidentCapture();
}

function stopResidentCapture() {
  capturing = false;
  teardown();
}

export function abortDuplex() {
  residentWanted = false;
  if (resident) { resident.abort(); resident = null; }
  teardown();
  playback.stop();
  render();
}

export function residentActive() { return !!resident; }

export function speak(text, ref) {
  const mode = voiceMode();
  if (mode.speak === 'off') return;
  if (ref && ref.route === 'plugin') { playback.replaced(); return; }
  if (!text) return;
  if (ref && ref.route === 'cloud' && ref.synthesis_id) { sayWithService(text, { id: ref.synthesis_id }); return; }
  if (ref && ref.route === 'browser') { if (mode.speak === 'on' || S.voiceSpeak) browserSpeak(text); return; }
  if (S.stats && S.stats.reply_voice) { sayWithService(text, { text: text }); return; }
  if (mode.speak !== 'on' && !S.voiceSpeak) return;
  browserSpeak(text);
}

function browserSpeak(text) {
  if (!window.speechSynthesis || !text) return;
  window.speechSynthesis.cancel();
  const u = new SpeechSynthesisUtterance(text);
  const stop = $('hush-speaking');
  if (stop) stop.hidden = false;
  u.onend = u.onerror = () => { if (stop) stop.hidden = true; };
  window.speechSynthesis.speak(u);
}

const hushedReplies = new Set();

function sayWithService(text, ask) {
  spokenAudio(ask).catch(err => {
    if (ask && ask.id && hushedReplies.has(ask.id)) return;
    toast('The speaking service refused this reply: ' + ((err && err.message) || err));
    browserSpeak(text);
  });
}

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

  const head = lanes[0];
  if (head && head.isOpen && sendJSON) sendJSON({ type: 'voice_session', voice: { action: 'interrupt' } });
  playback.stop();
}

export function connectionLost() {
  modeRev = 0;
  offSeenRev = 0;
  listenFollowedRevision = -1;
  speakerStoppedAtRevision = -1;
  held = false;
  latched = false;
  stopCapture(false);
  abortDuplex();
  speakerLane = null; speakerRefusedAt = 0;
  lanes = [];
  playback.stop();
  playback.park();
}

whenSpeaking(on => {
  cloudSpeaking = !!on;
  const b = $('hush-speaking');
  if (b) b.hidden = !on && !(speakerLane && playback.playing());
});

const MIC_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><rect x="5.5" y="1.8" width="5" height="8.4" rx="2.5"/><path d="M3.2 7.6a4.8 4.8 0 0 0 9.6 0M8 12.4v2"/></svg>';
const STOP_ICON = '<svg class="stop" viewBox="0 0 16 16" aria-hidden="true"><rect x="4.5" y="4.5" width="7" height="7" rx="1.5"/></svg>';
const EAR_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M4.9 6.9a3.1 3.1 0 1 1 6.2 0c0 1.7-1.5 2.3-2.1 3.1-.6.8-.3 2-1.5 2.4-1 .3-2-.3-2.1-1.3"/><circle cx="8" cy="6.9" r="1.1"/></svg>';
const MIC_OFF_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><rect x="5.5" y="1.8" width="5" height="8.4" rx="2.5"/><path d="M3.2 7.6a4.8 4.8 0 0 0 9.6 0M8 12.4v2"/><path d="M2.6 2.6l10.8 10.8"/></svg>';
const ROOM_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="5" r="2"/><path d="M4.6 12.3a3.4 3.4 0 0 1 6.8 0"/><circle cx="3.1" cy="6.6" r="1.3"/><path d="M1.2 11.6a2.4 2.4 0 0 1 1.9-2.4"/><circle cx="12.9" cy="6.6" r="1.3"/><path d="M14.8 11.6a2.4 2.4 0 0 0-1.9-2.4"/></svg>';
const GLYPHS = { mic: MIC_ICON, stop: STOP_ICON, ear: EAR_ICON, micoff: MIC_OFF_ICON, room: ROOM_ICON };

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

const MIC_TITLES = {
  setup: 'Voice isn\'t set up — opens Speech settings',
  unreachable: 'Voice can\'t reach its service — opens Speech settings',
  safe: 'Voice is paused while this identity is in SAFE',
};

export function render() {
  const m = voiceMode();
  followSpeakHalf(m.speak);
  followListenHalf(m.listen, m.speak, m.revision);
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
  const glyph = state !== 'cloud' || (recording && m.listen !== 'meeting') ? 'mic' : modeGlyph(m);
  drawGlyph(b, glyph);
  const speaks = !!(S.stats && S.stats.reply_voice);
  b.title = inputEndedWhy && !recording ? inputEndedWhy
    : state === 'cloud' ? micModeTitle(m, recording)
    : state === 'setup' && speaks ? 'Voice input isn\'t set up — opens Speech settings'
    : MIC_TITLES[state] || MIC_TITLES.setup;
  b.setAttribute('aria-label', b.title);
}

function micModeTitle(m, recording) {
  if (recording) return 'Tap to send what you said — the microphone then rests and replies are spoken';
  switch (modeGlyph(m)) {
    case 'room': return 'Meeting — hold to record the room, a segment at a time; tap to talk with the identity';
    case 'ear': return 'Earbuds — replies are spoken and you type; tap for silence, or hold to talk';
    case 'micoff': return 'Voice off — nothing is heard and nothing is spoken; tap to turn the microphone on, or hold to talk';
    default: return 'Interactive — tap to talk, or hold';
  }
}

function renderMicMenu() {
  const more = $('mic-more'), source = $('mic-source'), voice = $('mic-voice');
  if (!more) return;
  const state = micState();
  const speaks = (S.stats && S.stats.reply_voice) || '';
  const carried = (S.stats && S.stats.voice_speaker) || '';
  const hears = state === 'plugin' || state === 'cloud';
  more.hidden = !hears && !speaks && !carried;
  if (more.hidden) closeMicMenu();
  if (voice) {
    voice.hidden = !speaks && !carried;
    voice.textContent = speaks ? 'Replies spoken by ' + speaks
      : carried ? 'Replies spoken by ' + carried + ' until this voice session ends — the engine chosen in Settings speaks from the next one' : '';
  }
  if (source) {
    source.hidden = !hears;
    source.textContent = hears
      ? (state === 'plugin' ? 'Voice plugin' : 'Cloud speech') + (S.stats && S.stats.voice_source ? ' — ' + S.stats.voice_source : '')
      : '';
  }
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

function openSpeechSettings() {
  closeMicMenu();
  if (S.openSettings) S.openSettings('speech', 'sp-provider-stt');
}

function renderConverse() {
  const c = $('converse');
  if (c) {
    const available = micState() === 'plugin';
    c.hidden = !available;
    c.disabled = !available || !S.connected;
    const m = voiceMode();
    const live = m.listen !== 'off' && residentActive();
    const waiting = m.listen !== 'off' && !live && residentWanted;
    c.classList.toggle('live', live);
    c.classList.toggle('waiting', waiting);
    const glyph = m.listen === 'meeting' ? 'room' : live ? 'stop' : modeGlyph(m);
    drawGlyph(c, glyph);
    c.setAttribute('aria-pressed', live ? 'true' : 'false');
    const ended = inputEndedWhy || inputEndSentence(null);
    c.title = m.listen === 'meeting'
      ? 'Meeting — the room is being recorded and replies are not spoken; tap to talk with the identity'
      : live
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
  const stop = $('hush-speaking');
  if (stop) stop.onclick = () => hush();
  wireConverse();
  wireMicMenu();
  const b = $('mic');
  if (!b) return;
  b.removeAttribute('data-inert');

  b.addEventListener('pointerdown', e => {
    if (resident || residentWanted) return;
    e.preventDefault();
    const state = micState();
    if (state === 'setup' || state === 'unreachable') { openSpeechSettings(); return; }
    if (state !== 'cloud') return;
    if (latched) { sendLatched(); setVoiceMode('earbuds'); return; }
    downAt = Date.now(); held = true; hush(); startCapture();
  });
  b.addEventListener('pointerup', e => {
    if (resident || residentWanted || latched || !held) return;
    e.preventDefault();
    if (Date.now() - downAt < LATCH_MS) {
      const m = voiceMode();
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
      const m = voiceMode();
      if (m.listen === 'interactive') {
        if (!resident && !residentWanted) { residentWanted = true; render(); startDuplex('interactive'); return; }
        if (resident && resident.streaming() && !resident.finished && !resident.finishing) finishInput();
        else abortDuplex();
        setVoiceMode('earbuds');
        playback.prime();
        render(); return;
      }
      if (m.listen === 'off' && m.speak !== 'off') { setVoiceMode('off'); abortDuplex(); return; }
      if (m.listen === 'meeting') abortDuplex();
      setVoiceMode('interactive');
      residentWanted = true; render(); startDuplex('interactive');
    });
  }
  renderConverse();
}
