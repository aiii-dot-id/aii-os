import { h } from '../../util.js';
import { draw } from '../in-words/in-words.js';

let made = 0;
export function render(props = {}) {
  const el = h('article', { class: 'c-note' });
  update(el, props);
  return el;
}

function osNote(text, cls) {
  const m = /^AII OS:?/.exec(text);
  return h('p', { class: cls, role: 'note' }, ...(m ? [h('strong', {}, m[0]), text.slice(m[0].length)] : [text]));
}

const inWords = (el, text, files) => { if (!files?.(el, text)) draw(el, text); return el; };

function whereEl(where) {
  return h('details', { class: 'c-note__where' }, h('summary', {}, where.label),
    h('span', { class: 'c-note__links' }, ...where.links.map(l => h('a', { href: l.href }, l.text))));
}

export function update(el, { state = 'none', label = '', text = '', author = '', more = null, segments = null, after = [], footer = '', files = null } = {}) {
  const said = JSON.stringify([state, label, text, author, more, segments, after, footer]);
  if (el.dataset.said === said) return;
  el.dataset.said = said;
  el.dataset.state = state;
  const id = el.dataset.labelId || (el.dataset.labelId = 'c-note-label-' + (++made));
  el.setAttribute('aria-labelledby', id);
  const theirs = state !== 'none';
  el.replaceChildren(...[
    h('p', { class: 'c-note__label', id }, label),
    state === 'note' && segments
      ? h('div', { class: 'c-note__body' }, ...segments.flatMap(seg => [h('span', { class: 'c-note__voice' }, seg.text),
          seg.where ? whereEl(seg.where) : null, ...seg.notes.map(n => osNote(n, 'c-note__os'))]))
      : theirs ? inWords(h('div', { class: 'c-note__text in-words' }), text, files) : h('div', { class: 'c-note__none' }, text),
    more && h('a', { class: 'c-note__more', href: more.href }, more.text),
    theirs && author && h('p', { class: 'c-note__author' }, author),
    ...after.map(n => osNote(n, 'c-note__os c-note__os--after')),
    footer && h('p', { class: 'c-note__footer' }, footer)].filter(Boolean));
}
