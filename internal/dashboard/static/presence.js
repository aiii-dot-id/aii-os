
import { S } from './state.js';
import { $, esc } from './util.js';
import { toggleThinkingDots } from './views/chat.js';

export function phoneAsleep(connected, stats, hostname) {
  const here = ['127.0.0.1', 'localhost', '[::1]', '::1'].includes(hostname);
  return !connected && !here && !!(stats && stats.hosted_on_phone);
}

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
  const asleep = phoneAsleep(S.connected, S.stats, location.hostname);
  const state = asleep ? '<b>asleep</b> \u2014 this identity runs on a phone and answers while its app is open; this page reconnects by itself'
    : !S.connected ? '<b>offline</b>'
    : !S.stats ? 'connecting'
    : !S.identityExists ? 'awaiting <b>birth</b>'
    : S.thinking ? '<b>thinking</b>'
    : mode === 'safe' ? '<b>SAFE</b> — record frozen'
    : mode === 'degraded_witness' ? '<b>degraded</b> — witness dark'
    : '<b>present</b>';
  if (st.innerHTML !== state) st.innerHTML = state;

  if (S.cont) {
    const pm = $('pill-mode');
    pm.style.display = '';
    pm.className = 'pill mode-' + mode;
    pm.innerHTML = 'mode <b>' + esc(mode) + '</b>';
  }
}
const SEND_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 13V3M3.5 7.5 8 3l4.5 4.5"/></svg>';
export function setThinking(on) {
  S.thinking = on;
  if (S.chipsRedraw) S.chipsRedraw();
  renderPresence();
  toggleThinkingDots(on);

  const button = $('send-btn');
  if (button) {
    button.classList.toggle('stopping', !!on);
    button.title = on ? 'Stop this turn' : 'Send';
    button.innerHTML = on ? '&#9632;' : SEND_ICON;
    button.setAttribute('aria-label', button.title);
  }
}
export function toolPulse() {
  const orb = $('orb'); orb.classList.add('tool');
  if (S.toolBusyTimer) clearTimeout(S.toolBusyTimer);
  S.toolBusyTimer = setTimeout(() => orb.classList.remove('tool'), 1600);
}
