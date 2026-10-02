const CaptureBase = typeof AudioWorkletProcessor === 'function' ? AudioWorkletProcessor : class {
  constructor() { this.port = { postMessage() {}, onmessage: null }; }
};

class CaptureProcessor extends CaptureBase {
  constructor(options) {
    super();
    const opt = (options && options.processorOptions) || {};
    this.size = Math.max(64, (opt.frame | 0) || 480);
    this.channels = opt.reference ? 2 : 1;
    this.buf = new Float32Array(this.size * this.channels);
    this.n = 0;
    this.stopped = false;
    this.port.onmessage = e => {
      const m = e && e.data;
      if (m && m.flush !== undefined) this.flush(m.flush);
    };
  }
  flush(id) {
    const n = this.n;
    const buffer = n > 0 ? this.buf.slice(0, n * this.channels).buffer : null;
    this.n = 0;
    this.stopped = true;
    if (buffer) this.port.postMessage({ ack: id, samples: n, buffer }, [buffer]);
    else this.port.postMessage({ ack: id, samples: 0, buffer: null });
  }
  process(inputs) {
    if (this.stopped) return true;
    const input = inputs[0];
    const ch = input && input[0];
    const ref = inputs[1] && inputs[1][0];
    if (ch) {
      for (let i = 0; i < ch.length; i++) {
        this.buf[this.n * this.channels] = ch[i];
        if (this.channels === 2) this.buf[this.n * 2 + 1] = ref ? ref[i] : 0;
        this.n++;
        if (this.n === this.size) {
          const full = this.buf;
          this.port.postMessage(full.buffer, [full.buffer]);
          this.buf = new Float32Array(this.size * this.channels);
          this.n = 0;
        }
      }
    }
    return true;
  }
}

if (typeof registerProcessor === 'function') registerProcessor('voice-capture', CaptureProcessor);
export { CaptureProcessor };
