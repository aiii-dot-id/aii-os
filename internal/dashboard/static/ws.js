import './interaction-view.js';
import { hasShellAuth, shellAccessToken } from './shell-auth.js';

import { acceptSignIn, abandonSignIn } from './signin.js';
import { S } from './state.js';
import { $ } from './util.js';
import { announceWords } from './announce.js';
import { renderPresence, setThinking } from './presence.js';
import { go, renderFirstbootVisibility, toast } from './app.js';
import { sysLine, toolEventLive, thinkingEvent, renderChatSubstrate, renderComposer, acceptSubstrateConfig, rejectSubstrateConfig, substrateConnectionLost, renderSteering, renderAsks } from './views/chat.js';
import { renderHome } from './views/home.js';
import { renderWorkPill } from './views/work.js';
import { renderProjPill, renderProjects, rejectCreate, rejectFocusSave, rejectContractSave, acceptCreate, acceptFocusSave, acceptContractSave, projectsConnectionLost } from './views/projects.js';
import { renderMemory } from './views/memory.js';
import { renderIdentity } from './views/identity.js';
import { applyBackups, backupsReconnected, backupsConnectionLost, acceptBackupPreferences, rejectBackups } from './views/backups.js';
import { acceptSandbox, rejectSandbox, sandboxConnectionLost } from './sandbox.js';
import { acceptMailRepair, rejectMailRepair } from './views/mail-repair.js';
import { acceptMessages, rejectMessages } from './views/messages.js';
import { acceptExpertConfig, rejectExpertConfig, expertStatus, expertConnectionLost } from './views/expert.js';
import { applyRestoreNew } from './firstboot-restore.js';
import { renderPlugins, rejectPluginAction, applyPluginStored } from './views/plugins.js';
import { applySettingChoices } from './views/plugin-setting.js';
import { renderSettings, acceptSettingsConfig, rejectSettingsConfig, acceptProviderSave, rejectProviderSave, acceptSpeechLists, rejectSpeechLists, acceptDashboardToken, acceptAccessCode, rejectAccessCode, settingsConnectionLost } from './views/settings.js';
import { renderProviderOptions, discoveryAnswered, discoveryFailed, fbHint, fbResult, acceptDiscoveryResponse, firstbootConnectionLost } from './firstboot.js';
import { publish as publishToSections, onSections, onLayout, onTokensChanged } from './sections.js';
import { renderPanel } from './panel.js';
import { onTheme } from './theme.js';
import { onOverlayChanged } from './overlay.js';
import { bindTransport, render as renderVoice, speak, connectionLost as voiceConnectionLost } from './voice.js';

S.onThemeApplied = onTokensChanged;

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
function connectionLost(socket) {
  if (ws !== socket) return;
  ws = null;
  clearProbe();
  if (S.interactionLost) S.interactionLost();
  if (S.chatConnectionLost) S.chatConnectionLost();
  if (S.gradesLost) S.gradesLost();
  S.connected = false;
  if (canRetry()) recoverAuthentication();
  $('send-btn').disabled = true;
  voiceConnectionLost();
  renderVoice();
  setThinking(false);
  substrateConnectionLost();
  expertConnectionLost();
  settingsConnectionLost();
  if (S.view === 'plugins') renderPlugins();
  backupsConnectionLost();
  sandboxConnectionLost();
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
  if (!canRetry()) return;
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
  if (ws && ws.readyState < 2) return;
  if (ws && ws.readyState === 2) { retireStalled(ws); return; }

  const wsScheme = location.protocol === 'https:' ? 'wss://' : 'ws://';
  const viaChannel = channelWanted(location.protocol, location.hostname, typeof RTCPeerConnection === 'function');
  const socket = new WebSocket(wsScheme + location.host + '/ws?interaction_version=1' + (viaChannel ? '&transport=1' : ''));
  socket.binaryType = 'arraybuffer';
  const opened = carrier => {
    if (ws !== carrier) return;
    clearManualAuthentication();
    clearProbe();
    S.connected = true;
    reconnectDelay = 1000;
    S.wsEverOpened = true;
    backupsReconnected();
    if (S.reconnectTimer) { clearTimeout(S.reconnectTimer); S.reconnectTimer = null; }
    $('send-btn').disabled = false;
    renderVoice();
    query('providers');
    if (S.identityExists) query('steering');
    query('sections'); query('ui_layout'); query('ui_theme');
    query('ui.overlay');

    query('config'); query('asks'); query('work');
    query('continuity');
    renderPresence();
    go(S.view);
  };
  const adopt = carrier => {
    ws = carrier;
    carrier.onclose = () => connectionLost(carrier);
    carrier.onmessage = e => { if (ws === carrier) onMessage(e); };
    opened(carrier);
  };
  if (!viaChannel) {
    ws = socket;
    socket.onopen = () => opened(socket);
    socket.onclose = () => connectionLost(socket);
    socket.onmessage = e => { if (ws === socket) onMessage(e); };
    return;
  }
  const pending = settle(socket, adopt, () => connectionLost(pending), config => new RTCPeerConnection(config));
  ws = pending;
}

export async function recoverAuthentication() {
  if (!canRetry() || S.tokenPrompted || authCheck) return;
  authCheck = true;
  let status;
  try {
    status = await fetch('/auth/token', { cache: 'no-store', credentials: 'same-origin' });
  } catch (e) {
    authCheck = false;
    return;
  }
  if (status.status !== 401 || !canRetry()) {
    authCheck = false;
    return;
  }

  if (hasShellAuth()) {
    try {
      const token = await shellAccessToken();
      if (!canRetry()) return;
      const reply = await fetch('/auth/token', {
        method: 'POST', cache: 'no-store', credentials: 'same-origin',
        headers: { 'Content-Type': 'text/plain;charset=UTF-8' }, body: token,
      });
      if (reply.ok) { authCheck = false; wake(); }
    } catch (e) {
    } finally { authCheck = false; }
    return;
  }

  authCheck = false;
  showManualAuthentication();
}

async function tokenAct(path) {
  if (authCheck) return { ok: false, text: 'A sign-in is in progress; try again when it is done.' };
  authCheck = true;
  const previous = ws;
  try {
    const reply = await fetch(path, { method: 'POST', cache: 'no-store', credentials: 'same-origin' });
    if (reply.ok && previous && ws === previous) retireStalled(previous);
    return { ok: reply.ok, text: reply.ok ? '' : (await reply.text()).trim() };
  } catch (e) {
    return { ok: false, text: 'The answer could not be confirmed. Check the connection; if this page then asks you to sign in, read the token with aii dashboard-token on this machine.' };
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
  authDismissed = false; S.tokenPrompted = false;
}
const ACCESS_CODE_LENGTH = 12, ACCESS_CODE_ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
export function couldBeAccessCode(text) {
  const read = String(text).toUpperCase().replace(/[\s-]/g, '').replace(/O/g, '0').replace(/[IL]/g, '1');
  return read.length <= ACCESS_CODE_LENGTH && [...read].every(ch => ACCESS_CODE_ALPHABET.includes(ch));
}
function showManualAuthentication() {
  if (!foreground()) return;
  if (!authForm) {
    const box = document.createElement('section'); box.id = 'dashboard-signin'; box.setAttribute('aria-label', 'Dashboard sign-in');
    const note = document.createElement('p');
    note.textContent = 'Sign in to this dashboard. On a device that is already signed in, Settings \u2192 Dashboard \u2192 Open on another device shows an access code for this one. Or read the access token with aii dashboard-token on the AII OS machine, in its identity directory.';
    const reveal = document.createElement('button'); reveal.type = 'button'; reveal.className = 'btn ghost'; reveal.textContent = 'Sign in';
    const form = document.createElement('form');
    const label = document.createElement('label'); label.textContent = 'Access code or access token';
    const input = document.createElement('input'); input.type = 'text'; input.autocomplete = 'off'; input.autocapitalize = 'off'; input.spellcheck = false; input.setAttribute('aria-label', 'Dashboard access code or access token'); label.appendChild(input);
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
      if (!token) { note.textContent = 'Enter an access code or the dashboard access token.'; return; }
      const revision = ++authFormRevision;
      authCheck = true; submit.disabled = true; note.textContent = 'Signing in…';
      try {
        const reply = await fetch('/auth/token', { method: 'POST', cache: 'no-store', credentials: 'same-origin', headers: { 'Content-Type': 'text/plain;charset=UTF-8' }, body: token });
        const why = reply.status === 403 ? (await reply.text()).trim() : '';
        if (revision !== authFormRevision || box !== authForm) { if (reply.ok && canRetry()) wake(); return; }
        if (reply.ok) { clearManualAuthentication(); authCheck = false; wake(); }
        else note.textContent = why ? 'That was not accepted: ' + why + '.' : 'That was not accepted. An access code works once and for a short time. Try again or choose Not now.';
      } catch (_) {
        if (revision === authFormRevision && box === authForm) note.textContent = 'Sign-in could not be confirmed. Check the connection and try again.';
      } finally {
        authCheck = false; submit.disabled = false;
      }
    };
    authForm = box; ($('dashboard-auth') || document.body).prepend(box); visibility();
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

const voiceIn = bindTransport(sendVoiceFrame, send) || {};

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

  if (e.data instanceof ArrayBuffer) { if (voiceIn.receiveFrame) voiceIn.receiveFrame(e.data); return; }
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

  publishToSections(msg);
  switch (msg.type) {
    case 'probe': break;
    case 'sections': onSections(msg.sections || [], msg.message || ''); break;
    case 'layout': onLayout(msg.layout || null); break;

    case 'theme': onTheme(msg.theme || null); onTokensChanged(); break;
    case 'status': onStats(msg.stats); expertStatus(); renderPanel(); renderVoice(); break;
    case 'voice_session': if (voiceIn.sessionState) voiceIn.sessionState(msg.voice_session || {}); break;
    case 'voice_event': {
      const ve = msg.voice_event || {};
      if (ve.type === 'transcript_final' && ve.operator && (ve.text || '').trim()) {
        receivedTurnRevision++; S.operatorTurnBegins?.(); setThinking(true); finishHistoryReadback();
      }
      if (ve.type === 'speaker_observation' && ve.refers_to) {
        finishHistoryReadback();
      }
      if (voiceIn.voiceEvent) voiceIn.voiceEvent(ve);
      break;
    }
    case 'voice_hush': if (voiceIn.hushFromHost) voiceIn.hushFromHost(msg.voice_hush || {}); break;
    case 'continuity': S.cont = msg.continuity || null; renderPresence(); renderPanel(); if (S.view === 'home') renderHome(); if (S.view === 'identity') renderIdentity(); break;
    case 'response':
      if (!msg.stream && msg.role !== 'identity') {
        sysLine(msg.message); break;
      }
      receivedTurnRevision++;
      if (msg.stream) { setThinking(true); break; }
      setThinking(false);
  renderSteering([]);

      if (msg.role === 'identity') {
        announceWords(S.stats?.name || 'identity', msg.message);
        speak(msg.message, msg.voice_reply);
        if (S.view === 'memory' || S.view === 'identity') query('identity');
      }
      finishHistoryReadback();
      break;
    case 'system_line': sysLine(msg.message); break;
    case 'event':
      if (msg.kind === 'thinking') { thinkingEvent(msg.args); break; }
      toolEventLive(msg.name, msg.args);
      if (msg.name === 'work') query('work');
      break;
    case 'history': break;
    case 'interaction_changed': if (S.interactionChanged) S.interactionChanged(); break;
    case 'interaction_page': if (S.interactionPage) S.interactionPage(msg.interaction_page, msg.request_id, msg.interaction_window); break;
    case 'interaction_error': if (S.interactionError) S.interactionError(msg.interaction_reason, msg.message, msg.request_id); break;
    case 'chat_ack': if (S.chatAcknowledged) S.chatAcknowledged(msg.request_id, msg.message); break;
    case 'chat_receipt': if (S.chatReceipt) S.chatReceipt(msg.request_id, msg.chat_recording); break;
    case 'asks': S.asks = msg.asks || []; renderAsks(S.asks); renderPanel(); break;
    case 'identity': S.identity = msg.identity || null; if (S.view === 'memory') renderMemory(); if (S.view === 'identity') renderIdentity(); if (S.view === 'home') renderHome(); break;
    case 'recall': S.recall = { query: msg.query || '', text: msg.message || '' }; if (S.view === 'memory') renderMemory(); break;
    case 'tools': S.tools = msg.tools || []; if (S.view === 'settings') renderSettings(); break;
    case 'sandbox': S.sandbox = msg.sandbox || null; acceptSandbox(msg.request_id); if (S.view === 'settings') renderSettings(); break;
    case 'work': S.gradeAnswered?.(msg.request_id, null); S.work = msg.work || null; S.gradesRead?.(S.work); renderWorkPill(); renderPanel(); if (S.view === 'home') renderHome(); break;

    case 'workspace': S.workspace = msg.workspace || null; S.workspaceFor = (S.workspace && S.workspace.project) ? S.workspace.project.id : ''; if (S.view === 'projects') renderProjects(); break;
    case 'overlays': S.overlays = msg.overlays || []; renderPanel(); break;
    case 'overlay_changed': onOverlayChanged(msg.token, msg.paths); break;
    case 'setting_choices': applySettingChoices(msg.setting_choices); if (S.view === 'plugins') renderPlugins(); if (S.view === 'settings') renderSettings(); break;
    case 'plugin_stored': applyPluginStored(msg.plugin_stored); if (S.view === 'plugins') renderPlugins(); break;
    case 'config':
      S.config = msg.config || null;
      acceptSubstrateConfig(msg.request_id); acceptSettingsConfig(msg.request_id); acceptExpertConfig(msg.request_id);
      acceptBackupPreferences(msg.request_id);
      renderChatSubstrate(); renderComposer(); if (S.view === 'settings') renderSettings(); if (S.view === 'plugins') renderPlugins();
      S.chipsRedraw?.();
      if (S.view === 'memory') query('identity');
      break;
    case 'logs':

      if (msg.logs_tail) { S.logTail = msg.logs_tail; }
      else { S.logsList = msg.logs_list || []; S.logFile = ''; S.logTail = null; }
      if (S.view === 'settings') renderSettings(); break;
    case 'projects': {
      const prevActive = S.activeProject ? S.activeProject.id : '';
      const primed = S.projectsPrimed; S.projectsPrimed = true;

      const prevIDs = S.projects.map(p => p.id);
      S.projects = msg.projects || [];
      const made = acceptCreate(msg.request_id, prevIDs);
      if (made) S.viewedProject = made;
      S.activeProject = S.projects.find(p => p.active) || null;
      const nowActive = S.activeProject ? S.activeProject.id : '';
      if (primed && nowActive && nowActive !== prevActive) {

        sysLine('now working in “' + (S.activeProject.name) + '” — turns and work sessions are stamped with it');
      }
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

      renderPanel();
      if (S.view === 'projects') renderProjects();
      if (S.view === 'home') renderHome();
      break;
    }
    case 'providers': S.providers = msg.providers || []; S.brokenProviders = msg.broken_providers || []; S.skipSignInWithValidToken = msg.skip_signin_with_valid_token !== false; acceptProviderSave(msg.request_id); S.providersLoaded = true; if (!S.identityExists) renderProviderOptions(); renderChatSubstrate(); if (S.view === 'settings') renderSettings(); break;
    case 'provider_signin': case 'profile_signin': acceptSignIn(msg); break;
    case 'backups': applyBackups(msg.backups, msg.request_id); break;
    case 'restore_new': applyRestoreNew(msg.restore_new); break;
    case 'update_check': S.update = msg.update || null; if (S.renderUpdate) S.renderUpdate(); if (S.renderUpdateChip) S.renderUpdateChip(); break;
    case 'restart': sysLine('restarting to run the update \u2014 this page reconnects when the identity is back'); break;
    case 'public_name': if (S.renderPublicName) S.renderPublicName(msg.public_name || null); break;
    case 'speech_lists': acceptSpeechLists(msg.speech_lists); break;
    case 'dashboard_token': acceptDashboardToken(msg.request_id, msg.dashboard_token); break;
    case 'access_code': acceptAccessCode(msg.request_id, msg.access_code || null); break;
    case 'models': {
      if (!acceptDiscoveryResponse(msg.request_id, msg.provider)) {

        const p = (S.providers || []).find(x => x.name === msg.provider);
        if (p) { const seen = new Set(p.models || []); p.models = (p.models || []).concat((msg.model_list || []).filter(m => !seen.has(m))); }
        renderChatSubstrate(); if (S.view === 'settings') renderSettings();
        break;
      }
      discoveryAnswered(msg.model_list || []); break;
    }
    case 'outbox': if (msg.outbox?.length) finishHistoryReadback(); break;
    case 'held_mail': acceptMailRepair(msg); break;
    case 'messages': acceptMessages(msg); break;
    case 'steered': break;
    case 'steering': S.steering = msg.pending || []; renderSteering(S.steering); break;
    case 'cancelled': receivedTurnRevision++; setThinking(false); finishHistoryReadback(); break;
    case 'error': onError(msg.message || 'unknown error', msg.request_id, msg.provider, !!msg.needs_key); break;
  }
}
function onStats(stats) {
  S.stats = stats || null;

  S.update = (stats && stats.update) || null;
  if (S.renderUpdate) S.renderUpdate();
  if (S.renderUpdateChip) S.renderUpdateChip();
  const u = S.update, seen = u && (u.installed_version || u.available_version);
  if (seen && seen !== S.noticedUpdate) {
    S.noticedUpdate = seen;
    sysLine(u.needs_restart ? 'update ' + seen + ' is installed \u2014 click "Restart to update" in the sidebar when convenient'
      : (u.stage_refusal ? 'update ' + seen + ' is available \u2014 this install is managed by your package manager; Settings \u2192 Updates has the command'
      : (u.automatic ? 'update ' + seen + ' is available \u2014 it is downloaded, verified and installed on the next check'
      : 'update ' + seen + ' is available \u2014 Settings \u2192 Updates')));
  }
  S.pluginUpdates = (stats && stats.plugin_updates) || 0;
  if (S.renderNavBadges) S.renderNavBadges();
  const born = !!(stats && stats.name && stats.name !== '(not born)');
  const was = S.identityExists;
  S.identityExists = born;
  if (born && !was) { query('continuity'); query('projects'); query('work'); query('providers'); query('config'); query('asks'); query('work'); query('identity'); }
  renderPresence(); renderFirstbootVisibility(); renderComposer();
  if (S.view === 'home') renderHome();
}
function onError(text, requestID, provider, needsKey) {
  if (S.gradeAnswered?.(requestID, text)) return;
  if (rejectBackups(requestID, text) || rejectSandbox(requestID, text)) return;
  if (rejectMailRepair(requestID, text) || rejectMessages(requestID, text) || rejectExpertConfig(requestID, text)) return;
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
    fbResult('err', text);
    $('fb-birth').disabled = false;
    return;
  }
  if (rejectPluginAction(requestID)) { toast(text); return; }
  if (rejectAccessCode(text, requestID)) return;
  if (rejectSettingsConfig(text, requestID)) { if (S.view === 'plugins') renderPlugins(); return; }
  if (rejectSpeechLists(requestID, text)) return;
  if (rejectProviderSave(text, requestID) || rejectSubstrateConfig(text, requestID) || rejectCreate(requestID) || rejectFocusSave(requestID) || rejectContractSave(requestID)) { toast(text); return; }
  if (requestID) { toast(text); return; }
  if (S.view === 'chat') sysLine(text, true); else toast(text);
}

const PIECE_BYTES = 16 * 1024, BINARY = 1, LAST = 2;
const QUEUE_HIGH = 1024 * 1024, QUEUE_LOW = 256 * 1024;
const encoder = new TextEncoder(), decoder = new TextDecoder();

export function channelWanted(protocol, hostname, hasPeerConnection) {
  const here = ['127.0.0.1', 'localhost', '[::1]', '::1'].includes(hostname);
  return protocol === 'https:' && !here && !!hasPeerConnection;
}

export function cutIntoPieces(data, pieceBytes) {
  let bytes, head = 0;
  if (typeof data === 'string') bytes = encoder.encode(data);
  else {
    bytes = data instanceof ArrayBuffer ? new Uint8Array(data) : new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
    head = BINARY;
  }
  const room = Math.max(1, pieceBytes - 1), pieces = [];
  let at = 0;
  do {
    const n = Math.min(room, bytes.length - at);
    const piece = new Uint8Array(1 + n);
    piece[0] = head | (at + n === bytes.length ? LAST : 0);
    piece.set(bytes.subarray(at, at + n), 1);
    pieces.push(piece);
    at += n;
  } while (at < bytes.length);
  return pieces;
}

export function pieceJoiner() {
  let parts = [], size = 0, binary = false, started = false;
  return buffer => {
    const piece = new Uint8Array(buffer);
    if (!piece.length) return false;
    const isBinary = !!(piece[0] & BINARY);
    if (started && isBinary !== binary) return false;
    started = true; binary = isBinary;
    parts.push(piece.subarray(1)); size += piece.length - 1;
    if (!(piece[0] & LAST)) return null;
    const whole = new Uint8Array(size);
    let at = 0;
    for (const part of parts) { whole.set(part, at); at += part.length; }
    const message = { data: binary ? whole.buffer : decoder.decode(whole) };
    parts = []; size = 0; started = false;
    return message;
  };
}

class ChannelSocket {
  constructor(pc, dc) {
    this.pc = pc; this.dc = dc;
    this.readyState = 0; this.binaryType = 'arraybuffer';
    this.onmessage = null; this.onclose = null;
    this.queue = []; this.join = pieceJoiner();
    dc.binaryType = 'arraybuffer';
    dc.bufferedAmountLowThreshold = QUEUE_LOW;
    dc.onbufferedamountlow = () => this.pump();
    dc.onopen = () => { if (this.readyState === 0) this.readyState = 1; };
    dc.onclose = () => this.end();
    dc.onmessage = e => {
      const message = this.join(e.data);
      if (message === false) { this.close(); return; }
      if (message && this.onmessage) this.onmessage(message);
    };
    pc.onconnectionstatechange = () => { if (pc.connectionState === 'failed' || pc.connectionState === 'closed') this.end(); };
  }
  pieceBytes() {
    const stated = this.pc.sctp && this.pc.sctp.maxMessageSize;
    return stated > 1 && stated < PIECE_BYTES ? stated : PIECE_BYTES;
  }
  send(data) {
    if (this.readyState !== 1) throw new Error('the channel is not open');
    for (const piece of cutIntoPieces(data, this.pieceBytes())) this.queue.push(piece);
    this.pump();
  }
  pump() {
    try {
      while (this.queue.length && this.dc.readyState === 'open' && this.dc.bufferedAmount < QUEUE_HIGH) this.dc.send(this.queue.shift());
    } catch (_) { this.close(); }
  }
  close() {
    try { this.dc.close(); } catch (_) { }
    try { this.pc.close(); } catch (_) { }
    this.end();
  }
  end() {
    if (this.readyState === 3) return;
    this.readyState = 3; this.queue = [];
    if (this.onclose) this.onclose({});
  }
}

export function settle(socket, adopt, lost, makePeer) {
  let pc = null, channel = null, timer = null, over = false, negotiating = false;
  let offered = false, answered = false, answering = false;
  let localCandidates = [], remoteCandidates = [];
  const finish = () => { over = true; clearTimeout(timer); localCandidates = []; remoteCandidates = []; };
  const abandon = () => {
    try { socket.close(); } catch (_) { }
    if (channel) channel.onclose = null;
    if (pc) { try { pc.close(); } catch (_) { } }
  };
  const fail = () => { if (over) return; finish(); abandon(); lost(); };
  const onSocket = () => {
    finish();
    if (channel) channel.onclose = null;
    if (pc) { try { pc.close(); } catch (_) { } }
    adopt(socket);
  };
  const say = message => { if (!over && socket.readyState === 1) socket.send(JSON.stringify(message)); };

  const begin = setup => {
    negotiating = true;
    timer = setTimeout(fail, setup.ready_within_ms);
    pc = makePeer({ iceServers: (setup.ice_servers || []).map(s => ({ urls: s.urls, username: s.username, credential: s.credential })) });
    channel = new ChannelSocket(pc, pc.createDataChannel('dashboard', { ordered: true }));
    channel.onmessage = e => {
      if (over) return;
      let message = null;
      try { message = JSON.parse(e.data); } catch (_) { }
      if (!message || message.type !== 'transport_ready') return;
      finish();
      adopt(channel);
    };
    pc.onicecandidate = e => {
      if (over) return;
      const message = e.candidate
        ? { type: 'transport_candidate', candidate: e.candidate.candidate, sdp_mid: e.candidate.sdpMid, sdp_mline_index: e.candidate.sdpMLineIndex }
        : { type: 'transport_candidates_done' };
      if (offered) say(message);
      else localCandidates.push(message);
    };
    pc.createOffer()
      .then(offer => pc.setLocalDescription(offer).then(() => {
        if (over) return;
        say({ type: 'transport_offer', sdp: offer.sdp });
        offered = true;
        for (const candidate of localCandidates) say(candidate);
        localCandidates = [];
      }))
      .catch(() => { });
  };

  socket.onopen = () => { };
  socket.onclose = () => {
    if (over) return;
    if (!negotiating) fail();
  };
  socket.onmessage = e => {
    if (over) return;
    let message = null;
    if (typeof e.data === 'string') { try { message = JSON.parse(e.data); } catch (_) { } }
    if (!negotiating) {
      if (message && message.type === 'transport_setup' && message.ready_within_ms > 0) { begin(message); return; }
      finish();
      adopt(socket);
      if (socket.onmessage) socket.onmessage(e);
      return;
    }
    if (!message) return;
    if (message.type === 'transport_answer' && !answering) {
      answering = true;
      pc.setRemoteDescription({ type: 'answer', sdp: message.sdp }).then(() => {
        if (over) return;
        answered = true;
        for (const candidate of remoteCandidates) pc.addIceCandidate(candidate).catch(() => {});
        remoteCandidates = [];
      }).catch(() => {});
    } else if (message.type === 'transport_candidate') {
      const candidate = { candidate: message.candidate, sdpMid: message.sdp_mid, sdpMLineIndex: message.sdp_mline_index };
      if (answered) pc.addIceCandidate(candidate).catch(() => {});
      else remoteCandidates.push(candidate);
    }
    else if (message.type === 'transport_ready') onSocket();
  };
  return { readyState: 0, close: () => { if (over) return; finish(); abandon(); } };
}
