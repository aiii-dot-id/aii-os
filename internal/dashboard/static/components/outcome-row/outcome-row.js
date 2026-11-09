import { h, fill } from '../../util.js';
import * as osNote from '../os-note/os-note.js';
import W from '../../strings/chat.js';

const KIND = { recall: 'recalled', ls: 'folder', grep: 'search', read: 'read', web_fetch: 'page', write: 'wrote', edit: 'edited', shell: 'ran', exec: 'ran', run: 'ran', send: 'sent', send_message: 'sent' };
const WORK = { 'alarm.set': 'reminder', 'alarm.cancel': 'unset' };
const kindOf = it => (it.tool === 'work' ? WORK[it.args?.action] : KIND[it.tool]) || 'other';
const PLURAL = {
  recalled: ['outcomeRecalledThing', 'outcomeRecalledThings'], folder: ['outcomeLookedInFolder', 'outcomeLookedInFolders'],
  search: ['outcomeSearchedOnce', 'outcomeSearched'], read: ['outcomeReadFile', 'outcomeReadFiles'], page: ['outcomeReadPage', 'outcomeReadPages'],
  wrote: ['outcomeWroteFile', 'outcomeWroteFiles'], edited: ['outcomeEditedFile', 'outcomeEditedFiles'], ran: ['outcomeRanCommand', 'outcomeRanCommands'],
  reminder: ['outcomeSetReminder', 'outcomeSetReminders'], unset: ['outcomeCancelledReminder', 'outcomeCancelledReminders'], sent: ['outcomeSentMessage', 'outcomeSentMessages'],
};

export function itemsWords(items) {
  const n = { other: 0 };
  for (const it of items) { const k = kindOf(it); n[k] = (n[k] || 0) + 1; }
  const parts = [];
  for (const k of Object.keys(PLURAL)) if (n[k]) parts.push(n[k] === 1 ? W[PLURAL[k][0]] : fill(W[PLURAL[k][1]], { n: n[k] }));
  if (n.other) parts.push(n.other === 1 ? W.outcomeOtherStep : fill(W.outcomeOther, { n: n.other }));
  return parts.join(' · ');
}

export function done(items, qualifications = []) {
  return items.length > 0 && !qualifications.length && items.every(it => it.outcome === 'succeeded' && it.receipt);
}

const STEP = { recalled: 'stepRecall', folder: 'stepFolder', search: 'stepSearch', read: 'stepRead', page: 'stepPage', wrote: 'stepWrite', edited: 'stepEdit', ran: 'stepRun', reminder: 'stepReminder', unset: 'stepUnset', sent: 'stepSend' };
export const stepWords = it => W[STEP[kindOf(it)]] || '';
export function noteFor(it) {
  const named = stepWords(it), tool = named || (it.tool === 'work' ? '' : it.tool) || 'A step', reason = it.reason || '';
  switch (it.outcome) {
    case 'failed': return { tone: 'bad', text: fill(W.outcomeFailed, { tool, reason }) };
    case 'refused': return { tone: 'bad', text: fill(W.outcomeRefused, { tool, reason }) };
    case 'unknown': return { tone: 'warn', text: fill(W.outcomeUnknown, { tool: named ? named[0].toLowerCase() + named.slice(1) : tool }) };
    case 'cancelled': return { tone: 'warn', text: fill(W.outcomeCancelled, { tool }) };
    default: return null;
  }
}

export function deliveryNote(d) {
  if (!d || d.delivered || d.effect === 'performed') return null;
  if (d.effect === 'unknown' && !d.via) return { tone: 'warn', text: W.sendStoppedIn };
  if (d.effect === 'unknown') return { tone: 'warn', text: fill(W.sendMayHaveLeft, { channel: d.via }) };
  if (d.parked) return { tone: 'bad', text: fill(W.sendParked, { n: d.attempts }) };
  return { tone: 'info', text: fill(W.sendQueued, { n: d.attempts }) };
}

const WORST = [['failed', 'outcomeLabelFailed'], ['refused', 'outcomeLabelRefused'], ['unknown', 'outcomeLabelUnknown'], ['cancelled', 'outcomeLabelCancelled']];
export function verdict(items = [], qualifications = []) {
  if (done(items, qualifications)) return { text: W.outcomeDone, tone: 'good' };
  const worst = WORST.find(([o]) => items.some(it => it.outcome === o));
  if (worst) return { text: [W[worst[1]], ...qualifications].join(' · '), tone: 'bad' };
  return { text: qualifications.join(' · '), tone: 'warn' };
}

export function summary({ actor = '', items = [], qualifications = [] } = {}) {
  const what = itemsWords(items);
  if (done(items, qualifications)) return fill(W.outcomeSummary, { actor, items: what });
  return [actor, what, verdict(items, qualifications).text].filter(Boolean).join(' · ');
}

export function fillLine(el, { actor = '', items = [], qualifications = [] } = {}) {
  const v = verdict(items, qualifications);
  const what = itemsWords(items);
  const sig = JSON.stringify([actor, what, v]);
  if (el.dataset.line === sig) return;
  el.dataset.line = sig;
  const sep = () => document.createTextNode(' · ');
  const parts = [];
  if (actor) parts.push(h('strong', { class: 'c-outcome-row__actor' }, actor));
  if (what) { if (parts.length) parts.push(sep()); parts.push(document.createTextNode(what)); }
  if (v.text) { if (parts.length) parts.push(sep()); parts.push(h('span', { class: 'c-outcome-row__verdict c-outcome-row__verdict--' + v.tone }, v.text)); }
  el.replaceChildren(...parts);
}

export function fillNotes(el, items = [], href = '') {
  const said = items.map(noteFor).filter(Boolean);
  const sig = JSON.stringify([said, href]);
  if (el.dataset.notes === sig) return;
  el.dataset.notes = sig;
  el.replaceChildren(...said.map(n => osNote.render(Object.assign({ live: false }, n))),
    ...(href ? [h('a', { class: 'c-outcome-row__receipt receipt-view', href }, W.viewReceipt)] : []));
}

export function receiptHref(items = []) {
  const recorded = items.filter(i => i.seq);
  const step = recorded.find(i => noteFor(i)) || recorded.at(-1);
  return step ? '#/identity/record/' + encodeURIComponent(step.seq) : '';
}

export function render(props = {}) {
  const el = h('details', { class: 'c-outcome-row' },
    h('summary', { class: 'c-outcome-row__line' }),
    h('div', { class: 'c-outcome-row__notes' }),
    h('div', { class: 'c-outcome-row__steps' }));
  update(el, props);
  return el;
}

export function update(el, { actor = '', items = [], qualifications = [], receipt = '', steps = [] } = {}) {
  const ok = done(items, qualifications);
  el.classList.toggle('c-outcome-row--open', !ok);
  if (!ok) el.open = true;
  fillLine(el.querySelector('.c-outcome-row__line'), { actor, items, qualifications });
  fillNotes(el.querySelector('.c-outcome-row__notes'), items, receipt || receiptHref(items));
  el.querySelector('.c-outcome-row__steps').replaceChildren(...steps);
}
