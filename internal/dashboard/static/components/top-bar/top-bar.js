import { h, fill } from '../../util.js';
import W from '../../strings/shell.js';

const LINKS = [
  { id: 'chat', href: '#/chat', word: W.railChat },
  { id: 'projects', href: '#/projects', word: W.projects },
  { id: 'identity', href: '#/identity' },
  { id: 'record', href: '#/identity/record', word: W.record, wide: true },
  { id: 'plugins', href: '#/settings/permissions', word: W.plugins, wide: true },
  { id: 'settings', href: '#/settings', word: W.settings },
];
const LIVES_IN = { record: 'identity', plugins: 'settings' };

const ICONS = {
  home: 'M3 8l6-5 6 5v7H3z M7.5 15v-4h3v4',
  projects: 'M2.5 5.5h5l1.5 2h6.5v7h-13z',
  identity: 'M6 6.5a3 3 0 1 0 6 0a3 3 0 1 0-6 0 M3.5 15c.8-3 3-4.5 5.5-4.5s4.7 1.5 5.5 4.5',
  settings: 'M3 5h8 M14 5h1 M3 9h2 M8 9h7 M3 13h6 M12 13h3 M11 5a1.5 1.5 0 1 0 3 0a1.5 1.5 0 1 0-3 0 M5 9a1.5 1.5 0 1 0 3 0a1.5 1.5 0 1 0-3 0 M9 13a1.5 1.5 0 1 0 3 0a1.5 1.5 0 1 0-3 0',
};
ICONS.chat = 'M3 4.5h12v8H8l-3 2.5v-2.5H3z';
ICONS.record = 'M4 3h10v12H4z M6.5 6.5h5 M6.5 9h5 M6.5 11.5h3';
ICONS.plugins = 'M6.5 2.5V6 M11.5 2.5V6 M4.5 6h9v2.5a4.5 4.5 0 0 1-9 0z M9 13v2.5';
const SVG = 'http://www.w3.org/2000/svg';
function icon(id) {
  const svg = document.createElementNS(SVG, 'svg');
  for (const [k, v] of Object.entries({ class: 'c-top-bar__icon', 'aria-hidden': 'true', width: '20', height: '20', viewBox: '0 0 18 18', fill: 'none',
    stroke: 'currentColor', 'stroke-width': '1.6', 'stroke-linecap': 'round', 'stroke-linejoin': 'round' })) svg.setAttribute(k, v);
  const path = document.createElementNS(SVG, 'path');
  path.setAttribute('d', ICONS[id]);
  svg.append(path);
  return svg;
}

function placeLink({ id, href, word, wide }) {
  const cls = 'nav-item c-top-bar__place' + (wide ? ' c-top-bar__wide' : '');
  if (id === 'identity') {
    return h('a', { href, class: cls, 'data-view': id },
      icon(id), h('span', { class: 'c-top-bar__long' }), h('span', { class: 'c-top-bar__short' }));
  }
  return h('a', { href, class: cls, 'data-view': id }, icon(id), h('span', null, word));
}

export function render({ name = '', active = '', start = [], beside = [], end = [], below = [], onSearch = null } = {}) {
  const el = h('header', { class: 'c-top-bar', id: 'topbar' },
    h('a', { href: '#/chat', class: 'c-top-bar__who', 'data-view': 'chat' }, start, h('span', { class: 'c-top-bar__name', id: 'p-name' })),
    h('div', { class: 'c-top-bar__end' }, end,
      onSearch ? h('button', { type: 'button', class: 'c-top-bar__search', 'aria-label': W.searchLabel, onclick: onSearch }, W.search,
        h('kbd', { class: 'c-top-bar__key', 'aria-hidden': 'true' }, /Mac|iPhone|iPad/.test(navigator.platform || '') ? '\u2318K' : 'Ctrl K')) : null));
  el.rail = h('aside', { class: 'c-rail' },
    h('a', { href: '#/chat', class: 'c-rail__who', 'data-view': 'chat' }, h('span', { class: 'c-rail__mark', id: 'rail-mark', 'aria-hidden': 'true' }), h('span', { class: 'c-rail__name' })),
    beside,
    h('nav', { class: 'c-top-bar__nav', id: 'nav', 'aria-label': W.mainNav }, LINKS.map(placeLink)),
    h('div', { class: 'c-rail__below' }, h('div', { class: 'c-rail__pages' }), below));
  update(el, { name, active });
  return el;
}

export function update(el, { name = '', active = '' } = {}) {
  const rail = el.rail || el, said = name || W.yourIdentity;
  for (const who of [el.querySelector('.c-top-bar__who'), rail.querySelector('.c-rail__who')]) who?.setAttribute('aria-label', fill(W.whoLabel, { name: said }));
  for (const shown of [el.querySelector('.c-top-bar__name'), rail.querySelector('.c-rail__name')]) {
    if (!shown) continue;
    shown.textContent = said;
    shown.title = said;
  }
  rail.querySelector('.c-top-bar__long').textContent = name ? fill(W.about, { name }) : W.aboutNoName;
  rail.querySelector('.c-top-bar__short').textContent = name || W.aboutNoName;
  const onPhone = typeof matchMedia === 'function' && matchMedia('(max-width: 767px)').matches;
  for (const a of rail.querySelectorAll('.c-top-bar__place')) {
    const on = a.dataset.view === active || (onPhone && a.dataset.view === LIVES_IN[active]);
    if (on) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
  }
}
