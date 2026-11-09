import { $ } from './util.js';

export function renderPanel() {
  const el = $('slot-panel');
  const left = el && el.querySelector(':scope > .sys-panel');
  if (!left) return;
  left.remove();
  if (!el.querySelector('.section-box')) el.classList.remove('occupied');
}
