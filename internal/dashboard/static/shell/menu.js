import { S } from '../state.js';
import { h, fill } from '../util.js';
import { readRecord, recordWord, fieldOf } from '../views/identity/record-read.js';
import { send } from '../ws.js';
import W from '../strings/shell.js';
import './keys.js';
import { sheet } from './sheet.js';
sheet('shell/menu.css').catch(() => {});
import WI from '../strings/identity.js';
import WS from '../strings/status.js';
import WT from '../strings/settings-technical.js';
import WF from '../strings/settings-technical-forms.js';

const nameOf = () => { const n = (S.stats && S.stats.name) || ''; return n === 'Unnamed' ? '' : n; };

const SYNONYMS = {
  substrate: 'model thinking provider timeout request',
  providers: 'api key sign in openai anthropic gemini endpoint reasoning effort',
  speech: 'voice microphone speaking listening transcription',
  dashboard: 'port https access public name relay route address',
  backups: 'backup snapshot restore escrow keys',
  sandbox: 'folders files allowed',
  tools: 'tool shell browser',
  messages: 'channels email telegram contacts notices unsent held mail',
  updates: 'app theme dark light notifications version update install uninstall',
};
const TECHNICAL = [
  ['verification', WF.panels.verification, 'receipts witness server'],
  ['helpers', WF.panels.helpers, 'agency sub-agents roles local model'],
  ['reflection', WF.panels.reflection, 'identity dreaming consolidation morning brief rhythm cognition'],
  ['logs', WT.logs, 'log files'],
  ['storage', WF.panels.storage, 'database compression download'],
  ['limits', WF.panels.limits, 'prompt tokens context recent turns tools rounds budget'],
  ['inforce', WF.panels.inforce, 'config every setting key default changed'],
  ['layout', WF.panels.layout, 'ui-layout sections slots profile'],
];

let restartAsked = false;
function actions() {
  const out = [];
  if (S.thinking) out.push({ label: W.actionStop, keys: 'Esc', act: () => { close(); send({ type: 'cancel' }); } });
  out.push({ label: W.actionUpdates, href: '#/settings/updates' });
  out.push(restartAsked ? { label: W.actionRestartAsk, act: () => { close(); send({ type: 'restart' }); } }
    : { label: W.actionRestart, aside: W.actionRestartAsks, act: () => { restartAsked = true; draw(); } });
  out.push({ label: W.actionKeys, keys: '?', act: () => { close(); openKeys(); } });
  out.push({ label: W.actionAnotherDevice, href: '#/settings/phone' }, { label: W.actionModel, href: '#/settings/substrate' },
    { label: W.actionBackup, href: '#/settings/backups' }, { label: W.actionTheme, href: '#/settings/updates' });
  return out;
}

const RECENT = 'aii.searchRecent';
function recent() {
  try {
    const r = JSON.parse(localStorage.getItem(RECENT) || '[]');
    return Array.isArray(r) ? r.filter(x => x && typeof x.label === 'string' && typeof x.href === 'string').slice(0, 5) : [];
  } catch (err) { return []; }
}
function remember(it) {
  try {
    const kept = [{ label: it.label, href: it.href, voice: !!it.voice }, ...recent().filter(x => x.href !== it.href)].slice(0, 5);
    localStorage.setItem(RECENT, JSON.stringify(kept));
  } catch (err) { }
}

function places() {
  const name = nameOf();
  const about = name ? fill(W.about, { name }) : W.aboutNoName;
  return [
    { label: WS.title, href: '#/settings/status', words: 'status state health' },
    { label: W.railChat, href: '#/chat', keys: 'G C', words: 'conversation thread' },
    { label: W.projects, href: '#/projects', keys: 'G P' },
    { label: about, href: '#/identity', keys: 'G A' },
    { label: W.settings, href: '#/settings', keys: 'G S' },
    { label: about + ' · ' + WI.tabs.who, href: '#/identity/who', words: 'portrait charter' },
    { label: about + ' · ' + WI.tabs.between, href: '#/identity/between', words: 'changes visit relationships' },
    { label: about + ' · ' + WI.tabs.beliefs, href: '#/identity/beliefs', words: 'memory' },
    { label: about + ' · ' + WI.tabs.experiences, href: '#/identity/experiences', words: 'notes lived' },
    { label: about + ' · ' + WI.tabs.record, href: '#/identity/record', words: 'ledger entries' },
    ...sections.map(([id, label]) => ({ label: W.settings + ' · ' + label, href: '#/settings/' + id, words: SYNONYMS[id] || '' })),
    { label: W.settings + ' · ' + W.phoneAndDevices, href: '#/settings/phone', words: 'phone mobile device link scan code qr another sign out lost' },
    { label: W.settings + ' · ' + W.pluginsAndPermissions, href: '#/settings/permissions', words: 'plugins permissions' },
    ...TECHNICAL.filter(([id]) => id !== 'helpers' || S.config?.agency?.roles?.length).map(([id, label, words]) => ({ label: W.settings + ' · ' + WT.title + ' · ' + label, href: '#/settings/technical/' + id, words })),
    ...((S.projects || []).map(p => ({ label: W.projects + ' · ' + (p.name || p.id), href: '#/projects/' + encodeURIComponent(p.id) }))),
  ];
}

const matches = (q, ...texts) => texts.some(t => String(t || '').toLowerCase().includes(q));

function groups(q, record) {
  const name = nameOf();
  const out = [];
  if (!q) { const r = recent(); if (r.length) out.push([W.groupRecent, r]); }
  if (q) {
    const beliefs = ((S.identity && S.identity.beliefs) || []).filter(b => matches(q, b.statement)).slice(0, 5)
      .map(b => ({ label: b.statement, href: '#/identity/beliefs/' + encodeURIComponent(b.id), voice: true }));
    if (beliefs.length) out.push([name ? fill(W.groupBeliefs, { name }) : W.groupBeliefsUnnamed, beliefs]);
    if (record && record.rows.length) out.push([W.groupRecord, record.rows]);
    const alarms = ((S.work && S.work.alarms) || []).filter(a => matches(q, a.message, a.tag)).slice(0, 5)
      .map(a => ({ label: a.message || a.tag, href: '#/identity/who', voice: true }));
    if (alarms.length) out.push([W.groupAlarms, alarms]);
  }
  const go = places().filter(p => !q || matches(q, p.label, p.words));
  if (go.length) out.push([W.groupGoTo, go]);
  const acts = actions().filter(a => !q || matches(q, a.label));
  if (acts.length) out.push([W.groupActions, acts]);
  return out;
}

let dialog = null, input = null, list = null, note = null, at = 0, items = [], recordAsk = 0, record = null;

let recordTimer = 0;
function retireRecordRead() { clearTimeout(recordTimer); recordAsk++; record = null; }
function readRecordFor(q) {
  retireRecordRead();
  if (q.length < 3) return;
  const ask = recordAsk;
  recordTimer = setTimeout(() => {
    readRecord({ q }).then(page => {
      if (ask !== recordAsk) return;
      const words = r => ['content', 'statement', 'reason', 'text'].map(f => fieldOf(r.head, f)).find(Boolean) || '';
      record = { rows: (page.rows || []).slice(0, 5).map(r => ({ label: [recordWord(r.type, WI), words(r).slice(0, 60)].filter(Boolean).join(' · '), href: '#/identity/record/' + r.seq })) };
      draw();
    }, () => { if (ask === recordAsk) { record = { rows: [{ label: W.recordUnread, href: '#/identity/record' }] }; draw(); } });
  }, 250);
}

function draw() {
  const q = input.value.trim().toLowerCase();
  const found = groups(q, record);
  items = [];
  list.replaceChildren(...found.map(([title, rows], g) => h('section', { class: 'c-menu__group', 'aria-labelledby': 'menu-g' + g },
    h('h2', { class: 'c-menu__title', id: 'menu-g' + g }, title),
    h('ul', { class: 'c-menu__rows', role: 'listbox', 'aria-labelledby': 'menu-g' + g }, ...rows.map(r => {
      const i = items.length;
      const li = h('li', { class: 'c-menu__row', role: 'option', id: 'menu-r' + i, 'aria-selected': 'false', onclick: () => choose(i) },
        h('span', { class: r.voice ? 'c-menu__label c-menu__label--voice' : 'c-menu__label' }, r.label),
        r.aside ? h('span', { class: 'c-menu__aside' }, r.aside) : null,
        r.keys ? h('kbd', { class: 'c-menu__keys' }, r.keys) : null);
      items.push(Object.assign({ el: li }, r));
      return li;
    })))));
  if (!items.length) list.append(h('p', { class: 'c-menu__none' }, W.searchNothing));
  move(Math.min(at, Math.max(0, items.length - 1)));
}

function move(i) {
  if (!items.length) { input.removeAttribute('aria-activedescendant'); return; }
  at = (i + items.length) % items.length;
  items.forEach((it, n) => it.el.setAttribute('aria-selected', n === at ? 'true' : 'false'));
  input.setAttribute('aria-activedescendant', items[at].el.id);
  items[at].el.scrollIntoView({ block: 'nearest' });
}

function choose(i) {
  const it = items[i];
  if (!it) return;
  if (it.act) { it.act(); return; }
  remember(it);
  close();
  location.hash = it.href;
}

function close() {
  if (!dialog || !dialog.open) return;
  dialog.close();
  retireRecordRead();
}

function make() {
  input = h('input', { type: 'search', class: 'c-menu__field', role: 'combobox', 'aria-expanded': 'true', 'aria-controls': 'menu-list',
    'aria-autocomplete': 'list', autocomplete: 'off', spellcheck: 'false', 'aria-label': W.searchPlaceholder, placeholder: W.searchPlaceholder });
  list = h('div', { class: 'c-menu__list', id: 'menu-list' });
  note = h('p', { class: 'c-menu__note' });
  const foot = h('div', { class: 'c-menu__foot' },
    h('span', { class: 'c-menu__hint' }, h('kbd', {}, '↑'), ' ', h('kbd', {}, '↓'), ' ' + W.footMove),
    h('span', { class: 'c-menu__hint' }, h('kbd', {}, 'Enter'), ' ' + W.footOpen), note);
  dialog = h('dialog', { class: 'c-menu', 'aria-label': W.searchPlaceholder },
    h('div', { class: 'c-menu__head' }, input, h('kbd', { class: 'c-menu__esc', 'aria-hidden': 'true' }, 'Esc')), list, foot);
  input.addEventListener('input', () => { at = 0; readRecordFor(input.value.trim()); draw(); });
  input.addEventListener('keydown', e => {
    if (e.key === 'ArrowDown') { e.preventDefault(); move(at + 1); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); move(at - 1); }
    else if (e.key === 'Enter') { e.preventDefault(); choose(at); }
  });
  dialog.addEventListener('click', e => { if (e.target === dialog) close(); });
  dialog.addEventListener('cancel', retireRecordRead);
  dialog.addEventListener('close', retireRecordRead);
  document.body.append(dialog);
}

let sections = [], sectionsAsked = false;
function askSections() {
  if (sectionsAsked) return;
  sectionsAsked = true;
  import('../views/settings.js').then(m => { sections = m.SECTIONS || []; if (dialog && dialog.open) draw(); }, () => { sectionsAsked = false; });
}

export function openMenu() {
  askSections();
  if (!dialog) make();
  if (dialog.open) { input.focus(); return; }
  input.value = ''; at = 0; restartAsked = false;
  retireRecordRead();
  const name = nameOf();
  note.textContent = name ? fill(W.searchPrivate, { name }) : W.searchPrivateUnnamed;
  draw();
  dialog.showModal();
  input.focus();
}

let keysDialog = null;
const KEYS = () => [
  ['Ctrl K', W.keySearch], ['G C', W.railChat], ['G P', W.projects], ['G A', nameOf() ? fill(W.about, { name: nameOf() }) : W.aboutNoName],
  ['G S', W.settings], ['↑ ↓', W.keyMove], ['Enter', W.keyOpen], ['Esc', W.keyClose], ['?', W.keysTitle],
];
export function openKeys() {
  if (!keysDialog) {
    keysDialog = h('dialog', { class: 'c-menu c-menu--keys', 'aria-labelledby': 'keys-title' });
    document.body.append(keysDialog);
  }
  keysDialog.replaceChildren(h('h2', { class: 'c-menu__heading', id: 'keys-title' }, W.keysTitle),
    h('dl', { class: 'c-menu__keylist' }, ...KEYS().flatMap(([k, what]) => [h('dt', {}, h('kbd', {}, k)), h('dd', {}, what)])),
    h('p', { class: 'c-menu__note' }, W.keysHint));
  if (!keysDialog.open) keysDialog.showModal();
}
