import { h, fill } from '../../util.js';
import W from '../../strings/identity.js';
import { held, capital } from '../belief-row/belief-row.js';

const day = iso => { const t = Date.parse(iso || ''); return isNaN(t) ? '' : new Date(t).toLocaleDateString([], { day: 'numeric', month: 'long' }); };
function when(iso) {
  const t = Date.parse(iso || '');
  if (isNaN(t)) return '';
  const days = Math.round((new Date().setHours(0, 0, 0, 0) - new Date(t).setHours(0, 0, 0, 0)) / 86400000);
  return days < 7 ? new Date(t).toLocaleDateString([], { weekday: 'long' }) : day(iso);
}

function link(l) {
  const date = day(l.created_at);
  let source, theirs = true;
  if (l.kind === 'belief') source = fill(W.trailTheirReasoning, { date });
  else if (l.kind === 'experience' && l.provenance === 'operator') {
    theirs = false;
    source = l.turn ? fill(W.trailYourWords, { date, turn: l.turn }) : fill(W.trailYourWords.replace(', turn {turn}', ''), { date });
  } else if (l.kind === 'experience' && (l.provenance === 'self' || l.provenance === 'dream' || !l.provenance)) source = fill(W.trailTheirExperience, { date });
  else if (l.provenance === 'external') { theirs = false; source = [capital(W.fromOutside), date].filter(Boolean).join(' · '); }
  else if (l.provenance === 'system') { theirs = false; source = [capital(W.fromOs), date].filter(Boolean).join(' · '); }
  else source = date;
  return h('li', { class: 'c-trail__link', 'data-kind': l.kind || 'other' },
    h('p', { class: 'c-trail__text' + (theirs ? ' c-trail__text--theirs' : ' c-trail__text--quoted') }, l.text || ''),
    source && h('p', { class: 'c-trail__source' }, source));
}
function section(title, links, { cut = false, none = '' } = {}) {
  if (!links.length && !none) return null;
  return h('section', { class: 'c-trail__part' },
    h('h4', { class: 'c-trail__heading' }, title),
    links.length ? h('ul', { class: 'c-trail__links' }, links.map(link)) : h('p', { class: 'c-trail__none' }, none),
    cut && h('p', { class: 'c-trail__cut' }, W.trailTruncated));
}

export function render({ state = 'loading', trail = null, reason = '', name = '', subject = name, record = '', onQuote = null, onCopy = null }) {
  const el = h('article', { class: 'c-trail', 'data-state': state, 'aria-busy': state === 'loading' ? 'true' : null });
  if (state === 'loading') { el.append(h('p', { class: 'c-trail__wait' }, W.trailLoading)); return el; }
  if (state === 'refused' || !trail) { el.append(h('p', { class: 'c-trail__refused' }, fill(W.trailRefused, { reason }))); return el; }
  const b = trail.belief || {}, cut = trail.truncated || {};
  const revisions = (trail.supersedes || []).length;
  const stands = [capital(held(b.confidence)),
    b.held_since && fill(W.trailHeldSince, { date: day(b.held_since) }),
    revisions === 1 ? W.trailRevisedOnce : revisions > 1 ? fill(W.trailRevisedTimes, { n: revisions }) : '',
    (b.archived || trail.superseded_by) && W.retired].filter(Boolean).join(' · ');
  let recall = '';
  if (b.fading && typeof b.recall === 'number') recall = fill(W.recallFading, { strength: b.recall.toFixed(2) });
  else if (b.recall_count === 0) recall = W.recallNever;
  else if (b.recall_count === 1) recall = fill(W.recallOnce, { when: when(b.recalled_at) });
  else if (b.recall_count > 1) recall = fill(W.recallTimes, { n: b.recall_count.toLocaleString(), when: when(b.recalled_at) });
  const acts = h('div', { class: 'c-trail__acts' });
  if (onQuote) { const q = h('button', { type: 'button', class: 'c-button c-button--secondary', title: fill(W.quoteToHint, { name: subject }) }, fill(W.quoteTo, { name })); q.onclick = onQuote; acts.append(q); }
  if (onCopy) { const c = h('button', { type: 'button', class: 'c-button c-button--quiet' }, W.trailCopyLink); c.onclick = onCopy; acts.append(c); }
  if (record) acts.append(h('a', { class: 'c-trail__record', href: record }, W.trailOpenRecord));
  el.append(...[
    h('p', { class: 'c-trail__statement' }, b.statement || ''),
    h('section', { class: 'c-trail__part' },
      h('h4', { class: 'c-trail__heading' }, W.trailAsItStands),
      h('p', { class: 'c-trail__stands' }, stands),
      recall && h('p', { class: 'c-trail__recall' }, recall),
      recall && h('p', { class: 'c-trail__gloss' }, fill(W.recallGloss, { name: subject }))),
    section(W.trailWhereFrom, trail.origins || [], { cut: cut.origins }),
    section(W.trailReplaced, trail.supersedes || [], { cut: cut.supersedes }),
    section(W.trailReplacedBy, trail.superseded_by ? [trail.superseded_by] : []),
    section(W.trailContradicts, trail.contradicts || [], { cut: cut.contradicts, none: W.trailNothingContradicts }),
    acts].filter(Boolean));
  return el;
}
