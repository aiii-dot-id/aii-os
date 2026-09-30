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
import { renderPlugins } from './views/plugins.js';
import { applySettingChoices } from './views/plugin-setting.js';
import { renderSettings, acceptSettingsConfig, rejectSettingsConfig, acceptProviderSave, rejectProviderSave, acceptSpeechLists, rejectSpeechLists, acceptDashboardToken, settingsConnectionLost } from './views/settings.js';
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
  if (ws !== socket) return; // an older socket closed after its replacement opened
  ws = null;
  clearProbe();
  if (S.interactionLost) S.interactionLost();
  if (S.chatConnectionLost) S.chatConnectionLost();
  if (S.gradesLost) S.gradesLost();
  S.connected = false;
  if (canRetry()) recoverAuthentication();
  $('send-btn').disabled = true;
  // Pending operations belong to the lost socket even if it never
  // reached onopen. Each owner clears its slot once; retries are quiet.
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
  renderPresence(); // the standing offline state, without a new chat error per retry
  scheduleReconnect();
}
function retireStalled(socket) {
  if (ws !== socket) return;
  socket.onopen = socket.onclose = socket.onmessage = null;
  try { socket.close(); } catch (err) { /* already gone */ }
  connectionLost(socket);
}
// Mobile browsers can resume with an OPEN socket whose network path died
// while the page slept. A handler-independent probe proves transport is
// useful; its answer triggers the same history/status readback a reload gets.
function probeConnection() {
  if (!foreground() || !ws || probeTimer !== null) return;
  const socket = ws;
  // A queued timeout may still execute after cancellation. It belongs
  // only to this probe, never to a later foreground visit.
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
  const socket = new WebSocket(wsScheme + location.host + '/ws?interaction_version=1');
  ws = socket;
  socket.onopen = () => {
    if (ws !== socket) return;
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
    // The mode (normal, SAFE, witness dark) is standing state, not a view's:
    // every connection asks for it, so what the page shows never depends on
    // which pages were visited first.
    query('continuity');
    renderPresence();
    go(S.view);
  };
  socket.onclose = () => connectionLost(socket);
  socket.binaryType = 'arraybuffer';
  socket.onmessage = e => { if (ws === socket) onMessage(e); };
}

// A closed socket says only that transport ended. Ask the HTTP server
// whether the HttpOnly cookie was refused before asking the operator for
// a credential; a Wi-Fi change or server restart must remain a reconnect.
// The server is asked whatever the page knew when it loaded: a token can
// be asked for from Settings while this page is open, and a page that
// trusted its load would reconnect forever into a wall it cannot see.
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
      // The existing bounded reconnect loop retries. Never ask a phone user
      // for a CLI token, cache a stale bearer, or weaken server admission.
    } finally { authCheck = false; }
    return;
  }

  authCheck = false;
  showManualAuthentication();
}

// tokenAct is one of Settings' token acts: /auth/rotate for a new token,
// /auth/require to start asking for one. Its answer carries this browser's
// new cookie. The act has already closed this page's socket by then, so
// recovery stands aside until the answer is in — asking the person for a
// token this browser is about to hold would be wrong — and then reconnects
// on the new cookie.
async function tokenAct(path) {
  if (authCheck) return { ok: false, text: 'A sign-in is in progress; try again when it is done.' };
  authCheck = true;
  try {
    const reply = await fetch(path, { method: 'POST', cache: 'no-store', credentials: 'same-origin' });
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

// Manual recovery belongs only to browsers without a native bridge. There is
// one form and one in-flight request; cancelling the UI cannot undo a cookie.
let authForm = null, authDismissed = false, authFormRevision = 0;
function clearManualAuthentication() {
  authFormRevision++;
  if (authForm) { authForm.querySelector('input').value = ''; authForm.remove(); authForm = null; }
  authDismissed = false; S.tokenPrompted = false;
}
function showManualAuthentication() {
  if (!foreground()) return;
  if (!authForm) {
    const box = document.createElement('section'); box.id = 'dashboard-signin'; box.setAttribute('aria-label', 'Dashboard sign-in');
    // No role of its own: #dashboard-auth is the live region, it was there
    // first, and the words arrive and change inside it.
    const note = document.createElement('p');
    note.textContent = 'Sign in to this dashboard. Read the access token with aii dashboard-token on the AII OS machine, in its identity directory.';
    const reveal = document.createElement('button'); reveal.type = 'button'; reveal.className = 'btn ghost'; reveal.textContent = 'Sign in';
    const form = document.createElement('form');
    const label = document.createElement('label'); label.textContent = 'Access token';
    const input = document.createElement('input'); input.type = 'password'; input.autocomplete = 'off'; input.spellcheck = false; input.setAttribute('aria-label', 'Dashboard access token'); label.appendChild(input);
    const actions = document.createElement('div'); actions.className = 'auth-actions';
    const submit = document.createElement('button'); submit.type = 'submit'; submit.className = 'btn'; submit.textContent = 'Sign in';
    const cancel = document.createElement('button'); cancel.type = 'button'; cancel.className = 'btn ghost'; cancel.textContent = 'Not now';
    actions.append(submit, cancel); form.append(label, actions); box.append(note, reveal, form);
    const visibility = () => { form.hidden = authDismissed; reveal.hidden = !authDismissed; S.tokenPrompted = !authDismissed; };
    reveal.onclick = () => { authDismissed = false; visibility(); input.focus(); };
    cancel.onclick = () => { authFormRevision++; input.value = ''; authDismissed = true; note.textContent = 'Sign-in paused. Use Sign in when ready.'; visibility(); };
    form.onsubmit = async event => {
      event.preventDefault();
      if (authCheck || !canRetry()) return;
      const token = input.value.trim(); input.value = '';
      if (!token) { note.textContent = 'Enter the dashboard access token.'; return; }
      const revision = ++authFormRevision;
      authCheck = true; submit.disabled = true; note.textContent = 'Signing in…';
      try {
        const reply = await fetch('/auth/token', { method: 'POST', cache: 'no-store', credentials: 'same-origin', headers: { 'Content-Type': 'text/plain;charset=UTF-8' }, body: token });
        if (revision !== authFormRevision || box !== authForm) { if (reply.ok && canRetry()) wake(); return; }
        if (reply.ok) { clearManualAuthentication(); authCheck = false; wake(); }
        else note.textContent = 'That access token was not accepted. Try again or choose Not now.';
      } catch (_) {
        if (revision === authFormRevision && box === authForm) note.textContent = 'Sign-in could not be confirmed. Check the connection and try again.';
      } finally {
        authCheck = false; submit.disabled = false;
      }
    };
    authForm = box; ($('dashboard-auth') || document.body).prepend(box); visibility();
  }
}

// Admission failure says nothing about an existing turn. Reuse the bounded
// foreground probe; repeated refusal frames coalesce behind its current read.
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
  // Continuity belongs to a born identity. Connection, view and foreground
  // reads share this gate; the first born status requests it below.
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
    // A final reply may have overtaken this snapshot on the socket. Live
    // turn events received since the probe was sent are the newer truth.
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
      // Voice keeps its playback/capture duties. Speech and late attribution
      // wake the shared record; they never supply a second set of rows.
      if (ve.type === 'transcript_final' && ve.operator && (ve.text || '').trim()) {
        receivedTurnRevision++; S.operatorTurnBegins?.(); setThinking(true); finishHistoryReadback();
      }
      // The annotation owner supplies the label on the next readback.
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
        // The turn ends here, with the reply's own words: they are said once,
        // by name. Nothing else in the conversation is: not a stream frame,
        // a step, the operator's own words, or a page read back later.
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
    case 'history': break; // legacy frames cannot author this page’s history
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
    case 'setting_choices': applySettingChoices(msg.setting_choices); if (S.view === 'plugins') renderPlugins(); break;
    case 'config':
      S.config = msg.config || null;
      acceptSubstrateConfig(msg.request_id); acceptSettingsConfig(msg.request_id); acceptExpertConfig(msg.request_id);
      acceptBackupPreferences(msg.request_id);
      renderChatSubstrate(); renderComposer(); if (S.view === 'settings') renderSettings(); if (S.view === 'plugins') renderPlugins(); break;
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
    case 'profile_device': if (S.onProfileDevice) { S.onProfileDevice(msg.device); } break;
    case 'backups': applyBackups(msg.backups, msg.request_id); break;
    case 'restore_new': applyRestoreNew(msg.restore_new); break;
    case 'update_check': S.update = msg.update || null; if (S.renderUpdate) S.renderUpdate(); if (S.renderUpdateChip) S.renderUpdateChip(); break;
    case 'restart': sysLine('restarting to run the update \u2014 this page reconnects when the identity is back'); break;
    case 'public_name': if (S.renderPublicName) S.renderPublicName(msg.public_name || null); break;
    case 'speech_lists': acceptSpeechLists(msg.speech_lists); break;
    case 'dashboard_token': acceptDashboardToken(msg.request_id, msg.dashboard_token); break;
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
    case 'steered': break; // admission is pending until the recording receipt
    case 'steering': S.steering = msg.pending || []; renderSteering(S.steering); break;
    case 'cancelled': receivedTurnRevision++; setThinking(false); finishHistoryReadback(); break;
    case 'error': onError(msg.message || 'unknown error', msg.request_id, msg.provider); break;
  }
}
function onStats(stats) {
  S.stats = stats || null;

  S.update = (stats && stats.update) || null;
  if (S.renderUpdate) S.renderUpdate();
  if (S.renderUpdateChip) S.renderUpdateChip();
  // One notice per version, the first time the status carries it —
  // the reminder a package-managed install (Ubuntu) would otherwise
  // never flash; the sidebar chip stays as the standing one.
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
function onError(text, requestID, provider) {
  if (S.gradeAnswered?.(requestID, text)) return;
  if (rejectBackups(requestID, text) || rejectSandbox(requestID, text)) return;
  if (rejectMailRepair(requestID, text) || rejectMessages(requestID, text) || rejectExpertConfig(requestID, text)) return;
  abandonSignIn(requestID);
  if (!requestID) {
    receivedTurnRevision++;
    setThinking(false); // an unrelated query must not end an active turn
    finishHistoryReadback();
  }
  if (S.stats && !S.identityExists) {
    if (provider && !acceptDiscoveryResponse(requestID, provider)) return;
    const sel = document.getElementById('fb-provider');
    const current = S.providers[parseInt(sel && sel.value, 10)];
    if (provider && (!current || current.name !== provider)) return;
    if (text === 'not available') return;
    if (text.indexOf('API key') !== -1) { fbHint(text); return; }
    if (provider && discoveryFailed(text)) return;
    if (text.indexOf('list models') !== -1) { fbHint('Enter an API key, then re-select the provider to discover models.'); return; }
    fbResult('err', text);
    $('fb-birth').disabled = false;
    return;
  }
  if (rejectSettingsConfig(text, requestID)) { if (S.view === 'plugins') renderPlugins(); return; }
  if (rejectSpeechLists(requestID, text)) return; // said on the card, beside the field it was asked for
  if (rejectProviderSave(text, requestID) || rejectSubstrateConfig(text, requestID) || rejectCreate(requestID) || rejectFocusSave(requestID) || rejectContractSave(requestID)) { toast(text); return; }
  if (requestID) { toast(text); return; }
  if (S.view === 'chat') sysLine(text, true); else toast(text);
}
