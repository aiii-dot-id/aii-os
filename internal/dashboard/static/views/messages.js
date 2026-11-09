import { S } from '../state.js';
import { esc, fill } from '../util.js';
import { send } from '../ws.js';
import { pendingSlot } from '../pending.js';
import * as tabs from '../components/tabs/tabs.js';
import { say } from './settings/people-say.js';
import W from '../strings/settings-people.js';

const pending = pendingSlot();
let state = null;
let notice = W.refreshHint;
let tab = 'arrived';
const opened = new Set();
const render = () => { if (S.renderMessages) S.renderMessages(); };

export function requestMessages(more = false) {
  if (pending.waiting()) return;
  const limit = state ? (more ? Math.min(state.limit * 2, state.max_limit) : state.limit) : 0;
  const frame = { type: 'query', query: 'messages' };
  if (limit) frame.messages = { limit };
  if (!pending.arm({ limit }, send(frame))) { notice = W.notConnected; render(); return; }
  notice = 'Reading…'; render();
}
export function acceptMessages(reply) {
  if (!pending.claim(reply.request_id)) return false;
  state = reply.messages || null;
  notice = state ? 'Read at ' + when(state.read_ms) + '.' : 'Nothing came back.';
  render(); return true;
}
export function rejectMessages(requestID, text) {
  if (!pending.claim(requestID)) return false;
  notice = text; render(); return true;
}
export function messagesDisconnected() {
  if (pending.drop()) { notice = W.connectionLost; render(); }
}
export function installedChannels() { return (state && state.channels) || []; }

const when = ms => ms ? new Date(ms).toLocaleString() : 'an unrecorded time';
const plural = (n, one, many) => n + ' ' + (n === 1 ? one : many);
const capital = s => s ? s.charAt(0).toUpperCase() + s.slice(1) : s;
function readHTML(id, body, chars) {
  const shown = [...(body || '')].length;
  return '<details class="settings-people__read" data-row-read="' + esc(id) + '"' + (opened.has(id) ? ' open' : '') + '><summary>' + esc(W.readIt) + '</summary><pre class="settings-people__body">' + esc(body) + '</pre>' +
    (chars > shown ? '<p class="settings-people__hint" data-shortened>Shortened: the first ' + shown + ' of ' + chars + ' characters. The whole message is in the database copy under <a href="#/settings/technical/storage">Storage</a>.</p>' : '') + '</details>';
}
function omitted(shown, total, what, which = 'newest') {
  if (total <= shown) return '';
  const more = state.limit < state.max_limit;
  return '<p class="settings-people__hint" data-omitted>Showing the ' + which + ' ' + shown + ' of ' + total + ' ' + what + '. ' +
    (more ? '<button type="button" class="c-button c-button--quiet" data-messages-more>' + esc(W.showMore) + '</button>' : 'The rest are in the database copy under <a href="#/settings/technical/storage">Storage</a>.') + '</p>';
}
function arrivalHTML(a) {
  const who = a.relayed ? 'An event the plugin ' + a.relayed + ' relayed, not a person writing' : fill(W.fromBy, { who: a.from || a.address, channel: capital(a.channel) });
  const said = [];
  if (a.woke) said.push(say(W.wokeThem));
  else if (!a.from && !a.relayed) said.push(say(W.notInContacts));
  if (!a.woke) said.push(a.read ? W.turnReadIt : W.turnNotRead);
  const add = !a.from && !a.relayed ? '<button type="button" class="c-button c-button--quiet" data-add-contact="' + esc(a.id) + '">' + esc(W.addAsContact) + '</button>' : '';
  return '<div class="settings-people__row" data-arrival="' + esc(a.id) + '"><div class="settings-people__row-words"><span>' + esc(who) + ' <span class="settings-people__when">· ' + esc(when(a.received_ms)) + '</span></span>' +
    '<span class="settings-people__hint" data-woke>' + esc(said.join(' ')) + '</span>' + readHTML(a.id, a.body, a.body_chars) + '</div>' + add + '</div>';
}
const STATE_WORDS = { waiting: () => W.stateQueued, sending: () => W.stateSending, unknown: () => W.stateUnknown, parked: s => fill(W.stateFailed, { reason: String(s.note || 'parked').replace(/[.\s]+$/, '') }) };
function activityHTML(s) {
  const act = s.activity;
  if (!act) return '';
  let html = '<div class="settings-people__activity" data-activity>';
  if (act.unavailable) return html + '<p class="settings-people__hint">' + esc(act.unavailable) + '</p></div>';
  if (!act.count) return html + '<p class="settings-people__hint">Nothing from ' + esc(s.to) + ' since this left, ' + esc(when(act.since_ms)) + '.</p></div>';
  html += '<p class="settings-people__hint">' + plural(act.count, 'message', 'messages') + ' from ' + esc(s.to) + ' since this left, ' + esc(when(act.since_ms)) + '. ' + esc(say(W.activityNote)) + '</p>';
  html += (act.latest || []).map(a => '<div data-activity-item="' + esc(a.id) + '"><p class="settings-people__hint">' + esc(when(a.received_ms)) + ', ' + esc(capital(a.channel)) + '</p>' + readHTML(a.id, a.body, a.body_chars) + '</div>').join('');
  if (act.count > (act.latest || []).length) html += '<p class="settings-people__hint">' + plural(act.count - act.latest.length, 'earlier one is', 'earlier ones are') + ' not quoted here; see Arrived.</p>';
  return html + '</div>';
}
function sendHTML(s) {
  const delivered = s.state === 'delivered';
  const line = delivered ? fill(W.sentTo, { who: s.to, channel: s.via || 'an unrecorded adapter', when: when(s.sent_ms || s.created_ms) })
    : fill(W.notDeliveredRow, { who: s.to, when: when(s.created_ms), state: (STATE_WORDS[s.state] || (() => s.state))(s) });
  return '<div class="settings-people__row" data-send="' + esc(s.id) + '" data-state="' + esc(s.state) + '"><div class="settings-people__row-words"><span' + (delivered ? ' data-delivered' : '') + '>' + esc(line) + '</span>' +
    (s.note && !(s.state === 'parked') ? '<span class="settings-people__hint">' + esc(s.note) + '</span>' : '') +
    (s.answer ? '<span class="settings-people__hint">The last answer, in the adapter’s words:</span><pre class="settings-people__body" data-answer>' + esc(s.answer) + '</pre>' : '') +
    readHTML(s.id, s.body, s.body_chars) + activityHTML(s) + '</div></div>';
}

function noticeWhere(n) {
  if (n.route === 'page') return W.noticeToPage;
  if (n.route === 'channel') return W.noticeOff + (n.via ? ' (' + n.via + ')' : '');
  if (n.state === 'sending' || n.state === 'unknown') return W.noticeNotRecorded;
  return W.noticeWaiting;
}
function noticeHTML(n) {
  return '<div class="settings-people__row" data-notice="' + esc(n.id) + '" data-route="' + esc(n.route || '') + '" data-state="' + esc(n.state) + '"><div class="settings-people__row-words"><span>' + esc(when(n.created_ms)) + ' · <span data-notice-route>' + esc(noticeWhere(n)) + '</span></span>' +
    (n.note ? '<span class="settings-people__hint" data-notice-note>' + esc(n.note) + '</span>' : '') +
    (n.answer ? '<span class="settings-people__hint">The last answer, in the adapter’s words:</span><pre class="settings-people__body" data-answer>' + esc(n.answer) + '</pre>' : '') +
    readHTML(n.id, n.body, n.body_chars) + '</div></div>';
}

function panel(id, hook, inner) {
  return '<div class="settings-people__panel" role="tabpanel" id="messages-panel-' + id + '" aria-labelledby="messages-tab-' + id + '" data-panel="' + id + '"' + (tab === id ? '' : ' hidden') + ' ' + hook + '>' + inner + '</div>';
}
export function messagesHTML() {
  const busy = pending.waiting() ? ' disabled' : '';
  let html = '<section class="settings-people__part" data-messages aria-describedby="messages-hint"><div class="settings-people__h3-row"><h3 class="settings-people__h3">' + esc(W.messages) + '</h3>' +
    '<button type="button" class="c-button c-button--quiet" data-messages-refresh' + busy + '>' + esc(W.refresh) + '</button></div>' +
    '<p class="settings-people__hint" id="messages-hint">' + esc(say(W.messagesHint)) + '</p>' +
    '<p class="settings-people__said" data-messages-notice data-announce>' + esc(notice) + '</p>';
  if (!state) return html + '</section>';
  const arrivals = state.arrivals || [], open = state.open || [], delivered = state.delivered || [], notices = state.notices || [];
  html += '<div class="settings-people__tabs" data-messages-tabs></div>';
  html += panel('arrived', 'data-arrivals', (arrivals.length ? arrivals.map(arrivalHTML).join('') : '<p class="settings-people__hint">' + esc(W.arrivedNone) + '</p>') + omitted(arrivals.length, state.arrivals_total, 'arrivals'));
  html += panel('sent', 'data-delivered-sends', '<p class="settings-people__hint">' + esc(W.deliveredHint) + '</p>' + (delivered.length ? delivered.map(sendHTML).join('') : '<p class="settings-people__hint">' + esc(W.sentNone) + '</p>') + omitted(delivered.length, state.delivered_total, 'delivered messages'));
  html += panel('open', 'data-open-sends', '<p class="settings-people__hint">' + esc(say(W.notDeliveredHint)) + '</p>' +
    (open.length ? ['waiting', 'sending', 'parked', 'unknown'].map(id => {
      const rows = open.filter(s => s.state === id);
      return rows.length ? '<section data-send-state="' + id + '">' + rows.map(sendHTML).join('') + '</section>' : '';
    }).join('') : '<p class="settings-people__hint">' + esc(W.notDeliveredNone) + '</p>') + omitted(open.length, state.open_total, 'messages not delivered', 'oldest'));
  html += panel('notices', 'data-notices', (notices.length ? notices.map(noticeHTML).join('') : '<p class="settings-people__hint">' + esc(W.noticesNone) + '</p>') + omitted(notices.length, state.notices_total, 'notices'));
  return html + '</section>';
}
export function wireMessages(root) {
  root.querySelectorAll('[data-messages-refresh]').forEach(b => { b.onclick = () => requestMessages(); });
  root.querySelectorAll('[data-messages-more]').forEach(b => { b.disabled = !!pending.waiting(); b.onclick = () => requestMessages(true); });
  root.querySelectorAll('[data-row-read]').forEach(d => { d.ontoggle = () => { if (d.open) opened.add(d.dataset.rowRead); else opened.delete(d.dataset.rowRead); }; });
  root.querySelectorAll('[data-add-contact]').forEach(b => { b.onclick = () => {
    const a = ((state && state.arrivals) || []).find(x => x.id === b.dataset.addContact);
    if (a) import('./settings/people.js').then(m => m.openContactFor(a.channel, a.address));
  }; });
  const place = root.querySelector('[data-messages-tabs]');
  if (place && state) {
    const count = (label, n) => label + ' · ' + (n || 0);
    const list = tabs.render({ idPrefix: 'messages-tab', label: W.messages, selected: tab, tabs: [
      { id: 'arrived', label: count(W.arrived, state.arrivals_total) }, { id: 'sent', label: count(W.sent, state.delivered_total) },
      { id: 'open', label: count(W.notDelivered, state.open_total) }, { id: 'notices', label: W.yourNoticesTab },
    ], onSelect: id => {
      tab = id;
      root.querySelectorAll('[data-panel]').forEach(p => { p.hidden = p.dataset.panel !== id; });
    } });
    if (state.open_total) list.querySelector('[data-tab="open"]').setAttribute('data-any', '');
    place.append(list);
  }
}
