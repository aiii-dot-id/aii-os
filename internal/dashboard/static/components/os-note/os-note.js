import { h } from '../../util.js';

const TONES = ['info', 'warn', 'bad'];

export function render({ tone = 'info', text = '', children = [], actions = [], live = true } = {}) {
  const t = TONES.includes(tone) ? tone : 'info';
  const el = h('div', { class: 'c-os-note c-os-note--' + t, role: live ? (t === 'bad' ? 'alert' : 'status') : null });
  update(el, { text, children, actions });
  return el;
}

export function update(el, { text = '', children = [], actions = [] } = {}) {
  const lead = text.startsWith('AII OS') ? 'AII OS' : '';
  el.replaceChildren(...[
    h('p', { class: 'c-os-note__text' }, lead ? h('strong', { class: 'c-os-note__lead' }, lead) : null, lead ? text.slice(lead.length) : text),
    children,
    actions.length ? h('div', { class: 'c-os-note__actions' }, actions) : null,
  ].flat().filter(Boolean));
}
