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
    this.gesture = !!o.gesture;
    this.seq = 0; this.samples = 0; this.isOpen = false; this.pending = []; this.gap = false; this.dropped = 0;
    this.state = 'waiting'; this.ended = false; this.closeRequested = false; this.closeSent = false; this.aborted = false;
    this.inputFinished = false;
    this.onClosed = null;
  }
  open() {
    this.state = 'opening';
    const voice = { action: 'open', mode: this.mode, rate: this.rate, channels: this.channels };
    if (this.processing && this.mode !== 'output') voice.processing = this.processing;
    if (this.gesture) voice.gesture = true;
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
