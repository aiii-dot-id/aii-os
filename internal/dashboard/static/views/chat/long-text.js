import { S } from '../../state.js';
import { $ } from '../../util.js';
import { ownsThread } from '../../registry.js';
import { signedFetch } from '../../pagekey.js';
import { drawReply } from '../attach.js';
import { setText, setData } from './dom.js';
import { setWords } from '../../components/in-words/in-words.js';
import { messageChange, holdReading } from './thread.js';

const detailLimit = 16 * 1024 * 1024;
export async function retainedText(ref, check, signal) {
  if (!Number.isSafeInteger(ref.bytes) || ref.bytes < 1) throw Error('Invalid retained-text reference.');
  if (ref.bytes > detailLimit) throw Error('Message is too large to display here.');
  let offset = 0; const buffers = [];
  for (;;) {
    check();
    const params = new URLSearchParams({ id: ref.id, source: ref.source, incarnation: ref.incarnation, sha256: ref.sha256, offset: String(offset) });
    const response = await signedFetch('/interaction/detail?' + params, { cache: 'no-store', credentials: 'same-origin', signal });
    check();
    if (!response.ok) throw Error('The retained text is unavailable.');
    const chunk = await response.json(); check();
    if (chunk.version !== 1 || chunk.id !== ref.id || chunk.source !== ref.source || chunk.offset !== offset ||
        chunk.sha256 !== ref.sha256 || chunk.incarnation !== ref.incarnation || chunk.size !== ref.bytes) throw Error('Recorded text changed; refresh history.');
    if (!Number.isInteger(chunk.bytes) || chunk.bytes < 0 || chunk.bytes > 16384 || typeof chunk.eof !== 'boolean' || typeof chunk.data_b64 !== 'string' || chunk.data_b64.length > 21848) throw Error('Invalid text range.');
    const bytes = Uint8Array.from(atob(chunk.data_b64), c => c.charCodeAt(0));
    if (bytes.length !== chunk.bytes || !chunk.eof && !bytes.length || offset + bytes.length > ref.bytes) throw Error('Invalid text range.');
    buffers.push(bytes); offset += bytes.length;
    if (chunk.eof) { if (offset !== ref.bytes) throw Error('Incomplete retained text.'); break; }
  }
  const all = new Uint8Array(offset); let pos = 0;
  for (const bytes of buffers) { all.set(bytes, pos); pos += bytes.length; }
  return new TextDecoder('utf-8', { fatal: true }).decode(all);
}
export function detailReader(ref) {
  const box = document.createElement('div'); box.className = 'detail-read';
  const button = document.createElement('button'); button.type = 'button'; button.textContent = 'Read full retained detail';
  const status = document.createElement('span'); box.append(button, status);
  button.onclick = async () => {
    holdReading();
    button.disabled = true; setText(status, 'Loading…');
    try {
      const text = await retainedText(ref, () => {
        if (document.visibilityState === 'hidden' || !box.isConnected) throw Error('Detail paused; reopen while this entry is visible.');
      });
      const pre = document.createElement('pre'); pre.textContent = text;
      box.appendChild(pre); button.remove(); setText(status, '');
    } catch (e) { setText(status, String(e.message || e)); button.disabled = false; }
  };
  return box;
}
function drawBody(body, role, text) {
  if (role === 'resident') { if (!drawReply(body, text)) setWords(body, text); return; }
  if (body.dataset.words != null) { delete body.dataset.words; body.textContent = ''; }
  setText(body, text);
}
export const messageTextState = new WeakMap();
const unreadMessages = new Set();
let activeMessageRead = null;
const messageObserver = typeof IntersectionObserver === 'function'
  ? new IntersectionObserver(() => refreshMessageText(), { root: $('thread') }) : null;
function watchMessage(msg) { unreadMessages.add(msg); messageObserver?.observe(msg); }
function unwatchMessage(msg) { unreadMessages.delete(msg); messageObserver?.unobserve(msg); }
function messageVisible(msg) {
  if (!msg.isConnected || document.visibilityState === 'hidden' || S.view && !ownsThread(S.view)) return false;
  const viewport = $('thread').getBoundingClientRect(), box = msg.getBoundingClientRect();
  return viewport.height > 0 && box.height > 0 && box.bottom > viewport.top && box.top < viewport.bottom;
}
export function longMessage(msg, r, key) {
  const long = !r.content && !!r.content_ref, body = msg.querySelector('.body'), copy = msg.querySelector('.copyb');
  msg.classList.toggle('long-message', long);
  if (!long) {
    messageTextState.delete(msg); unwatchMessage(msg); msg.querySelector('.message-load')?.remove();
    drawBody(body, r.role, r.content || '');
    if (copy) { copy.hidden = !r.content; setData(copy, 'copyText', r.content || ''); }
    return;
  }
  const signature = JSON.stringify([key, r.content_ref]);
  if (messageTextState.get(msg)?.signature === signature) return;
  const state = { signature, ref: { ...r.content_ref }, kind: r.kind, role: r.role, loaded: false, error: false };
  messageTextState.set(msg, state); drawBody(body, r.role, '');
  if (copy) { copy.hidden = true; setData(copy, 'copyText', ''); }
  msg.querySelector('.message-load')?.remove();
  const loader = document.createElement('div'); loader.className = 'message-load detail-read';
  const status = document.createElement('span'); status.textContent = 'Loading message…';
  const retry = document.createElement('button'); retry.type = 'button'; retry.textContent = 'Retry'; retry.hidden = true;
  retry.onclick = () => {
    if (messageTextState.get(msg) !== state) return;
    state.error = false; retry.hidden = true; status.textContent = 'Loading message…'; watchMessage(msg); refreshMessageText();
  };
  loader.append(status, retry); msg.querySelector('.body').after(loader); watchMessage(msg);
}
export function refreshMessageText() {
  if (activeMessageRead && !activeMessageRead.current()) activeMessageRead.controller.abort();
  if (unreadMessages.size) queueMicrotask(pumpMessageText);
}
S.refreshMessageText = refreshMessageText;
document.addEventListener('visibilitychange', refreshMessageText);
$('thread')?.addEventListener('scroll', refreshMessageText, { passive: true });
window.addEventListener('resize', refreshMessageText, { passive: true });
async function pumpMessageText() {
  for (const msg of unreadMessages) if (!msg.isConnected) unwatchMessage(msg);
  if (activeMessageRead || document.visibilityState === 'hidden' || S.view && !ownsThread(S.view)) return;
  const msg = [...unreadMessages].find(messageVisible); if (!msg) return;
  const state = messageTextState.get(msg); if (!state || state.loaded || state.error) { unwatchMessage(msg); return; }
  const controller = new AbortController(); let timedOut = false;
  const current = () => messageTextState.get(msg) === state && messageVisible(msg);
  const check = () => { if (controller.signal.aborted || !current()) throw new DOMException('Message read paused', 'AbortError'); };
  activeMessageRead = { msg, controller, current };
  const timer = setTimeout(() => { timedOut = true; controller.abort(); }, 30000);
  try {
    const retained = [...$('thread-inner').querySelectorAll('.long-message')].reduce((n, el) => {
      const s = messageTextState.get(el); return n + (s?.loaded ? s.ref.bytes : 0);
    }, 0);
    if (retained + state.ref.bytes > detailLimit) throw Error("This view's full-text limit was reached.");
    const record = JSON.parse(await retainedText(state.ref, check, controller.signal)); check();
    if (record.id !== state.ref.id || record.kind !== state.kind || record.role !== state.role || typeof record.content !== 'string') throw Error('Retained text does not match this message.');
    messageChange(() => {
      state.loaded = true; drawBody(msg.querySelector('.body'), state.role, record.content); msg.querySelector('.message-load')?.remove();
      const copy = msg.querySelector('.copyb'); if (copy) { copy.hidden = !record.content; setData(copy, 'copyText', record.content); }
    });
    unwatchMessage(msg);
  } catch (e) {
    if (current() && (timedOut || e.name !== 'AbortError')) {
      state.error = true; unwatchMessage(msg);
      messageChange(() => {
        const loader = msg.querySelector('.message-load');
        setText(loader?.firstElementChild, timedOut ? 'Message took too long to load. Retry.' : 'Message unavailable: ' + (e.message || e));
        if (loader) loader.querySelector('button').hidden = false;
      });
    }
  } finally {
    clearTimeout(timer); activeMessageRead = null; refreshMessageText();
  }
}
