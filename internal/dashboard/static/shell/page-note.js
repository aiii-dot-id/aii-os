import { S } from '../state.js';
import { $, h, fill } from '../util.js';
import * as osNote from '../components/os-note/os-note.js';
import W from '../strings/status.js';

const clock = when => {
  const said = String(when || '');
  const t = Date.parse(said);
  if (!isNaN(t)) return new Date(t).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  return (said.match(/^\d{1,2}:\d{2}/) || [''])[0];
};

const SVG = 'http://www.w3.org/2000/svg';
function warningIcon() {
  const svg = document.createElementNS(SVG, 'svg');
  svg.setAttribute('class', 'c-os-note__icon');
  svg.setAttribute('viewBox', '0 0 18 18');
  svg.setAttribute('aria-hidden', 'true');
  for (const d of ['M9 2.5 16 15H2z', 'M9 7.5v3.5M9 13.2v.1']) {
    const path = document.createElementNS(SVG, 'path');
    path.setAttribute('d', d);
    svg.appendChild(path);
  }
  return svg;
}

export function pageNotes() {
  if (!S.connected) return $('dashboard-signin') ? [] : [{ tone: 'warn', text: S.phoneAsleepNow ? W.asleep : W.offline }];
  const read = S.pageFrames || {};
  if (!read.status || !read.continuity) return [];
  const n = (S.stats && S.stats.name) || '', name = n && n !== 'Unnamed' ? n : W.unnamed;
  const notes = [];
  const mode = S.cont && S.cont.mode;
  if (mode === 'safe') notes.push({ tone: 'bad', text: fill(W.safeMode, { name, reason: S.cont.safe_reason || '' }) });
  else if (mode === 'degraded_witness') notes.push({ tone: 'warn', text: fill(W.verificationDown, { name, time: clock(S.cont.degraded_since) }) });
  else if (mode === 'degraded_ring5') notes.push({ tone: 'warn', text: fill(W.waitingPrinciples, { name }) });
  const cw = S.stats && S.stats.credential_warning;
  if (cw) notes.push({ tone: 'warn', text: fill(W.credentialWarning, { what: cw }) });
  return notes;
}

export function renderPageNote() {
  const host = $('page-note');
  if (!host) return;
  const notes = S.firstConnect ? [] : pageNotes();
  const sig = JSON.stringify(notes);
  if (host.dataset.signature === sig) return;
  host.dataset.signature = sig;
  host.replaceChildren(...notes.map(n => {
    const el = osNote.render({ tone: n.tone, text: n.text, live: false,
      actions: S.connected ? [h('a', { class: 'c-os-note__link', href: '#/settings' }, W.linkStatus)] : [] });
    el.prepend(warningIcon());
    return el;
  }));
}
