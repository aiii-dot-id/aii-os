import { S } from './state.js';

export const WAITING_WORDS = {
  product: 'AII OS',
  switchLabel: 'Notify me in this browser when the identity asks me something',
  title: name => (name || 'Your identity') + ' is waiting on you',
  body: 'A question is open in Chat.',
  titleLine: 'This page’s title starts with how many questions are open, in every browser.',
  unavailable: 'This browser cannot show this page’s notifications here; the page’s title counts the open questions.',
  blocked: 'This browser has blocked notifications for this page. Allow them in the browser’s site settings, then turn this on again.',
};

const SWITCH_KEY = 'aii.notify.asks';

export function titleOf(open, name) {
  return (open > 0 ? '(' + open + ') ' : '') + (name ? name + ' — ' : '') + WAITING_WORDS.product;
}

let place = '';
export function titlePlace(name) { place = name || ''; renderTitle(); }
function identityName() { const n = (S.identityExists && S.stats && S.stats.name) || ''; return n === 'Unnamed' ? '' : n; }

export function renderTitle() {
  if (typeof document === 'undefined') return;
  const name = (S.view === 'projects' && place) || identityName();
  const next = titleOf((S.asks || []).length, name);
  if (document.title !== next) document.title = next;
}

const seen = new Set();
let loaded = false;

export function acceptAsks(asks) {
  const list = (asks || []).filter(a => a && a.id);
  const fresh = list.filter(a => !seen.has(a.id));
  list.forEach(a => seen.add(a.id));
  const first = !loaded;
  loaded = true;
  renderTitle();
  if (first || notifyState() !== 'on') return;
  fresh.forEach(notify);
}

function notify(a) {
  try {
    const n = new window.Notification(WAITING_WORDS.title(identityName()), { body: WAITING_WORDS.body, tag: 'aii-ask-' + a.id });
    n.onclick = () => {
      try { window.focus(); } catch (err) { }
      if (S.go) S.go('chat');
      n.close();
    };
  } catch (err) {
  }
}

export function notifyAvailability(w) {
  w = w || window;
  try {
    const nav = w.navigator || {};
    if (typeof w.Notification !== 'function' || !w.isSecureContext || (nav.userAgentData && nav.userAgentData.mobile)) return 'unavailable';
    return 'yes';
  } catch (err) {
    return 'unavailable';
  }
}

function switchedOn() {
  try { return window.localStorage.getItem(SWITCH_KEY) === 'on'; } catch (err) { return false; }
}
function rememberSwitch(on) {
  try {
    if (on) window.localStorage.setItem(SWITCH_KEY, 'on');
    else window.localStorage.removeItem(SWITCH_KEY);
  } catch (err) { }
}

export function notifyState() {
  const can = notifyAvailability();
  if (can !== 'yes') return can;
  if (window.Notification.permission === 'denied') return 'blocked';
  return switchedOn() && window.Notification.permission === 'granted' ? 'on' : 'off';
}

export function notifySaid(state) {
  return state === 'unavailable' ? WAITING_WORDS.unavailable : state === 'blocked' ? WAITING_WORDS.blocked : '';
}

export async function turnNotify(on) {
  if (!on) { rememberSwitch(false); return notifyState(); }
  if (notifyAvailability() !== 'yes') return notifyState();
  let permission = window.Notification.permission;
  if (permission === 'default') {
    try { permission = await window.Notification.requestPermission(); } catch (err) { permission = window.Notification.permission; }
  }
  rememberSwitch(permission === 'granted');
  return notifyState();
}
