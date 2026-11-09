import { S } from '../../state.js';
import { $, h, fill } from '../../util.js';
import * as note from '../../components/note/note.js';
import { arrivalOfThisVisit } from '../../shell/visit.js';
import { firstEntryTime } from '../identity/record-read.js';
import W from '../../strings/home.js';

const arrival = arrivalOfThisVisit();
let seqKept = false;
function rememberThisVisit() {
  try {
    if (!rememberThisVisit.at) { localStorage.setItem('aii.lastSeenAt', String(Date.now())); rememberThisVisit.at = true; }
    const seq = S.cont && S.cont.ledger_seq;
    if (seq && !seqKept) { localStorage.setItem('aii.lastSeenSeq', String(seq)); seqKept = true; }
  } catch (err) { }
}

let born = 0;
const bornRead = () => { if (S.view === 'chat') renderHome(); S.renderBetweenAgain?.(); };
firstEntryTime().then(t => { born = t; bornRead(); }, () => { born = -1; bornRead(); });
const visit = () => !born ? { at: 0, seq: 0, known: false } : born > 0 && arrival.at && arrival.at < born ? { at: 0, seq: 0, known: true, before: true } : { ...arrival, known: true };

export const lastVisit = visit;

function noteProps(n, name) {
  const taken = [];
  for (const c of n.claims || []) {
    if (!c.sentence) continue;
    let first = null, free = null;
    for (let at = n.text.indexOf(c.sentence); at >= 0 && !free; at = n.text.indexOf(c.sentence, at + 1)) {
      const end = at + c.sentence.length, held = taken.find(t => at < t.end && end > t.start);
      if (!held) free = { start: at, end, entries: [], outcomes: [] };
      else first ||= held;
    }
    const t = free || first;
    if (!t) continue;
    if (free) taken.push(free);
    for (const e of c.entries || []) if (!t.entries.includes(e)) t.entries.push(e);
    for (const o of c.outcomes || []) if (!t.outcomes.includes(o)) t.outcomes.push(o);
  }
  taken.sort((a, b) => a.start - b.start);
  const home = new Map();
  for (const o of n.not_succeeded || []) {
    const t = taken.find(t => t.outcomes.includes(o.id));
    if (t) (home.get(t) || home.set(t, []).get(t)).push(o);
  }
  const at = iso => { const t = Date.parse(iso || ''); return isNaN(t) ? '' : new Date(t).toLocaleString([], { weekday: 'short', hour: 'numeric', minute: '2-digit' }); };
  const from = Date.parse(n.window_from || '');
  const when = isNaN(from) || Date.now() - from <= 25 * 3600000 ? W.noteSinceYesterday
    : new Date(from).toLocaleString([], { weekday: 'long', day: 'numeric', month: 'long', hour: 'numeric', minute: '2-digit' });
  const line = (o, words) => fill(words, { summary: o.summary, outcome: o.outcome, time: at(o.at), when });
  const segments = [];
  let cursor = 0;
  for (const t of taken) {
    segments.push({ text: n.text.slice(cursor, t.end), notes: (home.get(t) || []).map(o => line(o, W.noteNotDone)),
      where: t.entries.length ? { label: W.whereFrom, links: t.entries.map(e => ({ href: '#/identity/record/' + e, text: '#' + e })) } : null });
    cursor = t.end;
  }
  if (cursor < n.text.length) segments.push({ text: n.text.slice(cursor), notes: [], where: null });
  for (let i = 1; i < segments.length; i++) {
    const lead = /^\s+/.exec(segments[i].text);
    if (lead) { segments[i - 1].text += lead[0]; segments[i].text = segments[i].text.slice(lead[0].length); }
  }
  const placed = new Set([...home.values()].flat());
  const after = (n.not_succeeded || []).filter(o => !placed.has(o)).map(o => line(o, W.noteNotDoneAfter));
  if (n.not_succeeded_beyond) after.push(fill(W.noteNotDoneMoreBeyond, { n: n.not_succeeded_more || 0 }));
  else if (n.not_succeeded_more) after.push(n.not_succeeded_more === 1 ? W.noteNotDoneMoreOne : fill(W.noteNotDoneMore, { n: n.not_succeeded_more }));
  if (n.not_succeeded_unread) after.push(fill(W.noteNotDoneUnread, { reason: n.not_succeeded_unread, when }));
  if (n.unchecked) after.push(n.unchecked === 1 ? W.noteUncheckedOne : fill(W.noteUnchecked, { n: n.unchecked }));
  const t = Date.parse(n.written_at || ''), d = new Date(t), time = isNaN(t) ? '' : d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  const day = time && d.toDateString() !== new Date().toDateString() ? d.toLocaleDateString([], { weekday: 'long', month: 'long', day: 'numeric' }) : '';
  return { state: 'note', label: name ? fill(W.noteLabel, { name }) : W.noteLabelUnnamed, segments, after,
    footer: time && name ? fill(day ? W.noteWrittenOn : W.noteWrittenAt, { name, time, day }) + ' ' + W.noteLinksHint : W.noteLinksHint };
}

function noteBlock(slot, kept, name) {
  const props = noteProps(kept, name);
  let box = slot.querySelector(':scope > details.chat-head__note');
  if (box && box.dataset.written !== String(kept.written_at || '')) { box.remove(); box = null; }
  const v = visit(), at = Date.parse(kept.written_at || '');
  const wanted = !v.known || !v.at || isNaN(at) || at > v.at;
  if (!box) {
    box = h('details', { class: 'chat-head__note', 'data-written': String(kept.written_at || '') },
      h('summary', { class: 'chat-head__note-line' }, props.label), note.render(props));
    box.open = box.pageOpen = wanted;
    box.settled = v.known;
    box.querySelector(':scope > summary').addEventListener('click', () => { box.theirs = true; });
    box.addEventListener('toggle', () => { if (box.open !== box.pageOpen) box.theirs = true; });
    slot.replaceChildren(box);
    return;
  }
  if (!box.settled && v.known) {
    box.settled = true;
    if (!box.theirs && box.open !== wanted) box.open = box.pageOpen = wanted;
  }
  const line = box.querySelector(':scope > summary');
  if (line.textContent !== props.label) line.textContent = props.label;
  note.update(box.querySelector(':scope > .c-note'), props);
}

function head() {
  let el = $('chat-head');
  if (el) return el;
  const view = $('view-chat'), body = view && view.querySelector(':scope > .chat-body');
  if (!body) return null;
  el = h('div', { class: 'chat-head', id: 'chat-head' },
    h('div', { class: 'chat-head__slot', id: 'chat-note' }));
  view.insertBefore(el, body);
  return el;
}

export function renderHome() {
  if (!head()) return;
  rememberThisVisit();
  const kept = S.identity && S.identity.note, slot = $('chat-note');
  const named = (S.stats && S.stats.name) || '', name = named === 'Unnamed' ? '' : named;
  if (kept && kept.text) noteBlock(slot, kept, name); else if (slot.firstChild) slot.replaceChildren();
}
