
import { S } from './state.js';
import { $ } from './util.js';
import { renderPresence, setThinking } from './presence.js';
import { go, renderFirstbootVisibility, toast } from './app.js';
import { addMsg, attachSpeaker, sysLine, toolEventLive, thinkingEvent, renderHistory, renderChatSubstrate, acceptSubstrateConfig, rejectSubstrateConfig, substrateConnectionLost, renderSteering, renderAsks } from './views/chat.js';
import { renderHome } from './views/home.js';
import { renderWorkPill } from './views/work.js';
import { renderProjPill, renderProjects, rejectCreate, rejectFocusSave, rejectContractSave, acceptCreate, acceptFocusSave, acceptContractSave, projectsConnectionLost } from './views/projects.js';
import { renderMemory } from './views/memory.js';
import { renderIdentity } from './views/identity.js';
import { renderPlugins } from './views/plugins.js';
import { renderSettings, acceptSettingsConfig, rejectSettingsConfig, acceptProviderSave, rejectProviderSave, settingsConnectionLost } from './views/settings.js';
import { renderProviderOptions, setModelOptions, fbHint, fbResult, acceptDiscoveryResponse, firstbootConnectionLost } from './firstboot.js';
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
function scheduleReconnect() {
  const jitter = 0.8 + Math.random() * 0.4;
  const delay = Math.min(reconnectDelay * jitter, RECONNECT_MAX);
  reconnectDelay = Math.min(reconnectDelay * 2, RECONNECT_MAX);

  if (S.reconnectTimer) clearTimeout(S.reconnectTimer);
  S.reconnectTimer = setTimeout(connect, delay);
}
export function connect() {

  if (ws && ws.readyState !== 3) return;

  const wsScheme = location.protocol === 'https:' ? 'wss://' : 'ws://';
  ws = new WebSocket(wsScheme + location.host + '/ws');
  ws.onopen = () => {
    S.connected = true;
    reconnectDelay = 1000;
    S.wsEverOpened = true;
    if (S.reconnectTimer) { clearTimeout(S.reconnectTimer); S.reconnectTimer = null; }
    $('send-btn').disabled = false;
    renderVoice();
    if (!S.identityExists) query('providers');
    else query('steering');
    query('sections'); query('ui_layout'); query('ui_theme');
    query('ui.overlay');

    query('config'); query('asks');
    renderPresence();
    go(S.view);
  };
  ws.onclose = () => {
    S.connected = false;

    const hasTok = /(^|; )aii_token=/.test(document.cookie);
    if (document.head.dataset.aiiTokenRequired === '1' && !S.tokenPrompted && (!hasTok || !S.wsEverOpened)) {
      S.tokenPrompted = true;
      const t = prompt((hasTok
        ? 'The stored access token was refused. Re-enter the dashboard access token'
        : 'This dashboard requires its access token')
        + ' (printed once on the runtime console at boot).');
      if (t && t.trim()) {
        document.cookie = 'aii_token=' + t.trim() + '; path=/; max-age=31536000; SameSite=Strict' + (location.protocol === 'https:' ? '; Secure' : '');
      }
    }
    $('send-btn').disabled = true;
    voiceConnectionLost();
    renderVoice();
    substrateConnectionLost();
    settingsConnectionLost();
    firstbootConnectionLost();
    projectsConnectionLost();
    renderPresence();
    if (S.identityExists) sysLine('connection lost — reconnecting…');
    scheduleReconnect();
  };
  ws.binaryType = 'arraybuffer';
  ws.onmessage = onMessage;
}

export function wake() {
  if (S.reconnectTimer) { clearTimeout(S.reconnectTimer); S.reconnectTimer = null; }
  connect();
}
if (typeof document !== 'undefined') {
  window.addEventListener('online', wake);
  document.addEventListener('visibilitychange', function () {
    if (document.visibilityState === 'visible') wake();
  });
}
export function send(obj) {
  if (!ws || ws.readyState !== 1) return '';
  if (!obj.request_id) obj.request_id = String(++requestSeq);
  ws.send(JSON.stringify(obj));
  return obj.request_id;
}
export function query(q, extra) { return send(Object.assign({ type: 'query', query: q }, extra || {})); }

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

  publishToSections(msg);
  switch (msg.type) {
    case 'sections': onSections(msg.sections || [], msg.message || ''); break;
    case 'layout': onLayout(msg.layout || null); break;

    case 'theme': onTheme(msg.theme || null); onTokensChanged(); break;
    case 'status': onStats(msg.stats); renderPanel(); renderVoice(); break;
    case 'voice_session': if (voiceIn.sessionState) voiceIn.sessionState(msg.voice_session || {}); break;
    case 'voice_event': {
      const ve = msg.voice_event || {};
      // The operator's spoken words are a message they sent: the
      // thread shows them and the identity is now thinking on them, the
      // way a typed message looks. The marker names the channel.
      if (ve.type === 'transcript_final' && ve.operator && (ve.text || '').trim()) {
        addMsg('operator', '[voice] ' + ve.text.trim(), '', ve.session_id && ve.sequence ? ve.session_id + '/' + ve.sequence : ''); setThinking(true);
      }
      // A speaker observation amends the bubble of the final it names
      // (seam 3); one the page never showed is passed over.
      if (ve.type === 'speaker_observation' && ve.refers_to) {
        attachSpeaker((ve.session_id || '') + '/' + ve.refers_to, ve.attribution || '');
      }
      if (voiceIn.voiceEvent) voiceIn.voiceEvent(ve);
      break;
    }
    case 'continuity': S.cont = msg.continuity || null; renderPresence(); renderPanel(); if (S.view === 'home') renderHome(); if (S.view === 'identity') renderIdentity(); break;
    case 'response':
      if (msg.stream) { setThinking(true); break; }
      setThinking(false);
  renderSteering([]);

      if (msg.role === 'identity') {
        addMsg('identity', msg.message);
        speak(msg.message, msg.voice_reply);
        if (S.view === 'memory' || S.view === 'identity') query('identity');
      } else {
        sysLine(msg.message);
      }
      break;
    case 'event':
      if (msg.kind === 'thinking') { thinkingEvent(msg.args); break; }
      toolEventLive(msg.name, msg.args);
      if (msg.name === 'work') query('work');
      break;
    case 'history': renderHistory(msg.history); break;
    case 'asks': S.asks = msg.asks || []; renderAsks(S.asks); renderPanel(); break;
    case 'identity': S.identity = msg.identity || null; if (S.view === 'memory') renderMemory(); if (S.view === 'identity') renderIdentity(); if (S.view === 'home') renderHome(); break;
    case 'recall': S.recall = { query: msg.query || '', text: msg.message || '' }; if (S.view === 'memory') renderMemory(); break;
    case 'tools': S.tools = msg.tools || []; if (S.view === 'settings') renderSettings(); break;
    case 'sandbox': S.sandbox = msg.sandbox || null; if (S.view === 'settings') renderSettings(); break;
    case 'work': S.work = msg.work || null; renderWorkPill(); renderPanel(); if (S.view === 'home') renderHome(); break;

    case 'workspace': S.workspace = msg.workspace || null; S.workspaceFor = (S.workspace && S.workspace.project) ? S.workspace.project.id : ''; if (S.view === 'projects') renderProjects(); break;
    case 'overlays': S.overlays = msg.overlays || []; renderPanel(); break;
    case 'overlay_changed': onOverlayChanged(msg.token, msg.paths); break;
    case 'config':
      S.config = msg.config || null;
      acceptSubstrateConfig(msg.request_id); acceptSettingsConfig(msg.request_id);
      renderChatSubstrate(); if (S.view === 'settings') renderSettings(); if (S.view === 'plugins') renderPlugins(); break;
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
          S.focusDraft = null;
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
    case 'providers': S.providers = msg.providers || []; acceptProviderSave(msg.request_id); S.providersLoaded = true; if (!S.identityExists) renderProviderOptions(); renderChatSubstrate(); if (S.view === 'settings') renderSettings(); break;
    case 'provider_signin': if (msg.signin_url && S.signInNavigate) { S.signInNavigate(msg.signin_url); } else if (S.signInAbandon) { S.signInAbandon(); } break;
    case 'profile_signin': if (msg.signin_url && S.profileSignInNavigate) { S.profileSignInNavigate(msg.signin_url); } else if (S.profileSignInAbandon) { S.profileSignInAbandon(); } break;
    case 'profile_device': if (S.onProfileDevice) { S.onProfileDevice(msg.device); } break;
    case 'update_check': S.update = msg.update || null; if (S.renderUpdate) S.renderUpdate(); if (S.renderUpdateChip) S.renderUpdateChip(); break;
    case 'restart': sysLine('restarting to run the update \u2014 this page reconnects when the identity is back'); break;
    case 'public_name': if (S.renderPublicName) S.renderPublicName(msg.public_name || null); break;
    case 'models': {
      if (!acceptDiscoveryResponse(msg.request_id, msg.provider)) {

        const p = (S.providers || []).find(x => x.name === msg.provider);
        if (p) { const seen = new Set(p.models || []); p.models = (p.models || []).concat((msg.model_list || []).filter(m => !seen.has(m))); }
        renderChatSubstrate(); if (S.view === 'settings') renderSettings();
        break;
      }
      const sel = document.getElementById('fb-provider');
      const p = S.providers[parseInt(sel && sel.value, 10)];
      setModelOptions(msg.model_list || []);
      if (p && p.default_model) document.getElementById('fb-model').value = p.default_model;
      fbHint(''); break;
    }
    case 'outbox': (msg.outbox || []).forEach(o => addMsg('identity', o.content, 'while you were away')); break;
    case 'steered': toast('delivered to the running turn'); break;
    case 'steering': S.steering = msg.pending || []; renderSteering(S.steering); break;
    case 'cancelled': setThinking(false); sysLine('The operator stopped this turn.'); break;
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
  if (born && !was) { query('projects'); query('work'); query('providers'); query('config'); query('asks'); query('identity'); }
  renderPresence(); renderFirstbootVisibility();
  if (S.view === 'home') renderHome();
}
function onError(text, requestID, provider) {
  setThinking(false);
  if (!S.identityExists) {
    if (provider && !acceptDiscoveryResponse(requestID, provider)) return;
    const sel = document.getElementById('fb-provider');
    const current = S.providers[parseInt(sel && sel.value, 10)];
    if (provider && (!current || current.name !== provider)) return;
    if (text === 'not available') return;
    if (text.indexOf('API key') !== -1) { fbHint(text); return; }
    if (text.indexOf('list models') !== -1) { fbHint('Enter an API key, then re-select the provider to discover models.'); return; }
    fbResult('err', text);
    $('fb-birth').disabled = false;
    return;
  }
  if (rejectProviderSave(text, requestID) || rejectSubstrateConfig(text, requestID) || rejectSettingsConfig(text, requestID) || rejectCreate(requestID) || rejectFocusSave(requestID) || rejectContractSave(requestID)) { toast(text); return; }
  if (S.view === 'chat') sysLine('[error] ' + text); else toast(text);
}
