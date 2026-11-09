import { h, fill } from '../../util.js';
import W from '../../strings/beliefs-show.js';
import WI from '../../strings/identity.js';

let pick = 'all', group = null;
const day = iso => { const t = Date.parse(iso || ''); return isNaN(t) ? '' : new Date(t).toLocaleDateString([], { day: 'numeric', month: 'long' }); };

export function kept(iso) {
  if (pick === 'all') return true;
  const t = new Date(Date.parse(iso || '')), now = new Date();
  return !isNaN(t) && t.getFullYear() === now.getFullYear() && t.getMonth() === now.getMonth();
}

export function chips(id, redraw) {
  if (!group) {
    const chip = (key, text) => h('button', { type: 'button', class: 'c-chip', 'data-show': key, onclick: () => { pick = key; redraw(); } }, text);
    group = h('div', { class: 'beliefs__show', role: 'group', 'aria-label': W.show }, chip('all', ''), chip('month', W.changed));
  }
  const n = (id.beliefs || []).length + (id.retired || []).length + (id.retired_more || 0);
  group.firstChild.textContent = fill(W.all, { n: n.toLocaleString() });
  for (const b of group.children) b.setAttribute('aria-pressed', String(b.dataset.show === pick));
  return group;
}

export function rows(id, match, selected, open) {
  return (id.retired || []).filter(r => match(r) && kept(r.retired_at)).map(r => {
    const date = day(r.retired_at), by = r.replaced_by;
    return h('li', { class: 'c-belief c-belief--gone' + (r.id === selected ? ' c-belief--selected' : ''), 'data-belief-id': r.id },
      h('p', { class: 'c-belief__statement' }, r.statement || ''),
      h('p', { class: 'c-belief__held' }, !by ? fill(W.archived, { date }) : by.retired ? fill(W.revisedGone, { date }) : fill(W.revised, { date, statement: by.statement || '' })),
      h('div', { class: 'c-belief__acts' }, h('button', { type: 'button', class: 'c-button c-button--quiet c-belief__why', 'aria-expanded': String(r.id === selected), onclick: () => open(r.id) }, WI.why)));
  });
}

export const more = id => id.retired_more > 0 ? h('p', { class: 'beliefs__more' }, h('a', { href: '#/identity/record' }, fill(W.more, { k: id.retired_more.toLocaleString() }))) : null;

export const none = name => pick !== 'month' ? '' : name ? fill(W.changedNone, { name }) : W.changedNoneUnnamed;
