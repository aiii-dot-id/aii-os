import { S } from '../state.js';
import { $ } from '../util.js';
import { toast } from '../app.js';
import { spokenAudio, hushSpoken, whenSpeaking } from '../say.js';
import { V } from './state.js';
import { stopCapture } from './capture.js';
import { micState } from '../voice.js';
import { resident, residentWanted, sendJSON, playback, newLane, engineSpeaks, voiceMode, abortDuplex } from './session.js';

export let speakerLane = null;
let speakerWatch = false;
let cloudSpeaking = false;
const SPEAKER_RETRY_MS = 15000;

const GESTURE_MS = 3000;
let lastGestureAt = -Infinity;
function byHand() { return Date.now() - lastGestureAt < GESTURE_MS; }
function takeHand() { const by = byHand(); lastGestureAt = -Infinity; return by; }
function operatorGesture() {
  lastGestureAt = Date.now();
  if (V.speakerRefusedAt && !speakerLane) { V.speakerRefusedAt = 0; V.speakerRefusedWhy = ''; syncSpeaker(); }
}
if (typeof document !== 'undefined' && document.addEventListener) {
  for (const ev of ['pointerdown', 'keydown', 'touchstart']) document.addEventListener(ev, operatorGesture, { passive: true, capture: true });
}
export function operatorGestureForTest() { operatorGesture(); }

function speakerWanted() {
  if (!engineSpeaks() || S.connected === false) return false;
  const m = voiceMode();
  if (m.revision <= V.speakerStoppedAtRevision) return false;
  if (m.listen !== 'off' || m.speak !== 'on') return false;
  if (resident || residentWanted || V.held || V.capturing || V.latched) return false;
  return micState() !== 'safe';
}

export function syncSpeaker() {
  if (!speakerWanted()) { if (speakerLane) retireSpeaker(); return; }
  if (speakerLane) return;
  if (V.speakerRefusedAt && Date.now() - V.speakerRefusedAt < SPEAKER_RETRY_MS) return;
  if (V.lanes.length) return;
  openSpeaker();
}

function openSpeaker() {
  const rate = (playback.ctx && playback.ctx.sampleRate) || 48000;
  const lane = newLane(rate, 'output', false, takeHand());
  speakerLane = lane;
  const queued = lane.onClosed;
  lane.onClosed = l => {
    if (speakerLane === l) { speakerLane = null; lastGestureAt = -Infinity; }
    if (queued) queued(l);
    syncSpeaker();
    speakerIdle();
  };
  if (typeof document !== 'undefined' && document.addEventListener) {
    const wake = () => { if (speakerLane === lane && speakerWanted()) playback.prime(rate); };
    document.addEventListener('pointerdown', wake, { once: true, passive: true });
    document.addEventListener('keydown', wake, { once: true, passive: true });
  }
}

export function retireSpeaker() {
  const l = speakerLane;
  speakerLane = null;
  speakerIdle();
  if (!l) return;
  if (l.state === 'waiting') { V.lanes = V.lanes.filter(x => x !== l); return; }
  playback.stop();
  l.abort();
}

export function watchSpeakerPlayback() {
  const b = $('hush-speaking');
  if (b) b.hidden = false;
  if (speakerWatch) return;
  speakerWatch = true;
  playback.onIdle = speakerIdle;
}

export function speakingNow() { return playback.sources.length > 0 || cloudSpeaking || !!(window.speechSynthesis && window.speechSynthesis.speaking); }

export function speakerPlaying() { return !!speakerLane && playback.sources.length > 0; }

function speakerIdle() {
  if (!speakerWatch || speakerPlaying()) return;
  speakerWatch = false;
  playback.onIdle = null;
  const stop = $('hush-speaking');
  if (stop && !cloudSpeaking) stop.hidden = true;
}

export function speakerLaneForTest() { return speakerLane; }

export function speak(text, ref) {
  const mode = voiceMode();
  if (mode.speak === 'off') return;
  if (ref && ref.route === 'plugin') { playback.replaced(); return; }
  if (!text) return;
  if (ref && ref.route === 'cloud' && ref.synthesis_id) { sayWithService(text, { id: ref.synthesis_id }); return; }
  if (ref && ref.route === 'browser') { if (mode.speak === 'on' || S.voiceSpeak) browserSpeak(text); return; }
  if (S.stats && S.stats.reply_voice) { sayWithService(text, { text: text }); return; }
  if (mode.speak !== 'on' && !S.voiceSpeak) return;
  if (engineSpeaks()) {
    notSpoken('no voice session of the speech engine was open for it', V.speakerRefusedAt ? V.speakerRefusedWhy : '');
    return;
  }
  browserSpeak(text);
}

export function notSpoken(reason, told) {
  replyNotice('This reply was not spoken' + (reason ? ': ' + reason : '') + '.' + (told ? ' ' + told : ''));
}

export function spokenInstead(route) {
  replyNotice(route === 'cloud'
    ? 'The speech engine did not speak this reply; the voice service chosen to speak replies spoke it.'
    : 'The voice service chosen to speak replies did not take this reply; the browser\'s own voice read it.');
}

function replyNotice(text) {
  toast(text);
  const el = $('voice-transcript');
  if (!el) return;
  el.textContent = text;
  el.classList.remove('provisional');
  el.classList.add('failed');
  el.hidden = false;
  V.transcriptPlaceholder = '';
}

function browserSpeak(text) {
  if (!window.speechSynthesis || !text) return;
  window.speechSynthesis.cancel();
  const u = new SpeechSynthesisUtterance(text);
  const stop = $('hush-speaking');
  if (stop) stop.hidden = false;
  u.onstart = () => S.renderMarks?.();
  u.onend = u.onerror = () => { if (stop) stop.hidden = true; S.renderMarks?.(); };
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
  S.renderMarks?.();
}

export function hush() {
  hushSpoken();
  if (window.speechSynthesis) window.speechSynthesis.cancel();

  const head = V.lanes[0];
  if (head && head.isOpen && sendJSON) sendJSON({ type: 'voice_session', voice: { action: 'interrupt' } });
  playback.stop();
  S.renderMarks?.();
}

export function connectionLost() {
  V.modeRev = 0;
  V.offSeenRev = 0;
  V.listenFollowedRevision = -1;
  V.speakerStoppedAtRevision = -1;
  V.held = false;
  V.latched = false;
  stopCapture(false);
  abortDuplex();
  speakerLane = null; V.speakerRefusedAt = 0; V.speakerRefusedWhy = '';
  speakerIdle();
  V.lanes = [];
  playback.stop();
  playback.park();
}

whenSpeaking(on => {
  cloudSpeaking = !!on;
  const b = $('hush-speaking');
  if (b) b.hidden = !on && !speakerPlaying();
  S.renderMarks?.();
});
