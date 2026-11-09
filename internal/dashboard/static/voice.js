import { S } from './state.js';
import { $ } from './util.js';
import { V } from './voice/state.js';
import { chooseMic, micDefaultLabel, micInputs, micInputsInto, micLabel, micPinned, micPinnedLabel, refreshMicInputs, startCapture, stopCapture } from './voice/capture.js';
export { captureMessage, flushPort, finishWithTail } from './voice/capture.js';
import { hush, speak, speakingNow, syncSpeaker } from './voice/speech.js';
export { operatorGestureForTest, retireSpeaker, speakerLaneForTest, speak, hushFromHost, hush, connectionLost } from './voice/speech.js';
import { abortDuplex, finishInput, followListenHalf, listeningNow, followSpeakHalf, inputEndSentence, inputEndedWhy, modeName, nextMode, playback, resident, residentActive, residentWanted, setVoiceMode, startDuplex, voiceMode, wantResident } from './voice/session.js';
export { abortDuplex, bindTransport, captureFenceForTest, captureLane, converseForTest, engineHears, engineSpeaks, finishInput, inputEndSentenceForTest, laneMode, lanesForTest, modeName, newLane, offAcceptedSince, playback, playbackForTest, receiveFrame, resident, residentActive, residentWanted, sendJSON, sessionState, startDuplex, stopTracks, voiceEvent, voiceMode } from './voice/session.js';

let downAt = 0;
const LATCH_MS = 400;

S.voiceNow = () => ({ listening: listeningNow(), speaking: speakingNow() });

export { STREAM_VERSION, STREAM_HEADER, STREAM_PCM, STREAM_GAP, STREAM_END, encodeStreamFrame, decodeStreamFrame, STREAM_QUEUE, StreamLane, PreRoll } from './voice-stream.js';

export { Conversation, Playback } from './voice/playback.js';

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
  setup: 'Voice isn\'t set up — opens Voice settings',
  unreachable: 'Voice can\'t reach its service — opens Voice settings',
  safe: 'Voice is paused while this identity is in SAFE',
  waiting: 'Voice is paused while this start waits for Ring 5',
};

export function render() {
  const m = voiceMode();
  followSpeakHalf(m.speak);
  followListenHalf(m.listen, m.speak, m.revision);
  syncSpeaker();
  renderConverse();
  renderMicMenu();
  S.renderMarks?.();
  const b = $('mic');
  if (!b) return;
  const state = micState(), recording = V.capturing || V.latched;
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
    : state === 'setup' && speaks ? 'Voice input isn\'t set up — opens Voice settings'
    : state === 'safe' && S.cont && S.cont.mode === 'degraded_ring5' ? MIC_TITLES.waiting
    : state === 'unreachable' && S.stats.voice_reason ? MIC_TITLES.unreachable + ' (' + S.stats.voice_reason + ')'
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

export function renderMicMenu() {
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

export function closeMicMenu() {
  const menu = $('mic-menu'), more = $('mic-more');
  if (menu) menu.hidden = true;
  if (more) more.setAttribute('aria-expanded', 'false');
}

function openSpeechSettings() {
  closeMicMenu();
  if (S.openSettings) S.openSettings('speech', 'sp-provider-stt');
}

export function renderConverse() {
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
    const placeholder = !text.trim() || text === V.transcriptPlaceholder;
    if (residentActive() && placeholder) {
      V.transcriptPlaceholder = resident.finished ? (inputEndedWhy || inputEndSentence(null)) : 'Listening…';
      tr.textContent = V.transcriptPlaceholder; tr.classList.add('provisional'); tr.hidden = false;
    } else if (!residentActive() && placeholder && text.trim()) {
      tr.textContent = ''; tr.hidden = true; V.transcriptPlaceholder = '';
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

const wired = new WeakSet();
export function wireMic() {
  const mic = $('mic');
  if (mic && wired.has(mic)) return;
  if (mic) wired.add(mic);
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
    if (V.latched) { sendLatched(); setVoiceMode('earbuds'); return; }
    downAt = Date.now(); V.held = true; hush(); startCapture();
  });
  b.addEventListener('pointerup', e => {
    if (resident || residentWanted || V.latched || !V.held) return;
    e.preventDefault();
    if (Date.now() - downAt < LATCH_MS) {
      const m = voiceMode();
      if (m.listen !== 'interactive') {
        V.held = false; stopCapture(false);
        setVoiceMode(nextMode(m));
        render(); return;
      }
      V.latched = true; render(); return;
    }
    V.held = false; stopCapture(true);
  });
  const abandon = () => { if (resident || residentWanted || V.latched || !V.held) return; V.held = false; stopCapture(false); };
  b.addEventListener('pointerleave', abandon);
  b.addEventListener('pointercancel', abandon);
  b.addEventListener('keydown', e => {
    if (e.key !== ' ' && e.key !== 'Enter') return;
    e.preventDefault();
    if (e.repeat || resident || residentWanted) return;
    const state = micState();
    if (state === 'setup' || state === 'unreachable') { openSpeechSettings(); return; }
    if (state !== 'cloud') return;
    if (V.latched || V.held) { sendLatched(); setVoiceMode('earbuds'); return; }
    const m = voiceMode();
    if (m.listen !== 'interactive') { setVoiceMode(nextMode(m)); return; }
    V.latched = true; V.held = true; hush(); startCapture(); render();
  });

  window.addEventListener('pagehide', () => { V.held = false; V.latched = false; stopCapture(false); abortDuplex(); });
  render();
}

function sendLatched() {
  V.latched = false; V.held = false;
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
      wantResident(); render(); startDuplex('meeting');
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
        if (!resident && !residentWanted) { wantResident(); render(); startDuplex('interactive'); return; }
        if (resident && resident.streaming() && !resident.finished && !resident.finishing) finishInput();
        else abortDuplex();
        setVoiceMode('earbuds');
        playback.prime();
        render(); return;
      }
      if (m.listen === 'off' && m.speak !== 'off') { setVoiceMode('off'); abortDuplex(); return; }
      if (m.listen === 'meeting') abortDuplex();
      setVoiceMode('interactive');
      wantResident(); render(); startDuplex('interactive');
    });
  }
  renderConverse();
}
