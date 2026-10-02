
import { S } from '../state.js';
import { $, copyText, esc } from '../util.js';
import { send, wsReady } from '../ws.js';
import { setThinking, toolPulse } from '../presence.js';
import { toast } from '../app.js';
import { announce, announceWords } from '../announce.js';
import { fillModelPicker } from './model-picker.js';
import { pendingSlot } from '../pending.js';
import { displayInteractions, foldTurns, locationKey } from '../interaction-display.js';

let thinkingEl = null;

function appendThread(el) {
  const t = $('thread'), inner = $('thread-inner');
  const follow = t && following;
  if (thinkingEl && thinkingEl.isConnected && el !== thinkingEl) inner.insertBefore(el, thinkingEl);
  else inner.appendChild(el);
  if (follow && t) positionThread(t, t.scrollHeight);
  S.interactionPositioned?.();
  renderJump();
}

export function renderSteering(pending) {
  const el = $('steerq');
  if (!el) return;
  if (!pending || !pending.length) { el.className = 'steerq'; el.innerHTML = ''; return; }
  el.className = 'steerq on';
  const title = pending.length + (pending.length === 1 ? ' message waiting' : ' messages waiting') + ' for the next tool call';
  let details = el.querySelector('details');
  if (!details) { details = document.createElement('details'); details.append(document.createElement('summary'), document.createElement('div')); el.appendChild(details); }
  setText(details.firstElementChild, title);
  setText(details.lastElementChild, pending.join('\n'));

}

const COPY_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><rect x="5.5" y="5.5" width="8" height="8" rx="1.8"/><path d="M10.5 3.5v-.3A1.7 1.7 0 0 0 8.8 1.5H4.2a1.7 1.7 0 0 0-1.7 1.7v4.6a1.7 1.7 0 0 0 1.7 1.7h.3"/></svg>';
const COPIED_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="m3.5 8.5 3 3 6-7"/></svg>';
function copyButton(text) {
  const b = document.createElement('button');
  b.type = 'button'; b.className = 'copyb'; b.title = 'Copy'; b.setAttribute('aria-label', 'Copy message');
  b.innerHTML = COPY_ICON; b.dataset.copyText = text;
  b.onclick = async () => {
    if (!(await copyText(b.dataset.copyText))) { toast('Could not copy \u2014 the browser refused the clipboard.'); return; }
    b.innerHTML = COPIED_ICON;
    setTimeout(() => { b.innerHTML = COPY_ICON; }, 1200);
  };
  return b;
}

function messageEl(role, text, whoNote, voiceRef) {
  if (!text) return null;
  const d = document.createElement('div');
  d.className = 'msg ' + role;
  const who = role === 'identity' ? (S.stats ? S.stats.name : 'identity') : role === 'operator' ? 'you' : role === 'participant' ? 'participant' : '';
  d.innerHTML = (who ? '<div class="who">' + esc(who) + (whoNote ? ' · <span class="speaker">' + esc(whoNote) + '</span>' : '') + '</div>' : '') +
    '<div class="body">' + esc(text) + '</div>';
  if (role === 'identity' || role === 'operator') {
    const acts = document.createElement('div');
    acts.className = 'msg-acts';
    acts.appendChild(copyButton(text));
    d.appendChild(acts);
  }
  if (voiceRef) d.dataset.voiceRef = voiceRef;
  return d;
}
let liveNotice = '', liveError = '', liveNoticeRef = null;
export function sysLine(text, important = false, ref = null) {
  if (!text) return null;
  if (important) liveError = text; else { liveNotice = text; liveNoticeRef = ref; }
  renderNotices();
  return null;
}
function renderNotices() {
  if (document.visibilityState === 'hidden') return;
  const host = chatSurface('chat-notices');
  for (const [id, text, important] of [['chat-notice', liveNotice, false], ['chat-error', liveError, true]]) {
    let el = $(id);
    if (!text) { el?.remove(); continue; }
    if (!el) {
      el = document.createElement('div'); el.id = id; el.className = 'chat-notice' + (important ? ' error' : '');
      const body = document.createElement('span'); if (important) body.setAttribute('role', 'alert');
      const close = document.createElement('button'); close.type = 'button'; close.textContent = 'Dismiss';
      close.onclick = () => { if (important) liveError = ''; else liveNotice = ''; renderNotices(); };
      el.append(body, close); host.appendChild(el);
    }
    if (!important && el.firstElementChild.textContent !== text) announce(text);
    setText(el.firstElementChild, text);
    let route = el.querySelector('.notice-route');
    if (!important && liveNoticeRef) {
      if (!route) { route = document.createElement('button'); route.type = 'button'; route.className = 'notice-route'; route.textContent = 'Latest'; el.appendChild(route); }
      route.onclick = () => viewReference(liveNoticeRef);
    } else route?.remove();
  }
}
export function thinkingEvent() {
  if (document.visibilityState !== 'hidden') toolPulse();
}
export function toolEventLive() {
  if (document.visibilityState !== 'hidden') toolPulse();
}
function thinkingMarker() {
  if (!thinkingEl) {
    thinkingEl = document.createElement('div');
    thinkingEl.className = 'msg identity thinking-row';
    thinkingEl.innerHTML = '<div class="thinking"><i></i><i></i><i></i></div>';
  }
  return thinkingEl;
}
export function toggleThinkingDots(on) {
  if (document.visibilityState === 'hidden') return;
  renderNotices();
  if (on) {
    const marker = thinkingMarker();
    if ($('thread-inner').lastElementChild !== marker) appendThread(marker);
  } else if (thinkingEl) { thinkingEl.remove(); thinkingEl = null; }
  if (liveReceipt && liveReceipt.el.isConnected) markReceipt(liveReceipt.el, liveReceipt.item, !!on);
}

let following = true;
function atBottom(t) { return t.scrollHeight - t.scrollTop - t.clientHeight <= 2; }
function positionThread(t, top) {
  const target = Math.max(0, Math.min(top, t.scrollHeight - t.clientHeight));
  if (Math.abs(t.scrollTop - target) > 0.5) t.scrollTop = target;
  S.interactionPositioned?.();
}
export function scrollThread(force) {
  const t = $('thread');
  if (force) { following = true; S.cancelReadingRestore?.(); }
  if (following) positionThread(t, t.scrollHeight);
  renderJump();
}
function renderJump() {
  const t = $('thread'), j = $('jump-latest');
  if (t && j) j.hidden = following && !historyEarlier;
}
{
  const t = $('thread'), j = $('jump-latest');
  let lastTop = t?.scrollTop || 0, directionFromInput = 0, dragging = false, touchY = null;
  const move = direction => {
    if (!direction || !t || document.visibilityState === 'hidden') return;
    S.cancelReadingRestore?.();
    if (direction < 0) { following = false; S.interactionHold?.(); }
    else if (atBottom(t)) following = true;
    renderJump();
    const viewport = t.getBoundingClientRect(), older = direction < 0;
    for (const source of ['recorded','transient']) {
      const page = currentPages.get(source);
      if (!(older ? page?.has_older : page?.has_newer) || !page.rows.length) continue;
      const row = older ? page.rows[0] : page.rows.at(-1);
      const key = CSS.escape(locationKey(source, page, row.id));
      const record = t.querySelector('[data-record-key="' + key + '"]')?.closest('.interaction-item');
      const edge = record?.closest('.turn-receipt') || record;
      if (!edge) continue;
      const rect = edge.getBoundingClientRect();
      if (older ? rect.top >= viewport.top - 140 && rect.top <= viewport.top + 140 :
          rect.bottom <= viewport.bottom + 140 && rect.bottom > viewport.top) {
        currentNavigate?.(source, older ? 'older' : 'newer'); break;
      }
    }
    S.interactionScroll?.();
  };
  if (t) {
    t.addEventListener('wheel', e => { directionFromInput = Math.sign(e.deltaY); move(directionFromInput); }, { passive: true });
    t.addEventListener('touchstart', e => { directionFromInput = 0; touchY = e.touches[0]?.clientY; }, { passive: true });
    t.addEventListener('touchmove', e => {
      const y = e.touches[0]?.clientY;
      if (touchY != null && y != null) { directionFromInput = Math.sign(touchY-y); move(directionFromInput); } touchY = y;
    }, { passive: true });
    t.addEventListener('pointerdown', () => { dragging = true; }, { passive: true });
    t.addEventListener('click', e => {
      const summary = e.target.closest('summary');
      if (summary && !summary.parentElement.open) { following = false; S.interactionHold?.(); renderJump(); }
    });
    for (const event of ['pointerup', 'pointercancel']) window.addEventListener(event, () => { dragging = false; }, { passive: true });
    t.addEventListener('scrollend', () => { directionFromInput = 0; }, { passive: true });
    t.addEventListener('keydown', e => {
      const space = e.key === ' ' && !e.target.closest('button,summary');
      if (!e.target.closest('input,textarea,select,[contenteditable]') && (space || ['ArrowUp','PageUp','Home','ArrowDown','PageDown','End'].includes(e.key))) {
        directionFromInput = space ? (e.shiftKey ? -1 : 1) : ['ArrowUp','PageUp','Home'].includes(e.key) ? -1 : 1; move(directionFromInput);
      }
    });
    t.addEventListener('scroll', () => {
      const direction = Math.sign(t.scrollTop-lastTop); lastTop = t.scrollTop; renderJump();
      if (dragging || direction && direction === directionFromInput) move(direction);
    }, { passive: true });
    S.interactionPositioned = () => { lastTop = t.scrollTop; };
  }
  if (j) j.onclick = () => {
    S.cancelReadingRestore?.();
    if (S.interactionLatest) S.interactionLatest();
    else {
      following = true;
      const still = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
      t.scrollTo({ top: t.scrollHeight, behavior: still ? 'auto' : 'smooth' });
    }
  };
}
S.interactionFollowing = () => following;
S.interactionVisible = source => {
  const t = $('thread'), page = currentPages.get(source);
  if (!t || !page?.rows.length) return null;
  const box = t.getBoundingClientRect(), ids = new Set();
  for (const item of t.querySelectorAll('.interaction-item')) {
    const rect = item.getBoundingClientRect(); if (rect.bottom <= box.top || rect.top >= box.bottom) continue;
    for (const record of item.querySelectorAll('[data-record-key]')) {
      const [identity, origin, incarnation, id] = JSON.parse(record.dataset.recordKey);
      if (origin === source && identity === page.identity && incarnation === page.incarnation) ids.add(id);
    }
  }
  const rows = page.rows.filter(r => ids.has(r.id));
  return rows.length ? { anchor_id: rows[0].id, end_id: rows.at(-1).id } : null;
};
export function sendChat(text) {
  text = (text || '').trim();
  if (!text) return false;
  if (!wsReady()) { toast('not connected — reconnecting'); return false; }
  if (pendingMessages.size >= 20) { toast('Resolve or dismiss unconfirmed messages before sending more.'); return false; }
  S.voiceSpeak = false;
  const request = send({ type: 'chat', message: text });
  if (!request) return false;
  showPendingMessage(request, text);
  operatorTurnBegins();
  setThinking(true);
  return true;
}
export function operatorTurnBegins() { liveReceipt = null; }
S.operatorTurnBegins = operatorTurnBegins;
export function composing(e) { return !!(e.isComposing || e.keyCode === 229); }
const input = $('msg-input');
input.addEventListener('keydown', e => {
  if (composing(e)) return;
  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); if (sendChat(input.value)) { input.value = ''; input.dispatchEvent(new Event('input', { bubbles: true })); } }
});
input.addEventListener('input', autosize);
function autosize() { input.style.height = 'auto'; input.style.height = Math.min(input.scrollHeight, 160) + 'px'; }
$('send-btn').onclick = () => {
  if (S.thinking) { send({ type: 'cancel' }); return; }
  if (sendChat(input.value)) { input.value = ''; input.dispatchEvent(new Event('input', { bubbles: true })); }
};

export function renderComposer() {
  const name = S.identityExists && S.stats ? S.stats.name : '';
  input.placeholder = name && name !== 'Unnamed' ? 'Message ' + name : 'Message your identity';
  renderComposerEffort();
}
const EFFORT_NAMES = { '': 'Default', none: 'None', minimal: 'Minimal', low: 'Low', medium: 'Medium', high: 'High', xhigh: 'Extra High', max: 'Max' };
function effortName(level) {
  if (Object.prototype.hasOwnProperty.call(EFFORT_NAMES, level)) return EFFORT_NAMES[level];
  return level.charAt(0).toUpperCase() + level.slice(1);
}
function renderComposerEffort() {
  const wrap = $('effort'), val = $('effort-val'), menu = $('effort-menu');
  if (!wrap || !val || !menu) return;
  const d = S.config && S.config.llm && S.config.llm.effort_choice;
  wrap.hidden = !(S.identityExists && d && d.levels && d.levels.length);
  if (wrap.hidden) { closeEffortMenu(); return; }
  const cur = d.in_force || '';
  val.textContent = effortName(cur);
  const checked = d.checked || [];
  const key = d.levels.join('\n') + '\n=' + cur + '\n?' + checked.join(',');
  if (menu.dataset.key === key) return;
  menu.dataset.key = key;
  menu.innerHTML = [''].concat(d.levels).map(l =>
    '<button type="button" role="menuitemradio" aria-checked="' + (l === cur) + '" data-level="' + esc(l) + '"' +
    (checked.includes(l) ? ' data-checked title="Checked with the provider before it applies"' : '') + '>' +
    '<span>' + esc(effortName(l)) + '</span><span class="check" aria-hidden="true">\u2713</span></button>').join('');
}
function closeEffortMenu() {
  const menu = $('effort-menu'), btn = $('effort-btn');
  if (menu) menu.hidden = true;
  if (btn) btn.setAttribute('aria-expanded', 'false');
}
{
  const btn = $('effort-btn'), menu = $('effort-menu');
  if (btn && menu) {
    btn.onclick = (e) => {
      e.stopPropagation();
      const open = menu.hidden;
      menu.hidden = !open;
      btn.setAttribute('aria-expanded', open ? 'true' : 'false');
      if (open) { const c = menu.querySelector('[aria-checked="true"]') || menu.querySelector('button'); if (c) c.focus(); }
    };
    menu.onclick = (e) => {
      const b = e.target.closest('button[data-level]');
      if (!b) return;
      closeEffortMenu();
      btn.focus();
      if (!wsReady()) { toast('not connected \u2014 reconnecting'); return; }
      if (b.hasAttribute('data-checked')) toast('Checking ' + effortName(b.dataset.level) + ' with the provider before it applies…');
      send({ type: 'effort_set', effort: b.dataset.level });
    };
    menu.addEventListener('keydown', (e) => {
      if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
      e.preventDefault();
      const items = [...menu.querySelectorAll('button')];
      const next = items[(items.indexOf(document.activeElement) + (e.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length];
      if (next) next.focus();
    });
    document.addEventListener('click', (e) => { if (!e.target.closest('#effort')) closeEffortMenu(); });
    document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && !menu.hidden) { closeEffortMenu(); btn.focus(); } });
  }
}

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
  return (S.providers || []).filter(p => p.chat !== false);
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
    appendThread(d);
    askCards.set(a.id, d);
    announceWords(d.querySelector('.who').textContent, d.querySelector('.ask-text').textContent);
  });
  askCards.forEach((d, id) => {
    if (seen.has(id)) return;
    d.classList.add('closed');
    d.querySelectorAll('button, input').forEach(b => { b.disabled = true; });
    const note = d.querySelector('.ask-note');
    if (note && !note.textContent) note.textContent = 'closed';
    askCards.delete(id);
  });
  S.interactionPositioned?.();
  renderJump();
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
    default:
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
  if (inp) inp.addEventListener('keydown', e => { if (composing(e)) return; if (e.key === 'Enter') { e.preventDefault(); const b = d.querySelector('[data-ask-say]'); if (b) b.onclick(); } });
}

function chatSurface(id) {
  let el = $(id);
  if (!el) {
    el = document.createElement('div'); el.id = id;
    const composer = $('composer-wrap');
    if (composer) composer.prepend(el); else $('thread').after(el);
  }
  return el;
}
function setText(el, text) { if (el && el.textContent !== text) el.textContent = text; }
function setData(el, key, value) { if (el.dataset[key] !== value) el.dataset[key] = value; }
function syncChildren(parent, nodes) {
  let next = parent.firstChild;
  for (const node of nodes) {
    if (node === next) next = next.nextSibling;
    else parent.insertBefore(node, next);
  }
  const keep = new Set(nodes);
  for (const node of Array.from(parent.childNodes)) if (!keep.has(node)) node.remove();
}
const pendingMessages = new Map();
let currentPages = new Map(), historyEarlier = false, currentNavigate = null;
function showPendingMessage(id, text) {
  const el = document.createElement('details'); el.className = 'chat-pending'; el.dataset.requestId = id;
  const title = document.createElement('summary'); title.textContent = 'Sending — awaiting confirmation';
  const body = document.createElement('div'); body.className = 'body'; body.textContent = text;
  const dismiss = document.createElement('button'); dismiss.type = 'button'; dismiss.textContent = 'Dismiss';
  dismiss.onclick = () => { pendingMessages.delete(id); el.remove(); };
  el.append(title, body, copyButton(text), dismiss);
  pendingMessages.set(id, { el, identity: currentPages.get('recorded')?.identity || currentPages.get('transient')?.identity || '', ack: '', receipt: null, lost: false });
  chatSurface('chat-pending').appendChild(el);
}
function pendingText(p, text) {
  const title = p.el.querySelector('summary');
  if (title.textContent !== text) announce(text);
  setText(title, text);
}
function viewReference(ref) {
  const page = currentPages.get(ref.source);
  if (!page || page.identity !== ref.identity || page.incarnation !== ref.incarnation) {
    sysLine('This reference belongs to a different or unavailable history source. Refresh the current view.'); return;
  }
  currentNavigate?.(ref.source, 'latest');
}
S.chatAcknowledged = (id, state) => {
  const p = pendingMessages.get(id); if (!p || p.lost) return;
  if (state !== 'accepted' && state !== 'refused') { pendingText(p, 'Confirmation unavailable — invalid admission reply'); return; }
  if (p.receipt) {
    if (state === 'refused' && p.receipt.error?.code !== 'CHAT_NOT_RECORDED') pendingText(p, 'Conflicting recording confirmations — check history');
    return;
  }
  p.ack = state;
  if (state === 'refused') S.reconcileTurn?.();
  pendingText(p, state === 'accepted' ? 'Accepted — waiting to be recorded' : 'Not accepted — your text is kept here');
};
function receiptKey(r) { return JSON.stringify([r.ref?.identity, r.ref?.source, r.ref?.incarnation, r.ref?.id, r.error?.code, r.error?.message]); }
function validReceipt(r) {
  if (!r || typeof r !== 'object' || (!r.ref && !r.error)) return false;
  const ref = r.ref, err = r.error;
  if (Object.hasOwn(r, 'ref') && !ref || Object.hasOwn(r, 'error') && !err) return false;
  if (ref && (typeof ref !== 'object' || !['identity','incarnation','id'].every(k => typeof ref[k] === 'string' && ref[k].length) || !['recorded','transient'].includes(ref.source))) return false;
  if (err && (typeof err !== 'object' || typeof err.message !== 'string' || !err.message.length || !['CHAT_NOT_RECORDED','CHAT_RECORDING_FAILED'].includes(err.code))) return false;
  return !err || !ref || ref.source === 'transient' && err.code === 'CHAT_RECORDING_FAILED';
}
S.chatReceipt = (id, r) => {
  const p = pendingMessages.get(id); if (!p || p.lost) return;
  if (!validReceipt(r) || r.ref && p.identity && p.identity !== r.ref.identity) {
    pendingText(p, 'Confirmation unavailable — invalid recording reply'); return;
  }
  if (p.receipt) {
    if (receiptKey(p.receipt) !== receiptKey(r)) pendingText(p, 'Conflicting recording confirmations — check history');
    return;
  }
  if (p.ack === 'refused' && r.error?.code !== 'CHAT_NOT_RECORDED') {
    p.receipt = r; pendingText(p, 'Conflicting recording confirmations — check history'); return;
  }
  p.receipt = r;
  if (r.error) {
    if (r.error.code === 'CHAT_NOT_RECORDED') S.reconcileTurn?.();
    pendingText(p, (r.error.code === 'CHAT_NOT_RECORDED' ? 'Not recorded: ' : 'Recording unconfirmed: ') + r.error.message + (r.ref ? ' · transient copy, not saved' : ''));
    const detail = document.createElement('pre'); detail.textContent = JSON.stringify(r, null, 2); p.el.appendChild(detail);
    if (r.ref) {
      const view = document.createElement('button'); view.type = 'button'; view.textContent = 'Latest transient entries'; view.onclick = () => viewReference(r.ref); p.el.appendChild(view);
    }
    return;
  }
  pendingMessages.delete(id); p.el.remove();
  if (r.ref.source === 'transient') {
    sysLine('Not saved — your words are in this session’s transient view.', false, r.ref);
  }
  if (S.interactionChanged) S.interactionChanged();
};
S.chatConnectionLost = () => {
  pendingMessages.forEach(p => {
    p.lost = true;
    if (!p.receipt && p.ack !== 'refused') pendingText(p, 'Connection lost — delivery unconfirmed; your text is kept here');
  });
};

function speakerLabel(p) {
  if (!p) return '';
  if (p.reason === 'speaker_profile_pending') return 'speaker identification pending (awaiting corroborating speech; not authentication)';
  if (p.speaker_uuid) {
    const label = p.display_label ? 'speaker label ' + JSON.stringify(p.display_label) : 'anonymous speaker';
    return label + ' (' + ['speaker_uuid=' + JSON.stringify(p.speaker_uuid), p.continuity && 'continuity=' + p.continuity, p.registry_revision && 'registry_revision=' + p.registry_revision, 'not authentication'].filter(Boolean).join('; ') + ')';
  }
  if (p.decision === 'known') return (p.speaker?.trim() || 'unknown speaker') + (p.speaker_id ? ' (speaker_id=' + JSON.stringify(p.speaker_id) + ')' : '');
  if (p.decision === 'uncertain') {
    if (p.reason === 'enrollment_unavailable') return 'speaker enrollment unavailable';
    if (p.reason === 'no_enrollments') return 'no speaker is enrolled';
    const label = p.speaker || p.reason?.replaceAll('_', ' ') || '';
    return 'uncertain' + (label ? ': ' + label : '') + (p.speaker && typeof p.score === 'number' && p.score > 0 ? ' ' + p.score.toFixed(2) : '');
  }
  return 'unknown speaker';
}
function occurrence(el, entry) {
  const r = entry.row;
  if (!el) {
    el = document.createElement('div'); el.className = 'record-detail'; el.dataset.recordKey = entry.key;
    const body = document.createElement('pre'); body.className = 'record-content';
    const meta = document.createElement('div'); meta.className = 'interaction-meta';
    const context = document.createElement('details'); context.className = 'record-context';
    const title = document.createElement('summary'); title.textContent = 'Recorded context';
    const raw = document.createElement('pre'); context.append(title, raw);
    el.append(body, meta, context);
  }
  setText(el.querySelector('.record-content'), r.content || '');
  setText(el.querySelector('.interaction-meta'), [r.kind, r.recorded_at || r.created_at || 'Time unavailable', entry.source === 'transient' ? 'not saved' : ''].filter(Boolean).join(' · '));
  setText(el.querySelector('.record-context pre'), JSON.stringify(r, null, 2));
  const signature = JSON.stringify(r.content_ref || null);
  if (el.dataset.detailRef !== signature) {
    el.querySelector('.detail-read')?.remove(); el.dataset.detailRef = signature;
    if (r.content_ref) el.appendChild(detailReader(r.content_ref));
  }
  return el;
}
const detailLimit = 16 * 1024 * 1024;
async function retainedText(ref, check, signal) {
  if (!Number.isSafeInteger(ref.bytes) || ref.bytes < 1) throw Error('Invalid retained-text reference.');
  if (ref.bytes > detailLimit) throw Error('Message is too large to display here.');
  let offset = 0; const buffers = [];
  for (;;) {
    check();
    const params = new URLSearchParams({ id: ref.id, source: ref.source, incarnation: ref.incarnation, sha256: ref.sha256, offset: String(offset) });
    const response = await fetch('/interaction/detail?' + params, { cache: 'no-store', credentials: 'same-origin', signal });
    check();
    if (!response.ok) throw Error('The retained text is unavailable.');
    const chunk = await response.json(); check();
    if (chunk.version !== 1 || chunk.id !== ref.id || chunk.source !== ref.source || chunk.offset !== offset ||
        chunk.sha256 !== ref.sha256 || chunk.incarnation !== ref.incarnation || chunk.size !== ref.bytes) throw Error('Recorded text changed; refresh history.');
    if (!Number.isInteger(chunk.bytes) || chunk.bytes < 0 || chunk.bytes > 16384 || typeof chunk.eof !== 'boolean' || typeof chunk.data_b64 !== 'string' || chunk.data_b64.length > 21848) throw Error('Invalid text range.');
    const bytes = Uint8Array.from(atob(chunk.data_b64), c => c.charCodeAt(0));
    if (bytes.length !== chunk.bytes || !chunk.eof && !bytes.length || offset + bytes.length > ref.bytes) throw Error('Invalid text range.');
    buffers.push(bytes); offset += bytes.length;
    if (chunk.eof) { if (offset !== ref.bytes) throw Error('Incomplete retained text.'); break; }
  }
  const all = new Uint8Array(offset); let pos = 0;
  for (const bytes of buffers) { all.set(bytes, pos); pos += bytes.length; }
  return new TextDecoder('utf-8', { fatal: true }).decode(all);
}
function detailReader(ref) {
  const box = document.createElement('div'); box.className = 'detail-read';
  const button = document.createElement('button'); button.type = 'button'; button.textContent = 'Read full retained detail';
  const status = document.createElement('span'); box.append(button, status);
  button.onclick = async () => {
    following = false; S.interactionHold?.(); renderJump();
    button.disabled = true; setText(status, 'Loading…');
    try {
      const text = await retainedText(ref, () => {
        if (document.visibilityState === 'hidden' || !box.isConnected) throw Error('Detail paused; reopen while this entry is visible.');
      });
      const pre = document.createElement('pre'); pre.textContent = text;
      box.appendChild(pre); button.remove(); setText(status, '');
    } catch (e) { setText(status, String(e.message || e)); button.disabled = false; }
  };
  return box;
}
function presentationRow(el, item) {
  const r = item.anchor.row, compact = item.tool || r.kind === 'notice' || r.kind === 'annotation' || r.kind === 'legacy' && r.role === 'system';
  if (!el) {
    el = document.createElement('div'); el.className = 'interaction-item'; el.dataset.interactionKey = item.key;
    if (compact) {
      const disclosure = document.createElement('details'); disclosure.className = 'interaction-disclosure tool-ev';
      const summary = document.createElement('summary');
      const label = document.createElement('span'); label.className = 'tname';
      const status = document.createElement('span'); status.className = 'tool-outcome';
      summary.append(label, status);
      const records = document.createElement('div'); records.className = 'interaction-records';
      disclosure.append(summary, records); el.appendChild(disclosure);
    } else {
      const msg = messageEl(r.role === 'resident' ? 'identity' : r.role, r.content || ' ');
      msg.classList.add('record-message');
      const disclosure = document.createElement('details'); disclosure.className = 'interaction-disclosure message-context';
      const summary = document.createElement('summary'); summary.textContent = '⋯'; summary.setAttribute('aria-label', 'Message details'); summary.title = 'Message details';
      const records = document.createElement('div'); records.className = 'interaction-records';
      disclosure.append(summary, records);
      let actions = msg.querySelector('.msg-acts');
      if (!actions) { actions = document.createElement('div'); actions.className = 'msg-acts'; msg.appendChild(actions); }
      actions.appendChild(disclosure); el.appendChild(msg);
    }
    const qualification = document.createElement('div'); qualification.className = 'record-qualification'; el.appendChild(qualification);
  }
  setData(el, 'turnId', r.id);
  setData(el, 'payload', JSON.stringify(r));
  setData(el, 'records', JSON.stringify(item.records.map(e => e.row)));
  const voice = r.annotations?.voice;
  if (voice?.session && voice?.sequence) setData(el, 'voiceRef', voice.session + '/' + voice.sequence);
  else if (el.dataset.voiceRef) delete el.dataset.voiceRef;
  if (compact) {
    setText(el.querySelector('.tname'), item.tool ? r.details?.tool || 'Tool' : (r.content || (r.kind === 'annotation' ? 'Recorded annotation' : 'Recorded notice')).split('\n')[0].slice(0, 100));
    const labels = { succeeded: 'Done', failed: 'Failed', cancelled: 'Cancelled', refused: 'Refused', unknown: 'Outcome unknown', conflict: 'Conflicting records' };
    setText(el.querySelector('.tool-outcome'), item.tool ? labels[item.outcome] || (item.outcome ? 'Outcome unknown' : 'Requested') : r.outcome || '');
    el.classList.toggle('record-failed', ['failed','refused','unknown','conflict'].includes(item.outcome || r.outcome));
  } else {
    const msg = el.querySelector('.record-message');
    longMessage(msg, r, item.key);
    const who = msg.querySelector('.who'), label = speakerLabel(r.annotations?.speaker);
    const role = r.role === 'resident' ? (S.stats?.name || 'identity') : r.role === 'operator' ? 'you' : r.role === 'participant' ? 'participant' : '';
    setText(who, role + (label ? ' · ' + label : ''));
  }
  const notes = [];
  if (item.conflict) notes.push('Conflicting records — inspect details');
  if (item.transient || item.source === 'transient') notes.push(item.tool ? 'Completion/activity not saved' : 'Not saved');
  if (item.records.some(e => e.row.details?.recording_error)) notes.push('Recording failed');
  setText(el.querySelector('.record-qualification'), notes.join(' · '));
  const records = el.querySelector('.interaction-records');
  const old = new Map(Array.from(records.children).map(e => [e.dataset.recordKey, e]));
  const nodes = item.records.map(entry => occurrence(old.get(entry.key), entry));
  let missing = el.querySelector('.missing-start');
  if (item.missingStart) {
    if (!missing) { missing = document.createElement('p'); missing.className = 'missing-start';  }
    setText(missing, 'The start of this execution is outside this view or unavailable.'); nodes.push(missing);
  }
  syncChildren(records, nodes);
  return el;
}
const messageTextState = new WeakMap(), unreadMessages = new Set();
let activeMessageRead = null, pendingTextReading = null;
const messageObserver = typeof IntersectionObserver === 'function'
  ? new IntersectionObserver(() => refreshMessageText(), { root: $('thread') }) : null;
function watchMessage(msg) { unreadMessages.add(msg); messageObserver?.observe(msg); }
function unwatchMessage(msg) { unreadMessages.delete(msg); messageObserver?.unobserve(msg); }
function messageVisible(msg) {
  if (!msg.isConnected || document.visibilityState === 'hidden' || S.view && S.view !== 'chat') return false;
  const viewport = $('thread').getBoundingClientRect(), box = msg.getBoundingClientRect();
  return viewport.height > 0 && box.height > 0 && box.bottom > viewport.top && box.top < viewport.bottom;
}
function messageChange(change) {
  const thread = $('thread'), inner = $('thread-inner');
  const anchor = readingAnchor(inner, thread.getBoundingClientRect().top), offset = anchor?.getBoundingClientRect().top;
  change();
  if (pendingTextReading) restoreReading(pendingTextReading, thread, inner);
  else if (following) positionThread(thread, thread.scrollHeight);
  else if (anchor?.isConnected) positionThread(thread, thread.scrollTop + anchor.getBoundingClientRect().top - offset);
  renderJump();
}
function longMessage(msg, r, key) {
  const long = !r.content && !!r.content_ref, body = msg.querySelector('.body'), copy = msg.querySelector('.copyb');
  msg.classList.toggle('long-message', long);
  if (!long) {
    messageTextState.delete(msg); unwatchMessage(msg); msg.querySelector('.message-load')?.remove();
    setText(body, r.content || '');
    if (copy) { copy.hidden = !r.content; setData(copy, 'copyText', r.content || ''); }
    return;
  }
  const signature = JSON.stringify([key, r.content_ref]);
  if (messageTextState.get(msg)?.signature === signature) return;
  const state = { signature, ref: { ...r.content_ref }, kind: r.kind, role: r.role, loaded: false, error: false };
  messageTextState.set(msg, state); setText(body, '');
  if (copy) { copy.hidden = true; setData(copy, 'copyText', ''); }
  msg.querySelector('.message-load')?.remove();
  const loader = document.createElement('div'); loader.className = 'message-load detail-read';
  const status = document.createElement('span'); status.textContent = 'Loading message…';
  const retry = document.createElement('button'); retry.type = 'button'; retry.textContent = 'Retry'; retry.hidden = true;
  retry.onclick = () => {
    if (messageTextState.get(msg) !== state) return;
    state.error = false; retry.hidden = true; status.textContent = 'Loading message…'; watchMessage(msg); refreshMessageText();
  };
  loader.append(status, retry); msg.querySelector('.body').after(loader); watchMessage(msg);
}
function refreshMessageText() {
  if (activeMessageRead && !activeMessageRead.current()) activeMessageRead.controller.abort();
  if (unreadMessages.size) queueMicrotask(pumpMessageText);
}
S.refreshMessageText = refreshMessageText;
S.cancelReadingRestore = () => { pendingTextReading = null; };
document.addEventListener('visibilitychange', refreshMessageText);
$('thread')?.addEventListener('scroll', refreshMessageText, { passive: true });
window.addEventListener('resize', refreshMessageText, { passive: true });
async function pumpMessageText() {
  for (const msg of unreadMessages) if (!msg.isConnected) unwatchMessage(msg);
  if (activeMessageRead || document.visibilityState === 'hidden' || S.view && S.view !== 'chat') return;
  const msg = [...unreadMessages].find(messageVisible); if (!msg) return;
  const state = messageTextState.get(msg); if (!state || state.loaded || state.error) { unwatchMessage(msg); return; }
  const controller = new AbortController(); let timedOut = false;
  const current = () => messageTextState.get(msg) === state && messageVisible(msg);
  const check = () => { if (controller.signal.aborted || !current()) throw new DOMException('Message read paused', 'AbortError'); };
  activeMessageRead = { msg, controller, current };
  const timer = setTimeout(() => { timedOut = true; controller.abort(); }, 30000);
  try {
    const retained = [...$('thread-inner').querySelectorAll('.long-message')].reduce((n, el) => {
      const s = messageTextState.get(el); return n + (s?.loaded ? s.ref.bytes : 0);
    }, 0);
    if (retained + state.ref.bytes > detailLimit) throw Error('This view’s full-text limit was reached. Open message details to read more.');
    const record = JSON.parse(await retainedText(state.ref, check, controller.signal)); check();
    if (record.id !== state.ref.id || record.kind !== state.kind || record.role !== state.role || typeof record.content !== 'string') throw Error('Retained text does not match this message.');
    messageChange(() => {
      state.loaded = true; setText(msg.querySelector('.body'), record.content); msg.querySelector('.message-load')?.remove();
      const copy = msg.querySelector('.copyb'); if (copy) { copy.hidden = !record.content; setData(copy, 'copyText', record.content); }
    });
    unwatchMessage(msg);
  } catch (e) {
    if (current() && (timedOut || e.name !== 'AbortError')) {
      state.error = true; unwatchMessage(msg);
      messageChange(() => {
        const loader = msg.querySelector('.message-load');
        setText(loader?.firstElementChild, timedOut ? 'Message took too long to load. Retry.' : 'Message unavailable: ' + (e.message || e));
        if (loader) loader.querySelector('button').hidden = false;
      });
    }
  } finally {
    clearTimeout(timer); activeMessageRead = null; refreshMessageText();
  }
}

function duration(ms) {
  if (!(ms >= 0)) return '';
  const s = Math.round(ms / 1000); if (s < 60) return s + ' s';
  const m = Math.round(s / 60); if (m < 60) return m + ' min';
  return Math.floor(m / 60) + ' h' + (m % 60 ? ' ' + (m % 60) + ' min' : '');
}
function receiptLabel(it, live) {
  const count = it.steps ? it.steps + (it.steps === 1 ? ' step' : ' steps') : it.notes + (it.notes === 1 ? ' recorded note' : ' recorded notes');
  const parts = [];
  if (live) parts.push('Working', count + (it.startOutside ? ' in view' : ''));
  else if (it.startOutside || it.endOutside) parts.push(count + ' in view');
  else if (it.steps && it.replied) { const took = duration(it.end - it.start); parts.push(took ? 'Worked ' + took : 'Worked', count); }
  else parts.push(count);
  if (it.failed) parts.push(it.failed + ' failed');
  if (it.unknown) parts.push(it.unknown + ' unknown');
  if (it.cancelled) parts.push(it.cancelled + ' cancelled');
  if (it.unfinished && !live && !it.endOutside) parts.push(it.unfinished + ' unfinished');
  if (it.conflict) parts.push('conflicting records');
  if (it.unsaved) parts.push(it.unsaved === it.items.length ? 'not saved' : it.unsaved + ' not saved');
  if (it.unsavedCompletions) parts.push(it.unsavedCompletions + (it.unsavedCompletions === 1 ? ' completion' : ' completions') + ' not saved');
  return parts.join(' · ');
}
function markReceipt(el, it, live) {
  setText(el.querySelector('.receipt-label'), receiptLabel(it, live));
  const trouble = !!(it.failed || it.conflict);
  const partial = !trouble && !live && !!(it.unknown || it.cancelled || (it.unfinished && !it.endOutside) || it.unsaved || it.unsavedCompletions);
  for (const [name, on] of [['trouble', trouble], ['live', live], ['partial', partial]]) if (el.classList.contains(name) !== on) el.classList.toggle(name, on);
}
function receiptGroups(it) {
  const groups = new Map();
  for (const item of it.items) {
    const r = item.anchor.row;
    const name = item.tool ? (r.details?.tool || 'Tool') : (r.content || (r.kind === 'annotation' ? 'Recorded annotation' : 'Recorded notice')).split('\n')[0].slice(0, 80);
    const g = groups.get(name) || { name, tool: !!item.tool, n: 0, bad: 0 };
    g.n++; if (item.tool && (item.outcome === 'failed' || item.outcome === 'refused')) g.bad++;
    groups.set(name, g);
  }
  return [...groups.values()];
}
function receiptRow(it, old, claimed, live) {
  let el = old.get(it.key);
  if (!el || claimed.has(el)) {
    el = null;
    for (const item of it.items) {
      const inside = old.get(item.key)?.closest('.turn-receipt');
      if (inside && !claimed.has(inside) && inside.parentElement) { el = inside; break; }
    }
  }
  if (!el) {
    el = document.createElement('div'); el.className = 'interaction-item turn-receipt';
    const box = document.createElement('details'); box.className = 'receipt';
    const line = document.createElement('summary'); line.className = 'receipt-line';
    const label = document.createElement('span'); label.className = 'receipt-label'; line.appendChild(label);
    const groups = document.createElement('ul'); groups.className = 'receipt-groups';
    const all = document.createElement('details'); all.className = 'receipt-all';
    const allTitle = document.createElement('summary'); const steps = document.createElement('div'); steps.className = 'receipt-steps';
    all.append(allTitle, steps);
    box.append(line, groups, all); el.appendChild(box);
  }
  claimed.add(el);
  setData(el, 'interactionKey', it.key);
  markReceipt(el, it, live);
  const groups = receiptGroups(it), signature = JSON.stringify(groups);
  const list = el.querySelector('.receipt-groups');
  if (list.dataset.signature !== signature) {
    list.dataset.signature = signature;
    list.replaceChildren(...groups.map(g => {
      const li = document.createElement('li');
      const name = document.createElement('span'); name.className = 'g-name' + (g.tool ? ' tool' : ''); name.textContent = g.name;
      const n = document.createElement('span'); n.className = 'g-count' + (g.bad ? ' bad' : '');
      n.textContent = (g.n > 1 ? '×' + g.n : '') + (g.bad ? (g.n > 1 ? ' · ' : '') + g.bad + ' failed' : '');
      li.append(name, n); return li;
    }));
  }
  setText(el.querySelector('.receipt-all > summary'), 'Every record, in order (' + it.items.length + ')');
  syncChildren(el.querySelector('.receipt-steps'), it.items.map(item => presentationRow(old.get(item.key), item)));
  return el;
}

function sourceControls(el, source, page, navigate, busy) {
  if (!el) {
    el = document.createElement('div'); el.className = 'interaction-controls'; el.dataset.source = source;
    el.appendChild(document.createElement('span'));
    const retry = document.createElement('button'); retry.type = 'button'; retry.textContent = 'Retry'; el.appendChild(retry);
  }
  const state = !page ? 'Loading conversation…' : page.error ? 'History unavailable: ' + page.error : page.stale ? 'Last known view — refreshing when connected' : page.reset ? 'History source changed — view refreshed' : '';
  const label = source === 'transient' ? 'Not saved — current session' : '';
  setText(el.firstElementChild, [label, state, page?.lost ? page.lost + ' earlier entries no longer retained' : ''].filter(Boolean).join(' · '));
  const retry = el.querySelector('button');
  if (retry.hidden !== !page?.error) retry.hidden = !page?.error;
  if (retry.disabled !== busy) retry.disabled = busy;
  retry.onclick = () => navigate?.(source, 'latest');
  const hidden = !el.firstElementChild.textContent && retry.hidden;
  if (el.hidden !== hidden) el.hidden = hidden;
  return el;
}
let liveReceipt = null;
export function renderInteractionPages(pages, navigate, busy, position = '', reading = null) {
  currentPages = pages; currentNavigate = navigate;
  if (document.visibilityState === 'hidden') return;
  const thread = $('thread'), inner = $('thread-inner'); if (!thread || !inner) return;
  renderNotices();
  historyEarlier = Array.from(pages.values()).some(p => p.has_newer);
  const top = thread.scrollTop;
  const boundary = thread.getBoundingClientRect().top;
  const anchor = readingAnchor(inner, boundary);
  const offset = anchor?.getBoundingClientRect().top;
  const anchorText = anchor && messageTextState.get(anchor.querySelector('.record-message'));
  const focused = document.activeElement;
  const old = new Map(Array.from(inner.querySelectorAll('.interaction-item[data-interaction-key]')).map(el => [el.dataset.interactionKey, el]));
  const projected = displayInteractions(pages), batch = [], claimed = new Set();
  const runningSource = S.cont?.mode === 'safe' ? 'transient' : 'recorded';
  liveReceipt = null;
  const firsts = new Map();
  for (const source of ['recorded','transient']) {
    const page = pages.get(source), items = projected.get(source);
    if (source === 'transient' && (!page || !page.error && !page.lost && !items.length)) continue;
    const controls = Array.from(inner.children).find(el => el.classList.contains('interaction-controls') && el.dataset.source === source);
    batch.push(sourceControls(controls, source, page, navigate, busy));
    const folded = foldTurns(items, page), newest = folded.at(-1);
    for (const item of folded) {
      let el;
      if (!item.receipt) el = presentationRow(old.get(item.key), item);
      else {
        const canRun = source === runningSource && item === newest && !page.has_newer;
        el = receiptRow(item, old, claimed, canRun && !!S.thinking);
        if (canRun) liveReceipt = { el, item };
      }
      if (!firsts.has(source)) firsts.set(source, el);
      batch.push(el);
    }
  }
  askCards.forEach(card => batch.push(card));
  if (S.thinking) batch.push(thinkingMarker()); else thinkingEl = null;
  syncChildren(inner, batch);
  if (focused && inner.contains(focused) && document.activeElement !== focused) focused.focus({ preventScroll: true });
  const changedText = anchor?.isConnected && messageTextState.get(anchor.querySelector('.record-message'));
  if (!reading && !pendingTextReading && !following && anchorText?.loaded && changedText && changedText !== anchorText && !changedText.loaded) {
    pendingTextReading = { key: anchor.dataset.interactionKey, offset: offset - boundary, following: false, open: S.interactionBookmark()?.open || [] };
  }
  if (reading || pendingTextReading) restoreReading(reading || pendingTextReading, thread, inner);
  else if (position === 'latest' || position !== 'hold' && following) {
    if (position === 'latest') following = true;
    positionThread(thread, thread.scrollHeight);
  } else if (anchor?.isConnected) positionThread(thread, thread.scrollTop + anchor.getBoundingClientRect().top - offset);
  else positionThread(thread, top);
  S.interactionPositioned?.();
  renderJump();
  continueOlder(pages, firsts, position);
  refreshMessageText();
}

const shownFirst = new Map();
function continueOlder(pages, firsts, position) {
  for (const source of ['recorded', 'transient']) {
    const page = pages.get(source), first = firsts.get(source) || null, row = page?.rows?.[0];
    const before = shownFirst.get(source);
    shownFirst.set(source, first && row ? { el: first, sequence: row.sequence } : null);
    if (position !== 'hold' || !before || !first || !row || first !== before.el) continue;
    if (!page.has_older || page.stale || page.error || !first.classList.contains('turn-receipt') || first.querySelector('.receipt').open) continue;
    let deeper = false;
    try { deeper = BigInt(row.sequence) < BigInt(before.sequence); } catch (err) { deeper = false; }
    if (deeper) queueMicrotask(() => {
      const thread = $('thread');
      if (currentPages.get(source) !== page || !first.isConnected || first.querySelector('.receipt').open ||
          document.visibilityState === 'hidden' || (S.view && S.view !== 'chat') ||
          first.getBoundingClientRect().bottom <= thread.getBoundingClientRect().top) return;
      currentNavigate?.(source, 'older', true);
    });
  }
}

function readingAnchor(inner, boundary) {
  let anchor = Array.from(inner.children).find(el => el.dataset.interactionKey && el.getBoundingClientRect().bottom > boundary);
  if (anchor?.classList.contains('turn-receipt') && anchor.querySelector('.receipt').open) {
    anchor = Array.from(anchor.querySelectorAll('.receipt-steps > .interaction-item')).find(el => el.getBoundingClientRect().bottom > boundary) || anchor;
  }
  return anchor;
}
S.interactionBookmark = () => {
  if (pendingTextReading) return pendingTextReading;
  const thread = $('thread'), inner = $('thread-inner'); if (!thread || !inner) return null;
  const boundary = thread.getBoundingClientRect().top, anchor = readingAnchor(inner, boundary);
  if (!anchor) return null;
  const open = [];
  inner.querySelectorAll('.interaction-item[data-interaction-key]').forEach(el => {
    const indices = Array.from(el.querySelectorAll('details')).filter(d => d.closest('.interaction-item') === el).flatMap((d, i) => d.open ? [i] : []);
    if (indices.length) open.push({ key: el.dataset.interactionKey, indices });
  });
  return { key: anchor.dataset.interactionKey, offset: anchor.getBoundingClientRect().top - boundary, following: S.interactionFollowing(), open };
};
function restoreReading(reading, thread, inner) {
  following = !!reading.following;
  for (const entry of reading.open || []) {
    const el = Array.from(inner.querySelectorAll('[data-interaction-key]')).find(e => e.dataset.interactionKey === entry.key);
    if (el) Array.from(el.querySelectorAll('details')).filter(d => d.closest('.interaction-item') === el).forEach((d, i) => { d.open = entry.indices.includes(i); });
  }
  const anchor = Array.from(inner.querySelectorAll('[data-interaction-key]')).find(e => e.dataset.interactionKey === reading.key);
  const text = anchor && messageTextState.get(anchor.querySelector('.record-message'));
  if (!following && text && !text.loaded) {
    pendingTextReading = reading;
    positionThread(thread, thread.scrollTop + anchor.getBoundingClientRect().top - thread.getBoundingClientRect().top);
  } else {
    pendingTextReading = null;
    if (following) positionThread(thread, thread.scrollHeight);
    else if (anchor) positionThread(thread, thread.scrollTop + anchor.getBoundingClientRect().top - thread.getBoundingClientRect().top - reading.offset);
  }
}
