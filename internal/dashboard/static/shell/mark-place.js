import { S } from '../state.js';
import { $, h, fill } from '../util.js';
import { firstEntryTime } from '../views/identity/record-read.js';
import { VIEWS } from '../registry.js';
import W from '../strings/identity.js';

const placeOf = v => (VIEWS[v] && VIEWS[v].place) || '';
let mark = null, fetching = null;
const fetchMark = () => { fetching ||= import('../components/mark/mark.js').then(m => { mark = m; renderMarks(); }, () => { fetching = null; }); };
fetchMark();
let born = NaN, asked = false;
function knowBorn() {
  if (asked || !S.identityExists) return;
  asked = true;
  firstEntryTime().then(t => { born = t; renderMarks(); }, () => { asked = false; });
}

export const bornToday = (born, now = Date.now()) => new Date(born).toDateString() === new Date(now).toDateString();
const name = () => { const n = (S.stats && S.stats.name) || ''; return n === 'Unnamed' ? '' : n; };

function inSlot(id, size, m) {
  const slot = $(id);
  if (!slot) return;
  const drawn = slot.querySelector(':scope > svg.c-mark'), p = { ...m, size };
  if (!m) { if (!slot.firstElementChild) slot.append(h('img', { src: './icon.svg', alt: '', width: size, height: size })); }
  else if (drawn) mark.update(drawn, p); else slot.replaceChildren(mark.render(p));
}

function inAbout(v, m) {
  const head = $('place-head');
  if (!head) return;
  if (placeOf(v) !== 'identity' || !m) { if (head.firstChild) head.replaceChildren(); return; }
  const lived = Math.round(m.hours), entries = Number((S.cont && S.cont.ledger_seq) || (S.stats && S.stats.ledger_seq) || 0);
  const line = fill(W.withYou, { date: bornToday(born) ? W.today : new Date(born).toLocaleDateString([], { day: 'numeric', month: 'long' }),
    hours: lived ? fill(lived === 1 ? W.hourOne : W.hours, { n: lived.toLocaleString() }) : W.hourLess, entries: entries.toLocaleString() });
  const p = { ...m, size: 96 };
  let el = head.querySelector(':scope > .place-head');
  if (!el) {
    el = h('div', { class: 'place-head' }, mark.render(p), h('div', { class: 'place-head__words' }, h('p', { class: 'place-head__name' }), h('p', { class: 'place-head__with' })));
    head.replaceChildren(el);
  } else mark.update(el.querySelector('svg.c-mark'), p);
  const n = el.querySelector('.place-head__name'), w = el.querySelector('.place-head__with');
  if (n.textContent !== name()) n.textContent = name();
  if (w.textContent !== line) w.textContent = line;
}

let iconSaid = '';
function inTab(m) {
  if (!m) return;
  const accent = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#5b3ee0';
  const said = born + ':' + mark.turns(m.hours, 64).toFixed(2) + ':' + Object.values(m.look) + ':' + accent;
  if (said === iconSaid) return;
  iconSaid = said;
  let link = document.querySelector('link[rel="icon"][data-mark]');
  if (!link) { link = h('link', { rel: 'icon', type: 'image/svg+xml', 'data-mark': '' }); document.head.append(link); }
  link.href = 'data:image/svg+xml,' + encodeURIComponent(mark.markup({ born, hours: m.hours, look: m.look, size: 64 }, accent));
}

export function markNow() {
  const p = S.config?.cognition?.pulse_seconds, t = S.stats?.lifetime_ticks, vo = S.voiceNow?.() || {};
  return mark && !isNaN(born) && p && t != null ? { mark, born, hours: t * p / 3600, look: mark.lookOf(getComputedStyle(document.documentElement)),
    state: S.wsEverOpened && !S.connected ? 'offline' : vo.listening ? 'listening' : vo.speaking ? 'speaking' : S.thinking ? 'answering' : 'rest' } : null;
}

export function renderMarks(v = S.view) {
  knowBorn(); fetchMark();
  const m = markNow();
  inSlot('bar-mark', 32, m); inSlot('rail-mark', 86, m); inAbout(v, m); inTab(m); S.markPreview?.();
}
S.renderMarks = renderMarks;
