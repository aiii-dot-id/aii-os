import { PreRoll, STREAM_END, STREAM_PCM, decodeStreamFrame } from '../voice-stream.js';

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
    this.onIdle = null;
    this.onSound = o.onSound || null;
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
    const entry = { src, until: this.at, stream: f.stream, st, startAt, frames, done: false };
    this.sources.push(entry);
    if (this.sources.length === 1 && this.onSound) this.onSound();
    src.onended = () => { this.ended(entry); };
    if (!this.timer && this.report) {
      this.timer = setInterval(() => { this.progress(); if (!this.sources.length) { clearInterval(this.timer); this.timer = null; } }, 250);
    }
  }
  ended(entry) {
    if (entry.done) return;
    entry.done = true;
    this.sources = this.sources.filter(e => e !== entry);
    this.idle();
    const st = entry.st;
    st.live = Math.max(0, st.live - 1);
    if (!st.terminal) st.played += entry.frames;
    if (this.streams.get(entry.stream) !== st) return;
    this.settle(entry.stream);
  }
  rendered(stream, at) {
    const st = this.streams.get(stream);
    if (!st) return 0;
    let n = st.played;
    const now = at === undefined ? (this.ctx ? this.ctx.currentTime : 0) : at;
    for (const e of this.sources) {
      if (e.st !== st || e.done) continue;
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
    if (!this.playingStream(s)) {
      this.settleUnfinished(s);
      this.tombstones.add(s);
      return 0;
    }
    const stopAt = this.ctx ? this.ctx.currentTime : 0;
    const own = this.streams.get(s);
    let n = 0;
    for (const e of this.sources) {
      if (e.st !== own) continue;
      try { e.src.stop(); n++; } catch (err) {  }
    }
    const played = this.rendered(s, stopAt);
    for (const e of this.sources) if (e.st === own) e.done = true;
    if (own) own.live = 0;
    this.sources = this.sources.filter(e => e.st !== own);
    this.stopped += n;
    this.at = this.reflow(stopAt);
    this.tombstones.add(s);
    if (!this.stream(s).terminal) this.terminate(s, played, 'stopped');
    this.idle();
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
    const st = this.streams.get(stream >>> 0);
    return !!st && this.sources.some(e => e.st === st && !e.done);
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
    for (const e of this.sources) if (this.streams.get(e.stream) === e.st) streams.add(e.stream);
    for (const e of this.sources) {
      try { e.src.stop(); n++; } catch (err) {  }
    }
    const played = new Map();
    for (const s of streams) played.set(s, this.rendered(s, stopAt));
    for (const e of this.sources) {
      e.done = true;
      e.st.live = 0;
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
    if (fence) {
      for (const [s, st] of this.streams) {
        if (st.terminal || streams.has(s)) continue;
        if (this.settleUnfinished(s)) this.tombstones.add(s >>> 0);
      }
    }
    this.idle();
    return n;
  }
  settleUnfinished(stream) {
    const st = this.streams.get(stream);
    if (!st || st.terminal || st.report || st.live > 0 || st.scheduled === 0) return false;
    this.terminate(stream, st.played, 'stopped');
    return true;
  }
  idle() { if (this.sources.length) return; if (this.onIdle) this.onIdle(); if (this.onSound) this.onSound(); }
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
