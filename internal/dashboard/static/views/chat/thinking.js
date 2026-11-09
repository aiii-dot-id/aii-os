import { signedFetch } from '../../pagekey.js';
import { h } from '../../util.js';
import W from '../../strings/chat.js';
import { setText } from './dom.js';

async function readThinking(turn, part) {
  const response = await signedFetch('/interaction/thinking?' + new URLSearchParams({ turn, part: String(part) }), { cache: 'no-store', credentials: 'same-origin' });
  if (!response.ok) throw Error(W.thinkingUnavailable);
  const a = await response.json();
  if (a.version !== 1 || a.turn !== turn || typeof a.recorded !== 'boolean') throw Error(W.thinkingUnavailable);
  if (a.recorded && (typeof a.text !== 'string' || !Number.isInteger(a.part) || !Number.isInteger(a.parts) || a.part !== part || a.parts < part)) throw Error(W.thinkingUnavailable);
  return a;
}

function fill(body, turn, first) {
  const summary = body.parentElement.querySelector(':scope > summary');
  setText(summary, first.may_be_summary ? W.thinkingSummary : W.thinking);
  const text = h('pre', { class: 'record-thinking__text' }, first.text);
  body.replaceChildren(
    h('p', { class: 'interaction-meta' }, [first.models, first.created_at].filter(Boolean).join(' · ')),
    first.may_be_summary && h('p', { class: 'thinking-note' }, W.thinkingSummaryHint),
    text);
  if (first.parts <= 1) return;
  let part = 1;
  const button = h('button', { type: 'button' }), status = h('span');
  const more = h('div', { class: 'detail-read' }, button, status);
  const label = () => { button.textContent = 'Read more (part ' + (part + 1) + ' of ' + first.parts + ')'; };
  label();
  button.onclick = async () => {
    button.disabled = true; setText(status, '…');
    try {
      const next = await readThinking(turn, part + 1);
      if (!next.recorded || next.parts !== first.parts) throw Error(W.thinkingChanged);
      text.textContent += next.text; part = next.part; setText(status, '');
      if (part >= first.parts) more.remove(); else { label(); button.disabled = false; }
    } catch (e) { setText(status, String(e.message || e)); button.disabled = false; }
  };
  body.appendChild(more);
}

export function disclosure(el) {
  const body = h('div', { class: 'record-thinking__body' });
  const box = h('details', { class: 'interaction-disclosure record-thinking' }, h('summary', {}, W.thinking), body);
  box.addEventListener('toggle', async () => {
    const turn = el.dataset.thinkingTurn || '';
    if (!box.open || !turn || box.dataset.thinking === turn) return;
    box.dataset.thinking = turn;
    body.replaceChildren();
    try {
      const first = await readThinking(turn, 1);
      if (box.dataset.thinking !== turn) return;
      if (first.recorded) fill(body, turn, first);
      else body.replaceChildren(h('p', { class: 'thinking-note' }, W.thinkingNone));
    } catch (e) {
      if (box.dataset.thinking !== turn) return;
      delete box.dataset.thinking;
      body.replaceChildren(h('p', { class: 'thinking-note' }, String(e.message || e)));
    }
  });
  return box;
}
