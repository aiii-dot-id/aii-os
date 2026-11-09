import { h } from '../../util.js';
import W from '../../strings/chat.js';

const BELIEF = /\bbelief_[0-9a-f]{6,}\b/g;

export function cited(pages) {
  const out = new Map();
  for (const page of pages.values()) for (const r of page?.rows || []) {
    if (r.kind !== 'tool_result' || !r.turn_id || typeof r.content !== 'string') continue;
    const ids = r.content.match(BELIEF);
    if (!ids) continue;
    const list = out.get(r.turn_id) || [];
    for (const id of ids) if (!list.includes(id)) list.push(id);
    out.set(r.turn_id, list);
  }
  return out;
}

export function drawWhy(msg, ids) {
  const acts = msg && msg.querySelector('.msg-acts');
  if (!acts) return;
  let why = acts.querySelector('.why-chip');
  const id = ids && ids[0];
  if (!id) { why?.remove(); return; }
  const href = '#/identity/beliefs/' + encodeURIComponent(id);
  if (!why) { why = h('a', { class: 'msg-chip why-chip', title: W.whyHint }, W.why); acts.appendChild(why); }
  if (why.getAttribute('href') !== href) why.setAttribute('href', href);
}
