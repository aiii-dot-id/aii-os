import { S } from '../../state.js';
import { setWords } from '../../components/in-words/in-words.js';
import { locationKey } from '../../interaction-display.js';
import { appendThread, scrollThread, currentPages, workOfTurn } from './thread.js';
import W from '../../strings/chat-draft.js';
import CHAT from '../../strings/chat.js';

document.head.append(Object.assign(document.createElement('link'), { rel: 'stylesheet', href: new URL('./draft.css', import.meta.url).href }));

let draft = null;
let el = null;
const settled = new Set();
const notes = new Map();
const SETTLED_KEPT = 32;

const refKey = r => JSON.stringify([r.identity || '', r.source || '', r.incarnation || '', r.id || '']);
const theirs = () => { const n = S.stats?.name || ''; return n && n !== 'Unnamed' ? n : CHAT.yourIdentity; };
const named = s => s.replace('{name}', theirs());

export function receiveDraft(d) {
  if (!d || !d.ref) return;
  const key = refKey(d.ref);
  if (settled.has(key)) return;
  if (draft && draft.key !== key) {
    if (!draft.end) return;
    clear();
  }
  if (!draft) {
    if (!d.snapshot) return;
    draft = { key, seq: 0, call: 0, kept: '', current: '', withdrawals: [], hidden: 0, end: '' };
  }
  if (d.seq < draft.seq || (d.seq === draft.seq && draft.end !== 'lost')) return;
  if (d.snapshot) {
    Object.assign(draft, { seq: d.seq, call: d.call || 0, kept: d.kept || '', current: d.add || '', withdrawals: d.withdrawals || [], hidden: d.hidden || 0, end: d.end || '' });
  } else {
    if (d.seq !== draft.seq + 1 || d.call !== draft.call) return;
    draft.seq = d.seq;
    draft.current += d.add || '';
    draft.hidden = d.hidden || 0;
  }
  if (!settle(currentPages, null)) draw();
}

function draw() {
  if (!document.getElementById('thread-inner')) return;
  if (!el) {
    el = document.createElement('div');
    el.className = 'msg identity reply-draft';
    el.dataset.replyDraft = '';
    el.innerHTML = '<div class="who"></div><div class="body"></div><div class="reply-draft__more"></div><div class="reply-draft__withdrawn"></div>';
  }
  el.querySelector('.who').textContent = theirs();
  const text = draft.kept + (draft.kept && draft.current ? '\n\n' : '') + draft.current;
  setWords(el.querySelector('.body'), text);
  el.querySelector('.reply-draft__more').textContent = draft.hidden ? W.more : '';
  withdrawnInto(el.querySelector('.reply-draft__withdrawn'), draft.withdrawals);
  if (draft.end) { el.removeAttribute('aria-busy'); el.removeAttribute('aria-label'); }
  else { el.setAttribute('aria-busy', 'true'); el.setAttribute('aria-label', named(W.writing)); }
  if (!el.isConnected) appendThread(el, workOf(currentPages)); else scrollThread();
}

const workOf = pages => { const turn = turnOf(pages, draft?.key); return turn ? workOfTurn(turn) || null : null; };

function withdrawnInto(box, withdrawals) {
  const parts = [];
  for (const { words, why } of withdrawals) {
    const del = document.createElement('del');
    del.textContent = words;
    const p = document.createElement('p');
    p.className = 'reply-draft__why';
    p.textContent = named(W.why[why] || W.why.provider);
    parts.push(del, p);
  }
  box.replaceChildren(...parts);
}

function clear() {
  if (el) el.remove();
  el = null;
  draft = null;
}

export function lost() {
  if (!draft || draft.end) return;
  draft.end = 'lost';
  if (!settle(currentPages, null)) draw();
}

function settle(pages, batch) {
  if (!draft?.end) return false;
  const reply = replyOf(pages, draft.key);
  if (!reply) return false;
  noteAbove(reply, draft.withdrawals, batch);
  if (draft.end === 'lost') {
    if (el) el.remove();
    el = null;
    return true;
  }
  settled.add(draft.key);
  if (settled.size > SETTLED_KEPT) settled.delete(settled.values().next().value);
  clear();
  return true;
}

function noteAbove(reply, withdrawals, batch) {
  if (!withdrawals.length) return;
  let note = notes.get(reply);
  if (!note) {
    note = document.createElement('div');
    note.className = 'reply-draft__withdrawn reply-draft__settled';
    notes.set(reply, note);
  }
  if (note.childElementCount !== 2 * withdrawals.length) withdrawnInto(note, withdrawals);
  const at = batch ? batch.findIndex(n => n.dataset?.interactionKey === reply) : -1;
  if (at >= 0) { if (!batch.includes(note)) batch.splice(at, 0, note); }
  else if (!note.isConnected) document.querySelector('[data-interaction-key="' + CSS.escape(reply) + '"]')?.before(note);
}

S.draftRows = (pages, batch) => {
  for (const [key, note] of notes) {
    const at = batch.findIndex(n => n.dataset?.interactionKey === key);
    if (at < 0) notes.delete(key); else batch.splice(at, 0, note);
  }
  if (!draft || settle(pages, batch) || !el) return;
  const at = batch.indexOf(workOf(pages));
  if (at >= 0) batch.splice(at, 0, el); else batch.push(el);
};
S.draftTurnBegins = () => { if (draft?.end) clear(); };

function turnOf(pages, key) {
  for (const source of ['recorded', 'transient']) {
    const page = pages?.get(source);
    const asked = (page?.rows || []).find(r => refKey({ identity: page.identity, source, incarnation: page.incarnation, id: r.id }) === key);
    if (asked?.turn_id) return asked.turn_id;
  }
  return '';
}

function replyOf(pages, key) {
  for (const source of ['recorded', 'transient']) {
    const page = pages?.get(source);
    const rows = page?.rows || [];
    const asked = rows.find(r => refKey({ identity: page.identity, source, incarnation: page.incarnation, id: r.id }) === key);
    if (!asked?.turn_id) continue;
    const reply = rows.find(r => r.turn_id === asked.turn_id && r.role === 'resident' && r.kind === 'message');
    if (reply) return locationKey(source, page, reply.id);
  }
  return '';
}
