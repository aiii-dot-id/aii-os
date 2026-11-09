import { h, fill } from '../../util.js';
import W from '../../strings/identity.js';

export function held(confidence) {
  const c = Number(confidence) || 0;
  const word = c >= 0.85 ? W.firmly : c >= 0.6 ? W.fairly : c >= 0.35 ? W.tentative : W.doubtful;
  return fill(W.confidence, { word, number: c.toFixed(2) });
}
export const capital = s => s ? s.charAt(0).toUpperCase() + s.slice(1) : s;
const evidence = n => n === 1 ? W.evidenceOne : fill(W.evidence, { n: Number(n || 0).toLocaleString() });

export function render({ belief: b, name = '', subject = name, selected = false, onWhy = null, onQuote = null }) {
  const why = h('button', { type: 'button', class: 'c-button c-button--quiet c-belief__why', 'aria-expanded': String(selected) }, W.why);
  const quote = h('button', { type: 'button', class: 'c-button c-button--quiet c-belief__quote', title: fill(W.quoteToHint, { name: subject }) }, fill(W.quoteTo, { name }));
  if (onWhy) why.onclick = () => onWhy(b);
  if (onQuote) quote.onclick = () => onQuote(b);
  const tone = b.confidence >= 0.85 ? 'firm' : b.confidence >= 0.6 ? 'fair' : b.confidence >= 0.35 ? 'tentative' : 'doubtful';
  return h('li', { class: 'c-belief' + (selected ? ' c-belief--selected' : ''), 'data-belief-id': b.id, 'data-tone': tone },
    h('p', { class: 'c-belief__statement' }, b.statement || ''),
    h('p', { class: 'c-belief__held' }, capital(held(b.confidence)) + ' · ' + evidence(b.evidence_count)),
    h('div', { class: 'c-belief__acts' }, why, quote));
}
