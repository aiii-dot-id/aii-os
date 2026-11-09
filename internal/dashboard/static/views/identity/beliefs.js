import { S } from '../../state.js';
import { $, h, fill, composing } from '../../util.js';
import { query } from '../../ws.js';
import * as beliefRow from '../../components/belief-row/belief-row.js';
import * as whyTrail from '../../components/why-trail/why-trail.js';
import { openTrail, trailNow, revisionOf } from './trail.js';
import { copyText, linkTo, showAddress, putInReply } from './page.js';
import { recallHTML } from '../memory.js';
import W from '../../strings/identity.js';
import WC from '../../strings/chat.js';
import WH from '../../strings/home.js';

let filter = '', selected = '', addressRead = false, show, asked;
const nameOf = () => { const n = (S.stats && S.stats.name) || ''; return n === 'Unnamed' ? '' : n; };
const them = () => nameOf() || 'them', subject = () => nameOf() || WH.yourIdentity;
const find = id => [...(S.identity?.beliefs || []), ...(S.identity?.retired || [])].find(b => b.id === id);
function shown() {
  asked ||= import('./beliefs-show.js').then(m => { show = m; renderBeliefs(); },
    err => { asked = null; import('../../recovery.js').then(r => r.partFailed(W.tabs.beliefs, err), () => {}); });
  return show;
}

export function openBelief(id) {
  if (!id) return;
  selected = id;
  openTrail(id, revisionOf(find(id)), true);
  showAddress('#/identity/beliefs/' + encodeURIComponent(id));
  renderBeliefs();
  document.querySelector('#beliefs-view .c-trail')?.scrollIntoView({ block: 'nearest' });
}
S.openBelief = openBelief;
function addressed() {
  const m = /^#\/identity\/beliefs\/(.+)$/.exec(location.hash || '');
  if (!m) return null;
  try { return decodeURIComponent(m[1]); } catch (err) { return m[1]; }
}

function quote(b) {
  const prefix = nameOf() ? WC.quotePrefix.replace('{name}', nameOf()) : WC.quotePrefix.replace('{name}, ', '');
  putInReply(prefix.replace('{statement}', () => String(b.statement || '')));
}

function trailFor(b) {
  const t = trailNow();
  const link = linkTo('#/identity/beliefs/' + encodeURIComponent(b.id));
  const last = t.trail && t.trail.belief && t.trail.belief.last_seq;
  return whyTrail.render({ state: t.id === b.id ? t.state || 'loading' : 'loading', trail: t.id === b.id ? t.trail : null, reason: t.reason, name: them(), subject: subject(),
    record: last ? '#/identity/record/' + last : '', onQuote: b.retired_at ? null : () => quote(b), onCopy: () => copyText(link) });
}

export function renderBeliefs() {
  const host = $('beliefs-view');
  if (!host || host.composing) return;
  if (S.identity && !addressRead) {
    addressRead = true;
    const at = addressed();
    if (at) return openBelief(at);
  }
  const name = nameOf();
  const id = S.identity, more = shown();
  const beliefs = (id && id.beliefs) || [];
  let search = host.querySelector('#mem-search');
  if (!search) {
    search = h('input', { type: 'search', id: 'mem-search', class: 'beliefs__search-field', autocomplete: 'off' });
    search.addEventListener('input', () => { filter = search.value; renderBeliefs(); });
    search.addEventListener('compositionstart', () => { host.composing = true; });
    search.addEventListener('compositionend', () => { host.composing = false; filter = search.value; renderBeliefs(); });
    search.addEventListener('keydown', e => { if (composing(e) || host.composing || e.key !== 'Enter') return; e.preventDefault(); recall(); });
  }
  const placeholder = name ? fill(W.searchBeliefs, { name }) : W.searchBeliefsUnnamed;
  if (search.placeholder !== placeholder) { search.placeholder = placeholder; search.setAttribute('aria-label', placeholder); }
  const ask = h('button', { type: 'button', class: 'c-button c-button--secondary beliefs__recall' }, fill(W.askRecall, { name: them() }));
  ask.onclick = recall;
  const f = filter.trim().toLowerCase();
  const match = b => !f || String(b.statement || '').toLowerCase().includes(f);
  const rows = beliefs.filter(b => match(b) && (!more || more.kept(b.changed_at))).map(b => beliefRow.render({ belief: b, name: them(), subject: subject(), selected: b.id === selected, onWhy: x => openBelief(x.id), onQuote: quote }));
  if (more && id) rows.push(...more.rows(id, match, selected, openBelief));
  const open = find(selected);
  if (!open) selected = '';
  if (open) openTrail(open.id, revisionOf(open));
  let side = null;
  if (open) {
    const at = rows.findIndex(r => r.dataset.beliefId === open.id);
    if (at >= 0) rows.splice(at + 1, 0, h('li', { class: 'beliefs__trail-row' }, trailFor(open)));
    else side = trailFor(open);
  }
  const empty = !id || rows.length ? '' : !beliefs.length && !(more && id.retired?.length) ? fill(W.beliefsNone, { name: subject() }) : f ? fill(W.recallNoneLoaded, { name: them() }) : more.none(name);
  const chips = more && id && more.chips(id, renderBeliefs);
  const r = S.recall;
  const recalled = r && h('section', { class: 'beliefs__recalled' },
    h('h3', { class: 'beliefs__recalled-title' }, fill(W.recalled, { name: subject(), cue: r.query })),
    r.text ? h('pre', { class: 'beliefs__recalled-text', id: 'mem-recall' }) : h('p', { class: 'beliefs__empty' }, fill(W.recallNothing, { name: subject() })));
  if (recalled && r.text) recalled.querySelector('pre').innerHTML = recallHTML(r.text);
  const was = document.activeElement, sel = was === search && [search.selectionStart, search.selectionEnd, search.selectionDirection];
  host.replaceChildren(...[
    h('div', { class: 'beliefs__search' }, search, ask),
    chips,
    recalled,
    h('div', { class: 'beliefs__body' + (side ? ' beliefs__body--open' : '') },
      h('div', {}, empty ? h('p', { class: 'beliefs__empty' }, empty) : h('ul', { class: 'beliefs__list', 'aria-label': W.tabs.beliefs }, rows), chips && more.more(id)),
      side && h('div', { class: 'beliefs__trail' }, side))].filter(Boolean));
  if (search.value !== filter) search.value = filter;
  if (was?.isConnected && was !== document.activeElement) was.focus({ preventScroll: true });
  if (sel) search.setSelectionRange(...sel);
}
S.renderBeliefs = renderBeliefs;

function recall() {
  const search = $('mem-search'), q = (search?.value || '').trim();
  if (!q) return;
  search.dataset.keepAsked = search.value;
  query('recall', { q });
}
window.matchMedia?.('(max-width: 767px)').addEventListener?.('change', () => { if (selected) renderBeliefs(); });
