import { S } from '../../state.js';
import { $, h, fill } from '../../util.js';
import { lastVisit } from '../home/note.js';
import { entriesSince, fieldOf, kindWord } from './record-read.js';
import { relationshipHTML } from './relationships.js';
import W from '../../strings/identity.js';

const MOST = 200, DAY = 86400000;
const WORDS = { 'belief.upsert': 'statement', 'belief.supersede': 'reason', 'experience.create': 'content' };
const wordsOf = row => WORDS[row.type] ? fieldOf(row.head, WORDS[row.type]) : '';

function itemEl(row) {
  const kind = kindWord(row.type, W);
  if (!kind) return null;
  const t = Date.parse(row.time);
  const when = isNaN(t) ? '' : new Date(t).toLocaleString([], { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
  const words = wordsOf(row);
  return h('li', { class: 'between__item', 'data-seq': row.seq, 'data-type': row.type },
    h('p', { class: 'between__label' }, kind, when ? ' · ' + when : ''),
    words && h('p', { class: 'between__words' }, words),
    h('a', { class: 'between__open', href: '#/identity/record/' + row.seq }, W.openInRecord));
}

let shown = null;
S.renderBetweenAgain = () => { shown = null; renderBetween(); };
export function renderBetween() {
  const stack = $('between-stack');
  if (!stack || S.view !== 'between') return;
  const named = (S.stats && S.stats.name) || '', name = named === 'Unnamed' ? '' : named;
  const { at, seq, known, before } = lastVisit(), head = Number(S.cont && S.cont.ledger_seq) || 0;
  const others = (S.identity && S.identity.relationships) || [];
  const key = JSON.stringify([seq, known, before, head, name, others.map(r => r.id + ':' + r.record)]);
  if (shown === key) return;
  shown = key;
  const changes = h('section', { class: 'between__changes', 'aria-labelledby': 'between-since' });
  const parts = [changes];
  if (others.length) parts.push(h('section', { class: 'between__others', 'aria-labelledby': 'between-others' },
    h('h2', { id: 'between-others', class: 'between__h' }, name ? fill(W.others, { name }) : W.othersUnnamed)));
  stack.replaceChildren(...parts);
  if (others.length) stack.querySelector('.between__others').insertAdjacentHTML('beforeend', others.map(relationshipHTML).join(''));
  if (!known) return;
  if (!seq && !before) { changes.append(h('p', { class: 'between__none' }, name ? fill(W.betweenFirstVisit, { name }) : W.betweenFirstVisitUnnamed)); return; }
  const days = Math.max(0, Math.floor((Date.now() - at) / DAY));
  changes.append(h('h2', { id: 'between-since', class: 'between__h' }, before ? W.sinceFirstMet : fill(W.sinceVisit, { days })));
  if (!before && head && head <= seq) { changes.append(h('p', { class: 'between__none' }, W.sinceNothing)); return; }
  entriesSince(before ? 0 : seq, MOST).then(({ rows, more }) => {
    if (shown !== key) return;
    if (!rows.length) { changes.append(h('p', { class: 'between__none' }, W.sinceNothing)); return; }
    const said = rows.filter(row => kindWord(row.type, W)), unsaid = rows.length - said.length;
    if (said.length) changes.append(h('ol', { class: 'between__list' }, ...said.map(itemEl)));
    if (unsaid) changes.append(h('p', { class: 'between__more' }, h('a', { href: '#/identity/record' }, unsaid === 1 ? W.betweenUnsaidOne : fill(W.betweenUnsaid, { n: unsaid.toLocaleString() }))));
    if (more) changes.append(h('p', { class: 'between__more' }, h('a', { href: '#/identity/record' }, W.openInRecord)));
  }, err => { if (shown === key) { shown = null; changes.append(h('p', { class: 'between__none', role: 'alert' }, err.message)); } });
}
