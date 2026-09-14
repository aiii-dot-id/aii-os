// voice-capture.worklet.js — bounded microphone capture for the resident
// conversation and push-to-talk. An AudioWorkletProcessor
// accumulates the input into fixed frames and posts each whole frame to
// the page thread; it holds AT MOST one partial frame, so a slow reader
// can never make it grow without bound. On a flush request it posts that
// held partial (if any) TOGETHER with an acknowledgement naming the
// samples flushed, then stops capturing — so the page can admit the exact
// tail BEFORE it names the session's one input cutoff, and nothing can be
// captured after the acknowledgement. voice.js falls back to the deprecated
// ScriptProcessor where AudioWorklet is unavailable.
//
// Testable off the audio thread: outside an AudioWorkletGlobalScope the
// base class and the registration are stubbed, so the page's browser
// proofs can import this file and drive process() and port.onmessage.
const CaptureBase = typeof AudioWorkletProcessor === 'function' ? AudioWorkletProcessor : class {
  constructor() { this.port = { postMessage() {}, onmessage: null }; }
};

class CaptureProcessor extends CaptureBase {
  constructor(options) {
    super();
    const opt = (options && options.processorOptions) || {};
    this.size = Math.max(64, (opt.frame | 0) || 480);
    this.buf = new Float32Array(this.size);
    this.n = 0;
    this.stopped = false;
    this.port.onmessage = e => {
      const m = e && e.data;
      if (m && m.flush !== undefined) this.flush(m.flush);
    };
  }
  // flush posts the held partial and the acknowledgement in ONE message,
  // then stops: after the ack no further samples exist, so the page's
  // admitted sample clock is exact when it names the cutoff.
  flush(id) {
    const n = this.n;
    const buffer = n > 0 ? this.buf.slice(0, n).buffer : null;
    this.n = 0;
    this.stopped = true;
    if (buffer) this.port.postMessage({ ack: id, samples: n, buffer }, [buffer]);
    else this.port.postMessage({ ack: id, samples: 0, buffer: null });
  }
  process(inputs) {
    if (this.stopped) return true;
    const input = inputs[0];
    const ch = input && input[0];
    if (ch) {
      for (let i = 0; i < ch.length; i++) {
        this.buf[this.n++] = ch[i];
        if (this.n === this.size) {
          const full = this.buf;
          this.port.postMessage(full.buffer, [full.buffer]);
          this.buf = new Float32Array(this.size);
          this.n = 0;
        }
      }
    }
    return true;
  }
}

if (typeof registerProcessor === 'function') registerProcessor('voice-capture', CaptureProcessor);
export { CaptureProcessor };
