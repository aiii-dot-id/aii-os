import { h } from '../../util.js';
import * as chip from '../chip/chip.js';

export function render({ title = '', label = '', items = [], selected = '' } = {}) {
  const el = h('nav', { class: 'c-side-nav', 'aria-label': label || title });
  update(el, { title, items, selected });
  return el;
}

export function update(el, { title = '', items = [], selected = '' } = {}) {
  let head = el.querySelector('.c-side-nav__title');
  if (title) {
    if (!head) { head = h('h2', { class: 'c-side-nav__title' }); el.prepend(head); }
    head.textContent = title;
  } else if (head) { head.remove(); head = null; }
  const have = new Map([...el.querySelectorAll(':scope > .c-side-nav__item')].map(a => [a.dataset.item, a]));
  for (const [id, a] of have) if (!items.some(it => it.id === id)) a.remove();
  let prev = head;
  for (const it of items) {
    const a = have.get(it.id) || h('a', { class: 'c-side-nav__item', 'data-item': it.id }, h('span'));
    a.setAttribute('href', it.href);
    a.firstElementChild.textContent = it.label;
    if (it.id === selected) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
    a.toggleAttribute('data-rule', !!it.rule);
    const count = a.querySelector('.c-chip');
    if (it.count && count) count.textContent = String(it.count);
    else if (it.count) a.append(chip.render({ text: String(it.count), tone: 'accent' }));
    else if (count) count.remove();
    const next = prev ? prev.nextElementSibling : el.firstElementChild;
    if (next !== a) el.insertBefore(a, next);
    prev = a;
  }
}
