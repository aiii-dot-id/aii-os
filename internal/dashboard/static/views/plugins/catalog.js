import { S } from '../../state.js';
import { $, esc, bytes, fill } from '../../util.js';
import { send } from '../../ws.js';
import { pendingSlot } from '../../pending.js';
import { renderPlugins } from '../plugins.js';
import { storedCounts } from './stored.js';
import { refusalDetailHTML, lifecycleHTML, componentSetsHTML } from './card.js';
import W from '../../strings/settings-permissions.js';

const store = { q: '', cat: '', sort: 'name', open: {}, shown: false };

const TIER = { T3: W.tierPlatform, T2: W.tierReviewed, T1: W.tierSigned, T0: W.tierUnsigned };
export function tierWords(t) { return TIER[t] || t || W.tierUnsigned; }
function tierLabel(t) { return esc(tierWords(t).toLowerCase()); }
function metaHTML(e) {
  if (e.installed && e.installed_tier) return esc(e.installed_version || e.version) + ' · ' + tierLabel(e.installed_tier);
  const build = buildLabel(e.build);
  return esc(e.version) + ' · the catalog says ' + tierLabel(e.tier) + (build ? ', ' + build : '');
}
function buildLabel(b) {
  return b === 'portable' ? 'portable' : b === 'system' ? 'built for this system' : '';
}
const START = 'Start';
const startPending = pendingSlot();
let startSaid = null;
function startButton(id) {
  const asked = startPending.waiting();
  return '<button class="btn sm" data-plugin-start="' + esc(id) + '"' + (asked ? ' disabled' : '') + '>' +
    (asked && asked.id === id ? 'Starting…' : START) + '</button>';
}
function startSaidHTML(id) {
  return startSaid && startSaid.id === id ? '<div class="prep-err" data-start-said>' + esc(startSaid.text) + '</div>' : '';
}
S.pluginStartAnswered = requestID => { if (startPending.claim(requestID)) startSaid = null; };
S.pluginStartRefused = (requestID, text) => {
  const asked = startPending.claim(requestID);
  if (!asked) return false;
  startSaid = { id: asked.id, text: text };
  if ($('plugins-stack')) renderPlugins();
  return true;
};
S.pluginsConnectionLost = () => { startPending.drop(); };
function titleOf(e) { return e.title || e.id; }
function catOf(e) { return String(e.category || 'other').toLowerCase(); }
function matches(e, q) {
  if (!q) return true;
  const hay = [e.title, e.id, e.summary, e.description, e.publisher, e.category, e.license]
    .concat(e.keywords || []).filter(Boolean).join(' ').toLowerCase();
  return q.toLowerCase().split(/\s+/).filter(Boolean).every(t => hay.includes(t));
}
function sorted(entries) {
  const list = entries.slice();
  const byName = (a, b) => titleOf(a).localeCompare(titleOf(b), undefined, { sensitivity: 'base' });
  if (store.sort === 'updated') list.sort((a, b) => String(b.updated || '').localeCompare(String(a.updated || '')) || byName(a, b));
  else if (store.sort === 'updates') list.sort((a, b) =>
    (Number(!!b.update_available) - Number(!!a.update_available)) || (Number(!!b.installed) - Number(!!a.installed)) || byName(a, b));
  else list.sort(byName);
  return list;
}
function visibleEntries(entries) {
  return sorted(entries.filter(e => matches(e, store.q) && (!store.cat || catOf(e) === store.cat)));
}

function chip(cls, text) { return '<span class="chip' + (cls ? ' ' + cls : '') + '">' + text + '</span>'; }

function detailHTMLFor(e) {
  const row = (k, v) => v ? '<div class="sp-kv"><span>' + k + '</span><b>' + v + '</b></div>' : '';
  return (e.description ? '<p class="store-desc">' + esc(e.description) + '</p>' : '') +
    row('id', esc(e.id)) +
    row('category', esc(catOf(e))) +
    row('keywords', e.keywords && e.keywords.length ? e.keywords.map(esc).join(', ') : '') +
    row('license', e.license && esc(e.license)) +
    row('homepage', e.homepage ? '<a href="' + esc(e.homepage) + '" target="_blank" rel="noopener">' + esc(e.homepage.replace(/^https:\/\//, '')) + '</a>' : '') +
    row('size', e.size ? bytes(e.size) : '') +
    row('needs', needsOf(e)) +
    row('updated', e.updated && esc(String(e.updated).slice(0, 10))) +
    row('installed', e.installed ? esc(e.installed_version || e.version) : '');
}

function needsOf(e) {
  if (e.needs) return esc(e.needs);
  if (e.available && !e.installed && !e.pending) return 'said once its package is fetched — before any model or runtime is';
  return '';
}

const REMAKE_WORDS = {
  line: (n, about) => 'This update changes the model that makes the meaning layer\'s vectors: ' + n +
    (n === 1 ? ' vector is' : ' vectors are') + ' made again' + (about ? ', about ' + about : '') + '.',
};
function aboutTime(seconds) {
  const s = Number(seconds) || 0;
  if (s <= 0) return '';
  if (s < 90) return Math.max(1, Math.round(s)) + ' seconds';
  if (s < 90 * 60) return Math.round(s / 60) + ' minutes';
  const h = Math.round(s / 3600);
  return h + (h === 1 ? ' hour' : ' hours');
}

let storedById = {};

function entryHTML(e) {
  const id = esc(e.id);
  let badge = '', btns = '';
  const install = label => '<button class="btn sm" data-plugin="install:' + id + '">' + label + '</button>';
  const uninstall = () => '<button class="btn sm ghost" data-plugin="uninstall:' + id + '">Uninstall</button>';
  if (e.pending === 'unstarted') {
    badge = chip('provisional', 'not started');
    btns = startButton(e.id) + ' ' + uninstall();
  }
  else if (e.pending) {
    const refused = e.pending === 'refused';
    badge = chip(refused ? 'unverified' : 'provisional', (refused ? 'refused — ' : e.pending === 'starting' ? 'starting — ' : e.pending === 'retiring' ? 'stopping — ' : e.pending === 'held' ? 'held — ' : 'preparing — ') + esc(e.pending_text || ''));
    btns = (refused ? '<button class="btn sm" data-plugin="retry:' + id + '">Try again</button> ' : '') + uninstall();
  }
  else if (e.installed && e.update_available) {
    const build = buildLabel(e.build);
    badge = chip('provisional', 'update to ' + esc(e.version) + (build ? ' — the catalog says ' + build : ''));
    btns = install('Update') + ' ' + uninstall();
  }
  else if (e.installed) { badge = chip('active', 'installed ' + esc(e.installed_version || e.version)); btns = uninstall(); }
  else if (e.available) { btns = install('Install'); }
  else if (e.requires) { badge = chip('unverified', esc(e.requires)); }
  else { badge = chip('', 'no build for this host'); }
  const open = !!store.open[e.id];
  return '<div class="store-row" data-entry="' + id + '">' +
    '<div class="store-main">' +
      '<div class="store-head">' +
        '<button class="store-title" data-detail="' + id + '" aria-expanded="' + open + '">' + esc(titleOf(e)) + '</button>' +
        '<span class="store-meta">' + metaHTML(e) + (e.publisher ? ' · by ' + esc(e.publisher) : '') + '</span>' + badge +
      '</div>' +
      (e.summary ? '<div class="store-summary">' + esc(e.summary) + '</div>' : '') +
      (e.needs ? '<div class="store-hint store-needs">' + esc(e.needs) + '</div>' : '') +
      (e.remakes > 0 ? '<div class="store-hint" data-remakes="' + id + '">' + esc(REMAKE_WORDS.line(Number(e.remakes), aboutTime(e.remake_seconds))) + '</div>' : '') +
      (e.pending === 'unstarted' ? '<div class="store-hint" data-unstarted>' + esc(e.pending_text || '') + '</div>' + startSaidHTML(e.id) : '') +
      (e.available && !e.installed && !e.pending ? '<div class="store-hint" data-install-grants="' + id + '">Install also grants what it asks of the identity\'s own storage, memory, records and embeddings; you can turn each off on its card.</div>' : '') +
      ((e.installed || e.pending) && storedById[e.id] ? '<div class="store-hint" data-stored-kept="' + id + '">Uninstall removes its package. What it stored here (' +
        esc(storedCounts(storedById[e.id])) + ') stays until you remove it under STORED BY PLUGINS.</div>' : '') +
      '<div class="store-detail" data-detail-of="' + id + '"' + (open ? '' : ' hidden') + '>' + detailHTMLFor(e) + '</div>' +
    '</div>' +
    '<div class="store-actions">' + btns + '</div></div>';
}

const relative = new Intl.RelativeTimeFormat([], { numeric: 'auto' });
function ago(when) {
  const t = Date.parse(when || '');
  if (isNaN(t)) return String(when || '');
  const s = Math.round((t - Date.now()) / 1000);
  for (const [unit, size] of [['day', 86400], ['hour', 3600], ['minute', 60]]) {
    if (Math.abs(s) >= size) return relative.format(Math.round(s / size), unit);
  }
  return relative.format(0, 'second');
}
export function catalogNote(pl) {
  const url = (pl && pl.catalog_url) || '';
  const dir = (pl && pl.catalog_dir) || '';
  if (pl && pl.catalog_error) return esc(pl.catalog_error);
  if (dir) return 'from the checkout at ' + esc(dir);
  if (pl && pl.catalog_fetched_at) return esc(fill(W.catalogChecked, { ago: ago(pl.catalog_fetched_at) }));
  if (url) return 'not fetched yet';
  return 'no catalog configured';
}

function listHTML(pl) {
  storedById = {};
  ((pl && pl.stored) || []).forEach(s => { storedById[s.id] = s; });
  const entries = (pl && pl.catalog) || [];
  const url = (pl && pl.catalog_url) || '';
  const dir = (pl && pl.catalog_dir) || '';
  const visible = visibleEntries(entries);
  let body;
  if (!entries.length) body = '<div class="empty">' + (url || dir ? 'the index lists nothing yet' : 'no catalog configured') + '</div>';
  else if (!visible.length) body = '<div class="empty">nothing matches</div>';
  else body = visible.map(entryHTML).join('');
  const count = entries.length
    ? (visible.length === entries.length ? entries.length : visible.length + ' of ' + entries.length) + ' plugin' + (entries.length === 1 ? '' : 's')
    : '';
  return body + (count ? '<div class="store-count"><span>' + count + '</span></div>' : '');
}

function chipsHTML(pl) {
  const entries = (pl && pl.catalog) || [];
  const cats = Array.from(new Set(entries.map(catOf))).sort();
  if (cats.length < 2) return '';
  return [''].concat(cats).map(c =>
    '<button class="chip' + (store.cat === c ? ' active' : '') + '" data-cat="' + esc(c) + '">' + (c ? esc(c) : 'all') + '</button>').join('');
}

export function storeHTML(pl) {
  return '<details class="card store" data-catalog' + (store.shown ? ' open' : '') + '><summary>' + esc(W.addPlugin) + '</summary>' +
    '<div class="store-bar">' +
      '<input type="text" id="store-q" placeholder="Search plugins" aria-label="Search plugins" value="' + esc(store.q) + '" autocomplete="off">' +
      '<select id="store-sort" aria-label="Sort">' +
        [['name', 'Name'], ['updated', 'Recently updated'], ['updates', 'Updates first']].map(o =>
          '<option value="' + o[0] + '"' + (store.sort === o[0] ? ' selected' : '') + '>' + o[1] + '</option>').join('') +
      '</select></div>' +
    '<div class="store-cats" id="store-cats">' + chipsHTML(pl) + '</div>' +
    '<div id="store-list">' + listHTML(pl) + '</div></details>';
}

export function pendingHTML(pending) {
  if (!pending || !pending.length) return '';
  return pending.map(p => {
    const id = esc(p.id);
    const refused = p.phase === 'refused', starting = p.phase === 'starting', retiring = p.phase === 'retiring', held = p.phase === 'held';
    const unstarted = p.phase === 'unstarted';
    const label = refused ? 'refused' : starting ? 'starting' : retiring ? 'stopping' : held ? 'held' : unstarted ? 'not started' : 'preparing';
    const when = p.retry_at ? ' at ' + esc(String(p.retry_at).slice(11, 19)) + ' UTC' : '';
    const stopped = !refused && !!p.remedy;
    const again = p.resumes ? 'tried again by itself when the plugin catalog is next fetched' : 'nothing retries it on its own';
    const state = starting ? 'activation follows on its own'
      : p.phase === 'ready' ? 'present and verified — activating'
      : stopped ? 'stopped — ' + again
      : p.phase === 'waiting' ? 'waiting to retry' + when
      : 'downloading';
    let bar = '';
    if (p.bytes_total) {
      const pct = Math.min(100, Math.round(100 * p.bytes_present / p.bytes_total));
      bar = '<div class="prep-bar" role="progressbar" aria-label="download" aria-valuenow="' + pct + '" aria-valuemin="0" aria-valuemax="100"><div class="prep-fill" style="width:' + pct + '%"></div></div>';
    }
    const body = refused
      ? '<div class="prep-err">refused: ' + esc(p.summary) + '</div>' + refusalDetailHTML(p) +
        '<div class="dim-note">' + (p.refusal && p.refusal.class === 'transient' && p.retry_at ? 'It is tried again on its own at ' + esc(p.retry_at) + '.' : p.resumes ? 'Nothing is downloading now; it is tried again by itself when the plugin catalog is next fetched. Try again to ask now, or remove it.' : 'Nothing is downloading and nothing retries on its own. Fix what the reason names and try again, or remove it.') + '</div>' +
        '<div class="savebar"><button class="btn sm" data-plugin="retry:' + id + '">Try again</button> <button class="btn sm ghost" data-plugin="uninstall:' + id + '">Uninstall</button></div>'
      : unstarted
      ? '<div class="dim-note" data-unstarted>' + esc(p.summary) + '</div>' + startSaidHTML(p.id) +
        '<div class="savebar">' + startButton(p.id) + ' <button class="btn sm ghost" data-plugin="uninstall:' + id + '">Uninstall</button></div>'
      : held
      ? '<div class="dim-note">' + esc(p.summary) + '</div>' +
        '<div class="dim-note">Nothing to click: it is activated on its own when the hold ends.</div>'
      : retiring
      ? '<div class="dim-note">' + esc(p.summary) + '</div>' + lifecycleHTML(p) +
        '<div class="dim-note">Nothing to click: the host asks the cleanup again on its own' +
        (p.cleanup_at ? ', next at ' + esc(String(p.cleanup_at).slice(11, 19)) + ' UTC' : '') +
        ', and nothing new starts for this plugin until what it held has come back.</div>'
      : stopped
      ? '<div class="dim-note">' + esc(p.summary) + ' · ' + state + '</div>' +
        '<div class="prep-err">' + esc(p.last_error || '') + '</div>' +
        '<div class="dim-note refusal">' + esc(p.remedy) + '</div>' +
        '<div class="savebar"><button class="btn sm" data-plugin="retry:' + id + '">Try again</button> <button class="btn sm ghost" data-plugin="uninstall:' + id + '">Uninstall</button></div>'
      : '<div class="dim-note">' + esc(p.summary) + ' · ' + state + (p.attempt > 1 ? ' · attempt ' + p.attempt : '') + '</div>' +
        (p.last_error ? '<div class="prep-err">last attempt: ' + esc(p.last_error) + '</div>' : '') +
        (p.refusal ? '<div class="prep-err">last attempt refused: ' + esc(p.refusal.cause || '') + '</div>' + refusalDetailHTML({ refusal: p.refusal }) : '') +
        '<div class="dim-note">Activation follows on its own once every declared file is present and verified; nothing to click.</div>';
    return '<div class="card prep" data-pending="' + id + '"><h3>' + id + ' <span class="soon">' + esc(p.version) + ' · ' + label + '</span></h3>' + bar + body + componentSetsHTML(p.selection) + '</div>';
  }).join('');
}

export function wireStore(st) {
  const pl = S.config && S.config.plugins;
  const list = st.querySelector('#store-list');
  const cats = st.querySelector('#store-cats');
  const refresh = () => {
    if (list) list.innerHTML = listHTML(pl);
    if (cats) cats.innerHTML = chipsHTML(pl);
    wireList(st);
  };
  const q = st.querySelector('#store-q');
  if (q) q.oninput = () => { store.q = q.value; refresh(); };
  const sort = st.querySelector('#store-sort');
  if (sort) sort.onchange = () => { store.sort = sort.value; refresh(); };
  const shown = st.querySelector('[data-catalog]');
  if (shown) shown.ontoggle = () => { store.shown = shown.open; };
  wireList(st);
}
function wireList(st) {
  st.querySelectorAll('[data-cat]').forEach(b => { b.onclick = () => {
    store.cat = b.dataset.cat;
    const pl = S.config && S.config.plugins;
    const list = st.querySelector('#store-list'), cats = st.querySelector('#store-cats');
    if (list) list.innerHTML = listHTML(pl);
    if (cats) cats.innerHTML = chipsHTML(pl);
    wireList(st);
  }; });
  st.querySelectorAll('[data-detail]').forEach(b => { b.onclick = () => {
    const id = b.dataset.detail, d = st.querySelector('[data-detail-of="' + CSS.escape(id) + '"]');
    if (!d) return;
    store.open[id] = !store.open[id];
    d.hidden = !store.open[id];
    b.setAttribute('aria-expanded', String(store.open[id]));
  }; });
  st.querySelectorAll('[data-plugin]').forEach(btn => { btn.onclick = () => {
    const spec = btn.dataset.plugin, i = spec.indexOf(':'), action = spec.slice(0, i), id = spec.slice(i + 1);
    if (action === 'uninstall' && window.confirm && !window.confirm(uninstallAsk(id))) return;
    btn.dataset.pluginLabel = btn.textContent;
    btn.disabled = true; btn.textContent = action === 'install' ? 'Installing…' : action === 'retry' ? 'Retrying…' : 'Removing…';
    const requestID = send({ type: 'plugin', plugin: { action: action, id: id } });
    if (requestID) btn.dataset.pluginRequest = requestID;
    else restorePluginAction(btn);
  }; });
  st.querySelectorAll('[data-plugin-start]').forEach(btn => { btn.onclick = () => {
    if (startPending.waiting()) return;
    const id = btn.dataset.pluginStart;
    const request = send({ type: 'plugin', plugin: { action: 'start', id: id } });
    startSaid = startPending.arm({ id: id }, request) ? null : { id: id, text: 'not connected — try again' };
    renderPlugins();
  }; });
  const rf = st.querySelector('[data-catalog-refresh]');
  if (rf) rf.onclick = () => { rf.disabled = true; rf.textContent = 'Refreshing…'; send({ type: 'catalog_refresh' }); };
}

function uninstallAsk(id) {
  const pl = (S.config && S.config.plugins) || {};
  const p = (pl.installed || []).concat(pl.catalog || []).find(x => x.id === id);
  return fill(W.uninstallConfirm, { plugin: (p && p.title) || id });
}

function restorePluginAction(btn) {
  btn.disabled = false;
  btn.textContent = btn.dataset.pluginLabel;
  delete btn.dataset.pluginRequest;
  delete btn.dataset.pluginLabel;
}

export function rejectPluginAction(requestID) {
  if (!requestID) return false;
  const btn = [...document.querySelectorAll('[data-plugin-request]')].find(b => b.dataset.pluginRequest === requestID);
  if (!btn) return false;
  restorePluginAction(btn);
  return true;
}
