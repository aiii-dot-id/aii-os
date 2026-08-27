/* ws.js — the WebSocket client: connect/reconnect, send, query,
   and the one inbound dispatch (onMessage). Owns the socket
   EXCLUSIVELY — no other module may touch `ws` (wsReady() is the
   only readiness surface). R66: this is the frame-owned socket
   (UI_FRAME.md §2/§4): dispatch cases become data.subscribe topic
   projections, and send/query become the command registry's
   forwarding edge. Sections never open sockets. */
import { S } from './state.js';
import { $ } from './util.js';
import { renderPresence, setThinking } from './presence.js';
import { go, renderFirstbootVisibility, toast } from './app.js';
import { addMsg, sysLine, toolEventLive, thinkingEvent, renderHistory, renderChatSubstrate, acceptSubstrateConfig, rejectSubstrateConfig, substrateConnectionLost, renderSteering } from './views/chat.js';
import { renderHome } from './views/home.js';
import { renderWorkPill } from './views/work.js';
import { renderProjPill, renderProjects, rejectCreate, acceptCreate, acceptFocusSave } from './views/projects.js';
import { dropAllPending } from './pending.js';
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

/* --- socket ------------------------------------------------ */
let ws = null;
let requestSeq = 0;
/* Backoff state: reconnects use exponential backoff with jitter,
   capped, reset on successful open. The fixed 2s timer polled a down
   server forever, from every open tab — the operator called it out as
   poor design, correctly: a reactive UI waits for the network to
   recover, it does not interrogate it on a schedule. Values: start
   1s, ×2 per failure, cap 10s, ±20% jitter so N tabs don't sync into
   a thundering herd. SUCCESS resets to the floor. */
let reconnectDelay = 1000;
const RECONNECT_MAX = 10000;
function scheduleReconnect() {
  const jitter = 0.8 + Math.random() * 0.4; // 0.8–1.2
  const delay = Math.min(reconnectDelay * jitter, RECONNECT_MAX);
  reconnectDelay = Math.min(reconnectDelay * 2, RECONNECT_MAX);
  // ONE pending attempt, kept true here rather than by argument. Today
  // onclose is the only caller and connect() refuses a second dial, so
  // there is nothing live to clear; a future second caller would leak a
  // timer that still fires, and that costs a reconnect chain.
  if (S.reconnectTimer) clearTimeout(S.reconnectTimer);
  S.reconnectTimer = setTimeout(connect, delay);
}
export function connect() {
  /* ONE DIAL AT A TIME, and this module is where that is decided — it
     owns the socket, so it owns the attempt. wake() guarded only OPEN, so
     a wake during a CONNECTING dial opened a SECOND socket while the
     first was still in flight; neither was closed or detached, both
     onmessage handlers stayed live, and once the server returned every
     broadcast was dispatched TWICE — duplicate renders, and no way back
     without a page reload. A CLOSED socket is the only one worth
     replacing. */
  if (ws && ws.readyState !== 3) return;
  // DERIVED FROM THE PAGE, never written down. This said ws:// and was
  // right until it wasn't: on an https page a plaintext socket is either
  // blocked as mixed content or dialled straight at the TLS port, and
  // the reconnect below then retries it every couple of seconds —
  // forever, from every open tab, with the operator seeing only a UI
  // that never connects.
  //
  // A constant here would be a second copy of the server's scheme, free
  // to drift from it exactly as the last one did. The page was loaded
  // over the scheme the server actually served, so ask the page.
  const wsScheme = location.protocol === 'https:' ? 'wss://' : 'ws://';
  ws = new WebSocket(wsScheme + location.host + '/ws');
  ws.onopen = () => {
    S.connected = true;
    reconnectDelay = 1000; // SUCCESS resets backoff to the floor
    S.wsEverOpened = true; // the door has opened at least once this page load (D76)
    if (S.reconnectTimer) { clearTimeout(S.reconnectTimer); S.reconnectTimer = null; }
    $('send-btn').disabled = false;
    renderVoice();
    if (!S.identityExists) query('providers'); // firstboot furniture — post-birth reconnects skip the outbound discover (#9)
    else query('steering'); // a reconnect mid-turn must not show an empty queue over a full one
    query('sections'); query('ui_layout'); query('ui_theme'); // frame furniture (R66 UP2): the section registry + layout profiles + T0 tokens
    query('ui.overlay'); // W2: the overlay readback owed the human — frame furniture, same family as the registry
    // Config is frame furniture too. It used to be fetched only on the
    // birth transition and on entering Settings, so a page reload into
    // the default chat view left S.config null — the substrate control
    // then rendered with nothing selected and the wrong model list,
    // because resolvedSubstrate() had no config to read (reported live,
    // 2026-08-23: 'page reload seems to break Chat UI').
    query('config');
    renderPresence();
    go(S.view); // a view restored before the socket opened never fetched — fetch now
  };
  ws.onclose = () => {
    S.connected = false;
    /* R74: when the server requires an access token, ask ONCE per page
       load — before the reconnect timer, so the retry carries the
       cookie. The flag rides as a data attribute on <head>: its first
       carrier was an inline <script>, which uiCSP (script-src 'self')
       refused to execute, so the prompt never fired in any enforcing
       engine (D75) — the byte-grep test could not see that.
       Ask in two cases: this browser holds no token, or it holds one
       the server keeps refusing — the socket has never once opened
       this page load, so the stored value is stale (a re-minted
       config, a mistyped paste) and silence here is a forever
       reconnect loop (D76). A new entry overwrites the stale cookie;
       cancel keeps it (the failure may be the server restarting, not
       the token). */
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
    /* EVERY correlated wait dies with the socket that carried it: the
       answer wears a request id only THAT socket can deliver. This swept
       four named pendings and missed the create, which left the button
       inert until a page reload — so it now sweeps the slots themselves
       and a new one cannot be left out. The surfaces above have already
       said what the loss means to them; this is the backstop. */
    dropAllPending();
    renderPresence();
    if (S.identityExists) sysLine('connection lost — reconnecting…');
    scheduleReconnect();
  };
  ws.onmessage = onMessage;
}
/* WAKE, don't poll: a tab at the backoff cap is dead to the user for
   up to 10s after the server returns — too long to feel alive. The
   `online` event says the network came back; `visibilitychange` says a hidden tab is now
   being looked at. Both are free, event-driven, and fire an
   immediate reconnect attempt with backoff state preserved (a wrong
   guess still costs one failed dial, not a poll loop). This closes
   the residual recorded in §8.10. */
export function wake() {
  if (S.reconnectTimer) { clearTimeout(S.reconnectTimer); S.reconnectTimer = null; }
  connect(); // connect() refuses while a socket is open or a dial is in flight
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
/* THE ONE BINARY SEND, and it stays in this module for the same reason
   every other send does: the socket is frame-owned and no other module
   may touch it. voice.js is handed this and knows nothing else about the
   connection. A frame dropped because the socket is down is silent on
   purpose — the mic is already disabled when disconnected, so reaching
   here at all means the socket died mid-utterance, and the words are
   gone either way. */
function sendVoiceFrame(buf) {
  if (!ws || ws.readyState !== 1) return;
  S.voiceSpeak = true; // asked aloud; answer aloud
  ws.send(buf);
}
bindTransport(sendVoiceFrame);
// The ONLY readiness surface other modules get — the raw `ws` never
// leaves this module (the future frame-owned socket, UI_FRAME.md §4).
export function wsReady() { return !!(ws && ws.readyState === 1); }

/* --- inbound ----------------------------------------------- */
function onMessage(e) {
  let msg; try { msg = JSON.parse(e.data); } catch (err) { return; }
  // R66 UP2: every inbound message is offered to the section bridge —
  // the dispatch cases below ARE the topic sources, and the bridge
  // relays only each section's declared+subscribed projections
  // (bridge.js topicOf owns the mapping).
  publishToSections(msg);
  switch (msg.type) {
    case 'sections': onSections(msg.sections || [], msg.message || ''); break;
    case 'layout': onLayout(msg.layout || null); break;
    // ORDER IS LOAD-BEARING: theme.js must apply the tokens to :root
    // BEFORE sections re-collect, or every mounted section is handed
    // the palette it already had. Apply, then propagate.
    case 'theme': onTheme(msg.theme || null); onTokensChanged(); break;
    case 'status': onStats(msg.stats); renderPanel(); renderVoice(); break;
    case 'continuity': S.cont = msg.continuity || null; renderPresence(); renderPanel(); if (S.view === 'home') renderHome(); if (S.view === 'identity') renderIdentity(); break;
    case 'response':
      if (msg.stream) { setThinking(true); break; }
      setThinking(false);
  renderSteering([]);
      /* The SPEAKER arrives as a key on the frame, and there is NO
         default: only an explicit 'identity' renders in the identity's
         voice. Substrate text therefore cannot land in the identity's
         bubble no matter what composed it — which is what the old
         branch did by inferring the speaker from identityExists, so the
         firstboot pointer spoke as the identity and the real birth
         greeting arrived bylined "(not born)" (review 2026-08-20).
         The firstboot->live transition is onStats' job now: the server
         sends status ahead of the greeting, so the name and the form
         are already right when the words land. */
      if (msg.role === 'identity') {
        addMsg('identity', msg.message);
        speak(msg.message);
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
    case 'identity': S.identity = msg.identity || null; if (S.view === 'memory') renderMemory(); if (S.view === 'identity') renderIdentity(); break;
    case 'tools': S.tools = msg.tools || []; if (S.view === 'settings') renderSettings(); break;
    case 'sandbox': S.sandbox = msg.sandbox || null; if (S.view === 'settings') renderSettings(); break;
    case 'work': S.work = msg.work || null; renderWorkPill(); renderPanel(); if (S.view === 'home') renderHome(); break;
    // A payload-less workspace message must not take the dispatch down
    // with it: reading .project off null throws, and the throw is
    // silent to the operator. Guard the whole chain, keep the stale
    // snapshot un-rendered by clearing workspaceFor.
    case 'workspace': S.workspace = msg.workspace || null; S.workspaceFor = (S.workspace && S.workspace.project) ? S.workspace.project.id : ''; if (S.view === 'projects') renderProjects(); break;
    case 'overlays': S.overlays = msg.overlays || []; renderPanel(); break; // W2: audit readback card — render-only; applying audit events would loop (the page already holds current bytes)
    case 'overlay_changed': onOverlayChanged(msg.token, msg.paths); break; // W3: live invalidation — fresh token + changed paths, the only reload trigger
    case 'config':
      S.config = msg.config || null;
      acceptSubstrateConfig(msg.request_id); acceptSettingsConfig(msg.request_id);
      renderChatSubstrate(); if (S.view === 'settings') renderSettings(); if (S.view === 'plugins') renderPlugins(); break;
    case 'logs':
      /* Tail and list arrive on the same type; which one a message
         carries is what the operator just asked for. */
      if (msg.logs_tail) { S.logTail = msg.logs_tail; }
      else { S.logsList = msg.logs_list || []; S.logFile = ''; S.logTail = null; }
      if (S.view === 'settings') renderSettings(); break;
    case 'projects': {
      const prevActive = S.activeProject ? S.activeProject.id : '';
      const primed = S.projectsPrimed; S.projectsPrimed = true;
      /* Identify a just-created project as the id that was not here a
         moment ago — no name matching (two projects may share a name)
         and no slug arithmetic duplicating the server's id rule. If the
         count of new ids is anything but exactly one, the answer is
         ambiguous and the view does not move: a guess would put the
         operator on a page they did not ask for. pendingCreate holds
         OUR create's request id and only the answer wearing it clears
         it — an unrelated broadcast landing first must not adopt
         whatever id it happens to carry, and a refused create disarms
         through the correlated error instead (rejectCreate) (D72). */
      const prevIDs = S.projects.map(p => p.id);
      S.projects = msg.projects || [];
      const made = acceptCreate(msg.request_id, prevIDs);
      if (made) S.viewedProject = made;
      S.activeProject = S.projects.find(p => p.active) || null;
      const nowActive = S.activeProject ? S.activeProject.id : '';
      if (primed && nowActive && nowActive !== prevActive) {
        /* "focus" meant two unrelated things: which project is active,
           and the manifest's free-text focus note. This line is about
           the first, so it no longer borrows the other one's word. */
        sysLine('now working in “' + (S.activeProject.name) + '” — turns and work sessions are stamped with it');
      }
      if (msg.request_id) {
        /* Our update's answer wears the same request id (b037d80).
           The "saved" note fires ONLY on this ack — the one moment the
           server has confirmed the bytes it holds. The draft is
           dropped only here, never client-side optimism. */
        if (acceptFocusSave(msg.request_id)) {
          S.focusDraft = null;
          const n = $('focus-note');
          if (n) { n.classList.add('show'); n.dataset.waiting = ''; setTimeout(() => n.classList.remove('show'), 1400); }
        }
      }
      renderProjPill();
      /* The panel's WORK card shows the active project. Without this,
         the card renders whatever project was active at the last
         stats/work message — stale by exactly one focus change. */
      renderPanel();
      if (S.view === 'projects') renderProjects();
      if (S.view === 'home') renderHome();
      break;
    }
    case 'providers': S.providers = msg.providers || []; acceptProviderSave(msg.request_id); S.providersLoaded = true; renderProviderOptions(); renderChatSubstrate(); if (S.view === 'settings') renderSettings(); break;
    case 'models': {
      if (!acceptDiscoveryResponse(msg.request_id, msg.provider)) break;
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
  const born = !!(stats && stats.name && stats.name !== '(not born)');
  const was = S.identityExists;
  S.identityExists = born;
  if (born && !was) { query('projects'); query('work'); query('providers'); query('config'); }
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
    if (text === 'not available') return; // mode artifact (a LIVE-only query in FIRSTBOOT), never birth guidance
    if (text.indexOf('API key') !== -1) { fbHint(text); return; }
    if (text.indexOf('list models') !== -1) { fbHint('Enter an API key, then re-select the provider to discover models.'); return; }
    fbResult('err', text);
    $('fb-birth').disabled = false;
    return;
  }
  if (rejectProviderSave(text, requestID) || rejectSubstrateConfig(text, requestID) || rejectSettingsConfig(text, requestID) || rejectCreate(requestID)) { toast(text); return; }
  if (S.view === 'chat') sysLine('[error] ' + text); else toast(text);
}
