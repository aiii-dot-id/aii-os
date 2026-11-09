
import { S } from './state.js';
import { $ } from './util.js';
import { toggleThinkingDots } from './views/chat.js';

export function phoneAsleep(connected, stats, hostname) {
  const here = ['127.0.0.1', 'localhost', '[::1]', '::1'].includes(hostname);
  return !connected && !here && !!(stats && stats.hosted_on_phone);
}

export function renderPresence() {
  const mode = S.cont ? (S.cont.mode || 'normal') : 'normal';
  if (S.renderTopBar) S.renderTopBar();
  else $('p-name').textContent = S.identityExists && S.stats.name !== 'Unnamed' ? S.stats.name : 'AII OS';
  const st = $('p-state');
  const connecting = S.firstConnect || (S.connected && !S.stats);
  st.className = 'p-state' + (S.firstConnect ? ' connecting' : mode === 'safe' ? ' safe' : mode === 'degraded_witness' || mode === 'degraded_ring5' ? ' degraded' : '');
  const asleep = phoneAsleep(S.connected, S.stats, location.hostname);
  const state = connecting ? 'connecting'
    : asleep ? '<b>asleep</b>'
    : !S.connected ? '<b>offline</b>'
    : !S.identityExists ? 'awaiting <b>birth</b>'
    : S.thinking ? '<b>thinking</b>'
    : mode === 'safe' ? '<b>SAFE</b> — record frozen'
    : mode === 'degraded_witness' ? '<b>degraded</b> — witness dark'
    : mode === 'degraded_ring5' ? '<b>degraded</b> — waiting for Ring 5'
    : '<b>present</b>';
  if (st.innerHTML !== state) st.innerHTML = state;
  S.phoneAsleepNow = asleep;
}
const SEND_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 13V3M3.5 7.5 8 3l4.5 4.5"/></svg>';
export function setThinking(on) {
  S.thinking = on;
  S.sectionsTurn?.();
  if (S.chipsRedraw) S.chipsRedraw();
  renderPresence();
  toggleThinkingDots(on);

  if (S.renderComposer) { S.renderComposer(); return; }
  const button = $('send-btn');
  if (button) {
    button.classList.toggle('stopping', !!on);
    button.title = on ? 'Stop this turn' : 'Send';
    button.innerHTML = on ? '&#9632;' : SEND_ICON;
    button.setAttribute('aria-label', button.title);
  }
}
