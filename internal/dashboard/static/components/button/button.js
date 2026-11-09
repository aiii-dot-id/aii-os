import { h } from '../../util.js';

const KINDS = ['primary', 'secondary', 'quiet', 'danger'];

export function render({ label, kind = 'secondary', icon = null, busy = false, disabled = false, onPress = null } = {}) {
  const el = h('button', {
    type: 'button',
    class: 'c-button c-button--' + (KINDS.includes(kind) ? kind : 'secondary') + (icon ? ' c-button--icon' : ''),
    'aria-label': icon ? label : null,
    onclick: onPress,
  }, icon, icon ? null : label);
  update(el, { busy, disabled });
  return el;
}

export function update(el, { busy = false, disabled = false } = {}) {
  el.disabled = !!(busy || disabled);
  if (busy) el.setAttribute('aria-busy', 'true'); else el.removeAttribute('aria-busy');
}
