import { S } from '../../state.js';
import { $, h, fill } from '../../util.js';
import { copyText, linkTo } from './page.js';
import { draw } from '../../components/in-words/in-words.js';
import { SINCE_BIRTH, within, shortWhen, choice, dateFilter } from './when.js';
import W from '../../strings/identity.js';
import WC from '../../strings/components.js';

const filters = { kind: '', when: SINCE_BIRTH, from: '', to: '', who: '', kept: false };
let opened = '', drawn = '';

export function whoOf(x) {
  if (x.provenance === 'operator' && x.operator_verbatim) return 'you';
  if (x.provenance === 'system') return 'os';
  return 'them';
}
const whoWords = (who, name) => who === 'you' ? W.whoYou : who === 'os' ? W.whoOs : (name || W.whoThemUnnamed);

function matches(x) {
  if (filters.kind && (x.category || '') !== filters.kind) return false;
  if (filters.who && whoOf(x) !== filters.who) return false;
  if (filters.kept && !x.unmetabolized) return false;
  return within(Date.parse(x.created_at || ''), filters);
}

function copying(label, text, done) {
  const said = h('span', { class: 'experience__said', role: 'status' });
  const button = h('button', { type: 'button', class: 'c-button c-button--quiet' }, label);
  button.onclick = async () => { said.textContent = (await copyText(text())) ? done : WC.copyFailed; };
  return [button, said];
}

const changed = () => { drawn = ''; renderExperiences(); };
const select = (label, key, options) => choice(label, options, filters[key], v => { filters[key] = v; changed(); });

function entryEl(x, name) {
  const who = whoOf(x), t = Date.parse(x.created_at || '');
  const when = shortWhen(t);
  const words = h('div', { class: who === 'you' ? 'experience__words experience__words--yours in-words' : 'experience__words in-words' });
  draw(words, x.content);
  const open = opened === x.id;
  const link = x.created_seq ? copying(WC.copyLink, () => linkTo('#/identity/record/' + x.created_seq), WC.linkCopied) : [];
  const toggle = h('button', { type: 'button', class: 'c-button c-button--quiet', 'aria-expanded': String(open) }, open ? WC.close : WC.open);
  toggle.onclick = () => { opened = open ? '' : x.id; drawn = ''; renderExperiences(); };
  const parts = [
    h('p', { class: 'experience__line' }, fill(W.entryKind, { kind: x.category || W.kindNone, who: whoWords(who, name), when })),
    words,
    x.unmetabolized ? h('p', { class: 'experience__kept' }, h('span', { class: 'c-chip' }, W.keptOutChip), ' ', h('span', { class: 'experience__hint' }, name ? fill(W.keptOut, { name }) : W.keptOutUnnamed)) : null,
    h('div', { class: 'experience__actions' }, toggle, ...link),
  ];
  if (open) parts.push(written(x));
  return h('li', { class: 'experience', 'data-experience-id': x.id }, ...parts);
}

function written(x) {
  const inWords = h('div', { class: 'experience__in-words in-words' });
  draw(inWords, x.content);
  return h('div', { class: 'experience__open' },
    h('section', { 'aria-label': W.inWords }, h('h3', { class: 'experience__h' }, W.inWords), inWords),
    h('section', { 'aria-label': W.asWritten }, h('h3', { class: 'experience__h' }, W.asWritten), h('pre', { class: 'experience__as-written' }, x.content)),
    h('div', { class: 'experience__actions' }, ...copying(W.copyEntry, () => x.content, WC.copied)));
}

const CONTROLS = 'button, select, input, a[href]';
function focused(stack) {
  const el = document.activeElement, box = el && stack.contains(el) && el.closest('[data-experience-id], .experiences__filters');
  return box ? [box.dataset.experienceId || '', [...box.querySelectorAll(CONTROLS)].indexOf(el)] : null;
}
function refocus(stack, at) {
  const box = at && (at[0] ? stack.querySelector('[data-experience-id="' + CSS.escape(at[0]) + '"]') : stack.querySelector('.experiences__filters'));
  const el = box && box.querySelectorAll(CONTROLS)[at[1]];
  if (el) el.focus();
}

S.renderExperiences = () => renderExperiences();
export function renderExperiences() {
  const stack = $('experiences-stack');
  if (!stack || S.view !== 'experiences') return;
  const named = (S.stats && S.stats.name) || '', name = named === 'Unnamed' ? '' : named;
  const all = (S.identity && S.identity.experiences) || [];
  const key = JSON.stringify([all, name, filters, opened]);
  if (key === drawn) return;
  drawn = key;
  if (!S.identity) { stack.replaceChildren(); return; }
  if (!all.length) { stack.replaceChildren(h('p', { class: 'experiences__none' }, W.experiencesNone)); return; }
  const kinds = [...new Set(all.map(x => x.category || ''))].filter(Boolean).sort();
  const row = h('div', { class: 'experiences__filters', role: 'group', 'aria-label': W.filters },
    select(W.filterKind, 'kind', [['', W.anyKind], ...kinds.map(k => [k, k])]),
    ...dateFilter(W, filters, changed),
    select(W.filterWho, 'who', [['', W.anyone], ['you', W.whoYou], ['them', name || W.whoThemUnnamed], ['os', W.whoOs]]));
  const kept = h('input', { type: 'checkbox', checked: filters.kept });
  kept.onchange = () => { filters.kept = kept.checked; changed(); };
  row.append(h('label', { class: 'c-filter c-filter--check' }, kept, h('span', {}, W.keptOutChip)));
  const shown = all.filter(matches), at = focused(stack);
  stack.replaceChildren(h('section', { class: 'experiences', 'aria-label': W.tabs.experiences },
    row,
    h('p', { class: 'experiences__scope' }, W.experiencesScope, ' ', h('a', { href: '#/identity/record' }, W.openRecord)),
    shown.length ? h('ol', { class: 'experiences__list' }, ...shown.map(x => entryEl(x, name))) : h('p', { class: 'experiences__none' }, W.experiencesNoMatch)));
  refocus(stack, at);
}
