/* presence.js — the orb and the presence strip: the identity's
   honest live state (connected/thinking/tool/SAFE/degraded) and the
   mode pill. The rail-footer status numbers moved to the right-hand
   system panel (panel.js) so status is grouped in one column.
   R66: frame, permanently —
   the conversation surface with the orb is never suspendable and
   never lives inside any section viewport (UI_FRAME.md §2). */
import { S } from './state.js';
import { $, esc } from './util.js';
import { toggleThinkingDots } from './views/chat.js';

/* --- presence (the orb is honest) --------------------------- */
export function renderPresence() {
  const orb = $('orb');
  const mode = S.cont ? (S.cont.mode || 'normal') : 'normal';
  orb.className = 'orb-wrap';
  if (!S.connected) orb.classList.add('offline');
  else if (mode === 'safe') orb.classList.add('safe');
  else if (mode === 'degraded_witness') orb.classList.add('degraded');
  if (S.thinking) orb.classList.add('thinking');
  $('p-name').textContent = S.identityExists ? S.stats.name : 'AII OS';
  const st = $('p-state');
  st.className = 'p-state' + (mode === 'safe' ? ' safe' : mode === 'degraded_witness' ? ' degraded' : '');
  st.innerHTML = !S.connected ? '<b>offline</b>'
    : !S.identityExists ? 'awaiting <b>birth</b>'
    : S.thinking ? '<b>thinking</b>'
    : mode === 'safe' ? '<b>SAFE</b> — record frozen'
    : mode === 'degraded_witness' ? '<b>degraded</b> — witness dark'
    : '<b>present</b>';
  // The rail-footer readings (ledger / life / witnessed / channel /
  // credential) moved to the right-hand system panel — panel.js renders
  // them from the same S.stats / S.cont, so this module no longer writes
  // them. One fact, one writer: presence.js owns the orb and the mode pill.

  if (S.cont) {
    const pm = $('pill-mode');
    pm.style.display = '';
    pm.className = 'pill mode-' + mode;
    pm.innerHTML = 'mode <b>' + esc(mode) + '</b>';
  }
}
export function setThinking(on) {
  S.thinking = on;
  renderPresence();
  toggleThinkingDots(on);
  // While the identity is working, the send control IS the stop control.
  // A turn that cannot be stopped is one the operator watches spend its
  // whole budget going somewhere they can already see is wrong — and a
  // cancel verb no button can send is a cancel that does not exist.
  const b = $('send-btn');
  if (b) {
    b.classList.toggle('stopping', !!on);
    b.title = on ? 'Stop this turn' : 'Send';
    b.innerHTML = on ? '&#9632;' : '&#10148;';
  }
}
export function toolPulse() {
  const orb = $('orb'); orb.classList.add('tool');
  if (S.toolBusyTimer) clearTimeout(S.toolBusyTimer);
  S.toolBusyTimer = setTimeout(() => orb.classList.remove('tool'), 1600);
}
