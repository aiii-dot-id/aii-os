import { S } from '../../../state.js';
import { h, fill } from '../../../util.js';
import * as sideNav from '../../../components/side-nav/side-nav.js';
import { loadSees } from './sent.js';
import { readLogs, drawLogs } from './logs.js';
import W from '../../../strings/settings-technical.js';
import WF from '../../../strings/settings-technical-forms.js';

const opened = load => (S.settingsIn ? S.settingsIn() : Promise.resolve()).then(load);
const didNotOpen = err => import('../../../recovery.js').then(r => r.partFailed(W.title, err), () => {});
const form = (id, load, draw, read) => {
  let m = null, failed = false, reads = 0;
  const stopped = err => { failed = true; didNotOpen(err); return true; };
  return { id, label: () => WF.panels[id], live: true,
    read: () => {
      failed = false;
      if (!read) return;
      const mine = ++reads;
      opened(load).then(x => { m = x; if (mine === reads && S.view === 'technical' && drawn?.panel === id) read(x); }, stopped);
    },
    draw: (root, name, pane = root) => {
      if (m) return m[draw](root) !== false;
      if (failed) return true;
      return opened(load).then(x => { m = x; return pane.dataset.panel !== id || x[draw](root) !== false; }, stopped);
    } };
};
const forms = () => import('./forms.js');
function logsParts(pane) {
  if (!pane.querySelector(':scope > .technical__part')) pane.replaceChildren(h('div', { class: 'technical__part' }), h('div', { class: 'technical__part' }));
  return pane.querySelectorAll(':scope > .technical__part');
}
const logsForm = form('logs', forms, 'drawLogsForm');
function drawLogsPanel(pane) {
  const [settings, reader] = logsParts(pane);
  drawLogs(reader);
  return logsForm.draw(settings, '', pane);
}
const nameNow = () => { const name = (S.identityExists && S.stats && S.stats.name) || ''; return name === 'Unnamed' ? '' : name; };
export const seesName = () => { const name = nameNow(); return name ? fill(W.sees, { name }) : W.seesNoName; };
const PANELS = [
  { id: 'sent', label: seesName, draw: loadSees },
  form('verification', forms, 'drawVerification', m => m.readRecordState()),
  form('helpers', forms, 'drawHelpers'),
  form('reflection', () => import('./reflection.js'), 'drawReflection'),
  { id: 'logs', label: () => W.logs, read: () => { logsForm.read(); readLogs(); }, draw: drawLogsPanel, live: true },
  form('storage', forms, 'drawStorage'),
  form('limits', () => import('./limits.js'), 'drawLimits'),
  form('inforce', () => import('./in-force.js'), 'drawInForce', m => m.readInForce()),
  form('layout', () => import('./layout.js'), 'drawLayout'),
];

const panelsNow = () => PANELS.filter(p => p.id !== 'helpers' || S.config?.agency?.roles?.length);

function section() {
  let el = document.getElementById('view-technical');
  if (el) return el;
  const views = document.getElementById('views');
  if (!views) return null;
  el = h('section', { class: 'view', id: 'view-technical', 'aria-hidden': 'true' }, h('div', { class: 'stack', id: 'technical-stack' }));
  el.inert = true;
  views.appendChild(el);
  return el;
}
section();

let drawn = null;
let stale = true;

export function readTechnical() { stale = true; }

function address() {
  const m = /^#\/settings\/technical(?:\/([^/?#]+))?/.exec(location.hash || '');
  return m && m[1] ? m[1] : '';
}

function skeleton(host) {
  const title = h('h1', { class: 'technical__title' });
  const nav = h('div', { class: 'technical__nav' });
  const pane = h('div', { class: 'ledger-pane technical__pane' });
  const root = h('div', { class: 'technical' }, nav, title, pane);
  host.replaceChildren(root);
  return { root, title, nav, pane, panel: '', name: null, failed: false };
}

export function renderTechnical() {
  const el = section();
  const host = el && el.querySelector('#technical-stack');
  if (!host) return;
  if (!drawn || !host.contains(drawn.root)) drawn = skeleton(host);
  const name = nameNow();
  const panels = panelsNow();
  const want = panels.find(p => p.id === address()) || panels[0];
  const items = panels.map(p => ({ id: p.id, label: p.label(name), href: '#/settings/technical/' + p.id }));
  const listed = drawn.nav.firstChild;
  if (listed && drawn.name === name) sideNav.update(listed, { items, selected: want.id });
  else drawn.nav.replaceChildren(sideNav.render({ label: W.title, items, selected: want.id }));
  drawn.name = name;
  const rail = document.querySelector('#place-tabs .c-side-nav');
  if (rail) {
    rail.querySelectorAll(':scope > .technical__nav').forEach(old => { if (old !== drawn.nav) old.remove(); });
    const entry = rail.querySelector(':scope > [data-item="technical"]');
    if (entry) {
      if (entry.nextElementSibling !== drawn.nav) entry.after(drawn.nav);
      entry.setAttribute('aria-current', 'true');
    }
  }
  drawn.title.textContent = want.label(name);
  drawn.nav.querySelector('[aria-current="page"]')?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' });
  const entering = drawn.panel !== want.id || stale || drawn.failed;
  if (entering) {
    if (drawn.panel !== want.id) { drawn.pane.textContent = ''; drawn.pane.technicalDrawn = null; delete drawn.pane.dataset.form; }
    drawn.panel = want.id;
    drawn.pane.dataset.panel = want.id;
    stale = false;
    drawn.failed = false;
    if (want.read) want.read(name);
  }
  if (entering || want.live) {
    const d = drawn;
    Promise.resolve(want.draw(d.pane, name)).then(ok => { if (ok === false && drawn === d && d.panel === want.id) d.failed = true; });
  }
}

S.renderTechnical = renderTechnical;
window.addEventListener('hashchange', () => { if (S.view === 'technical') S.renderTechnical?.(); });
