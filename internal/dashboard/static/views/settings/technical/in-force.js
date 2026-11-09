import { REFLECTION_GROUPS } from './reflection.js';
import { LIMIT_GROUPS } from './limits.js';
import { named, settingOf, shownUnit } from './forms.js';
import { seesName } from './technical.js';
import WSHELL from '../../../strings/shell.js';
import { signedFetch } from '../../../pagekey.js';
import { configBusy, configSaidHTML, sendConfigChanges } from '../save.js';
import { S } from '../../../state.js';
import { copyText, esc, fill } from '../../../util.js';
import W from '../../../strings/settings-technical-forms.js';
import WT from '../../../strings/settings-technical.js';
import WACCESS from '../../../strings/settings-access.js';
import WPEOPLE from '../../../strings/settings-people.js';
import WAPP from '../../../strings/settings-app.js';

const F = W.inForce;
const TABLE = new Map([
  ...REFLECTION_GROUPS.flatMap(([, rows]) => rows.map(([key, word]) => [key, { word: W.reflection[word], panel: 'reflection' }])),
  ...LIMIT_GROUPS.flatMap(([, rows]) => rows.map(([key, word]) => [key, { word: W.limits[word], panel: 'limits' }])),
  ['prompt.max_tokens', { word: W.limits.promptBudget, panel: 'limits' }],
  ['prompt.recent_turns', { word: W.limits.recentTurns, panel: 'limits' }],
  ['witness_server.url', { word: W.verification.server, panel: 'verification' }],
  ['witness_server.interval_events', { word: W.verification.interval, panel: 'verification' }],
  ['logs.dir', { word: W.logsForm.folder, panel: 'logs' }],
  ['logs.max_backups', { word: W.logsForm.keep, panel: 'logs' }],
  ['logs.compress_days', { word: W.logsForm.compressAfter, panel: 'logs' }],
  ['logs.max_bytes', { word: W.logsForm.rollAt, panel: 'logs' }],
  ['logs.max_total_bytes', { word: W.logsForm.keepUnder, panel: 'logs' }],
  ['cognition.morning_brief_at', { word: W.reflection.briefAt, panel: 'reflection' }],
]);
const PANEL_NAMES = { reflection: W.panels.reflection, limits: W.panels.limits, verification: W.panels.verification, logs: WT.logs };
const BLOCKS = [[/^identity\.db_/, 'technical/storage', W.panels.storage], [/^agency\.roles/, 'technical/helpers', W.panels.helpers],
  [/^witness_server\./, 'technical/verification', W.panels.verification], [/^logs\./, 'technical/logs', WT.logs],
  [/^dashboard\.sent_requests\./, 'technical/sent', seesName], [/^llm\./, 'substrate', WSHELL.model], [/^speech\./, 'speech', W.groups.speech],
  [/^(dashboard|certificate)\./, 'dashboard', WACCESS.title], [/^contacts/, 'messages', WPEOPLE.title], [/^updates\./, 'updates', WAPP.title]];
export function placeOf(key) {
  const known = TABLE.get(key);
  if (known) return { address: 'technical/' + known.panel, label: PANEL_NAMES[known.panel] };
  const block = BLOCKS.find(([re]) => re.test(key));
  if (!block) return null;
  if (block[1] === 'technical/helpers' && !S.config?.agency?.roles?.length) return null;
  return { address: block[1], label: typeof block[2] === 'function' ? block[2]() : block[2] };
}
const TABLED = new Set([...REFLECTION_GROUPS, ...LIMIT_GROUPS].flatMap(([, rows]) => rows.map(([key]) => key)));
const canSetBack = row => row.stated && row.usual && row.value !== row.usual && TABLED.has(row.key) && row.key !== 'llm.timeout_seconds';

let rows = null, fileUnread = '', failed = '', query = '', changedOnly = false, open = '', reading = false, copied = '', here = '', lastRead = 0;
const plain = value => (value === '[]' ? '' : value || '');
const changed = r => plain(r.value) !== plain(r.usual) && (r.stated || !!fileUnread);
export function readInForce(again) {
  if (reading && !again) return;
  const mine = ++lastRead;
  reading = true; failed = '';
  signedFetch('/settings-in-force', { cache: 'no-store', credentials: 'same-origin' }).then(async res => {
    if (!res.ok) throw new Error((await res.text()).trim() || String(res.status));
    const read = await res.json();
    if (mine === lastRead) { rows = read.settings || []; fileUnread = read.file_unread || ''; }
  }).catch(err => { if (mine === lastRead) failed = (err && err.message) || String(err); }).finally(() => { if (mine === lastRead) reading = false; S.renderTechnical?.(); });
}
const NAMED_UNITS = { seconds: 'seconds', ms: 'milliseconds', minutes: 'minutes', hours: 'hours', days: 'days', bytes: 'bytes', chars: 'characters', tokens: 'tokens' };
function unitOf(r) {
  const set = S.config && settingOf(S.config, r.key);
  if (set) return set;
  const named = /_(seconds|ms|minutes|hours|days|bytes|chars|tokens)$/.exec(r.key);
  return named ? { unit: NAMED_UNITS[named[1]], default: Number(r.usual) || Number(r.value) } : null;
}
function inItsUnit(r, value) {
  const set = unitOf(r);
  if (!set || !/^\d+$/.test(value || '')) return value;
  const [unit, per] = shownUnit(set);
  return (+(value / per).toFixed(3)).toLocaleString() + (unit ? ' ' + unit : '');
}
function rowHTML(r) {
  const known = TABLE.get(r.key);
  const name = known ? named(known.word) : '';
  const value = !r.hidden ? inItsUnit(r, r.value) || '' : fileUnread ? F.hiddenUnread : r.stated ? F.hidden : F.hiddenAbsent;
  const setBy = fileUnread || (r.hidden && !r.stated) ? '' : r.stated ? F.setByFile : F.setByDefault;
  const place = placeOf(r.key);
  const isOpen = open === r.key;
  return '<tr class="technical-force__row"' + (here === r.key ? ' data-force-here' : '') + ' data-force-row="' + esc(r.key) + '"><td class="technical-force__name"><div class="technical-force__names">' + (name ? '<span class="technical-force__title">' + esc(name) + '</span>' : '') + '<code>' + esc(r.key).replace(/[._]/g, '$&<wbr>') + '</code></div></td>' +
    '<td class="technical-force__value"><span>' + esc(value) + '</span>' + (plain(r.usual) && r.usual !== r.value ? ' <span class="technical-force__usual">' + esc(fill(F.defaultIs, { value: inItsUnit(r, r.usual) })) + '</span>' : '') + '</td>' +
    '<td class="technical-force__by">' + esc(setBy) + '</td>' +
    '<td class="technical-force__more"><details data-force-more="' + esc(r.key) + '"' + (isOpen ? ' open' : '') + '><summary aria-label="' + esc(fill(F.more, { setting: name || r.key })) + '">…</summary><div class="technical-force__menu">' +
    '<button type="button" class="c-button c-button--quiet" data-force-link="' + esc(r.key) + '">' + esc(copied === 'link:' + r.key ? F.copiedLink : F.copyLink) + '</button>' +
    '<button type="button" class="c-button c-button--quiet" data-force-key="' + esc(r.key) + '">' + esc(copied === 'key:' + r.key ? F.copiedKey : F.copyKey) + '</button>' +
    (place ? '<a class="c-button c-button--quiet" href="#/settings/' + place.address + '">' + esc(fill(F.openIn, { panel: place.label })) + '</a>' : '') +
    (canSetBack(r) ? '<button type="button" class="c-button c-button--quiet" data-force-back="' + esc(r.key) + '">' + esc(fill(F.setBack, { value: inItsUnit(r, r.usual) })) + '</button>' : '') +
    '</div></details></td></tr>';
}
const matches = r => {
  const q = query.trim().toLowerCase();
  if (changedOnly && !changed(r)) return false;
  if (!q) return true;
  const known = TABLE.get(r.key);
  return r.key.toLowerCase().includes(q) || (known && named(known.word).toLowerCase().includes(q));
};
function wire(root) {
  const search = root.querySelector('[data-force-search]');
  if (search) search.oninput = () => { query = search.value; draw(root); };
  root.querySelectorAll('[data-force-filter]').forEach(b => { b.onclick = () => { changedOnly = b.dataset.forceFilter === 'changed'; draw(root); }; });
  root.querySelectorAll('[data-force-more]').forEach(d => { d.ontoggle = () => { if (d.open) open = d.dataset.forceMore; else if (open === d.dataset.forceMore) open = ''; }; });
  const copy = (button, kind, text) => { button.onclick = async () => { if (await copyText(text)) { copied = kind + ':' + button.dataset[kind === 'link' ? 'forceLink' : 'forceKey']; draw(root); } }; };
  root.querySelectorAll('[data-force-link]').forEach(b => copy(b, 'link', location.origin + location.pathname + '#/settings/technical/inforce/' + b.dataset.forceLink));
  root.querySelectorAll('[data-force-key]').forEach(b => copy(b, 'key', b.dataset.forceKey));
  root.querySelectorAll('[data-force-back]').forEach(b => { b.onclick = () => { sendConfigChanges('inforce', { [b.dataset.forceBack]: null }); readAfter = true; S.renderTechnical?.(); }; });
}
let readAfter = false;
let drawnFor = null;
function keepFocus(root, write) {
  const was = document.activeElement;
  const row = was && root.contains(was) ? was.closest('[data-force-row]') : null;
  const press = row && ['data-force-link', 'data-force-key', 'data-force-back'].find(name => was.hasAttribute(name));
  const key = row && row.dataset.forceRow;
  write();
  if (!key) return;
  const again = root.querySelector('[data-force-row="' + CSS.escape(key) + '"]');
  if (again) ((press && again.querySelector('[' + press + ']')) || again.querySelector('summary'))?.focus();
}
function bringRow(root) {
  let key = '';
  try { key = decodeURIComponent((location.hash.match(/^#\/settings\/technical\/inforce\/(.+)$/) || [])[1] || ''); } catch { key = ''; }
  if (key === here) return;
  here = key;
  const row = rows.find(r => r.key === key);
  if (row && !matches(row)) {
    query = ''; changedOnly = false;
    const search = root.querySelector('[data-force-search]');
    if (search) search.value = '';
  }
  draw(root);
  if (row) root.querySelector('[data-force-row="' + CSS.escape(key) + '"]')?.scrollIntoView({ block: 'center' });
}
function draw(root) {
  const list = (rows || []).filter(matches);
  const table = root.querySelector('[data-force-table]');
  if (table) keepFocus(root, () => { table.innerHTML = list.map(rowHTML).join(''); });
  const none = root.querySelector('[data-force-none]');
  if (none) none.hidden = !!list.length;
  root.querySelectorAll('[data-force-filter]').forEach(b => { const on = (b.dataset.forceFilter === 'changed') === changedOnly; b.setAttribute('aria-pressed', on ? 'true' : 'false'); });
  wire(root);
}
export function drawInForce(root) {
  if (failed) { root.innerHTML = '<p class="technical-form__warn" role="status">' + esc(fill(F.unreadable, { why: failed })) + '</p>'; return false; }
  if (!rows) { root.textContent = W.loading; return true; }
  if (readAfter && !configBusy()) { readAfter = false; readInForce(true); }
  if (!root.querySelector('[data-force-table]')) {
    drawnFor = null; here = '';
    root.innerHTML = '<div class="technical-form technical-force"><p class="technical-form__heading">' + esc(F.heading) + '</p>' +
      '<p class="technical-form__warn" role="status" data-force-unread hidden></p>' +
      '<div class="technical-force__tools"><label class="technical-force__search"><span class="technical-force__label" data-force-searching></span>' +
      '<input class="technical-form__input" type="search" data-force-search value="' + esc(query) + '"></label>' +
      '<button type="button" class="c-chip" data-force-filter="all"></button><button type="button" class="c-chip" data-force-filter="changed"></button></div>' +
      '<p class="technical-form__said" role="status" data-force-said hidden></p>' +
      '<div class="technical-force__scroll"><table class="technical-force__table"><thead><tr><th>' + esc(F.colSetting) + '</th><th>' + esc(F.colValue) + '</th><th>' + esc(F.colSetBy) + '</th><th><span class="technical-force__label">' + esc(F.colActions) + '</span></th></tr></thead><tbody data-force-table></tbody></table></div>' +
      '<p class="technical-form__hint" data-force-none hidden>' + esc(F.none) + '</p>' +
      '<p class="technical-form__hint">' + esc(F.note) + '</p>' +
      '<section class="technical-form__group"><h3 class="technical-form__group-title">' + esc(F.wrongTitle) + '</h3><p class="technical-form__hint">' + fill(esc(F.wrong), { name: esc(named('{name}')), shipped: '<a href="/shipped/"><code>/shipped/</code></a>', plain: '<a href="/plain/"><code>/plain/</code></a>' }) + '</p></section></div>';
  }
  const said = configSaidHTML('inforce');
  const sig = JSON.stringify([rows, fileUnread, said, S.config?.cognition?.settings?.length || 0]);
  if (drawnFor !== sig) {
    drawnFor = sig;
    const all = rows.length, searchWords = fill(F.search, { n: all });
    root.querySelector('[data-force-searching]').textContent = searchWords;
    root.querySelector('[data-force-search]').placeholder = searchWords;
    root.querySelector('[data-force-filter="all"]').textContent = fill(F.all, { n: all });
    root.querySelector('[data-force-filter="changed"]').textContent = fill(F.changed, { n: rows.filter(changed).length });
    const unread = root.querySelector('[data-force-unread]');
    unread.hidden = !fileUnread;
    unread.textContent = fileUnread ? fill(F.fileUnread, { why: fileUnread }) : '';
    const saying = root.querySelector('[data-force-said]');
    saying.hidden = !said;
    if (saying.innerHTML !== said) saying.innerHTML = said;
    draw(root);
  }
  bringRow(root);
  return true;
}
