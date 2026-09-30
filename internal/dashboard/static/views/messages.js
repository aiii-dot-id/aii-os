import { S } from '../state.js';
import { esc } from '../util.js';
import { send } from '../ws.js';
import { pendingSlot } from '../pending.js';

// What came in, what the identity sent to people, and the operator's own
// notices with the route each took, read from the record when asked.
// Nothing here changes anything: the contacts beside it are saved through
// the configuration door. Every word a sender or an adapter wrote is shown
// as text.
const pending = pendingSlot();
let state = null;
let notice = 'Refresh to read what came in and what was sent.';
const render = () => { if (S.renderMessages) S.renderMessages(); };

export function requestMessages(more = false) {
  if (pending.waiting()) return;
  const limit = state ? (more ? Math.min(state.limit * 2, state.max_limit) : state.limit) : 0;
  const frame = { type: 'query', query: 'messages' };
  if (limit) frame.messages = { limit };
  if (!pending.arm({ limit }, send(frame))) { notice = 'Not connected; nothing was read.'; render(); return; }
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
  if (pending.drop()) { notice = 'Connection lost before the answer. Refresh after reconnecting.'; render(); }
}
export function installedChannels() { return (state && state.channels) || []; }

const when = ms => ms ? new Date(ms).toLocaleString() : 'an unrecorded time';
const plural = (n, one, many) => n + ' ' + (n === 1 ? one : many);
function bodyHTML(body, chars) {
  const shown = [...(body || '')].length;
  return '<pre style="white-space:pre-wrap;overflow-wrap:anywhere">' + esc(body) + '</pre>' +
    (chars > shown ? '<p class="muted" data-shortened>Shortened: the first ' + shown + ' of ' + chars + ' characters. The whole message is in the database copy under <a href="#" data-open-section="storage">Storage</a>.</p>' : '');
}
function sender(a) {
  if (a.relayed) return 'An event the plugin ' + esc(a.relayed) + ' relayed, not a person writing';
  if (a.from) return '<b>' + esc(a.from) + '</b> (' + esc(a.address) + ')';
  return 'Not in your contacts: ' + esc(a.address);
}
function woke(a) {
  if (a.woke) return 'Woke the identity.';
  return a.read ? 'Did not wake the identity; a turn has read it.' : 'Did not wake the identity; no turn has read it yet.';
}
function omitted(shown, total, what, which = 'newest') {
  if (total <= shown) return '';
  const more = state.limit < state.max_limit;
  return '<p class="muted" data-omitted>Showing the ' + which + ' ' + shown + ' of ' + total + ' ' + what + '. ' +
    (more ? '<button class="btn ghost" data-messages-more>Show more</button>' : 'The rest are in the database copy under <a href="#" data-open-section="storage">Storage</a>.') + '</p>';
}
function arrivalHTML(a) {
  return '<div class="item" data-arrival="' + esc(a.id) + '"><div>' + sender(a) + ' on ' + esc(a.channel) + ', ' + esc(when(a.received_ms)) + '</div>' +
    '<div data-woke>' + woke(a) + '</div>' + bodyHTML(a.body, a.body_chars) + '</div>';
}
const STATES = [
  ['waiting', 'Waiting'], ['sending', 'Being sent'], ['parked', 'Parked'], ['unknown', 'Effect unknown']];
function activityHTML(s) {
  const act = s.activity;
  if (!act) return '';
  let html = '<div data-activity><h4>Wrote since sent</h4>';
  if (act.unavailable) return html + '<p>' + esc(act.unavailable) + '</p></div>';
  if (!act.count) return html + '<p>Nothing from ' + esc(s.to) + ' since this left, ' + esc(when(act.since_ms)) + '.</p></div>';
  html += '<p>' + plural(act.count, 'message', 'messages') + ' from ' + esc(s.to) + ' since this left, ' + esc(when(act.since_ms)) + '. ' +
    'This is activity, not an answer: whether any of it answers this message is the identity’s judgement, not this page’s.</p>';
  html += (act.latest || []).map(a => '<div class="item" data-activity-item="' + esc(a.id) + '">' + esc(when(a.received_ms)) + ' on ' + esc(a.channel) + bodyHTML(a.body, a.body_chars) + '</div>').join('');
  if (act.count > (act.latest || []).length) html += '<p class="muted">' + plural(act.count - act.latest.length, 'earlier one is', 'earlier ones are') + ' not quoted here; see Arrivals.</p>';
  return html + '</div>';
}
function sendHTML(s) {
  let html = '<div class="item" data-send="' + esc(s.id) + '" data-state="' + esc(s.state) + '"><div>To <b>' + esc(s.to) + '</b>, written ' + esc(when(s.created_ms)) + '</div>' + bodyHTML(s.body, s.body_chars);
  if (s.state === 'delivered') {
    html += '<p data-delivered>Delivered via ' + esc(s.via || 'an unrecorded adapter') + (s.sent_ms ? ', sent ' + esc(when(s.sent_ms)) : '') + '.</p>';
  }
  if (s.note) html += '<p>' + esc(s.note) + '</p>';
  if (s.answer) html += '<p>The last answer, in the adapter’s words:</p><pre style="white-space:pre-wrap;overflow-wrap:anywhere" data-answer>' + esc(s.answer) + '</pre>';
  return html + activityHTML(s) + '</div>';
}

// A notice's route: the page took it, or a walk off the dashboard claimed
// it — through which adapter, when one is recorded — or neither yet.
function noticeRoute(n) {
  if (n.route === 'page') return 'Route: a page' + (n.delivered_at ? ', ' + when(Date.parse(n.delivered_at)) : '') + '.';
  if (n.route === 'channel') return 'Route: off the dashboard' + (n.via ? ' via ' + n.via : '') + (n.sent_ms ? ', sent ' + when(n.sent_ms) : '') + '.';
  return 'Route: not taken yet.';
}
function noticeHTML(n) {
  let html = '<div class="item" data-notice="' + esc(n.id) + '" data-route="' + esc(n.route || '') + '" data-state="' + esc(n.state) + '"><div>Written ' + esc(when(n.created_ms)) + '. <span data-notice-route>' + esc(noticeRoute(n)) + '</span></div>' + bodyHTML(n.body, n.body_chars);
  if (n.note) html += '<p data-notice-note>' + esc(n.note) + '</p>';
  if (n.answer) html += '<p>The last answer, in the adapter’s words:</p><pre style="white-space:pre-wrap;overflow-wrap:anywhere" data-answer>' + esc(n.answer) + '</pre>';
  return html + '</div>';
}

export function messagesHTML() {
  const busy = pending.waiting() ? ' disabled' : '';
  let html = '<div class="card" data-messages><h3>MESSAGES</h3><p>What came in on the channels and what the identity sent to people. Reading this page changes nothing.</p>' +
    '<p data-messages-notice data-announce>' + esc(notice) + '</p><button class="btn" data-messages-refresh' + busy + '>Refresh</button></div>';
  if (!state) return html;
  const arrivals = state.arrivals || [], open = state.open || [], delivered = state.delivered || [];
  html += '<div class="card" data-arrivals><h3>ARRIVALS</h3>' +
    (arrivals.length ? arrivals.map(arrivalHTML).join('') : '<p class="muted">Nothing has arrived.</p>') + omitted(arrivals.length, state.arrivals_total, 'arrivals') + '</div>';
  html += '<div class="card" data-open-sends><h3>NOT DELIVERED</h3>' +
    (open.length ? STATES.map(([id, label]) => {
      const rows = open.filter(s => s.state === id);
      return rows.length ? '<section data-send-state="' + id + '"><h4>' + label + '</h4>' + rows.map(sendHTML).join('') + '</section>' : '';
    }).join('') : '<p class="muted">Every message the identity sent to a person has been delivered.</p>') +
    omitted(open.length, state.open_total, 'messages not delivered', 'oldest') + '</div>';
  html += '<div class="card" data-delivered-sends><h3>DELIVERED</h3><p>Delivered means an adapter took the message, not that a person read it.</p>' +
    (delivered.length ? delivered.map(sendHTML).join('') : '<p class="muted">Nothing delivered yet.</p>') +
    omitted(delivered.length, state.delivered_total, 'delivered messages') + '</div>';
  const notices = state.notices || [];
  html += '<div class="card" data-notices><h3>YOUR NOTICES</h3><p>What the identity and the host addressed to you, and the route each took: a page, or off the dashboard to the lines marked as you while no page was open. A page that was sent one did not necessarily show it to anyone, and one of unknown effect stays unknown here.</p>' +
    (notices.length ? notices.map(noticeHTML).join('') : '<p class="muted">No notices yet.</p>') +
    omitted(notices.length, state.notices_total, 'notices') + '</div>';
  return html;
}
export function wireMessages(root) {
  root.querySelectorAll('[data-messages-refresh]').forEach(b => { b.onclick = () => requestMessages(); });
  root.querySelectorAll('[data-messages-more]').forEach(b => { b.disabled = !!pending.waiting(); b.onclick = () => requestMessages(true); });
}
