import { S } from '../state.js';
import { esc } from '../util.js';
import { send } from '../ws.js';
import { pendingSlot } from '../pending.js';

const pending = pendingSlot();
let state = { messages: [], routes: [] };
let cursor = '';
let notice = 'Refresh to inspect held historical mail. Viewing never sends it.';
const render = () => { if (S.renderMailRepair) S.renderMailRepair(); };
function ask(payload) {
  if (pending.waiting()) return;
  if (!pending.arm(payload, send(payload))) { notice = 'Not connected; nothing was requested.'; render(); return; }
  notice = 'Waiting for the runtime to confirm…'; render();
}
export function requestHeldMail(after = '') {
  cursor = after; ask({ type: 'query', query: 'held_mail', name: after });
}
export function acceptMailRepair(reply) {
  if (!pending.claim(reply.request_id)) return false;
  const value = reply.mail_repair || {};
  state = { ...value, messages: value.messages || [], routes: value.routes || [] };
  notice = state.queued ? 'Correction saved; ' + state.queued + ' is queued on the chosen channel. Delivery is not yet confirmed.' : (state.messages.length ? 'Only held historical mail is shown.' : 'No held mail on this page.');
  render(); return true;
}
export function rejectMailRepair(requestID, text) {
  if (!pending.claim(requestID)) return false;
  notice = text; render(); return true;
}
export function mailRepairDisconnected() {
  if (pending.drop()) { notice = 'Connection lost before confirmation. Refresh to learn the actual state; nothing is automatically retried.'; render(); }
}
export function mailRepairHTML() {
  const disabled = pending.waiting() ? ' disabled' : '';
  return '<div class="card"><h3>HELD MAIL</h3><p>Correct one historical message using a recipient and channel already in your current contacts. This queues that message on the chosen channel; it does not change contacts or claim delivery. Messages already attempted or of uncertain effect cannot be repaired here.</p>' +
    '<p data-mail-notice data-announce>' + esc(notice) + '</p><button class="btn" data-mail-refresh' + disabled + '>Refresh from first page</button>' +
    (state.next ? '<button class="btn" data-mail-next' + disabled + '>Next page</button>' : '') +
    state.messages.map((m, i) => '<section data-held-message><h4>' + esc(m.id) + '</h4><p>Recorded recipient: ' + esc(m.recipient) + '; requested channel: ' + esc(m.channel || 'none') + '. ' + esc(m.reason) + '</p><pre style="white-space:pre-wrap;overflow-wrap:anywhere">' + esc(m.preview) + '</pre>' +
      (m.truncated ? '<p>Preview truncated.</p><button class="btn" data-mail-inspect="' + i + '"' + disabled + '>Read complete message</button>' : '') +
      '<label>Correct recipient and channel: <select data-mail-route="' + i + '"' + disabled + '><option value="">Choose an existing contact route</option>' +
      state.routes.map((r, n) => '<option value="' + n + '">' + esc(r.name + ' via ' + r.channel) + '</option>').join('') + '</select></label>' +
      '<button class="btn" data-mail-repair="' + i + '"' + disabled + '>Correct and queue</button></section>').join('') + '</div>';
}
export function wireMailRepair(root) {
  const refresh = root.querySelector('[data-mail-refresh]');
  if (refresh) refresh.onclick = () => requestHeldMail();
  const next = root.querySelector('[data-mail-next]');
  if (next) next.onclick = () => requestHeldMail(state.next);
  root.querySelectorAll('[data-mail-inspect]').forEach(button => { button.onclick = () => ask({ type: 'inspect_mail', name: cursor, mail_repair: { id: state.messages[Number(button.dataset.mailInspect)].id } }); });
  root.querySelectorAll('[data-mail-repair]').forEach(button => {
    button.onclick = () => {
      const i = Number(button.dataset.mailRepair);
      const selected = root.querySelector('[data-mail-route="' + i + '"]').value;
      if (selected === '') { notice = 'Choose the intended recipient and channel; nothing changed.'; render(); return; }
      const route = state.routes[Number(selected)], message = state.messages[i];
      ask({ type: 'repair_mail', name: cursor, mail_repair: { id: message.id, sha256: message.sha256, recipient: route.name, channel: route.channel } });
    };
  });
}
