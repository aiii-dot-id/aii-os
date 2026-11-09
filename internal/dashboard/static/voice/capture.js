import { S } from '../state.js';
import { $ } from '../util.js';
import { toast } from '../app.js';
import { browserEcho } from '../voice-echo.js';
import { V } from './state.js';
import { render, renderMicMenu, closeMicMenu } from '../voice.js';
import { resident, residentWanted, playback, captureLane, newLane, engineHears, voiceMode, laneMode, offAcceptedSince, stopTracks, abortDuplex } from './session.js';
import { speakerLane, retireSpeaker } from './speech.js';

const FRAME_VERSION = 1;
const HEADER_BYTES = 8;

let node = null;
let chunks = [];
let captured = 0;
let overflow = false;
let stopping = false;

export let sendFrame = null;
export function setFrameSender(fn) { sendFrame = fn; }

const MIC_KEY = 'aii.mic';
export let micPinned = '';
export let micPinnedLabel = '';
export let micInputs = [];
export let micDefaultLabel = '';
export let micLabel = '';

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

export async function openMicStream(selected = 'browser') {
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

export function noteCaptureDevice(stream) {
  micLabel = '';
  const track = stream && stream.getAudioTracks ? stream.getAudioTracks()[0] : null;
  if (track) {
    micLabel = track.label || '';
    track.addEventListener('ended', () => {
      if (V.media !== stream) return;
      toast((micLabel || 'The microphone') + ' was disconnected — listening stopped.');
      if (resident || residentWanted) abortDuplex();
      else { V.held = false; V.latched = false; stopCapture(false); }
      refreshMicInputs();
      render();
    });
  }
  refreshMicInputs();
}

export async function refreshMicInputs() {
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

export function chooseMic(id) {
  const chosen = micInputs.find(d => d.id === id);
  micPinned = id || '';
  micPinnedLabel = chosen ? chosen.label : '';
  savePinnedMic();
  renderMicMenu();
  closeMicMenu();
  if (V.capturing || resident) {
    toast('The next time you talk: ' + (micPinned ? (micPinnedLabel || 'the microphone you chose') : 'the browser\'s default') + '.');
  }
}

export function micInputsInto(box) {
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

export async function startCapture() {
  if (V.capturing) return;

  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    toast('The microphone works only over https or on this computer.');
    return;
  }
  const askedRev = voiceMode().revision;
  const epoch = ++V.captureEpoch;
  let stream;
  try {
    stream = await openMicStream();
  } catch (err) {
    if (epoch !== V.captureEpoch) return;
    toast('No microphone: ' + (err && err.message ? err.message : err));
    return;
  }

  if (epoch !== V.captureEpoch) { stopTracks(stream); return; }
  if (!V.held || offAcceptedSince(askedRev)) { stopTracks(stream); V.held = false; V.latched = false; render(); return; }
  V.media = stream;
  noteCaptureDevice(V.media);
  V.ctx = new (window.AudioContext || window.webkitAudioContext)();
  reportCaptureSettings(V.media);
  const src = V.ctx.createMediaStreamSource(V.media);

  chunks = [];
  captured = 0;
  overflow = false;

  let lane = null;
  if (engineHears() && speakerLane) retireSpeaker();
  if (engineHears()) lane = newLane(V.ctx.sampleRate, laneMode(voiceMode().listen));
  await openMicNode(V.ctx, src, buf => {
    if (!V.capturing || overflow) return;
    if (lane) { lane.chunk(buf); return; }

    const limit = frameLimit();
    if (limit && (captured + buf.length) * 2 + HEADER_BYTES > limit) {
      overflow = true;

      setTimeout(() => { V.held = false; V.latched = false; stopCapture(true); render(); }, 0);
      return;
    }
    captured += buf.length;
    chunks.push(buf);
  });
  const mine = V.media === stream;
  if (!mine || !V.held || offAcceptedSince(askedRev)) {
    if (lane) lane.abort();
    if (mine) { V.held = false; V.latched = false; teardown(); } else stopTracks(stream);
    render();
    return;
  }
  V.capturing = true;
  render();
}

function frameLimit() {
  const n = S.stats && S.stats.voice_max_frame_bytes;
  return typeof n === 'number' && n > 0 ? n : 0;
}

export async function stopCapture(send) {
  if (!V.capturing || stopping) return;
  let tail = { samples: 0, confirmed: true, reason: '' };
  if (send) { stopping = true; tail = await flushCapture(); stopping = false; }
  if (!V.capturing) return;
  V.capturing = false;
  captured = 0;
  const rate = V.ctx ? V.ctx.sampleRate : 0;
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

export function teardown() {
  V.captureEpoch++;
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
  if (V.ctx) { if (playback.reference) { try { playback.reference.disconnect(); } catch (e) {} }
    if (V.ctx !== playback.ctx) { try { V.ctx.close(); } catch (e) {} } V.ctx = null; }
  if (V.media) { V.media.getTracks().forEach(t => t.stop()); V.media = null; }
  micLabel = '';
}

let captureKind = '';
let flushSeq = 0;
const flushWaiters = new Map();
export let staleCapture = 0;

export async function openMicNode(ac, src, onFrame, reference = null) {
  const frame = Math.max(160, Math.round(ac.sampleRate * 0.02));
  const epoch = V.captureEpoch;
  if (ac.audioWorklet && typeof AudioWorkletNode !== 'undefined') {
    try {
      await ac.audioWorklet.addModule(new URL('../voice-capture.worklet.js', import.meta.url));
      if (epoch !== V.captureEpoch) throw new Error('Capture superseded');
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
  sp.onaudioprocess = e => { if (epoch !== V.captureEpoch) { staleCapture++; return; } onFrame(new Float32Array(e.inputBuffer.getChannelData(0))); };
  src.connect(sp); sp.connect(ac.destination);
  node = sp; captureKind = 'scriptprocessor';
  return 'scriptprocessor';
}

export function captureMessage(data, onFrame, epoch) {
  if (!data) return;
  if (epoch !== undefined && epoch !== V.captureEpoch) { staleCapture++; return; }
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

export function flushCapture() {
  if (captureKind === 'worklet' && node && node.port) return flushPort(node.port, 300);
  if (captureKind === 'scriptprocessor') return Promise.resolve({ samples: 0, confirmed: false, reason: 'the ScriptProcessor fallback cannot flush its held tail' });
  return Promise.resolve({ samples: 0, confirmed: true, reason: '' });
}

export function reportTail(tail) {
  S.captureTail = { confirmed: !!tail.confirmed, samples: tail.samples | 0, reason: tail.reason || '' };
  if (!tail.confirmed) toast('The end of what you said could not be confirmed (' + (tail.reason || 'no acknowledgement') + '); the input ended at the last sample the engine was given.');
}

export async function finishWithTail(conv, flush) {
  if (!conv || !conv.active || conv.finished) return { samples: 0, confirmed: true, reason: '', finished: false };
  const tail = await flush();
  conv.finishInput();
  return Object.assign({ finished: true }, tail);
}

export function reportCaptureSettings(stream) {
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
