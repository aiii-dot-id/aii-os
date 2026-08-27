/* views/chat.js — the conversation surface: thread rendering
   (operator/identity/system messages, tool events, history replay,
   thinking dots) and the composer. R66: frame, not a section —
   conversation is frame (UI_FRAME.md §6), so this module stays in
   the binary when the other views ride out as section packages. */
import { S } from '../state.js';
import { $, esc } from '../util.js';
import { send, wsReady } from '../ws.js';
import { setThinking, toolPulse } from '../presence.js';
import { toast } from '../app.js';
import { fillModelPicker } from './model-picker.js';
import { pendingSlot } from '../pending.js';

/* The steering queue: operator words accepted into a running turn that
   have NOT yet reached the model. Accepted is not heard — the gap is up
   to one tool call, and if the turn ends first they are heard next turn.
   Showing the queue is what keeps those two states distinguishable; a
   toast that fades leaves the operator guessing which one they are in. */
export function renderSteering(pending) {
  const el = $('steerq');
  if (!el) return;
  if (!pending || !pending.length) { el.className = 'steerq'; el.innerHTML = ''; return; }
  el.className = 'steerq on';
  el.innerHTML = '<div class="steerq-h">' + pending.length +
    (pending.length === 1 ? ' message waiting' : ' messages waiting') +
    ' for the identity\'s next tool call</div>' +
    pending.map(t => '<div class="steerq-item">' + esc(t) + '</div>').join('');
}

/* --- chat --------------------------------------------------- */
export function addMsg(role, text, whoNote) {
  if (!text) return;
  const d = document.createElement('div');
  d.className = 'msg ' + role;
  const who = role === 'identity' ? (S.stats ? S.stats.name : 'identity') : role === 'operator' ? 'you' : '';
  d.innerHTML = (who ? '<div class="who">' + esc(who) + (whoNote ? ' · ' + esc(whoNote) : '') + '</div>' : '') +
    '<div class="body">' + esc(text) + '</div>';
  $('thread-inner').appendChild(d);
  scrollThread();
}
export function sysLine(text) {
  const d = document.createElement('div');
  d.className = 'msg system';
  d.innerHTML = '<div class="body">' + esc(text) + '</div>';
  $('thread-inner').appendChild(d);
  scrollThread();
}
function toolEventEl(summary, bodyText) {
  const det = document.createElement('details');
  det.className = 'tool-ev';
  const s = document.createElement('summary');
  s.textContent = summary;
  det.appendChild(s);
  if (bodyText) {
    const pre = document.createElement('pre');
    pre.textContent = bodyText;
    det.appendChild(pre);
  }
  return det;
}
// Reasoning is shown COLLAPSED and labelled as reasoning. It is not the
// identity speaking — it is the substrate's account of how it got there,
// and rendering it as speech would put words in the resident's mouth.
export function thinkingEvent(text) {
  toolPulse();
  const el = toolEventEl('◇ thinking', text || '');
  el.classList.add('thinking-ev');
  $('thread-inner').appendChild(el);
  scrollThread();
}
export function toolEventLive(name, args) {
  toolPulse();
  const a = args && args.length > 90 ? args.slice(0, 87) + '…' : (args || '');
  $('thread-inner').appendChild(toolEventEl('→ ' + name + '(' + a + ')', null));
  scrollThread();
}
function addHistoryTurn(t) {
  const c = t.content || '';
  if (t.role === 'system' && c.indexOf('→ ') === 0) {
    const nl = c.indexOf('\n');
    const head = nl > 0 ? c.slice(2, nl) : c.slice(2);
    const body = nl > 0 ? c.slice(nl + 1).replace(/^← /, '') : '';
    $('thread-inner').appendChild(toolEventEl('→ ' + head, body));
    return;
  }
  if (t.role === 'system') { sysLine(c); return; }
  addMsg(t.role === 'resident' ? 'identity' : 'operator', c);
}
let thinkingEl = null;
export function toggleThinkingDots(on) {
  if (on && !thinkingEl) {
    thinkingEl = document.createElement('div');
    thinkingEl.className = 'msg identity';
    thinkingEl.innerHTML = '<div class="thinking"><i></i><i></i><i></i></div>';
    $('thread-inner').appendChild(thinkingEl);
    scrollThread();
  } else if (!on && thinkingEl) { thinkingEl.remove(); thinkingEl = null; }
}
// History replay owns the thread wholesale: clear, drop any thinking
// bubble, re-add every stored turn. (Extracted from the socket's
// dispatch so thinkingEl stays module-local — UP1.)
export function renderHistory(history) {
  $('thread-inner').innerHTML = '';
  thinkingEl = null;
  (history || []).forEach(t => addHistoryTurn(t));
  scrollThread(true);
}
export function scrollThread(force) {
  const t = $('thread');
  const nearBottom = t.scrollHeight - t.scrollTop - t.clientHeight < 140;
  if (force || nearBottom) t.scrollTop = t.scrollHeight;
}
export function sendChat(text) {
  text = (text || '').trim();
  if (!text) return;
  if (!wsReady()) { toast('not connected — reconnecting'); return; }
  S.voiceSpeak = false; // typed, so answered in text
  addMsg('operator', text);
  send({ type: 'chat', message: text });
  setThinking(true);
}
const input = $('msg-input');
input.addEventListener('keydown', e => {
  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); sendChat(input.value); input.value = ''; autosize(); }
});
input.addEventListener('input', autosize);
function autosize() { input.style.height = 'auto'; input.style.height = Math.min(input.scrollHeight, 160) + 'px'; }
$('send-btn').onclick = () => {
  // What the control says is what it does. While a turn runs it reads
  // "Stop this turn", so it stops the turn — anything else would be a
  // label the operator has to learn to discount.
  if (S.thinking) { send({ type: 'cancel' }); return; }
  sendChat(input.value); input.value = ''; autosize();
};


/* Provider and model are separate, with models scoped to one provider.
   Use is transactional: the current client stays live until real inference
   proves the candidate. */
const substrate = pendingSlot(); // the candidate awaiting real inference
let substrateResult = null;

function resolvedSubstrate() {
  const l = S.config && S.config.llm;
  if (!l) return { provider: '', model: '' };
  return {
    provider: l.resolved_provider || l.provider || '',
    model: l.resolved_model || l.model || '',
  };
}

function substrateCandidates() {
  // Catalogue status is discovery evidence, not inference authority. A
  // private endpoint or alias may answer chat without exposing /models;
  // the real-inference check on Use is the gate.
  return S.providers || [];
}

function fillModelList(providerName, preferred) {
  const model = document.getElementById('chat-model');
  if (!model) return;
  const p = substrateCandidates().find(x => x.name === providerName);
  fillModelPicker(model, p, preferred);
}

export function renderChatSubstrate() {
  const wrap = document.getElementById('chat-substrate-wrap');
  const provider = document.getElementById('chat-provider');
  const model = document.getElementById('chat-model');
  const apply = document.getElementById('chat-substrate-apply');
  const status = document.getElementById('chat-substrate-status');
  if (!wrap || !provider || !model || !apply || !status) return;
  const candidates = substrateCandidates();
  if (!S.identityExists || !candidates.length) { wrap.style.display = 'none'; return; }
  wrap.style.display = '';
  const cur = resolvedSubstrate();
  const shown = substrate.waiting() || cur;
  provider.innerHTML = candidates.map(p => '<option value="' + esc(p.name) + '"' +
    (p.name === shown.provider ? ' selected' : '') + '>' + esc(p.name) + '</option>').join('');
  fillModelList(shown.provider || provider.value, shown.model);
  provider.disabled = model.disabled = apply.disabled = !!substrate.waiting();
  status.className = 'substrate-status' + (substrateResult ? ' ' + substrateResult.kind : '');
  status.textContent = substrate.waiting()
    ? 'Checking real inference — the current provider remains active.'
    : (substrateResult ? substrateResult.text : '');
  provider.onchange = () => {
    substrateResult = null;
    fillModelList(provider.value, null);
    status.textContent = '';
  };
  apply.onclick = () => {
    const target = { provider: provider.value, model: model.value.trim() };
    if (!target.provider || !target.model) { toast('Choose a provider and model.'); return; }
    if (target.provider === cur.provider && target.model === cur.model) {
      substrateResult = { kind: 'good', text: 'Already active.' };
      renderChatSubstrate();
      return;
    }
    substrateResult = null;
    const requestID = send({ type: 'config_set', config: { 'llm.provider': target.provider, 'llm.model': target.model } });
    if (!substrate.arm(target, requestID)) {
      substrateResult = { kind: 'bad', text: 'Not connected — current provider unchanged.' };
    }
    renderChatSubstrate();
  };
}

export function substrateConnectionLost() {
  if (!substrate.drop()) return false;
  substrateResult = { kind: 'bad', text: 'Connection lost before confirmation — check the active provider after reconnect.' };
  renderChatSubstrate();
  return true;
}

// A racing config query cannot fake success: the acknowledged resolved
// provider/model must equal the candidate we sent.
export function acceptSubstrateConfig(requestID) {
  // claim() clears: this frame IS the answer, so the wait is over
  // whatever it says (pending.js records the wedge this prevents).
  const want = substrate.claim(requestID);
  if (!want) return false;
  const cur = resolvedSubstrate();
  if (cur.provider !== want.provider || cur.model !== want.model) {
    substrateResult = { kind: 'bad', text: 'Acknowledged, but the active substrate is ' +
      (cur.provider || 'unset') + ' / ' + (cur.model || 'unset') + ' — the change did not take.' };
    return true;
  }
  substrateResult = { kind: 'good', text: 'Active — inference verified.' };
  return true;
}

export function rejectSubstrateConfig(message, requestID) {
  if (!substrate.claim(requestID)) return false;
  substrateResult = { kind: 'bad', text: message };
  renderChatSubstrate();
  return true;
}
