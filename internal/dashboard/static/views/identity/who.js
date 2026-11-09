import { S } from '../../state.js';
import { $, h, fill } from '../../util.js';
import { approvalHTML } from './relationships.js';
import { readRecord, payloadOf } from './record-read.js';
import W from '../../strings/identity.js';
import WH from '../../strings/home.js';

const day = t => { const d = new Date(t); return isNaN(d) ? '' : d.toLocaleDateString([], { day: 'numeric', month: 'long' }); };
const voice = (tag, text, cls) => h(tag, { class: 'who__voice' + (cls ? ' ' + cls : '') }, text);
const said = (named, unnamed, name, slots) => fill(name ? named : unnamed, { name, ...slots });
const section = (id, title, ...body) => h('section', { class: 'who__part', 'aria-labelledby': id }, h('h2', { id, class: 'who__h' }, title), ...body);

let portraitAsk = 0;
function portrait(id, name) {
  const meta = h('p', { class: 'who__meta' });
  const part = section('who-portrait', said(W.portrait, W.portraitUnnamed, name),
    id.synthesis ? voice('p', id.synthesis, 'who__portrait') : h('p', { class: 'who__none' }, said(W.portraitNone, W.portraitNoneUnnamed, name)), meta);
  if (id.synthesis) {
    const ask = ++portraitAsk, shown = id.synthesis;
    readRecord({ type: 'self_model.synthesize' }).then(async page => {
      const row = (page.rows || [])[0];
      if (ask !== portraitAsk || !meta.isConnected || !row) return;
      let own;
      try { own = JSON.parse(await payloadOf(row, page.range_bytes)).synthesis_text; } catch (err) { return; }
      if (ask !== portraitAsk || !meta.isConnected || typeof own !== 'string' || own !== shown) return;
      meta.replaceChildren(fill(W.portraitMeta, { date: day(row.time) }), ' · ', h('a', { href: '#/identity/record/' + row.seq }, WH.whereFrom));
    }, () => {});
  }
  return part;
}

function charter(id) {
  if (!id.charter) return null;
  const a = id.approval;
  const approval = h('div'); approval.innerHTML = approvalHTML(a);
  return section('who-charter', W.charter, approval.firstElementChild, voice('div', id.charter, 'who__charter'),
    a && a.record ? h('p', { class: 'who__meta' }, h('a', { href: '#/identity/record/' + a.record }, W.openInRecord)) : null);
}

function workingOn(id) {
  const open = (id.intentions || []).filter(i => i.state !== 'done' && i.state !== 'abandoned');
  return section('who-working', W.workingOn,
    open.length ? h('ul', { class: 'who__list' }, ...open.map(i => h('li', {}, voice('span', i.statement)))) : h('p', { class: 'who__none' }, W.workingOnNone));
}

const UNITS = [['day', 86400000], ['hour', 3600000], ['minute', 60000], ['second', 1000]];
function span(ms) {
  const [unit, size] = UNITS.find(([, n]) => ms % n === 0 && ms >= n) || UNITS[3];
  const parts = new Intl.NumberFormat([], { style: 'unit', unit, unitDisplay: 'long' }).formatToParts(Math.round(ms / size));
  return ms === size ? parts.filter(p => p.type === 'unit').map(p => p.value).join('') : parts.map(p => p.value).join('');
}
function alarms(name) {
  const list = S.work && S.work.alarms;
  if (!Array.isArray(list)) return null;
  const at = ms => { const d = new Date(ms); return isNaN(d) ? '' : d.toLocaleString([], { weekday: 'long', hour: '2-digit', minute: '2-digit' }); };
  const now = Date.now();
  const row = a => h('li', { class: 'who__alarm' }, h('details', {},
    h('summary', {}, fill(W.alarmRow, { when: at(a.due_at), message: '' }), voice('span', a.message || a.tag || ''),
      a.every ? ' · ' + fill(W.alarmRepeats, { every: span(a.every) }) : '',
      a.due_at < now ? ' · ' + fill(W.alarmOverdue, { when: at(a.due_at) }) : ''),
    h('p', { class: 'who__meta' }, a.fired_at ? fill(W.alarmLastFired, { when: at(a.fired_at) }) : W.alarmNeverFired)));
  return section('who-alarms', W.setToComeBack, h('p', { class: 'who__hint' }, said(W.setToComeBackHint, W.setToComeBackHintUnnamed, name)),
    list.length ? h('ul', { class: 'who__list' }, ...list.map(row)) : h('p', { class: 'who__none' }, W.alarmsNone));
}

function firm(id) {
  const held = (id.beliefs || []).filter(b => b.status !== 'archived').sort((a, b) => b.confidence - a.confidence);
  if (!held.length) return null;
  return section('who-firm', W.firm, h('ul', { class: 'who__list' }, ...held.slice(0, 3).map(b => h('li', {}, voice('span', b.statement)))),
    h('p', { class: 'who__more' }, h('a', { href: '#/identity/beliefs' }, fill(W.allBeliefs, { n: held.length.toLocaleString() }))));
}

function notes(id, name) {
  return section('who-notes', W.notes, h('p', { class: 'who__hint' }, said(W.notesHint, W.notesHintUnnamed, name)),
    id.brief ? h('details', { class: 'who__brief' }, h('summary', {}, W.notesRead), h('div', { class: 'who__brief-text' }, id.brief)) : h('p', { class: 'who__none' }, W.notesNone));
}

function levels(id, name) {
  if (!id.trust_level && !id.autonomy_level) return null;
  return section('who-levels', W.levels, h('p', { class: 'who__hint' }, said(W.levelsHint, W.levelsHintUnnamed, name)),
    h('dl', { class: 'who__levels' }, h('dt', {}, 'trust'), h('dd', {}, id.trust_level || '—'), h('dt', {}, 'autonomy'), h('dd', {}, id.autonomy_level || '—')));
}

let drawn = '';
export function renderWho(entered = false) {
  if (entered) drawn = '';
  const stack = $('identity-stack');
  if (!stack || S.view !== 'identity') return;
  const id = S.identity;
  const named = (S.stats && S.stats.name) || '', name = named === 'Unnamed' ? '' : named;
  const key = JSON.stringify([id, name, S.work && S.work.alarms]);
  if (key === drawn) return;
  drawn = key;
  if (!id) { stack.replaceChildren(); return; }
  stack.replaceChildren(h('div', { class: 'who' },
    h('div', { class: 'who__main' }, portrait(id, name), charter(id)),
    h('aside', { class: 'who__side' }, workingOn(id), alarms(name), firm(id), notes(id, name), levels(id, name))));
}
