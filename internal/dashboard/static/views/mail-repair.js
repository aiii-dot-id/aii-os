import { S } from '../state.js';
import { esc, fill } from '../util.js';
import { send } from '../ws.js';
import { pendingSlot } from '../pending.js';
import { say } from './settings/people-say.js';
import W from '../strings/settings-people.js';

const pending = pendingSlot();
let state = { messages: [], routes: [] };
let cursor = '';
let routeChosen = null;
let notice = W.unsentHow;
const render = () => { if (S.renderMailRepair) S.renderMailRepair(); };
function ask(payload) {
  if (pending.waiting()) return;
  if (!pending.arm(payload, send(payload))) { notice = W.notConnected; render(); return; }
  notice = 'Waiting for the runtime to confirm…'; render();
}
export function requestHeldMail(after = '') {
  cursor = after; ask({ type: 'query', query: 'held_mail', name: after });
}
export function acceptMailRepair(reply) {
  if (!pending.claim(reply.request_id)) return false;
  const value = reply.mail_repair || {};
  state = { ...value, messages: value.messages || [], routes: value.routes || [] };
  notice = state.queued ? fill(W.queued, { contact: routeChosen?.name || state.queued, channel: routeChosen?.channel || '' }) : (state.messages.length ? W.onlyHeld : W.unsentNone);
  render(); return true;
}
export function rejectMailRepair(requestID, text) {
  if (!pending.claim(requestID)) return false;
  notice = text; render(); return true;
}
export function mailRepairDisconnected() {
  if (pending.drop()) { notice = 'Connection lost before confirmation. Refresh to learn the actual state; nothing is automatically retried.'; render(); }
}
let open = false;
export function openUnsent() { open = true; }
export function mailRepairHTML() {
  const disabled = pending.waiting() ? ' disabled' : '', n = state.messages.length;
  const count = n === 1 ? W.unsentCountOne : n ? fill(W.unsentCount, { n }) : W.unsentNone;
  return '<details class="settings-people__more" data-unsent' + (open ? ' open' : '') + '><summary>' + esc(W.unsent) + ' <span class="settings-people__more-hint">· ' + esc(count) + '</span></summary><div class="settings-people__more-body">' +
    '<p class="settings-people__hint">' + esc(say(W.unsentHint)) + ' ' + esc(W.unsentHow) + '</p>' +
    '<p class="settings-people__said" data-mail-notice data-announce>' + esc(notice) + '</p>' +
    '<div class="settings-people__acts"><button type="button" class="c-button c-button--quiet" data-mail-refresh' + disabled + '>' + esc(W.firstPage) + '</button>' +
    (state.next ? '<button type="button" class="c-button c-button--quiet" data-mail-next' + disabled + '>' + esc(W.nextPage) + '</button>' : '') + '</div>' +
    state.messages.map((m, i) => '<section class="settings-people__part" data-held-message><p class="settings-people__to"><span class="settings-people__who">' + esc(fill(W.unsentRow, { name: m.recipient })) + '</span></p>' +
      '<p class="settings-people__hint">' + esc(fill(W.unsentWhy, { name: m.recipient })) + ' ' + esc(m.reason) + '</p><pre class="settings-people__body">' + esc(m.preview) + '</pre>' +
      (m.truncated ? '<p class="settings-people__hint">Preview truncated.</p><div class="settings-people__acts"><button type="button" class="c-button c-button--quiet" data-mail-inspect="' + i + '"' + disabled + '>' + esc(W.readIt) + '</button></div>' : '') +
      '<div class="settings-people__line"><select class="settings-people__select" data-mail-route="' + i + '" aria-label="' + esc(W.chooseRecipient) + '"' + disabled + '><option value="">' + esc(W.chooseRoute) + '</option>' +
      state.routes.map((r, k) => '<option value="' + k + '">' + esc(fill(W.sendTo, { contact: r.name, channel: r.channel })) + '</option>').join('') + '</select>' +
      '<button type="button" class="c-button c-button--secondary" data-mail-repair="' + i + '"' + disabled + '>' + esc(W.queue) + '</button></div></section>').join('') + '</div></details>';
}
export function wireMailRepair(root) {
  root.querySelectorAll('[data-unsent]').forEach(d => { d.ontoggle = () => { open = d.open; }; });
  const refresh = root.querySelector('[data-mail-refresh]');
  if (refresh) refresh.onclick = () => requestHeldMail();
  const next = root.querySelector('[data-mail-next]');
  if (next) next.onclick = () => requestHeldMail(state.next);
  root.querySelectorAll('[data-mail-inspect]').forEach(button => { button.onclick = () => ask({ type: 'inspect_mail', name: cursor, mail_repair: { id: state.messages[Number(button.dataset.mailInspect)].id } }); });
  root.querySelectorAll('[data-mail-repair]').forEach(button => {
    button.onclick = () => {
      const i = Number(button.dataset.mailRepair);
      const selected = root.querySelector('[data-mail-route="' + i + '"]').value;
      if (selected === '') { notice = W.chooseRecipient; render(); return; }
      const route = state.routes[Number(selected)], message = state.messages[i];
      routeChosen = route;
      ask({ type: 'repair_mail', name: cursor, mail_repair: { id: message.id, sha256: message.sha256, recipient: route.name, channel: route.channel } });
    };
  });
}
