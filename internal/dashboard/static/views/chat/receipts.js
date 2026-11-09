import { S } from '../../state.js';
import * as outcomeRow from '../../components/outcome-row/outcome-row.js';
import W from '../../strings/chat.js';
import { fill } from '../../util.js';
import { setText, setData, syncChildren } from './dom.js';
import { presentationRow } from './thread.js';

function duration(ms) {
  if (!(ms >= 0)) return '';
  const s = Math.round(ms / 1000); if (s < 60) return s + ' s';
  const m = Math.round(s / 60); if (m < 60) return m + ' min';
  return Math.floor(m / 60) + ' h' + (m % 60 ? ' ' + (m % 60) + ' min' : '');
}
function argsOf(row) {
  const call = /^→ [^(\n]*\(([\s\S]*)\)$/.exec(row.content || '');
  try { return call ? JSON.parse(call[1]) : null; } catch { return null; }
}
function replyItself(item, live) {
  const row = item.anchor.row;
  return !!item.tool && row.details?.tool === 'recall' && (item.outcome === 'succeeded' || (live && !item.outcome)) && argsOf(row)?.source === 'conversation';
}
function clip(t) {
  if (t.length <= 200) return t;
  const at = Math.max(t.lastIndexOf(' ', 200), t.lastIndexOf('/', 200), t.lastIndexOf('\\', 200));
  return t.slice(0, at > 0 ? at : 200) + '…';
}
function counted(it, live) {
  const steps = it.items.filter(item => item.tool && !replyItself(item, live));
  return steps.length ? steps : it.items.filter(item => item.tool);
}
function quiet(it, live) {
  return it.items.length > 0 && it.items.every(item => replyItself(item, live)) && !it.conflict && !it.unsaved && !it.unsavedCompletions && !it.startOutside && !it.endOutside;
}
function receiptLabel(it, live) {
  const steps = it.steps ? counted(it, live).length : 0;
  const count = steps ? steps + (steps === 1 ? ' step' : ' steps') : it.notes + (it.notes === 1 ? ' recorded note' : ' recorded notes');
  const parts = [];
  if (live) parts.push('Working', count + (it.startOutside ? ' in view' : ''));
  else if (it.startOutside || it.endOutside) parts.push(count + ' in view');
  else if (it.steps && it.replied) { const took = duration(it.end - it.start); parts.push(took ? 'Worked ' + took : 'Worked', count); }
  else parts.push(count);
  if (it.failed) parts.push(it.failed + ' failed');
  if (it.unknown) parts.push(it.unknown + ' unknown');
  if (it.cancelled) parts.push(it.cancelled + ' cancelled');
  if (it.unfinished && !live && !it.endOutside) parts.push(it.unfinished + ' unfinished');
  if (it.conflict) parts.push('conflicting records');
  if (it.unsaved) parts.push(it.unsaved === it.items.length ? 'not saved' : it.unsaved + ' not saved');
  if (it.unsavedCompletions) parts.push(it.unsavedCompletions + (it.unsavedCompletions === 1 ? ' completion' : ' completions') + ' not saved');
  return parts.join(' · ');
}
function outcomeOf(it) {
  const items = counted(it, false).map(item => {
    const result = item.records.find(e => e.row.kind === 'tool_result');
    const said = String(result?.row.content || ''), back = said.indexOf('\n← ');
    return {
      tool: item.anchor.row.details?.tool || 'A step', args: argsOf(item.anchor.row), outcome: item.outcome || '',
      reason: result && item.outcome && item.outcome !== 'succeeded' ? clip((back >= 0 ? said.slice(back + 3) : said).split('\n')[0]) : '',
      receipt: !!result && item.source === 'recorded' && !item.transient,
      seq: result && item.source === 'recorded' && !item.transient ? String(result.row.sequence || '') : '',
    };
  });
  const parts = receiptLabel(it, false).split(' · ');
  const cut = !!(it.startOutside || it.endOutside);
  const qualifications = parts.slice(it.steps && it.replied && !cut ? 2 : 1).filter(q => !/^\d+ (failed|unknown|cancelled)$/.test(q));
  if (cut) qualifications.unshift('in view');
  const n = (S.stats && S.stats.name) || '';
  return { actor: n && n !== 'Unnamed' ? n : W.yourIdentity, items, qualifications, cut };
}
export function markReceipt(el, it, live) {
  if (el.hidden !== quiet(it, live)) el.hidden = !el.hidden;
  const o = !live && it.steps ? outcomeOf(it) : null;
  const label = el.querySelector('.receipt-label');
  if (o) outcomeRow.fillLine(label, o);
  else { delete label.dataset.line; setText(label, receiptLabel(it, live)); }
  const ok = !!o && outcomeRow.done(o.items, o.qualifications);
  const cutOnly = !!o && !ok && o.cut && o.qualifications.length === 1 && !o.items.some(i => outcomeRow.noteFor(i));
  const verdict = o ? (ok ? 'done' : cutOnly ? 'cut' : 'open') : '';
  setData(el, 'outcome', verdict);
  const notes = el.querySelector('.receipt-notes');
  if (notes) outcomeRow.fillNotes(notes, o ? o.items : [], o ? outcomeRow.receiptHref(o.items) : '');
  const trouble = !!(it.failed || it.conflict);
  const partial = !trouble && !live && !!(it.unknown || it.cancelled || (it.unfinished && !it.endOutside) || it.unsaved || it.unsavedCompletions);
  for (const [name, on] of [['trouble', trouble], ['live', live], ['partial', partial]]) if (el.classList.contains(name) !== on) el.classList.toggle(name, on);
}
const OTHER = Symbol('other steps');
function receiptGroups(it) {
  const groups = new Map();
  for (const item of it.items) {
    const r = item.anchor.row;
    const words = item.tool ? outcomeRow.stepWords({ tool: r.details?.tool, args: argsOf(r) }) : '';
    const name = words || (item.tool ? (r.details?.tool === 'work' ? OTHER : r.details?.tool || 'Tool') : (r.content || (r.kind === 'annotation' ? 'Recorded annotation' : 'Recorded notice')).split('\n')[0].slice(0, 80));
    const g = groups.get(name) || { name, tool: !!item.tool, words: !!words || name === OTHER, n: 0, bad: 0 };
    g.n++; if (item.tool && (item.outcome === 'failed' || item.outcome === 'refused')) g.bad++;
    groups.set(name, g);
  }
  const other = groups.get(OTHER);
  if (other) { other.name = other.n === 1 ? W.outcomeOtherStep : fill(W.outcomeOther, { n: other.n }); other.n = 1; }
  return [...groups.values()];
}
export function receiptRow(it, old, claimed, live) {
  let el = old.get(it.key);
  if (!el || claimed.has(el)) {
    el = null;
    for (const item of it.items) {
      const inside = old.get(item.key)?.closest('.turn-receipt');
      if (inside && !claimed.has(inside) && inside.parentElement) { el = inside; break; }
    }
  }
  if (!el) {
    el = document.createElement('div'); el.className = 'interaction-item turn-receipt';
    const box = document.createElement('details'); box.className = 'receipt';
    const line = document.createElement('summary'); line.className = 'receipt-line';
    const label = document.createElement('span'); label.className = 'receipt-label'; line.appendChild(label);
    const groups = document.createElement('ul'); groups.className = 'receipt-groups';
    const all = document.createElement('details'); all.className = 'receipt-all';
    const allTitle = document.createElement('summary'); const steps = document.createElement('div'); steps.className = 'receipt-steps';
    all.append(allTitle, steps);
    const notes = document.createElement('div'); notes.className = 'receipt-notes';
    box.append(line, notes, groups, all); el.appendChild(box);
  }
  claimed.add(el);
  setData(el, 'interactionKey', it.key);
  markReceipt(el, it, live);
  const groups = receiptGroups(it), signature = JSON.stringify(groups);
  const list = el.querySelector('.receipt-groups');
  if (list.dataset.signature !== signature) {
    list.dataset.signature = signature;
    list.replaceChildren(...groups.map(g => {
      const li = document.createElement('li');
      const name = document.createElement('span'); name.className = 'g-name' + (g.words ? ' word' : g.tool ? ' tool' : ''); name.textContent = g.name;
      const n = document.createElement('span'); n.className = 'g-count' + (g.bad ? ' bad' : '');
      n.textContent = (g.n > 1 ? '×' + g.n : '') + (g.bad ? (g.n > 1 ? ' · ' : '') + g.bad + ' failed' : '');
      li.append(name, n); return li;
    }));
  }
  setText(el.querySelector('.receipt-all > summary'), 'Every record, in order (' + it.items.length + ')');
  syncChildren(el.querySelector('.receipt-steps'), it.items.map(item => presentationRow(old.get(item.key), item)));
  return el;
}
