import { S } from '../state.js';
import { send } from '../ws.js';
import { pendingSlot } from '../pending.js';

export const EXPERT_WORDS = '';
export const EXPERT_TIP = '';
export function expertShown() { return true; }
export function expertBoxState() { return ''; }
export function expertBoxHTML() { return ''; }
export function wireExpertBoxes() {}

const KEY = 'dashboard.notices_off_dashboard';
const slot = pendingSlot();
let said = '';
let confirmed = null;

function inForce() {
  const held = slot.waiting();
  if (held) return held.value;
  if (confirmed !== null) return confirmed;
  const n = S.stats && S.stats.notices;
  return !!(n && n.on);
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

export function noticeRouteState() {
  const n = (S.stats && S.stats.notices) || null;
  return JSON.stringify([inForce(), !!slot.waiting(), said, n]);
}

export function noticeRouteDrawn() {
  return { on: inForce(), busy: !!slot.waiting(), said, notices: (S.stats && S.stats.notices) || null };
}

export function wireNoticeRoute(root) {
  root.querySelectorAll('[data-notice-route-switch]').forEach(box => { box.onchange = () => save(box.checked); });
}

export function renderExpert() {
  if (S.renderNoticeRoute) S.renderNoticeRoute();
}

function save(value) {
  said = '';
  const id = send({ type: 'config_set', config: { [KEY]: value } });
  if (!slot.arm({ value }, id)) said = 'Not connected; nothing was saved.';
  renderExpert();
}

export function acceptExpertConfig(requestID) {
  const held = slot.claim(requestID);
  if (!held) return false;
  confirmed = held.value;
  renderExpert();
  return true;
}

export function rejectExpertConfig(requestID, text) {
  if (!slot.claim(requestID)) return false;
  said = text;
  renderExpert();
  return true;
}

export function expertStatus() {
  confirmed = null;
  renderExpert();
}

export function expertConnectionLost() {
  if (slot.drop()) said = 'Connection lost before the answer. What is shown is the host’s last word.';
  renderExpert();
}
