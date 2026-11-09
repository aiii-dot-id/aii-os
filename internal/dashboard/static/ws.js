import './interaction-view.js';
import { hasShellAuth, shellAccessToken } from './shell-auth.js';
import { keepPageKey, signedFetch, signIn, keptInMemoryOnly, MEMORY_ONLY, PAGE_KEY_STORE } from './pagekey.js';

import { acceptSignIn, abandonSignIn } from './signin.js';
import { S } from './state.js';
import { $ } from './util.js';
import { announceWords } from './announce.js';
import { renderPresence, setThinking } from './presence.js';
import { go, renderFirstbootVisibility, toast } from './app.js';
import { sysLine, toolEventLive, thinkingEvent, renderComposer, renderSteering, renderAsks } from './views/chat.js';
import CHAT from './strings/chat.js';
import { renderHome } from './views/home.js';
import { acceptAsks, renderTitle } from './waiting.js';
import { renderProjPill } from './shell/project-pill.js';
import { LATER } from './shell/later.js';
import { fetchFirstRun } from './views.js';
import { publish as publishToSections, onSections, onLayout, onTokensChanged } from './sections.js';
import { ownsThread } from './registry.js';
import { channelWanted, settle, hello } from './transport/channel.js';
import { onTheme } from './theme.js';
import { onOverlayChanged, reloadForTheOverlay } from './overlay.js';
import { bindVoice, toVoice } from './shell/voice-later.js';
const { acceptSandbox, rejectSandbox, sandboxConnectionLost, acceptTools, rejectTools, toolsConnectionLost,
  acceptMailRepair, rejectMailRepair, acceptMessages, rejectMessages, acceptExpertConfig, rejectExpertConfig,
  expertStatus, expertConnectionLost, renderPlugins, rejectPluginAction, applyPluginStored, applySettingChoices,
  renderSettings, acceptSettingsConfig, rejectSettingsConfig, acceptProviderSave, rejectProviderSave,
  acceptSpeechLists, rejectSpeechLists, acceptAccessCode, rejectAccessCode, settingsConnectionLost, settingsSettled, localPart,
  renderMemory, renderIdentity, renderBetween, renderStatus,
  applyBackups, backupsReconnected, backupsConnectionLost, acceptBackupPreferences, rejectBackups, applyRestoreNew,
  renderProviderOptions, discoveryAnswered, discoveryFailed, fbHint, acceptDiscoveryResponse,
  firstbootConnectionLost, acceptFirstRunConfig, refuseFirstRunConfig, firstRunConfigArrived, beginAgain } = LATER;
const { renderProjects, rejectCreate, rejectFocusSave, rejectContractSave, acceptCreate, acceptFocusSave,
  acceptContractSave, projectsConnectionLost } = LATER;
const theirs = () => { const n = S.stats?.name || ''; return n && n !== 'Unnamed' ? n : CHAT.yourIdentity; };

S.onThemeApplied = () => { onTokensChanged(); S.renderMarks?.(); };

let ws = null;
let requestSeq = 0;

let reconnectDelay = 1000;
const RECONNECT_MAX = 10000;
const FOREGROUND_PROBE_MS = 8000;
let probeTimer = null;
let probeRequestID = '';

let receivedTurnRevision = 0;
let probeTurnRevision = 0;
let probeThinkingAtSend = false;
let authCheck = false;
const CLOSE_SIGN_IN = 4401;
let refused = false;
const settingsOpen = () => S.view === 'settings' || S.view === 'phone';
let admission = 0;
function foreground() { return document.visibilityState !== 'hidden'; }
function canRetry() { return foreground() && navigator.onLine !== false; }
function clearReconnect() {
  if (S.reconnectTimer) clearTimeout(S.reconnectTimer);
  S.reconnectTimer = null;
}
function suspendRecovery() {
  clearReconnect();
  clearProbe();
}
function finishHistoryReadback() { if (S.interactionWake) S.interactionWake(); }

function clearProbe() {
  if (probeTimer !== null) clearTimeout(probeTimer);
  probeTimer = null;
  probeRequestID = '';
}
let draftIn = null;
const toDraft = f => (draftIn ||= import('./views/chat/draft.js')).then(f, () => { draftIn = null; });
function connectionLost(socket, code) {
  if (ws !== socket) return;
  ws = null;
  if (code === CLOSE_SIGN_IN) refused = true;
  clearProbe();
  if (S.interactionLost) S.interactionLost();
  S.chatConnectionLost?.(); if (draftIn) toDraft(m => m.lost());
  if (S.gradesLost) S.gradesLost();
  S.passNowLost?.();
  S.beliefTrailLost?.();
  S.connected = false;
  S.firstConnect = false;
  S.pageFrames = { status: false, continuity: false };
  if (canRetry()) recoverAuthentication();
  $('send-btn').disabled = true;
  renderComposer();
  toVoice('connectionLost');
  toVoice('render');
  setThinking(false);
  expertConnectionLost();
  settingsConnectionLost();
  S.renderModelSwitch?.();
  S.sectionsLost?.();
  if (S.pluginsConnectionLost) S.pluginsConnectionLost();
  S.revokeLost?.();
  S.actsLost?.();
  if (S.view === 'plugins') renderPlugins();
  backupsConnectionLost();
  sandboxConnectionLost();
  toolsConnectionLost();
  S.logsConnectionLost?.();
  abandonSignIn();
  firstbootConnectionLost();
  projectsConnectionLost();
  renderPresence();
  scheduleReconnect();
}
function retireStalled(socket) {
  if (ws !== socket) return;
  socket.onopen = socket.onclose = socket.onmessage = null;
  try { socket.close(); } catch (err) { }
  connectionLost(socket);
}
function probeConnection() {
  if (!foreground() || !ws || probeTimer !== null) return;
  const socket = ws;
  const expire = () => {
    if (probeTimer !== timer || !foreground()) return;
    retireStalled(socket);
  };
  const timer = setTimeout(expire, FOREGROUND_PROBE_MS);
  probeTimer = timer;
  if (socket.readyState === 0) {
    return;
  }
  if (socket.readyState !== 1) { clearProbe(); connect(); return; }
  probeTurnRevision = receivedTurnRevision;
  probeThinkingAtSend = !!S.thinking;
  probeRequestID = 'probe-' + String(++requestSeq);
  try {
    socket.send(JSON.stringify({ type: 'probe', request_id: probeRequestID }));
  } catch (err) { retireStalled(socket); }
}
function scheduleReconnect() {
  clearReconnect();
  if (!canRetry() || refused) return;
  const jitter = 0.8 + Math.random() * 0.4;
  const delay = Math.min(reconnectDelay * jitter, RECONNECT_MAX);
  reconnectDelay = Math.min(reconnectDelay * 2, RECONNECT_MAX);

  const timer = setTimeout(() => {
    if (S.reconnectTimer !== timer) return;
    S.reconnectTimer = null;
    if (canRetry()) connect();
  }, delay);
  S.reconnectTimer = timer;
}
export function connect() {
  if (!foreground()) return;
  if (refused) { recoverAuthentication(); return; }
  if (ws && ws.readyState < 2) return;
  if (ws && ws.readyState === 2) { retireStalled(ws); return; }

  const wsScheme = location.protocol === 'https:' ? 'wss://' : 'ws://';
  const viaChannel = channelWanted(location.protocol, location.hostname, typeof RTCPeerConnection === 'function');
  const socket = new WebSocket(wsScheme + location.host + '/ws?interaction_version=1' + (viaChannel ? '&transport=1' : ''));
  socket.binaryType = 'arraybuffer';
  const opened = carrier => {
    if (ws !== carrier) return;
    admission++;
    clearManualAuthentication();
    clearProbe();
    S.connected = true;
    S.pageFrames = { status: false, continuity: false };
    reconnectDelay = 1000;
    S.wsEverOpened = true;
    backupsReconnected();
    if (S.reconnectTimer) { clearTimeout(S.reconnectTimer); S.reconnectTimer = null; }
    $('send-btn').disabled = false;
    renderComposer();
    toVoice('render');
    query('providers');
    if (S.identityExists) query('steering');
    query('sections'); query('ui_layout'); query('ui_theme');
    query('ui.overlay');

    query('config'); query('asks'); query('work'); query('identity');
    query('continuity');
    renderPresence();
    go(S.view, 'refresh');
  };
  const adopt = carrier => {
    ws = carrier;
    carrier.onclose = () => connectionLost(carrier);
    carrier.onmessage = e => { if (ws === carrier) onMessage(e); };
    opened(carrier);
  };
  if (!viaChannel) {
    ws = socket;
    socket.onopen = () => { hello(socket); opened(socket); };
    socket.onclose = e => connectionLost(socket, e && e.code);
    socket.onmessage = e => { if (ws === socket) onMessage(e); };
    return;
  }
  const pending = settle(socket, adopt, code => connectionLost(pending, code), config => new RTCPeerConnection(config));
  ws = pending;
}

export async function recoverAuthentication() {
  if (!canRetry() || S.tokenPrompted || authCheck) return;
  authCheck = true;
  const asked = admission;
  let status;
  try {
    status = await signedFetch('/auth/token', { cache: 'no-store', credentials: 'same-origin' });
  } catch (e) {
    authCheck = false;
    if (refused) showManualAuthentication();
    return;
  }
  if (status.status !== 401 || !canRetry()) {
    authCheck = false;
    if (refused && status.ok) { refused = false; wake(); }
    else if (refused && status.status !== 401) showManualAuthentication();
    return;
  }

  if (hasShellAuth()) {
    try {
      const token = await shellAccessToken();
      if (!canRetry()) return;
      const reply = await signIn(token);
      if (reply.ok) { authCheck = false; signedIn(reply); wake(); }
    } catch (e) {
    } finally { authCheck = false; }
    return;
  }

  if (admission === asked) {
    try { status = await signedFetch('/auth/token', { cache: 'no-store', credentials: 'same-origin' }); } catch (e) { status = null; }
  }
  authCheck = false;
  if (admission !== asked || !status || status.status !== 401 || !canRetry()) {
    if (status && status.ok) refused = false;
    if (refused) { if (admission === asked) showManualAuthentication(); return; }
    if (canRetry() && !S.connected) wake();
    return;
  }
  showManualAuthentication();
}

function signedIn(reply) {
  admission++; refused = false;
  if (keptInMemoryOnly()) { toast(MEMORY_ONLY, 'note'); return; }
  if (reply && reply.headers.get('Aii-Overlay') === 'reload') reloadForTheOverlay();
}

async function tokenAct(path) {
  if (authCheck) return { ok: false, text: 'A sign-in is in progress; try again when it is done.' };
  authCheck = true;
  const previous = ws;
  try {
    const reply = await signedFetch(path, { method: 'POST', cache: 'no-store', credentials: 'same-origin' });
    if (keepPageKey(reply)) signedIn();
    if (reply.ok && previous && ws === previous) retireStalled(previous);
    return { ok: reply.ok, text: reply.ok ? '' : (await reply.text()).trim() };
  } catch (e) {
    return { ok: false, text: 'The answer could not be confirmed. Check the connection and try again.' };
  } finally {
    authCheck = false;
    wake();
  }
}

S.rotateAccessToken = () => tokenAct('/auth/rotate');
S.requireAccessToken = () => tokenAct('/auth/require');

let authForm = null, authDismissed = false, authFormRevision = 0;
function clearManualAuthentication() {
  authFormRevision++;
  if (authForm) { authForm.querySelector('input').value = ''; authForm.remove(); authForm = null; }
  document.documentElement.classList.remove('signin-asked');
  authDismissed = false; S.tokenPrompted = false;
}
const ACCESS_CODE_LENGTH = 12, ACCESS_CODE_ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
export function couldBeAccessCode(text) {
  const read = String(text).toUpperCase().replace(/[\s-]/g, '').replace(/O/g, '0').replace(/[IL]/g, '1');
  return read.length <= ACCESS_CODE_LENGTH && [...read].every(ch => ACCESS_CODE_ALPHABET.includes(ch));
}
const ON_THIS_COMPUTER = ['127.0.0.1', 'localhost', '[::1]'].includes(location.hostname);
const SIGN_IN = ON_THIS_COMPUTER
  ? 'Sign in to use this page. Open the AII OS app on this computer, and it opens this page signed in.'
  : 'Sign in to use this page here. On your computer, open Settings \u2192 Phone and other devices, and scan the code with this phone, or type it here.';
function showManualAuthentication() {
  if (!foreground()) return;
  if (!authForm) {
    const box = document.createElement('section'); box.id = 'dashboard-signin'; box.setAttribute('aria-label', 'Sign in');
    const note = document.createElement('p');
    note.textContent = SIGN_IN;
    const reveal = document.createElement('button'); reveal.type = 'button'; reveal.className = 'btn ghost'; reveal.textContent = 'Sign in';
    const form = document.createElement('form');
    const label = document.createElement('label'); label.textContent = 'Access code';
    const input = document.createElement('input'); input.type = 'text'; input.autocomplete = 'off'; input.autocapitalize = 'off'; input.spellcheck = false; input.setAttribute('aria-label', 'Access code'); label.appendChild(input);
    const actions = document.createElement('div'); actions.className = 'auth-actions';
    const submit = document.createElement('button'); submit.type = 'submit'; submit.className = 'btn'; submit.textContent = 'Sign in';
    const cancel = document.createElement('button'); cancel.type = 'button'; cancel.className = 'btn ghost'; cancel.textContent = 'Not now';
    const show = document.createElement('button'); show.type = 'button'; show.className = 'btn ghost'; show.dataset.signinShow = ''; show.hidden = true;
    actions.append(submit, cancel, show); form.append(label, actions); box.append(note, reveal, form);
    let shown = false;
    const mask = () => {
      const code = couldBeAccessCode(input.value);
      if (code) shown = false;
      show.hidden = code; show.textContent = shown ? 'Hide' : 'Show'; show.setAttribute('aria-pressed', String(shown));
      input.type = code || shown ? 'text' : 'password';
    };
    input.addEventListener('input', mask);
    show.onclick = () => { shown = !shown; mask(); input.focus(); };
    mask();
    const visibility = () => { form.hidden = authDismissed; reveal.hidden = !authDismissed; S.tokenPrompted = !authDismissed; };
    reveal.onclick = () => { authDismissed = false; visibility(); input.focus(); };
    cancel.onclick = () => { authFormRevision++; input.value = ''; mask(); authDismissed = true; note.textContent = 'Sign-in paused. Use Sign in when ready.'; visibility(); };
    form.onsubmit = async event => {
      event.preventDefault();
      if (authCheck || !canRetry()) return;
      const token = input.value.trim(); input.value = ''; mask();
      if (!token) { note.textContent = 'Enter the access code.'; return; }
      const revision = ++authFormRevision;
      authCheck = true; submit.disabled = true; note.textContent = 'Signing in…';
      try {
        const reply = await signIn(token);
        const why = reply.status === 403 ? (await reply.text()).trim() : '';
        if (revision !== authFormRevision || box !== authForm) { if (reply.ok && canRetry()) wake(); return; }
        if (reply.ok) { clearManualAuthentication(); authCheck = false; signedIn(reply); wake(); }
        else note.textContent = why ? 'That was not accepted: ' + why + '.' : 'That was not accepted. An access code works once and for a short time. Try again or choose Not now.';
      } catch (_) {
        if (revision === authFormRevision && box === authForm) note.textContent = 'Sign-in could not be confirmed. Check the connection and try again.';
      } finally {
        authCheck = false; submit.disabled = false;
      }
    };
    authForm = box; ($('dashboard-auth') || document.body).prepend(box); visibility();
    document.documentElement.classList.add('signin-asked');
    S.firstConnect = false;
    renderPresence(); renderComposer();
  }
}

S.reconcileTurn = () => wake();
export function wake() {
  if (!foreground()) return;
  clearReconnect();
  if (ws && ws.readyState < 2) probeConnection();
  else connect();
}
if (typeof document !== 'undefined') {
  window.addEventListener('online', wake);
  window.addEventListener('storage', e => {
    if (e.key !== PAGE_KEY_STORE || !e.newValue) return;
    admission++; refused = false;
    if (authForm) { clearManualAuthentication(); wake(); }
  });
  window.addEventListener('offline', suspendRecovery);
  window.addEventListener('pagehide', suspendRecovery);
  window.addEventListener('pageshow', event => { if (event.persisted) wake(); });
  const visibility = () => {
    document.documentElement.classList.toggle('page-hidden', !foreground());
    if (foreground()) wake();
    else suspendRecovery();
  };
  document.documentElement.classList.toggle('page-hidden', !foreground());
  document.addEventListener('visibilitychange', visibility);
}
function statusOpen() { if (S.view === 'status') renderStatus(); if (S.view === 'technical') S.renderTechnical?.(); }

export function send(obj) {
  if (!ws || ws.readyState !== 1) return '';
  if (!obj.request_id) obj.request_id = String(++requestSeq);
  ws.send(JSON.stringify(obj));
  return obj.request_id;
}
export function query(q, extra) {
  if (q === 'history') { if (S.interactionWake) S.interactionWake(); return ''; }
  if (q === 'continuity' && !S.identityExists) return '';
  return send(Object.assign({ type: 'query', query: q }, extra || {}));
}

function sendVoiceFrame(buf) {
  if (!ws || ws.readyState !== 1) return;
  S.voiceSpeak = true;
  ws.send(buf);
}

bindVoice(sendVoiceFrame, send);

export function wsReady() { return !!(ws && ws.readyState === 1); }

const PRESENCE_MIN_GAP_MS = 30000;
let lastPresencePing = -Infinity;
export function pingPresence(now) {
  now = now == null ? Date.now() : now;
  if (!wsReady()) return false;
  if (now - lastPresencePing < PRESENCE_MIN_GAP_MS) return false;
  lastPresencePing = now;
  ws.send(JSON.stringify({ type: 'presence' }));
  return true;
}
if (typeof document !== 'undefined') {
  for (const ev of ['pointerdown', 'keydown', 'touchstart', 'wheel']) {
    document.addEventListener(ev, () => { pingPresence(); }, { passive: true, capture: true });
  }
}

function onMessage(e) {

  if (e.data instanceof ArrayBuffer) { toVoice('receiveFrame', e.data); return; }
  let msg; try { msg = JSON.parse(e.data); } catch (err) { return; }
  if (probeRequestID && msg.type === 'probe' && msg.request_id === probeRequestID) {
    const newerTurnEvent = receivedTurnRevision !== probeTurnRevision || !!S.thinking !== probeThinkingAtSend;
    clearProbe();
    if (!newerTurnEvent) setThinking(!!msg.turn_active);

    const reads = new Set(['status', 'continuity']);
    if (S.interactionWake) S.interactionWake();
    if (S.identityExists) { reads.add('asks'); reads.add('steering'); }
    if (S.refreshCurrentView) S.refreshCurrentView(q => reads.add(q));
    reads.forEach(q => query(q));
  }

  try { publishToSections(msg); } catch (err) { console.warn('sections: publish failed', err); }
  switch (msg.type) {
    case 'probe': break;
    case 'sections': onSections(msg.sections || [], msg.message || ''); break;
    case 'layout': onLayout(msg.layout || null); break;

    case 'theme': onTheme(msg.theme || null); onTokensChanged(); S.renderMarks?.(); S.themeAnswered?.(msg.request_id); break;
    case 'status': (S.pageFrames ||= {}).status = true; S.firstConnect = false; onStats(msg.stats); expertStatus(); statusOpen(); toVoice('render'); break;
    case 'voice_session': toVoice('sessionState', msg.voice_session || {}); break;
    case 'voice_event': {
      const ve = msg.voice_event || {};
      if (ve.type === 'transcript_final' && ve.operator && (ve.text || '').trim()) {
        receivedTurnRevision++; S.operatorTurnBegins?.(); setThinking(true); finishHistoryReadback();
      }
      if (ve.type === 'speaker_observation' && ve.refers_to) {
        finishHistoryReadback();
      }
      toVoice('voiceEvent', ve);
      break;
    }
    case 'voice_hush': toVoice('hushFromHost', msg.voice_hush || {}); break;
    case 'continuity': S.cont = msg.continuity || null; (S.pageFrames ||= {}).continuity = true; renderPresence(); statusOpen(); if (S.view === 'chat') renderHome(); if (S.view === 'identity') renderIdentity(); if (S.view === 'between') renderBetween(); S.renderRecordState?.(); break;
    case 'draft': toDraft(m => m.receiveDraft(msg.draft)); break;
    case 'response':
      if (!msg.stream && msg.role !== 'identity') {
        toast(msg.message, 'note'); break;
      }
      receivedTurnRevision++;
      if (msg.stream) { setThinking(true); break; }
      setThinking(false);
  renderSteering([]);

      if (msg.role === 'identity') {
        announceWords(theirs(), msg.message);
        toVoice('speak', msg.message, msg.voice_reply);
        if (S.view === 'memory' || S.view === 'identity') query('identity');
        S.turnEnded?.();
      }
      finishHistoryReadback();
      break;
    case 'system_line': toast(msg.message, 'note'); break;
    case 'event':
      if (msg.kind === 'thinking') { thinkingEvent(msg.args); break; }
      toolEventLive(msg.name, msg.args);
      if (msg.name === 'work') query('work');
      break;
    case 'history': break;
    case 'interaction_changed': if (S.interactionChanged) S.interactionChanged(); break;
    case 'work_changed': query('work'); break;
    case 'interaction_page': if (S.interactionPage) S.interactionPage(msg.interaction_page, msg.request_id, msg.interaction_window); break;
    case 'interaction_error': if (S.interactionError) S.interactionError(msg.interaction_reason, msg.message, msg.request_id); break;
    case 'chat_ack': if (S.chatAcknowledged) S.chatAcknowledged(msg.request_id, msg.message); break;
    case 'chat_receipt': if (S.chatReceipt) S.chatReceipt(msg.request_id, msg.chat_recording); break;
    case 'asks': S.asks = msg.asks || []; renderAsks(S.asks); statusOpen(); acceptAsks(S.asks); break;
    case 'identity': S.identity = msg.identity || null; if (S.view === 'memory') renderMemory(); if (S.view === 'identity') renderIdentity(); if (S.view === 'between') renderBetween(); if (S.view === 'experiences') S.renderExperiences?.(); if (S.view === 'chat') renderHome(); break;
    case 'recall': S.recall = { query: msg.query || '', text: msg.message || '' }; if (S.view === 'memory') renderMemory(); break;
    case 'tools': S.tools = msg.tools || []; acceptTools(msg.request_id); statusOpen(); if (settingsOpen()) renderSettings(); break;
    case 'sandbox': S.sandbox = msg.sandbox || null; acceptSandbox(msg.request_id, msg.message); if (settingsOpen()) renderSettings(); break;
    case 'work': S.gradeAnswered?.(msg.request_id, null); S.work = msg.work || null; S.gradesRead?.(S.work); statusOpen(); if (S.view === 'chat') renderHome(); if (S.view === 'identity') renderIdentity(); if (S.view === 'projects') renderProjects(); S.renderComposer?.(); break;

    case 'workspace': S.workspace = msg.workspace || null; S.workspaceFor = (S.workspace && S.workspace.project) ? S.workspace.project.id : ''; if (S.view === 'projects') renderProjects(); break;
    case 'overlays': S.overlays = msg.overlays || []; statusOpen(); break;
    case 'pass_now': S.passNowAnswered?.(msg); break;
    case 'overlay_changed': onOverlayChanged(msg.token, msg.paths); break;
    case 'setting_choices': applySettingChoices(msg.setting_choices); if (S.view === 'plugins') renderPlugins(); if (settingsOpen()) renderSettings(); break;
    case 'plugin_stored': applyPluginStored(msg.plugin_stored); if (S.view === 'plugins') renderPlugins(); break;
    case 'config':
      S.config = msg.config || null; S.renderMarks?.();
      firstRunConfigArrived(); acceptFirstRunConfig(msg.request_id);
      acceptSettingsConfig(msg.request_id); acceptExpertConfig(msg.request_id); S.revokeAnswered?.(msg.request_id, null);
      settingsSettled(msg.request_id);
      acceptBackupPreferences(msg.request_id);
      if (S.pluginStartAnswered) S.pluginStartAnswered(msg.request_id);
      S.renderModelSwitch?.();
      renderComposer(); statusOpen(); if (settingsOpen()) renderSettings(); if (S.view === 'plugins') renderPlugins();
      S.chipsRedraw?.();
      if (S.view === 'memory') query('identity');
      break;
    case 'spend':
      S.spend = msg.spend || { days: [] }; S.spendRequest = msg.request_id || '';
      statusOpen(); if (settingsOpen()) renderSettings(); break;
    case 'logs':

      if (msg.logs_tail) { S.logTail = msg.logs_tail; }
      else { S.logsList = msg.logs_list || []; S.logFile = ''; S.logTail = null; }
      if (settingsOpen()) renderSettings();
      if (S.view === 'technical') S.renderTechnical?.(); break;
    case 'projects': {
      S.projectsPrimed = true;

      const prevIDs = S.projects.map(p => p.id);
      S.projects = msg.projects || [];
      const made = acceptCreate(msg.request_id, prevIDs);
      if (made) S.viewedProject = made;
      S.activeProject = S.projects.find(p => p.active) || null;
      if (msg.request_id) {

        if (acceptFocusSave(msg.request_id)) {
          const n = $('focus-note');
          if (n) { n.classList.add('show'); n.dataset.waiting = ''; setTimeout(() => n.classList.remove('show'), 1400); }
        }

        if (acceptContractSave(msg.request_id)) {
          const n = $('ct-note');
          if (n) { n.classList.add('show'); n.dataset.waiting = ''; setTimeout(() => n.classList.remove('show'), 1400); }
        }
      }
      renderProjPill();

      statusOpen();
      if (S.view === 'projects') renderProjects();
      if (S.view === 'chat') renderHome();
      break;
    }
    case 'providers': S.providers = msg.providers || []; S.renderModelSwitch?.(); S.brokenProviders = msg.broken_providers || []; S.skipSignInWithValidToken = msg.skip_signin_with_valid_token !== false; acceptProviderSave(msg.request_id); localPart(msg); S.providersLoaded = true; if (!S.identityExists) renderProviderOptions(); statusOpen(); if (settingsOpen()) renderSettings(); break;
    case 'provider_signin': case 'profile_signin': acceptSignIn(msg); break;
    case 'backups': applyBackups(msg.backups, msg.request_id); break;
    case 'restore_new': applyRestoreNew(msg.restore_new); break;
    case 'update_check': S.update = msg.update || null; if (S.renderUpdate) S.renderUpdate(); if (S.renderUpdateChip) S.renderUpdateChip(); break;
    case 'restart': toast('restarting to run the update \u2014 this page reconnects when the identity is back', 'note'); break;
    case 'public_name': if (S.renderPublicName) S.renderPublicName(msg.public_name || null); break;
    case 'speech_lists': acceptSpeechLists(msg.speech_lists); break;
    case 'access_code': acceptAccessCode(msg.request_id, msg.access_code || null); break;
    case 'models': {
      if (!acceptDiscoveryResponse(msg.request_id, msg.provider)) {

        const p = (S.providers || []).find(x => x.name === msg.provider);
        if (p) { const seen = new Set(p.models || []); p.models = (p.models || []).concat((msg.model_list || []).filter(m => !seen.has(m))); }
        settingsSettled(msg.request_id, msg.provider);
        if (settingsOpen()) renderSettings();
        break;
      }
      discoveryAnswered(msg.model_list || []); break;
    }
    case 'outbox': if (msg.outbox?.length) finishHistoryReadback(); break;
    case 'held_mail': acceptMailRepair(msg); break;
    case 'messages': acceptMessages(msg); break;
    case 'steered': break;
    case 'steering': S.steering = msg.pending || []; renderSteering(S.steering); break;
    case 'cancelled': receivedTurnRevision++; setThinking(false); finishHistoryReadback(); S.turnEnded?.(); break;
    case 'error':
      if (msg.said || S.beliefTrailRefused?.(msg.request_id, msg.message || '', msg.request_error_code || '') || S.themeRefused?.(msg.request_id, msg.message || '') || localPart(msg)) break;
      onError(msg.message || 'unknown error', msg.request_id, msg.provider, !!msg.needs_key, msg.turn_failure, msg.turn_reason, msg.turn_ref); break;
    case 'belief_trail': S.beliefTrailAnswered?.(msg); break;
    case 'local_found': localPart(msg); break;
  }
}
function onStats(stats) {
  S.stats = stats || null;
  renderTitle();

  S.update = (stats && stats.update) || null;
  if (S.renderUpdate) S.renderUpdate();
  if (S.renderUpdateChip) S.renderUpdateChip();
  S.pluginUpdates = (stats && stats.plugin_updates) || 0;
  if (S.renderNavBadges) S.renderNavBadges();
  const born = !!(stats && stats.name && stats.name !== '(not born)');
  const was = S.identityExists;
  S.identityExists = born;
  if (!born) fetchFirstRun();
  if (born && !was) { query('continuity'); query('projects'); query('work'); query('providers'); query('config'); query('asks'); query('work'); query('identity'); }
  renderPresence(); renderFirstbootVisibility(); renderComposer(); S.recordNamed?.();
  if (S.view === 'chat') renderHome();
}
function turnFailureWords(text, failure, reason) {
  const llm = (S.config && S.config.llm) || {};
  const slots = { provider: llm.resolved_provider || llm.provider || '', name: theirs(), reason: reason || text };
  const fill = words => words.replace(/\{(\w+)\}/g, (m, k) => k in slots ? slots[k] : m);
  if (!slots.provider) return text;
  if (failure === 'provider_timeout') return fill(CHAT.providerTimeout);
  if (failure === 'provider_failed') return fill(CHAT.providerFailed);
  return text;
}
function onError(text, requestID, provider, needsKey, turnFailure, turnReason, turnRef) {
  if (S.gradeAnswered?.(requestID, text) || S.revokeAnswered?.(requestID, text)) return;
  if (S.askRefused?.(requestID, text) || S.actRefused?.(requestID, text) || S.chatRefused?.(requestID, text) || S.passNowRefused?.(requestID, text)) return;
  if (rejectBackups(requestID, text) || rejectSandbox(requestID, text) || rejectTools(requestID, text)) return;
  if (rejectMailRepair(requestID, text) || rejectMessages(requestID, text) || rejectExpertConfig(requestID, text)) return;
  if (refuseFirstRunConfig(requestID, text)) return;
  abandonSignIn(requestID);
  if (!requestID) {
    receivedTurnRevision++;
    setThinking(false);
    finishHistoryReadback();
  }
  if (S.stats && !S.identityExists) {
    if (provider && !acceptDiscoveryResponse(requestID, provider)) return;
    const sel = document.getElementById('fb-provider');
    const current = S.providers[parseInt(sel && sel.value, 10)];
    if (provider && (!current || current.name !== provider)) return;
    if (text === 'not available') return;
    if (needsKey) { fbHint(text); return; }
    if (provider && discoveryFailed(text)) return;
    beginAgain(text);
    return;
  }
  if (rejectPluginAction(requestID)) { toast(text); return; }
  if (rejectAccessCode(text, requestID)) return;
  if (rejectSettingsConfig(text, requestID)) { S.renderModelSwitch?.(); if (S.view === 'plugins') renderPlugins(); return; }
  if (S.pluginStartRefused && S.pluginStartRefused(requestID, text)) return;
  if (rejectSpeechLists(requestID, text)) return;
  if (settingsSettled(requestID, provider, text, toast)) return;
  if (rejectProviderSave(text, requestID) || rejectCreate(requestID) || rejectFocusSave(requestID) || rejectContractSave(requestID)) { toast(text); return; }
  if (S.logsAskFailed && S.logsAskFailed(requestID, text)) return;
  if (requestID) { toast(text); return; }
  if (turnFailure) { S.turnFailed?.(turnFailureWords(text, turnFailure, turnReason), turnRef); return; }
  if (ownsThread(S.view)) sysLine(text); else toast(text);
}

export { channelWanted, cutIntoPieces, pieceJoiner, settle } from './transport/channel.js';
