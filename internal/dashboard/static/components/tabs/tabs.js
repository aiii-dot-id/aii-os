import { h } from '../../util.js';

function edges(list) {
  const more = list.scrollWidth - list.clientWidth > 1;
  const start = more && list.scrollLeft > 1, end = more && list.scrollLeft < list.scrollWidth - list.clientWidth - 1;
  const said = start && end ? 'both' : start ? 'start' : end ? 'end' : '';
  if ((list.dataset.more || '') !== said) { if (said) list.dataset.more = said; else delete list.dataset.more; }
}

export function render({ tabs, selected, onSelect = null, label = null, idPrefix = 'tab' } = {}) {
  const list = h('div', { role: 'tablist', class: 'c-tabs', 'aria-label': label });
  const buttons = tabs.map(t => h('button', {
    type: 'button', role: 'tab', class: 'c-tabs__tab', id: idPrefix + '-' + t.id,
    'data-tab': t.id, onclick: () => choose(t.id, false),
  }, t.label));
  list.append(...buttons);
  const choose = (id, focus) => {
    update(list, { selected: id });
    if (focus) list.querySelector('[data-tab="' + CSS.escape(id) + '"]').focus();
    if (onSelect) onSelect(id);
  };
  list.addEventListener('keydown', e => {
    const at = buttons.indexOf(document.activeElement);
    if (at < 0) return;
    const to = { ArrowRight: at + 1, ArrowLeft: at - 1, Home: 0, End: buttons.length - 1 }[e.key];
    if (to === undefined) return;
    e.preventDefault();
    choose(buttons[(to + buttons.length) % buttons.length].dataset.tab, true);
  });
  list.addEventListener('scroll', () => edges(list), { passive: true });
  if (typeof ResizeObserver === 'function') new ResizeObserver(() => requestAnimationFrame(() => edges(list))).observe(list);
  update(list, { selected });
  return list;
}

export function update(list, { selected }) {
  let chosen = null;
  for (const b of list.querySelectorAll('[role="tab"]')) {
    const on = b.dataset.tab === selected;
    b.setAttribute('aria-selected', on ? 'true' : 'false');
    b.tabIndex = on ? 0 : -1;
    if (on) chosen = b;
  }
  if (chosen && list.scrollWidth > list.clientWidth + 1) {
    const left = chosen.offsetLeft - list.offsetLeft, right = left + chosen.offsetWidth;
    if (left < list.scrollLeft) list.scrollLeft = left;
    else if (right > list.scrollLeft + list.clientWidth) list.scrollLeft = right - list.clientWidth;
  }
  edges(list);
}

export function panelFor(list, el) {
  const on = list.querySelector('[role="tab"][aria-selected="true"]');
  el.setAttribute('role', 'tabpanel');
  if (on) el.setAttribute('aria-labelledby', on.id); else el.removeAttribute('aria-labelledby');
}
