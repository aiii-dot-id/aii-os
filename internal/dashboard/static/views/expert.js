import { S } from '../state.js';
import { esc } from '../util.js';
import { renderInto } from '../announce.js';
import { send } from '../ws.js';
import { pendingSlot } from '../pending.js';

// The top of Chat, the page the dashboard opens on: the Expert
// checkbox, what it reveals, and one plain line about the operator's
// notice route whenever that route is on.
//
// Expert is the host's setting (dashboard.expert). It is read from the
// status every page receives and saved through the configuration door,
// never kept in this browser, so every page and every browser agrees. It
// only shows and hides: the control under it saves its own setting, and
// the line about an active route is said whether Expert is on or off.

const KEYS = { expert: 'dashboard.expert', route: 'dashboard.notices_off_dashboard' };
const slots = { expert: pendingSlot(), route: pendingSlot() };
// What the host said to this page's last save of each: a refusal in its
// own words, or a lost connection. Cleared by the next save.
const said = { expert: '', route: '' };
// A save the host acknowledged, shown until the status that follows it.
const confirmed = { expert: null, route: null };

// inForce is the setting as the host last said it, or as this page asked
// for it while the host has yet to answer.
function inForce(which) {
  const held = slots[which].waiting();
  if (held) return held.value;
  if (confirmed[which] !== null) return confirmed[which];
  const st = S.stats || {};
  return which === 'expert' ? !!st.expert : !!(st.notices && st.notices.on);
}

const thenList = xs => xs.length < 2 ? xs.join('') : xs.slice(0, -1).join(', ') + ', then ' + xs[xs.length - 1];

// routeLine is the one plain line about an active route: where the
// notices go, or — when nothing can carry them now — that they wait here.
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
  // The panel is rebuilt at every status, so its refusal is said through
  // the announcer as the rebuild first shows it; opening the panel shows
  // what was already said.
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

// acceptExpertConfig takes the configuration door's answer to one of this
// page's saves; the status that follows it carries the setting to every page.
export function acceptExpertConfig(requestID) {
  for (const which of Object.keys(slots)) {
    const held = slots[which].claim(requestID);
    if (held) { confirmed[which] = held.value; renderExpert(); return true; }
  }
  return false;
}

// rejectExpertConfig shows the door's refusal beside the control, in its
// own words; the setting stays as the host has it.
export function rejectExpertConfig(requestID, text) {
  for (const which of Object.keys(slots)) {
    if (slots[which].claim(requestID)) { said[which] = text; renderExpert(); return true; }
  }
  return false;
}

// expertStatus is a status's arrival: the host's word replaces what was
// acknowledged before it.
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
