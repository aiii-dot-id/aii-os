
import { S } from '../state.js';
import { $, esc } from '../util.js';
import { send, wsReady } from '../ws.js';
import { setThinking, toolPulse } from '../presence.js';
import { toast } from '../app.js';
import { fillModelPicker } from './model-picker.js';
import { pendingSlot } from '../pending.js';

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

export function addMsg(role, text, whoNote, voiceRef) {
  if (!text) return null;
  const d = document.createElement('div');
  d.className = 'msg ' + role;
  const who = role === 'identity' ? (S.stats ? S.stats.name : 'identity') : role === 'operator' ? 'you' : '';
  d.innerHTML = (who ? '<div class="who">' + esc(who) + (whoNote ? ' · <span class="speaker">' + esc(whoNote) + '</span>' : '') + '</div>' : '') +
    '<div class="body">' + esc(text) + '</div>';
  if (voiceRef) d.dataset.voiceRef = voiceRef;
  $('thread-inner').appendChild(d);
  scrollThread();
  return d;
}
// attachSpeaker puts (or replaces) a speaker attribution on the bubble
// of the spoken final it names (seam 3): "you · Sam", "you · unknown
// speaker", "you · uncertain: Sam 0.62". A late result amends the same
// bubble; a bubble the page never had is left alone.
export function attachSpeaker(voiceRef, attribution) {
  if (!voiceRef || !attribution) return false;
  const d = document.querySelector('[data-voice-ref="' + voiceRef.replace(/"/g, '\\"') + '"]');
  if (!d) return false;
  const who = d.querySelector('.who');
  if (!who) return false;
  let sp = who.querySelector('.speaker');
  if (!sp) {
    who.appendChild(document.createTextNode(' · '));
    sp = document.createElement('span'); sp.className = 'speaker'; who.appendChild(sp);
  }
  sp.textContent = attribution;
  return true;
}
export function sysLine(text) {
  const d = document.createElement('div');
  d.className = 'msg system';
  d.innerHTML = '<div class="body">' + esc(text) + '</div>';
  $('thread-inner').appendChild(d);
  scrollThread();
}

function toolTitle(name) {
  if (!name) return 'tool';
  if (name.indexOf('pl_') === 0) {
    const parts = name.slice(3).split('_');
    if (parts.length > 1) return 'plugin · ' + parts.slice(0, -1).join('.') + ' · ' + parts[parts.length - 1];
    return 'plugin · ' + name.slice(3);
  }
  return name;
}

function prettyArgs(args) {
  if (!args) return '';
  try {
    const o = JSON.parse(args);
    return JSON.stringify(o, null, 2);
  } catch (e) { return args; }
}
function toolEventEl(summaryText, bodyText) {
  const det = document.createElement('details');
  det.className = 'tool-ev';
  const s = document.createElement('summary');

  const chev = document.createElement('span');
  chev.className = 'chev';
  chev.textContent = '▶';
  const tname = document.createElement('span');
  tname.className = 'tname';
  tname.textContent = summaryText;
  const tprev = document.createElement('span');
  tprev.className = 'tprev';
  tprev.textContent = bodyText || '';
  s.appendChild(chev); s.appendChild(tname); s.appendChild(tprev);
  det.appendChild(s);
  if (bodyText) {
    const pre = document.createElement('pre');
    pre.textContent = prettyArgs(bodyText);
    det.appendChild(pre);
  }
  return det;
}

export function thinkingEvent(text) {
  toolPulse();
  const el = toolEventEl('thinking', text || '');
  el.classList.add('thinking-ev');
  $('thread-inner').appendChild(el);
  scrollThread();
}
export function toolEventLive(name, args) {
  toolPulse();
  $('thread-inner').appendChild(toolEventEl(toolTitle(name), args || ''));
  scrollThread();
}
function addHistoryTurn(t) {
  const c = t.content || '';
  if (t.role === 'system' && c.indexOf('→ ') === 0) {

    const nl = c.indexOf('\n');
    const head = nl > 0 ? c.slice(2, nl) : c.slice(2);
    const body = nl > 0 ? c.slice(nl + 1).replace(/^← /, '') : '';
    const open = head.indexOf('(');
    let hname = head, hargs = '';
    if (open > 0 && head.charAt(head.length - 1) === ')') {
      hname = head.slice(0, open);
      hargs = head.slice(open + 1, -1);
    }
    const det = toolEventEl(toolTitle(hname), hargs);
    if (body) {
      const res = document.createElement('pre');
      res.className = 'tres';
      res.textContent = body;
      det.appendChild(res);
    }
    $('thread-inner').appendChild(det);
    return;
  }
  if (t.role === 'system') { sysLine(c); return; }
  addMsg(t.role === 'resident' ? 'identity' : 'operator', c, t.note, t.voice_ref);
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
  S.voiceSpeak = false;
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

  if (S.thinking) { send({ type: 'cancel' }); return; }
  sendChat(input.value); input.value = ''; autosize();
};

const substrate = pendingSlot();
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

export function acceptSubstrateConfig(requestID) {

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

// ── the identity asks; the operator answers ──
//
// A card in the thread, host-authored from the typed ask. The answer is
// sent as {type:'ask'} and becomes one operator turn marked [ask <id>];
// a plugin's confirm card answers through the plugins route, exactly as
// the Plugins page does. No card ever carries a credential field: connect
// points at the Plugins page, where granting and credentials stay the
// operator's own acts.
const askCards = new Map();
export function renderAsks(asks) {
  const inner = $('thread-inner');
  if (!inner) return;
  const seen = new Set();
  (asks || []).forEach(a => {
    seen.add(a.id);
    if (askCards.has(a.id)) return;
    const d = document.createElement('div');
    d.className = 'msg ask ask-' + a.kind;
    d.dataset.askId = a.id;
    d.innerHTML = askCardHTML(a);
    wireAskCard(d, a);
    inner.appendChild(d);
    askCards.set(a.id, d);
  });
  askCards.forEach((d, id) => {
    if (seen.has(id)) return;
    d.classList.add('closed');
    d.querySelectorAll('button, input').forEach(b => { b.disabled = true; });
    const note = d.querySelector('.ask-note');
    if (note && !note.textContent) note.textContent = 'closed';
    askCards.delete(id);
  });
  scrollThread();
}
function askCardHTML(a) {
  const who = a.kind === 'confirm' ? esc(a.from) : (S.stats ? esc(S.stats.name) : 'identity');
  let head = '', body = '', bar = '';
  switch (a.kind) {
    case 'confirm':
      head = who + ' asks you to confirm';
      body = '<div class="ask-text"><b>' + esc(a.text || a.operation) + '</b> <span class="store-hint">' + esc(a.operation || '') + (a.effects ? ' · ' + esc(a.effects) : '') + '</span></div>' +
        Object.keys(a.args || {}).sort().map(k => '<div class="act-arg"><span>' + esc(k) + '</span><b>' + esc(typeof a.args[k] === 'string' ? a.args[k] : JSON.stringify(a.args[k])) + '</b></div>').join('') +
        '<div class="store-hint">runs once, with exactly these arguments, when you confirm; Always runs it now and every time from now on, until you revoke it on the Plugins page' + (a.expires ? ' · expires ' + esc(a.expires) : '') + '</div>';
      bar = '<button class="btn" data-ask-act="confirm">Confirm</button><button class="btn" data-ask-act="always">Always</button><button class="btn ghost" data-ask-act="deny">Deny</button>';
      break;
    case 'choose':
      head = who + ' asks you to choose';
      body = '<div class="ask-text">' + esc(a.text) + '</div>';
      bar = (a.choices || []).map(c => '<button class="btn" data-ask-choice="' + esc(c) + '">' + esc(c) + '</button>').join('') +
        '<button class="btn ghost" data-ask-else="1">Something else…</button><button class="btn ghost" data-ask-answer="not_now">Not now</button>';
      break;
    case 'connect':
      head = who + ' asks to connect ' + esc(a.connector || 'something');
      body = '<div class="ask-text">' + esc(a.text) + '</div><div class="store-hint">You set it up on the Plugins page: installing, granting and any credential stay yours. Read only lets it look; read and modify lets it act.</div>';
      bar = '<button class="btn" data-ask-connect="read">Connect, read only</button><button class="btn" data-ask-connect="modify">Connect, read and modify</button><button class="btn ghost" data-ask-answer="not_now">Not now</button>';
      break;
    default: // clarify
      head = who + ' asks';
      body = '<div class="ask-text">' + esc(a.text) + '</div>';
      bar = '<div class="ask-reply"><input type="text" class="ask-input" placeholder="Your answer" maxlength="2000"><button class="btn" data-ask-say="1">Send</button></div><button class="btn ghost" data-ask-answer="not_now">Not now</button><button class="btn ghost" data-ask-answer="no">No</button>';
  }
  return '<div class="who">' + head + '</div><div class="body">' + body + '<div class="savebar ask-bar">' + bar + '</div><div class="ask-note"></div></div>';
}
function wireAskCard(d, a) {
  const settle = (note) => {
    d.querySelectorAll('button, input').forEach(b => { b.disabled = true; });
    d.querySelector('.ask-note').textContent = note;
  };
  const answer = (ans) => { send({ type: 'ask', ask: Object.assign({ id: a.id }, ans) }); };
  d.querySelectorAll('[data-ask-act]').forEach(b => { b.onclick = () => {
    const act = b.dataset.askAct;
    settle(act === 'confirm' ? 'Running…' : act === 'always' ? 'Running… and from now on without asking (revoke on the Plugins page)' : 'Dropped');
    send({ type: 'plugin', plugin: { action: act, id: a.plugin, act: a.id } });
  }; });
  d.querySelectorAll('[data-ask-choice]').forEach(b => { b.onclick = () => { settle('You chose: ' + b.dataset.askChoice); answer({ answer: 'chose', choice: b.dataset.askChoice }); }; });
  d.querySelectorAll('[data-ask-connect]').forEach(b => { b.onclick = () => {
    settle('Set it up on the Plugins page');
    answer({ answer: 'connect', scope: b.dataset.askConnect });
    // The card lands on the Plugins page with the connector and the
    // chosen scope: the New profile form opens prefilled from them,
    // and the operator finishes there.
    S.connectRequest = { connector: a.connector || '', scope: b.dataset.askConnect };
    if (S.go) S.go('plugins');
  }; });
  d.querySelectorAll('[data-ask-answer]').forEach(b => { b.onclick = () => { settle(b.dataset.askAnswer === 'no' ? 'You said no' : 'Not now'); answer({ answer: b.dataset.askAnswer }); }; });
  const els = d.querySelector('[data-ask-else]');
  if (els) els.onclick = () => {
    const bar = d.querySelector('.ask-bar');
    bar.innerHTML = '<div class="ask-reply"><input type="text" class="ask-input" placeholder="Your answer" maxlength="2000"><button class="btn" data-ask-say="1">Send</button></div><button class="btn ghost" data-ask-answer="not_now">Not now</button>';
    wireAskCard(d, a);
    const inp = d.querySelector('.ask-input'); if (inp) inp.focus();
  };
  d.querySelectorAll('[data-ask-say]').forEach(b => { b.onclick = () => {
    const inp = d.querySelector('.ask-input'); const text = (inp && inp.value || '').trim();
    if (!text) { if (inp) inp.focus(); return; }
    settle('You said: ' + text); answer({ answer: 'said', text });
  }; });
  const inp = d.querySelector('.ask-input');
  if (inp) inp.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); const b = d.querySelector('[data-ask-say]'); if (b) b.onclick(); } });
}
