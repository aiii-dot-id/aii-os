import { h } from '../../util.js';

const TONES = ['neutral', 'accent', 'good', 'warn', 'bad'];

export function render({ text, tone = 'neutral', onPress = null } = {}) {
  const cls = 'c-chip c-chip--' + (TONES.includes(tone) ? tone : 'neutral');
  if (onPress) return h('button', { type: 'button', class: cls, onclick: onPress }, text);
  return h('span', { class: cls }, text);
}
