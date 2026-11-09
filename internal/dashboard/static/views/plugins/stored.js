import { S } from '../../state.js';
import { esc } from '../../util.js';
import { send } from '../../ws.js';
import { renderPlugins } from '../plugins.js';

export function storedCounts(s) {
  const count = (n, one, many) => (n === 1 ? '1 ' + one : n + ' ' + many);
  return count(s.memories || 0, 'memory', 'memories') + (s.replaced ? ' (' + s.replaced + ' of them replaced)' : '') +
    ' and ' + count(s.keys || 0, 'key', 'keys');
}

export function storedHTML(pl) {
  const stored = (pl && pl.stored) || [];
  const failed = (pl && pl.stored_error) || '', removed = (pl && pl.stored_removed) || '';
  if (!stored.length && !failed && !removed) return '';
  const days = (pl && pl.stored_replaced_days) || 0;
  const rows = stored.map(s => '<div class="store-row" data-stored="' + esc(s.id) + '"><div class="store-main">' +
    '<div><b>' + esc(s.id) + '</b> <span class="store-hint">' + esc(storedInstalled(s)) + '</span></div>' +
    '<div class="store-hint">' + esc(storedCounts(s)) + '</div>' + storedReadHTML(s) + '</div>' +
    '<div class="store-actions">' + storedReadButtons(s) +
    '<button class="btn sm ghost" data-stored-remove="' + esc(s.id) + '">Remove what it stored</button></div></div>').join('');
  return '<div class="card" data-stored-card><h3>STORED BY PLUGINS</h3>' +
    '<div class="dim-note">What each plugin has stored in this identity\'s database: its memories and its keys. Uninstalling a plugin removes its package and nothing it stored.' +
    (days ? ' A replaced memory is removed on its own ' + days + ' days after it was replaced (plugins.runtime.replaced_memory_days in the config file); a plugin may hold as many memories as plugins.runtime.max_memories allows.' : '') + '</div>' +
    (failed ? '<div class="prep-err" data-stored-error>' + esc(failed) + '</div>' : '') + rows +
    (removed ? '<div class="dim-note" data-stored-removed>' + esc(removed) + '</div>' : '') + '</div>';
}
export function wireStored(st) {
  st.querySelectorAll('[data-stored-remove]').forEach(btn => { btn.onclick = () => {
    const id = btn.dataset.storedRemove;
    const s = ((S.config && S.config.plugins && S.config.plugins.stored) || []).find(x => x.id === id);
    if (!s) return;
    const ask = 'Remove what ' + id + ' stored in this identity\'s database? ' + storedCounts(s) + ' will be deleted from it. Snapshots and exports already made still hold them.' +
      (s.installed || s.pending ? ' The plugin stays installed, with an empty record.' : '') +
      (storedRead && storedRead.id === id ? '' : ' You have not opened them on this page: Cancel, then Read its memories or Read its keys, to read them first.');
    if (window.confirm && !window.confirm(ask)) return;
    btn.disabled = true; btn.textContent = 'Removing…';
    send({ type: 'plugin', plugin: { action: 'remove_stored', id: id } });
  }; });
  st.querySelectorAll('[data-stored-read]').forEach(btn => { btn.onclick = () => {
    const spec = btn.dataset.storedRead, i = spec.indexOf(':'), kind = spec.slice(0, i), id = spec.slice(i + 1);
    storedRead = { id: id, kind: kind, rows: [], total: 0, next: 0, error: '', loading: true };
    send({ type: 'plugin_stored', stored: { plugin: id, kind: kind } });
    renderPlugins();
  }; });
  const more = st.querySelector('[data-stored-more]');
  if (more) more.onclick = () => {
    if (!storedRead || !storedRead.next) return;
    storedRead.loading = true;
    send({ type: 'plugin_stored', stored: { plugin: storedRead.id, kind: storedRead.kind, before: storedRead.next } });
    renderPlugins();
  };
  const close = st.querySelector('[data-stored-close]');
  if (close) close.onclick = () => { storedRead = null; renderPlugins(); };
}

let storedRead = null;
export function applyPluginStored(reply) {
  if (!reply || !storedRead || reply.plugin !== storedRead.id || reply.kind !== storedRead.kind) return;
  storedRead.loading = false;
  storedRead.error = reply.error || '';
  if (storedRead.error) return;
  const page = (reply.kind === 'keys' ? reply.keys : reply.memories) || [];
  storedRead.rows = reply.before ? storedRead.rows.concat(page) : page;
  storedRead.total = reply.total || 0;
  storedRead.next = reply.next || 0;
}
function storedInstalled(s) {
  if (s.installed) return 'installed';
  if (s.pending) return 'installed, ' + (s.pending === 'refused' ? 'refused' : s.pending === 'starting' ? 'starting' : s.pending === 'retiring' ? 'stopping' : s.pending === 'held' ? 'held' : 'preparing');
  return 'not installed; what it stored is still here';
}
function storedReadButtons(s) {
  const id = esc(s.id);
  return (s.memories ? '<button class="btn sm ghost" data-stored-read="memories:' + id + '">Read its memories</button>' : '') +
    (s.keys ? '<button class="btn sm ghost" data-stored-read="keys:' + id + '">Read its keys</button>' : '');
}
function storedReadHTML(s) {
  const r = storedRead;
  if (!r || r.id !== s.id) return '';
  const what = r.kind === 'keys' ? 'keys' : 'memories';
  const when = t => (t ? esc(String(t).replace('T', ' ').replace('Z', ' UTC')) : 'at a time that was not recorded');
  const rows = r.rows.map(x => {
    const about = r.kind === 'keys'
      ? '<b>' + esc(x.key) + '</b> · ' + (x.deletion_marker ? 'a running activation\'s mark that it deleted this key for itself; no value' : 'last written ' + when(x.stored_at)) + (x.temporary ? ' · temporary, ends with its activation' : '')
      : 'stored ' + when(x.stored_at) + ' · ' + (x.replaced_by ? 'replaced ' + when(x.replaced_at) + ' by ' + esc(x.replaced_by) : 'current') + (x.temporary ? ' · temporary, ends with its activation' : '') + ' · ' + esc(x.id);
    const body = r.kind === 'keys' ? (x.deletion_marker ? '' : x.value) : x.text;
    return '<div class="record-detail" data-stored-item><div class="store-hint">' + about + '</div>' + (r.kind === 'keys' && x.deletion_marker ? '' : '<pre>' + esc(body) + '</pre>') + '</div>';
  }).join('');
  const count = r.error ? '' : r.loading && !r.rows.length ? 'reading…'
    : 'showing ' + r.rows.length + ' of ' + r.total + ', newest first' + (r.next ? '' : (r.rows.length < r.total ? '; the rest changed while this was read: read again' : '; that is all of them'));
  return '<div class="store-detail" data-stored-reader="' + esc(r.kind) + '"><div class="dim-note">The ' + what + ' ' + esc(s.id) + ' stored, each whole. ' + count + '</div>' +
    (r.error ? '<div class="prep-err" data-stored-read-error>' + esc(r.error) + '</div>' : '') + rows +
    '<div class="savebar">' + (r.next ? '<button class="btn sm" data-stored-more' + (r.loading ? ' disabled' : '') + '>' + (r.loading ? 'Reading…' : 'Next ' + what) + '</button> ' : '') +
    '<button class="btn sm ghost" data-stored-close>Close</button></div></div>';
}
