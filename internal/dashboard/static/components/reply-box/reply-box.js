import { $, esc, composing } from '../../util.js';
import W from '../../strings/chat.js';

const SEND_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 13V3M3.5 7.5 8 3l4.5 4.5"/></svg>';
const STOP_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><rect x="4" y="4" width="8" height="8" rx="1.5"/></svg>';
const EFFORT_NAMES = { '': 'Default', none: 'None', minimal: 'Minimal', low: 'Low', medium: 'Medium', high: 'High', xhigh: 'Extra High', max: 'Max' };
export function effortName(level) {
  if (Object.prototype.hasOwnProperty.call(EFFORT_NAMES, level)) return EFFORT_NAMES[level];
  return level.charAt(0).toUpperCase() + level.slice(1);
}

let on = {};
export function mount(handlers) {
  on = handlers;
  const input = $('msg-input'), sendBtn = $('send-btn');
  if (!input || !sendBtn) return;
  const autosize = () => { input.style.height = 'auto'; input.style.height = Math.min(input.scrollHeight, 160) + 'px'; };
  const sent = () => { if (on.onSend(input.value)) { input.value = ''; input.dispatchEvent(new Event('input', { bubbles: true })); } };
  input.addEventListener('keydown', e => {
    if (composing(e)) return;
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); sent(); }
  });
  input.addEventListener('input', autosize);
  sendBtn.onclick = () => { if (sendBtn.dataset.state === 'answering') on.onStop(); else sent(); };
  document.addEventListener('keydown', e => {
    if (e.key !== 'Escape' || e.defaultPrevented || sendBtn.dataset.state !== 'answering') return;
    if ([...document.querySelectorAll('dialog[open], [role="menu"]')].some(m => !m.hidden && m.getClientRects().length)) return;
    const t = e.target;
    if (t !== input && t.closest && t.closest('input, textarea, select, [contenteditable]')) return;
    if (!sendBtn.isConnected || !sendBtn.getClientRects().length) return;
    e.preventDefault();
    on.onStop();
  });
  mountPlus();
  mountEffort();
}

function waitsLine(on, name) {
  const ms = on.startsWith('time:') ? Number(on.slice(5)) : 0, at = new Date(ms);
  const w = on === 'word' ? W.waitsWord : on.startsWith('delivery:') ? W.waitsDelivery : ms > 0 ? W.waitsTime : '';
  return w.replace('{name}', name).replace('{when}', at.toDateString() === new Date().toDateString()
    ? 'at ' + at.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
    : 'on ' + at.toLocaleString([], { weekday: 'long', hour: 'numeric', minute: '2-digit' }));
}

export function update({ name = '', named = '', state = 'idle', answering = state === 'answering', nothingScheduled = false, waitsOn = '', levels = null, inForce = '', checked = [], model = '' } = {}) {
  const input = $('msg-input'), sendBtn = $('send-btn'), line = $('reply-line');
  const placeholder = W.replyPlaceholder.replace('{name}', name);
  if (input && input.placeholder !== placeholder) { input.placeholder = placeholder; input.setAttribute('aria-label', placeholder); }
  if (sendBtn && sendBtn.dataset.state !== (answering ? 'answering' : 'idle')) {
    const stop = answering;
    sendBtn.dataset.state = stop ? 'answering' : 'idle';
    sendBtn.classList.toggle('stopping', stop);
    sendBtn.title = stop ? W.stop : W.send;
    sendBtn.setAttribute('aria-label', sendBtn.title);
    sendBtn.innerHTML = stop ? STOP_ICON : SEND_ICON;
  }
  if (line) {
    const waits = waitsLine(waitsOn, named || 'They');
    const said = state === 'offline' ? W.offline : state === 'answering' && named ? W.answering.replace('{name}', named)
      : state === 'idle' && waits ? waits
      : state === 'idle' && nothingScheduled ? (named ? W.idle.replace('{name}', named) : W.idleUnnamed) : '';
    if (line.textContent !== said) line.textContent = said;
    line.dataset.state = state;
  }
  drawEffort(levels, inForce, checked);
  const item = $('plus-model'), words = $('plus-model-name');
  if (item && words) {
    item.hidden = !model;
    const text = W.plusModel.replace('{model}', model);
    if (words.textContent !== text) words.textContent = text;
  }
}

function menuItems(menu) {
  return [...menu.querySelectorAll('[role="menuitem"], [role="menuitemradio"]')].filter(b => !b.closest('[hidden]') && b.getClientRects().length);
}
function closePlus(focusBack) {
  const menu = $('plus-menu'), btn = $('attach-btn');
  if (!menu || menu.hidden) return;
  menu.hidden = true;
  closeEffort();
  if (btn) { btn.setAttribute('aria-expanded', 'false'); if (focusBack) btn.focus(); }
}
function mountPlus() {
  const btn = $('attach-btn'), menu = $('plus-menu');
  if (!btn || !menu) return;
  btn.title = W.plus; btn.setAttribute('aria-label', W.plus); menu.setAttribute('aria-label', W.plus);
  const said = (id, text) => { const el = $(id); if (el) el.textContent = text; };
  said('plus-files', W.plusFiles);
  said('effort-words', W.plusThinking.split('{level}')[0]);
  document.querySelector('#plus-model .c-reply__change')?.replaceChildren(' · ' + W.plusModelChange);
  btn.setAttribute('aria-haspopup', 'menu');
  btn.setAttribute('aria-expanded', 'false');
  btn.onclick = e => {
    e.stopPropagation();
    if (!menu.hidden) { closePlus(false); return; }
    menu.hidden = false;
    btn.setAttribute('aria-expanded', 'true');
    menuItems(menu)[0]?.focus();
  };
  const files = $('plus-files');
  if (files) files.onclick = () => { closePlus(true); on.onFiles?.(); };
  const model = $('plus-model');
  if (model) model.onclick = () => { closePlus(false); on.onModel?.(); };
  menu.addEventListener('keydown', e => {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); if (!$('effort-menu')?.hidden) { closeEffort(); $('effort-btn')?.focus(); } else closePlus(true); return; }
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
    e.preventDefault();
    const items = menuItems(menu);
    const next = items[(items.indexOf(document.activeElement) + (e.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length];
    next?.focus();
  });
  document.addEventListener('click', e => { if (!e.target.closest('#plus-menu, #attach-btn')) closePlus(false); });
}

function closeEffort() {
  const menu = $('effort-menu'), btn = $('effort-btn');
  if (menu) menu.hidden = true;
  if (btn) btn.setAttribute('aria-expanded', 'false');
}
function drawEffort(levels, inForce, checked) {
  const wrap = $('effort'), val = $('effort-val'), menu = $('effort-menu');
  if (!wrap || !val || !menu) return;
  wrap.hidden = !(levels && levels.length);
  if (wrap.hidden) { closeEffort(); return; }
  const words = $('effort-words');
  if (words) { const before = W.plusThinking.split('{level}')[0]; if (words.textContent !== before) words.textContent = before; }
  val.textContent = effortName(inForce);
  const key = levels.join('\n') + '\n=' + inForce + '\n?' + checked.join(',');
  if (menu.dataset.key === key) return;
  menu.dataset.key = key;
  menu.innerHTML = [''].concat(levels).map(l =>
    '<button type="button" role="menuitemradio" aria-checked="' + (l === inForce) + '" data-level="' + esc(l) + '"' +
    (checked.includes(l) ? ' data-checked title="Checked with the provider before it applies"' : '') + '>' +
    '<span>' + esc(effortName(l)) + '</span><span class="check" aria-hidden="true">✓</span></button>').join('');
}
function mountEffort() {
  const btn = $('effort-btn'), menu = $('effort-menu');
  if (!btn || !menu) return;
  btn.onclick = e => {
    e.stopPropagation();
    const open = menu.hidden;
    menu.hidden = !open;
    btn.setAttribute('aria-expanded', open ? 'true' : 'false');
    if (open) (menu.querySelector('[aria-checked="true"]') || menu.querySelector('button'))?.focus();
  };
  menu.onclick = e => {
    const b = e.target.closest('button[data-level]');
    if (!b) return;
    e.stopPropagation();
    closeEffort();
    if ($('plus-menu')) closePlus(true); else btn.focus();
    on.onEffort?.(b.dataset.level, b.hasAttribute('data-checked'));
  };
  menu.addEventListener('keydown', e => {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
    e.preventDefault(); e.stopPropagation();
    const items = [...menu.querySelectorAll('button')];
    items[(items.indexOf(document.activeElement) + (e.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length]?.focus();
  });
  document.addEventListener('click', e => { if (!e.target.closest('#effort')) closeEffort(); });
  if (!$('plus-menu')) document.addEventListener('keydown', e => { if (e.key === 'Escape' && !menu.hidden) { closeEffort(); btn.focus(); } });
}
