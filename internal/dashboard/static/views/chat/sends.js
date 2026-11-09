import { S } from '../../state.js';
import { announce } from '../../announce.js';
import { setText } from './dom.js';
import { asksConnectionLost } from './asks.js';
import { chatSurface, copyButton, sysLine, isLocation, currentPages, currentNavigate } from './thread.js';

const pendingMessages = new Map();
const MOMENT_MS = 1000;
export const pendingCount = () => pendingMessages.size;
export function showPendingMessage(id, text) {
  const el = document.createElement('details'); el.className = 'chat-pending'; el.dataset.requestId = id;
  const title = document.createElement('summary'); title.textContent = 'Sending — awaiting confirmation';
  const body = document.createElement('div'); body.className = 'body'; body.textContent = text;
  const dismiss = document.createElement('button'); dismiss.type = 'button'; dismiss.textContent = 'Dismiss';
  dismiss.onclick = () => { pendingMessages.delete(id); el.remove(); };
  el.append(title, body, copyButton(text), dismiss);
  pendingMessages.set(id, { el, identity: currentPages.get('recorded')?.identity || currentPages.get('transient')?.identity || '', ack: '', receipt: null, lost: false });
  chatSurface('chat-pending').appendChild(el);
}
function pendingText(p, text, moment) {
  p.told = !moment;
  const title = p.el.querySelector('summary');
  if (title.textContent !== text) announce(text);
  setText(title, text);
}
function keptText(p, text) {
  p.el.open = true;
  pendingText(p, text);
}
export function viewReference(ref) {
  const page = currentPages.get(ref.source);
  if (!page || page.identity !== ref.identity || page.incarnation !== ref.incarnation) {
    sysLine('This reference belongs to a different or unavailable history source. Refresh the current view.'); return;
  }
  currentNavigate?.(ref.source, 'latest');
}
S.chatAcknowledged = (id, state) => {
  const p = pendingMessages.get(id); if (!p || p.lost) return;
  if (state !== 'accepted' && state !== 'refused') { pendingText(p, 'Confirmation unavailable — invalid admission reply'); return; }
  if (p.receipt) {
    if (state === 'refused' && p.receipt.error?.code !== 'CHAT_NOT_RECORDED') pendingText(p, 'Conflicting recording confirmations — check history');
    return;
  }
  p.ack = state;
  if (state === 'refused') S.reconcileTurn?.();
  if (state === 'accepted') setTimeout(() => { if (pendingMessages.get(id) === p && !p.told) pendingText(p, 'Still saving your message…', true); }, MOMENT_MS);
  else keptText(p, (p.refusal ? 'Not accepted: ' + p.refusal : 'Not accepted') + ' — your text is kept below');
};
S.chatRefused = (id, text) => {
  const p = id && pendingMessages.get(id);
  if (!p || p.lost) return false;
  p.refusal = text;
  return true;
};
function receiptKey(r) { return JSON.stringify([r.ref?.identity, r.ref?.source, r.ref?.incarnation, r.ref?.id, r.error?.code, r.error?.message]); }
function validReceipt(r) {
  if (!r || typeof r !== 'object' || (!r.ref && !r.error)) return false;
  const ref = r.ref, err = r.error;
  if (Object.hasOwn(r, 'ref') && !ref || Object.hasOwn(r, 'error') && !err) return false;
  if (ref && !isLocation(ref)) return false;
  if (err && (typeof err !== 'object' || typeof err.message !== 'string' || !err.message.length || !['CHAT_NOT_RECORDED','CHAT_RECORDING_FAILED'].includes(err.code))) return false;
  return !err || !ref || ref.source === 'transient' && err.code === 'CHAT_RECORDING_FAILED';
}
S.chatReceipt = (id, r) => {
  const p = pendingMessages.get(id); if (!p || p.lost) return;
  if (!validReceipt(r) || r.ref && p.identity && p.identity !== r.ref.identity) {
    pendingText(p, 'Confirmation unavailable — invalid recording reply'); return;
  }
  if (p.receipt) {
    if (receiptKey(p.receipt) !== receiptKey(r)) pendingText(p, 'Conflicting recording confirmations — check history');
    return;
  }
  if (p.ack === 'refused' && r.error?.code !== 'CHAT_NOT_RECORDED') {
    p.receipt = r; pendingText(p, 'Conflicting recording confirmations — check history'); return;
  }
  p.receipt = r;
  if (r.error) {
    if (r.error.code === 'CHAT_NOT_RECORDED') S.reconcileTurn?.();
    pendingText(p, (r.error.code === 'CHAT_NOT_RECORDED' ? 'Not recorded: ' : 'Recording unconfirmed: ') + r.error.message + (r.ref ? ' · transient copy, not saved' : ''));
    const detail = document.createElement('pre'); detail.textContent = JSON.stringify(r, null, 2); p.el.appendChild(detail);
    if (r.ref) {
      const view = document.createElement('button'); view.type = 'button'; view.textContent = 'Latest transient entries'; view.onclick = () => viewReference(r.ref); p.el.appendChild(view);
    }
    return;
  }
  pendingMessages.delete(id); p.el.remove();
  if (S.interactionChanged) S.interactionChanged();
};
S.chatConnectionLost = () => {
  pendingMessages.forEach(p => {
    if (!p.receipt && p.ack !== 'refused') {
      if (p.lost) pendingText(p, 'Connection lost — delivery unconfirmed; your text is kept below');
      else keptText(p, 'Connection lost — delivery unconfirmed; your text is kept below');
    }
    p.lost = true;
  });
  asksConnectionLost();
};
