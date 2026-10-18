import { S } from '../state.js';
import { esc } from '../util.js';
import { renderInto } from '../announce.js';
import { send } from '../ws.js';
import { pendingSlot } from '../pending.js';

const KEYS = { expert: 'dashboard.expert', route: 'dashboard.notices_off_dashboard' };
const slots = { expert: pendingSlot(), route: pendingSlot() };
const said = { expert: '', route: '' };
const confirmed = { expert: null, route: null };

function inForce(which) {
  const held = slots[which].waiting();
  if (held) return held.value;
  if (confirmed[which] !== null) return confirmed[which];
  const st = S.stats || {};
  return which === 'expert' ? !!st.expert : !!(st.notices && st.notices.on);
}

const thenList = xs => xs.length < 2 ? xs.join('') : xs.slice(0, -1).join(', ') + ', then ' + xs[xs.length - 1];

export function routeLine(n) {
  if (!n || !n.on) return '';
  const carried = (n.lines || []).filter(l => l.carried);
  if (n.held || !carried.length) {
    return 'Your notices are set to go to you off the dashboard when no page is open, but nothing can carry them now: ' +
      (n.held || 'no installed channel carries a line marked as you') + '. They wait here for a page.';
  }
  return 'Your notices also go to ' + carried[0].name + ' on ' + thenList(carried.map(l => l.channel)) + ' when no page is open.';
}

function linesHTML(n) {
  const lines = (n && n.lines) || [];
  if (!lines.length) return '<p data-notice-lines>No contact line is marked as you.</p>';
  return '<ul data-notice-lines>' + lines.map(l => '<li>' + esc(l.name) + ' on ' + esc(l.channel) +
    (l.carried ? '' : ' — no installed channel carries it now') + '</li>').join('') + '</ul>';
}

function panelHTML() {
  const n = (S.stats && S.stats.notices) || null;
  const busy = slots.route.waiting() ? ' disabled' : '';
  return '<label class="expert-control"><input type="checkbox" data-notice-route' + (inForce('route') ? ' checked' : '') + busy + '> Send my notices to me off the dashboard</label>' +
    '<p class="muted">When no page is open, your notices go to the contact lines marked as you, in your order. This page keeps every one either way.</p>' +
    linesHTML(n) +
    '<p class="muted">Which lines are you is set in <a href="#/settings/messages">Settings → Messages</a>, under Contacts: This is me.</p>' +
    '<p class="config-result bad" data-notice-said data-announce' + (said.route ? '' : ' hidden') + '>' + esc(said.route) + '</p>';
}

export function renderExpert() {
  const bar = document.getElementById('expert-bar');
  if (!bar) return;
  if (!S.stats || !S.identityExists) { bar.hidden = true; return; }
  bar.hidden = false;
  const expert = inForce('expert');
  const box = bar.querySelector('[data-expert]');
  box.checked = expert;
  box.disabled = !!slots.expert.waiting();
  const expertSaid = bar.querySelector('[data-expert-said]');
  expertSaid.textContent = said.expert;
  expertSaid.hidden = !said.expert;
  const line = bar.querySelector('[data-route-line]');
  const words = routeLine(S.stats.notices);
  line.textContent = words;
  line.hidden = !words;
  const panel = bar.querySelector('[data-expert-panel]');
  renderInto(panel, expert ? panelHTML() : '', panel.hidden);
  panel.hidden = !expert;
  const route = panel.querySelector('[data-notice-route]');
  if (route) route.onchange = () => save('route', route.checked);
  box.onchange = () => save('expert', box.checked);
}

function save(which, value) {
  said[which] = '';
  const id = send({ type: 'config_set', config: { [KEYS[which]]: value } });
  if (!slots[which].arm({ value }, id)) said[which] = 'Not connected; nothing was saved.';
  renderExpert();
}

export function acceptExpertConfig(requestID) {
  for (const which of Object.keys(slots)) {
    const held = slots[which].claim(requestID);
    if (held) { confirmed[which] = held.value; renderExpert(); return true; }
  }
  return false;
}

export function rejectExpertConfig(requestID, text) {
  for (const which of Object.keys(slots)) {
    if (slots[which].claim(requestID)) { said[which] = text; renderExpert(); return true; }
  }
  return false;
}

export function expertStatus() {
  confirmed.expert = confirmed.route = null;
  renderExpert();
}

export function expertConnectionLost() {
  for (const which of Object.keys(slots)) {
    if (slots[which].drop()) said[which] = 'Connection lost before the answer. What is shown is the host’s last word.';
  }
  renderExpert();
}
