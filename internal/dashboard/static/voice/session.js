import { S } from '../state.js';
import { $ } from '../util.js';
import { toast } from '../app.js';
import { echoMode, nativeEcho, referenceRoles } from '../voice-echo.js';
import { StreamLane } from '../voice-stream.js';
import { V } from './state.js';
import { MODE_PAIRS, voiceMode, modeName, nextMode, laneMode } from './modes.js';
export { voiceMode, modeName, nextMode, laneMode } from './modes.js';
import { Conversation, Playback } from './playback.js';
import { finishWithTail, flushCapture, noteCaptureDevice, openMicNode, openMicStream, reportCaptureSettings, reportTail, sendFrame, setFrameSender, staleCapture, stopCapture, teardown } from './capture.js';
import { hush, hushFromHost, notSpoken, retireSpeaker, speakerLane, speakerPlaying, spokenInstead, watchSpeakerPlayback } from './speech.js';
import { micState, render, renderConverse } from '../voice.js';

export let resident = null;
export let residentWanted = false;
export function wantResident() { residentWanted = true; }

export let sendJSON = null;

export function bindTransport(fn, jsonFn) {
  setFrameSender(fn); sendJSON = jsonFn || null;
  return { receiveFrame, sessionState, voiceEvent, hushFromHost };
}

export const playback = new Playback({
  onSound: () => S.renderMarks?.(),
  report: r => { if (sendJSON) sendJSON({ type: 'voice_session', voice: { action: 'playback', playback: r } }); },
  onUnresolved: (stream, reason) => toast('The engine did not accept the playback receipt for this reply' + (reason ? ' (' + reason + ')' : '') + '; its drain cannot be reported as clean.'),
});
if (typeof document !== 'undefined' && document.addEventListener) {
  document.addEventListener('visibilitychange', () => { if (!document.hidden) playback.resume(); });
}

export function captureLane() { return V.lanes.length ? V.lanes[V.lanes.length - 1] : null; }

export function newLane(rate, mode, paired = false, gesture = false) {
  const c = S.captureSettings;
  const processing = c && mode !== 'output' ? {
    echo_cancellation: c.echoCancellation,
    noise_suppression: c.noiseSuppression,
    auto_gain_control: c.autoGainControl,
    sample_rate: c.sampleRate || null,
    ...(paired ? { channel_roles: referenceRoles } : {}),
  } : null;
  const l = new StreamLane({ sendFrame, sendJSON, rate, channels: paired ? 2 : 1, mode: mode || 'conversation', processing, gesture });
  l.onClosed = closedLane => {
    V.lanes = V.lanes.filter(x => x !== closedLane);
    const next = V.lanes[0];
    if (next && next.state === 'waiting') next.open();
  };
  V.lanes.push(l);
  if (V.lanes.length === 1) l.open();
  return l;
}

export function sessionState(st) {
  const head = V.lanes[0];
  const refusedSpeaker = !!(st && st.state === 'refused' && head && head.mode === 'output');
  const byHandLane = !!(head && head.gesture);
  if (refusedSpeaker) { V.speakerRefusedAt = Date.now(); V.speakerRefusedWhy = (st.reason || '').trim(); }
  if (st && st.state === 'open' && head && head.mode === 'output') { V.speakerRefusedAt = 0; V.speakerRefusedWhy = ''; }
  if (head) head.onState(st || {});
  if (st && st.state === 'open') { inputEndedWhy = ''; playback.reset(st.session_id, head ? head.rate : 0, head ? head.channels : 1); }
  if (st && st.state === 'input_complete') engineClosedInput(st);
  if (st && st.state === 'refused' && (!refusedSpeaker || byHandLane)) toast(st.reason || 'The speech engine refused the session.');
  render();
}

export let inputEndedWhy = '';

export function inputEndSentence(reason) {
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

export function receiveFrame(buf) { playback.feed(buf); if (speakerPlaying()) watchSpeakerPlayback(); }

export function voiceEvent(ev) {
  ev = ev || {};
  const t = ev.type || '';
  const ownSession = !ev.session_id || ev.session_id === playback.sessionID;
  if (ownSession && t === 'interruption_requested' && ev.stream) playback.retire(ev.stream);
  else if (ownSession && (t === 'speech_start' || t === 'interruption_requested')) playback.stop();
  if (ownSession && t === 'synthesis_start' && ev.stream) playback.announce(ev.stream);
  if (t === 'reply_refused') { if (!ev.superseded) notSpoken(ev.reason || ''); return; }
  if (t === 'reply_fallback') { spokenInstead(ev.reason || ''); return; }
  if (t === 'receipt_refused') { playback.receiptRefused(ev.session_id || '', (ev.stream || 0) >>> 0, ev.reason || ''); return; }
  const el = $('voice-transcript');
  if (!el) return;
  if (t === 'transcript_final' || t === 'transcript_partial') {
    const text = (ev.text || '').trim();
    if (t === 'transcript_final' && ev.operator && text) {
      el.textContent = ''; el.classList.remove('provisional', 'failed'); el.hidden = true;
      return;
    }
    const heardAs = text && ev.corrections > 0 ? (ev.recognized_text || '').trim() : '';
    el.textContent = text ? ((ev.speaker ? ev.speaker + ': ' : '') + text + (heardAs ? '  (heard as: ' + heardAs + ')' : '')) : '';
    el.classList.toggle('corrected', !!heardAs);
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
export function captureFenceForTest() { return { epoch: () => V.captureEpoch, retire: () => teardown(), stale: () => staleCapture }; }
export function lanesForTest() { return V.lanes; }
export function inputEndSentenceForTest(reason) { return inputEndSentence(reason); }

function engineLane() { return !!(S.stats && S.stats.voice_engine && sendJSON); }

export function engineHears() { return engineLane() && micState() === 'plugin'; }
export function engineSpeaks() { return engineLane() && !(S.stats && S.stats.reply_voice); }

export function setVoiceMode(name) {
  const pair = MODE_PAIRS[name];
  if (!pair || !sendJSON) return;
  if (pair.listen !== 'off' || pair.speak !== 'on') {
    V.speakerStoppedAtRevision = voiceMode().revision;
    if (speakerLane) retireSpeaker();
  }
  if (pair.speak === 'off') hushForSpeakOff();
  sendJSON({ type: 'config_set', config: { 'speech.mode': { listen: pair.listen, speak: pair.speak } } });
}

let spokenHalf = 'auto';
function hushForSpeakOff() { spokenHalf = 'off'; hush(); }
export function followSpeakHalf(speak) {
  const was = spokenHalf;
  spokenHalf = speak;
  if (speak === 'off' && was !== 'off') hush();
}

export function followListenHalf(listen, speak, revision) {
  const changed = revision !== V.listenFollowedRevision;
  V.listenFollowedRevision = revision;
  if (listen !== 'off') return;
  if (speak === 'off' && (resident || residentWanted)) {
    if (changed) abortDuplex();
    return;
  }
  if (resident && resident.streaming() && !resident.finished && !resident.finishing) { finishInput(); return; }
  if (resident) return;
  if (V.latched && V.capturing) { V.latched = false; stopCapture(true); return; }
}

export async function startDuplex(want) {
  if (resident) return;
  if (speakerLane) retireSpeaker();
  const listen = want || voiceMode().listen;
  const selectedEcho = echoMode();
  const paired = nativeEcho(selectedEcho);
  const askedRev = voiceMode().revision;
  if (listen === 'off') { toast('Listening is off — one tap on the microphone turns it back on.'); residentWanted = false; render(); return; }
  if (V.held || V.capturing) { toast('Finish the push-to-talk first.'); residentWanted = false; render(); return; }
  if (!engineHears()) { toast('A speech engine must be active for a spoken conversation.'); residentWanted = false; render(); return; }
  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    toast('The microphone works only over https or on this computer.');
    residentWanted = false; render(); return;
  }
  const epoch = ++V.captureEpoch;
  try { await playback.release(); }
  catch (e) {
    if (epoch === V.captureEpoch) {
      residentWanted = false;
      toast('The previous speaker route did not close; listening cannot start safely.');
      render();
    }
    return;
  }
  if (epoch !== V.captureEpoch || !residentWanted || offAcceptedSince(askedRev)) return;
  playback.holdForCapture(epoch);
  let stream;
  try {
    stream = await openMicStream(selectedEcho);
  } catch (err) {
    playback.captureReady(epoch);
    if (epoch !== V.captureEpoch) return;
    toast('No microphone: ' + (err && err.message ? err.message : err));
    residentWanted = false; render(); return;
  }
  if (epoch !== V.captureEpoch) { stopTracks(stream); playback.captureReady(epoch); return; }
  if (!residentWanted) { stopTracks(stream); playback.captureReady(epoch); render(); return; }
  if (offAcceptedSince(askedRev)) { stopTracks(stream); playback.captureReady(epoch); residentWanted = false; render(); return; }
  V.media = stream;
  noteCaptureDevice(V.media);
  try { V.ctx = new (window.AudioContext || window.webkitAudioContext)(); }
  catch (e) {
    playback.captureReady(epoch);
    stopTracks(stream);
    V.media = null;
    residentWanted = false;
    toast('The microphone audio route could not open: ' + (e && e.message ? e.message : e));
    render();
    return;
  }
  reportCaptureSettings(V.media);
  playback.captureReady(epoch);
  const reference = paired ? playback.useCaptureContext(V.ctx) : null;
  if (!paired) playback.prime(V.ctx.sampleRate);
  const src = V.ctx.createMediaStreamSource(V.media);
  const rate = V.ctx.sampleRate;
  resident = new Conversation({
    newLane: () => newLane(rate, laneMode(listen), paired),
    prerollSamples: Math.round(rate * 0.3) * (paired ? 2 : 1),
    onEnded: () => { abortDuplex(); },
  });
  const conv = resident;
  try {
    await openMicNode(V.ctx, src, f32 => { if (resident === conv) conv.frame(f32); }, reference);
  } catch (e) {
    if (V.media !== stream) { stopTracks(stream); return; }
    toast('The microphone could not start: ' + (e && e.message ? e.message : e));
    abortDuplex(); return;
  }
  if (V.media !== stream) { stopTracks(stream); return; }
  if (!residentWanted || offAcceptedSince(askedRev)) { abortDuplex(); return; }
  resident.start();
  render();
}

export function offAcceptedSince(rev) {
  voiceMode();
  return V.offSeenRev > rev;
}

export function stopTracks(stream) {
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
  V.capturing = false;
  teardown();
  S.renderMarks?.();
}

export function listeningNow() { return V.capturing || !!(resident && resident.streaming()); }

export function abortDuplex() {
  residentWanted = false;
  if (resident) { resident.abort(); resident = null; }
  teardown();
  playback.stop();
  render();
}

export function residentActive() { return !!resident; }
