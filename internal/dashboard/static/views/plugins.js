
import { S } from '../state.js';
import { $, esc } from '../util.js';
import { send } from '../ws.js';
import { saveConfigSection, sendConfigChanges, configFeedbackHTML } from './settings.js';

// The store's view state — what the operator typed, chose and opened —
// survives the re-render every status frame causes. It is not
// configuration and is never sent anywhere.
const store = { q: '', cat: '', sort: 'name', open: {} };

// A tier is evidence the host established, named for a person: the
// index's letter says nothing to someone who has not read the design.
const TIER = { T3: 'platform-signed', T2: 'reviewed', T1: 'signed', T0: 'unsigned' };
function tierLabel(t) { return TIER[t] || (t ? esc(t) : 'unsigned'); }
function titleOf(e) { return e.title || e.id; }
function catOf(e) { return String(e.category || 'other').toLowerCase(); }
function sizeText(n) {
  if (!n) return '';
  if (n < 1024) return n + ' B';
  if (n < 1048576) return Math.round(n / 1024) + ' KB';
  return (n / 1048576).toFixed(1) + ' MB';
}
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
    row('size', sizeText(e.size)) +
    row('updated', e.updated && esc(String(e.updated).slice(0, 10))) +
    row('installed', e.installed ? esc(e.installed_version || e.version) : '');
}

function entryHTML(e) {
  const id = esc(e.id);
  let badge = '', btns = '';
  const install = label => '<button class="btn sm" data-plugin="install:' + id + '">' + label + '</button>';
  const uninstall = () => '<button class="btn sm ghost" data-plugin="uninstall:' + id + '">Uninstall</button>';
  if (e.installed && e.update_available) { badge = chip('provisional', 'update to ' + esc(e.version)); btns = install('Update') + ' ' + uninstall(); }
  else if (e.installed) { badge = chip('active', 'installed ' + esc(e.installed_version || e.version)); btns = uninstall(); }
  else if (e.available) { btns = install('Install'); }
  else { badge = chip('', 'no build for this host'); }
  const open = !!store.open[e.id];
  return '<div class="store-row" data-entry="' + id + '">' +
    '<div class="store-main">' +
      '<div class="store-head">' +
        '<button class="store-title" data-detail="' + id + '" aria-expanded="' + open + '">' + esc(titleOf(e)) + '</button>' +
        '<span class="store-meta">' + esc(e.version) + ' · ' + tierLabel(e.tier) + (e.publisher ? ' · by ' + esc(e.publisher) : '') + '</span>' + badge +
      '</div>' +
      (e.summary ? '<div class="store-summary">' + esc(e.summary) + '</div>' : '') +
      '<div class="store-detail" data-detail-of="' + id + '"' + (open ? '' : ' hidden') + '>' + detailHTMLFor(e) + '</div>' +
    '</div>' +
    '<div class="store-actions">' + btns + '</div></div>';
}

function catalogNote(pl) {
  const url = (pl && pl.catalog_url) || '';
  const dir = (pl && pl.catalog_dir) || '';
  if (pl && pl.catalog_error) return 'last refresh refused: ' + esc(pl.catalog_error);
  if (dir) return 'from the checkout at ' + esc(dir);
  if (pl && pl.catalog_fetched_at) return 'fetched ' + esc(pl.catalog_fetched_at);
  if (url) return 'not fetched yet';
  return 'no catalog configured';
}

function listHTML(pl) {
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
  return body + '<div class="store-count"><span>' + count + (count ? ' · ' : '') + catalogNote(pl) + '</span>' +
    (url || dir ? '<button class="btn sm ghost" data-catalog-refresh>Refresh now</button>' : '') + '</div>';
}

function chipsHTML(pl) {
  const entries = (pl && pl.catalog) || [];
  const cats = Array.from(new Set(entries.map(catOf))).sort();
  if (cats.length < 2) return '';
  return [''].concat(cats).map(c =>
    '<button class="chip' + (store.cat === c ? ' active' : '') + '" data-cat="' + esc(c) + '">' + (c ? esc(c) : 'all') + '</button>').join('');
}

function storeHTML(pl) {
  const n = (pl && pl.catalog_updates) || 0;
  return '<div class="card store"><h3>PLUGINS' + (n ? '<span class="soon">' + n + ' update' + (n === 1 ? '' : 's') + '</span>' : '') + '</h3>' +
    '<div class="store-bar">' +
      '<input type="text" id="store-q" placeholder="Search plugins" aria-label="Search plugins" value="' + esc(store.q) + '" autocomplete="off">' +
      '<select id="store-sort" aria-label="Sort">' +
        [['name', 'Name'], ['updated', 'Recently updated'], ['updates', 'Updates first']].map(o =>
          '<option value="' + o[0] + '"' + (store.sort === o[0] ? ' selected' : '') + '>' + o[1] + '</option>').join('') +
      '</select></div>' +
    '<div class="store-cats" id="store-cats">' + chipsHTML(pl) + '</div>' +
    '<div id="store-list">' + listHTML(pl) + '</div></div>';
}

function settingsHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';
  const lvl = c.plugins.autoload || 'T1';
  const opts = [
    ['T3', 'T3 only — platform-signed'],
    ['T2', 'T2 or above — reviewed + signed'],
    ['T1', 'T1 or above — signed (default)'],
    ['T0', 'any — including unsigned (dev)'],
    ['none', 'none — nothing auto-loads'],
  ];
  const url = (c.plugins && c.plugins.catalog_url) || '';
  const isDefault = /raw\.githubusercontent\.com\/aiii-dot-id\/plugin-catalog\//.test(url);
  return '<div class="card"><h3>PLUGIN SETTINGS</h3>' +
    '<div class="dim-note">Installing from the store downloads only the build for this host, verifies its hash, and stages it; the sweep activates a package only if its <b>verified</b> tier meets the auto-load level — evidence, never the package\'s claim. The sandbox never relaxes with the level, and a package whose signature fails verification is refused at every level. A directory dropped into plugins/ beside config.json is discovered the same way.</div>' +
    '<label class="f">AUTO-LOAD</label><select id="cfg-plevel">' +
    opts.map(o => '<option value="' + o[0] + '"' + (o[0] === lvl ? ' selected' : '') + '>' + o[1] + '</option>').join('') + '</select>' +
    '<div class="savebar"><button class="btn" data-save="plugins">Save</button><span class="savenote">saved — applies live</span></div>' +
    skipsHTML(c.plugins.skips) +
    '<label class="f">CATALOG URL' + (isDefault ? ' <span class="store-hint">— the platform\'s catalog, by default; leave empty to keep it</span>' : '') + '</label>' +
    '<input type="text" id="cfg-caturl" value="' + esc(url) + '" placeholder="https://…/aiios-plugins.md">' +
    '<div class="savebar"><button class="btn" data-save="catalog">Save</button><span class="savenote">saved — the index is fetched from here</span></div>' +
    runtimeLimitsHTML(c.plugins.runtime) +
    '</div>';
}

// The runtime-class ceilings: what a native engine's
// installed runtime tree may measure, and how many retired trees stay
// for rollback. The operator's numbers; applied at the next activation.
function runtimeLimitsHTML(r) {
  if (!r) return '';
  const mib = n => Math.round((n || 0) / 1048576);
  return '<label class="f">NATIVE RUNTIME CEILINGS <span class="store-hint">— a native engine\'s installed runtime tree may measure at most this; refused above it</span></label>' +
    '<label class="f sub">installed MiB<input type="number" id="cfg-rt-installed" min="1" value="' + mib(r.max_installed_bytes) + '"></label>' +
    '<label class="f sub">files<input type="number" id="cfg-rt-files" min="1" value="' + (r.max_files || 0) + '"></label>' +
    '<label class="f sub">largest file MiB<input type="number" id="cfg-rt-file" min="1" value="' + mib(r.max_file_bytes) + '"></label>' +
    '<label class="f sub">archive MiB<input type="number" id="cfg-rt-archive" min="1" value="' + mib(r.max_compressed_bytes) + '"></label>' +
    '<label class="f sub">path depth<input type="number" id="cfg-rt-depth" min="1" value="' + (r.max_depth || 0) + '"></label>' +
    '<label class="f sub">trees kept for rollback<input type="number" id="cfg-rt-kept" min="1" value="' + (r.roots_kept || 0) + '"></label>' +
    '<div class="savebar"><button class="btn" data-save="plugin_runtime">Save</button><span class="savenote">saved — applies at the next activation</span></div>';
}

function installedHTML(installed) {
  if (!installed || !installed.length) return '';
  return installed.map(p => {
    // THE PACKAGE'S OWN WORDS COME FIRST. A card that led with an id and
    // a version made the operator read the version as a statement of
    // what the thing is — and a version is the author's own checkpoint
    // ladder, which need not match what anyone calls it. The manifest of
    // the voice engine says exactly what it is and what it cannot do yet
    // while its version reads 0.1.0-native-cp1; the operator read the
    // version, called the card stale, and the sentence he wanted was in
    // the package the whole time. Title heads the card, the
    // id and version stay beside it as the identifiers they are.
    const named = p.title ? esc(p.title) : esc(p.id);
    const head = '<h3>' + named + ' <span class="soon">' + esc(p.version) + ' · ' + esc(p.tier) + '</span></h3>' +
      (p.title ? '<div class="dim-note pkg-id">' + esc(p.id) + '</div>' : '') +
      (p.description ? '<div class="pkg-desc">' + esc(p.description) + '</div>' : '') +
      '<div class="dim-note">' + esc(p.mode) + ', variant ' + esc(p.variant) +
      (p.tools && p.tools.length ? ' · ' + p.tools.length + ' operation' + (p.tools.length === 1 ? '' : 's') + ', reached through the tools organ, never listed in the prompt' : '') + '</div>' +
      readinessHTML(p) + detailHTML(p) + grantsHTML(p) + actsHTML(p);
    if (!p.settings || !p.settings.length) return '<div class="card">' + head + '<div class="empty">no settings declared</div></div>';
    return '<div class="card">' + head + p.settings.map(s => settingHTML(p.id, s)).join('') + sessionSettingsHTML(p) +
      '<div class="savebar"><button class="btn" data-save="plugin:' + esc(p.id) + '">Save</button><span class="savenote">' + appliesNote(p) + '</span></div></div>';
  }).join('');
}
// WHAT THE ENGINE CAME UP WITH. A native engine reports its own
// readiness when its child is warm: how many models it loaded, what it
// is computing on, and how long its warm probe took. The host recorded
// it and nothing showed it, so the one fact that changes what an
// operator should expect — running on the processor rather than the
// graphics unit, where synthesis is several times slower — was legible
// only in the log (the common-native checkpoint on this
// Mac: 0.64 times real time on the processor against 0.08 with Vulkan).
// Shown as the engine's own word, never as a promise about speed.
function readinessHTML(p) {
  const r = p.readiness;
  if (!r) return '';
  const bits = [];
  if (r.models_loaded) bits.push(r.models_loaded + ' model' + (r.models_loaded === 1 ? '' : 's') + ' loaded');
  if (r.accelerator) bits.push('computing on ' + esc(r.accelerator));
  if (r.probe_ms) bits.push('warm in ' + r.probe_ms + ' ms');
  if (!bits.length) return '';
  return '<div class="dim-note engine-ready">the engine reported itself ready: ' + bits.join(' · ') + '</div>';
}
// AWAITING YOUR CONFIRMATION (seam 1): an operation the plugin's
// identity proposed that runs only when the operator confirms exactly
// these arguments — shown for a person: the summary, each argument, and
// the recorded text of every transcript final the arguments name, so
// what is confirmed is what was heard. Confirm runs it once; Deny drops
// it; an act expires on its own.
function actsHTML(p) {
  const acts = p.acts || [];
  if (!acts.length) return '';
  const argLine = (k, v) => '<div class="act-arg"><span>' + esc(k) + '</span><b>' + esc(typeof v === 'string' ? v : JSON.stringify(v)) + '</b></div>';
  return '<div class="acts"><label class="f">AWAITING YOUR CONFIRMATION</label>' + acts.map(a => {
    const args = Object.keys(a.args || {}).sort().filter(k => k !== 'finals' && k !== 'sequences' || !(a.finals && a.finals.length)).map(k => argLine(k, a.args[k])).join('');
    const finals = (a.finals || []).map(f => '<div class="act-final">#' + esc(f.sequence) + ' ' + (f.heard ? '“' + esc(f.text) + '”' : '<span class="setting-stale">never heard on this session</span>') + '</div>').join('');
    return '<div class="act" data-act-card="' + esc(a.id) + '"><div><b>' + esc(a.summary || a.operation) + '</b> <span class="store-hint">' + esc(a.operation) + (a.effects ? ' · ' + esc(a.effects) : '') + (a.session ? ' · session ' + esc(a.session) : '') + '</span></div>' +
      args + finals +
      '<div class="store-hint">proposed ' + esc(a.proposed) + ' · expires ' + esc(a.expires) + ' · runs once, with exactly these arguments, when you confirm; Always runs it now and every time from now on, until you revoke it below</div>' +
      '<div class="savebar"><button class="btn" data-act-plugin="' + esc(p.id) + '" data-act-id="' + esc(a.id) + '" data-act-decision="confirm">Confirm</button> ' +
      '<button class="btn" data-act-plugin="' + esc(p.id) + '" data-act-id="' + esc(a.id) + '" data-act-decision="always">Always</button> ' +
      '<button class="btn ghost" data-act-plugin="' + esc(p.id) + '" data-act-id="' + esc(a.id) + '" data-act-decision="deny">Deny</button></div></div>';
  }).join('') + '</div>';
}
// The five grants the page sets, each keyed by the capability the
// release must be signed for: a yes for a capability the release was
// not signed for answers nothing, so it is not offered. The lists
// (outbound hosts, filesystem roots, credential handles) are the config
// file's; the page shows what the file says and names the key.
const GRANTS = [
  ['ring4.kv', 'kv', 'key-value store', 'a scoped namespace in the identity\'s store (ring4.kv)'],
  ['ring4.memory', 'memory', 'memory', 'remember and recall through the identity\'s memory instruments (ring4.memory)'],
  ['voice.observe', 'voice', 'voice', 'submit heard utterances into the conversation (voice.observe)'],
  ['model.embeddings', 'embeddings', 'embeddings', 'vectors from the identity\'s own model endpoint, at its cost (model.embeddings)'],
  ['tools.publish', 'tools', 'publish tools', 'grow the identity\'s tool surface at run time (tools.publish)'],
];
function grantsHTML(p) {
  const g = p.grants || {};
  const caps = p.capabilities || [];
  const key = field => 'plugins.grants.' + esc(p.id) + '.' + field;
  const listed = (v, none) => v && v.length ? esc(v.join(', ')) : none;
  const rows = GRANTS.filter(x => caps.includes(x[0])).map(x =>
    '<label class="grant-row"><input type="checkbox" data-grant-plugin="' + esc(p.id) + '" data-grant-field="' + x[1] + '"' + (g[x[1]] ? ' checked' : '') + '>' +
    '<span><b>' + x[2] + '</b> <span class="store-hint">' + esc(x[3]) + '</span></span></label>');
  // The connect scope, offered for every installed plugin: read only
  // refuses, before any dispatch, an operation that writes or executes.
  rows.push('<label class="grant-row"><input type="checkbox" data-grant-plugin="' + esc(p.id) + '" data-grant-field="read_only"' + (g.read_only ? ' checked' : '') + '>' +
    '<span><b>read only</b> <span class="store-hint">operations that write or execute are refused; the plugin may look, not act</span></span></label>');
  const lists = [];
  const standing = g.auto_confirm || [];
  if (standing.length) lists.push('always confirmed, without asking: ' + standing.map(op => esc(op) + ' <button class="btn ghost" data-revoke-auto="' + esc(p.id) + '" data-op="' + esc(op) + '">revoke</button>').join(', '));
  if (caps.includes('net.outbound')) lists.push('outbound hosts: ' + listed(g.hosts, 'none') + ' — a list, set as ' + key('hosts') + ' in the config file');
  if (caps.includes('net.local')) lists.push('local network: ' + listed(g.local, 'none') + ' — a list, set as ' + key('local') + ' (an address, a range like 192.168.1.0/24, or a name on your own network, each with :port or :* for any); ' + (g.plaintext_credentials ? 'credentials may be sent in the clear to these devices' : 'a credential over plain http needs ' + key('plaintext_credentials') + ': true — anyone on your network could read it'));
  if (caps.includes('fs.roots')) lists.push('filesystem roots: ' + listed(g.roots, 'none') + ' — a list, set as ' + key('roots') + ' in the config file');
  if ((p.settings || []).some(s => s.type === 'secret') || (g.handles && g.handles.length)) lists.push('credential handles: ' + listed(g.handles, 'none') + ' — a list, set as ' + key('credential_handles') + ' in the config file');
  if (!rows.length && !lists.length) return '';
  return '<div class="grants"><label class="f">GRANTED BY YOU</label>' + rows.join('') +
    lists.map(l => '<div class="store-hint grant-list">' + l + '</div>').join('') +
    (rows.length ? '<div class="savebar"><button class="btn" data-save="grants:' + esc(p.id) + '">Save grants</button><span class="savenote">saved — applies to the plugin\'s next call</span></div>' : '') +
    '</div>';
}
// detailHTML is what the verified manifest says about an installed
// plugin: who published it, what it is, what it implements, what it was
// signed to touch, where it runs, and the hash that names its bytes.
function detailHTML(p) {
  const row = (k, v) => v ? '<div class="sp-kv"><span>' + k + '</span><b>' + v + '</b></div>' : '';
  const publisher = p.publisher ? esc(p.publisher) + (p.publisher_id ? ' <span class="store-hint">(certified ' + esc(p.publisher_id) + ')</span>' : '') : '';
  const caps = p.capabilities && p.capabilities.length ? p.capabilities.map(esc).join(', ') : 'none';
  return '<div class="plugin-detail">' +
    row('publisher', publisher) +
    row('family', p.family && esc(p.family)) +
    row('implements', p.interfaces && p.interfaces.length ? p.interfaces.map(esc).join(', ') : '') +
    row('signed for', caps) +
    row('runs as', p.runtime && esc(p.runtime)) +
    row('package', p.package_hash && '<span title="' + esc(p.package_hash) + '">' + esc(p.package_hash.slice(0, 23)) + '…</span>') +
    '</div>';
}
// When a save reaches the plugin — the host's word (PluginView.applies):
// a resident engine reads its settings as a session opens, so a
// running spoken session keeps what it opened with; every other plugin
// reads them at its next call.
function appliesNote(p) {
  return p.applies === 'next_session'
    ? 'saved — the next spoken session opens with these; a session already running keeps the values it opened with'
    : 'saved — the plugin reads it on its next call';
}
// What a live spoken session reports it is running with — the engine's
// own word at open (session_ready.models.operator_settings) — beside the
// saved values, so the operator sees what is IN EFFECT and what the
// next session will open with; a resident engine's card says so even
// when no session is open.
function sessionSettingsHTML(p) {
  if (p.applies !== 'next_session') return '';
  const decl = key => (p.settings || []).find(s => s.key === key);
  const ids = Object.keys(p.session_settings || {});
  if (!ids.length) return '<div class="store-hint session-applied">no spoken session is open — the next one opens with the saved values</div>';
  return ids.map(id => {
    const vals = p.session_settings[id] || {};
    const parts = Object.keys(vals).sort().map(k => esc(k) + ': ' + esc(decl(k) ? shown(decl(k), vals[k]) : JSON.stringify(vals[k])));
    return '<div class="store-hint session-applied">in effect in spoken session ' + esc(id) + ' — ' + (parts.length ? parts.join(' · ') : 'the engine reported no values') + '</div>';
  }).join('');
}
// A choice list longer than this gets a filter box: it narrows what is
// shown, never what is offered — the chosen value always stays listed,
// and an empty filter lists everything.
const CHOICE_FILTER_FROM = 12;
function settingHTML(id, s) {
  const attrs = ' data-pset-plugin="' + esc(id) + '" data-pset-key="' + esc(s.key) + '" data-pset-type="' + esc(s.type) + '"';
  // A VALUE THIS RELEASE NO LONGER OFFERS. Kept from an earlier one and
  // read by nothing: there is nothing to edit, so the only control is
  // forgetting it. Unticked it is left exactly as it is — an upgrade
  // that drops a setting must not quietly discard what was chosen.
  if (s.undeclared) {
    return '<div class="store-setting setting-orphan">' +
      '<label class="f"><input type="checkbox" data-pset-plugin="' + esc(id) + '" data-pset-key="' + esc(s.key) + '" data-pset-type="forget"> forget ' + esc(s.key) + '</label>' +
      '<div class="store-hint">saved value ' + esc(JSON.stringify(s.value)) + ' — ' + esc(s.description) + '</div></div>';
  }
  const current = s.value !== undefined && s.value !== null ? s.value : s.default;
  let field;
  if (s.type === 'boolean') {
    field = '<label class="f"><input type="checkbox"' + attrs + (current ? ' checked' : '') + '> ' + esc(s.title) + '</label>';
  } else if (s.type === 'enum') {
    const labels = s.labels || {};
    const values = (s.values || []).slice();
    // A stored choice this release no longer offers stays visible AS
    // the stored choice, named as such, so the operator sees what they
    // chose and chooses again; saving it unchanged is refused by the
    // host by name, never silently replaced.
    const stale = s.invalid && s.value !== undefined && s.value !== null && !values.includes(String(s.value));
    // The name is what the person chooses by; the stable value is what
    // is stored, shown on hover and never appended to the name (the
    // voice platform: a label may change, the selection
    // must not).
    const name = v => labels[v] ? labels[v] : v;
    field = '<label class="f">' + esc(s.title) + (values.length > CHOICE_FILTER_FROM ? ' <span class="store-hint">— ' + values.length + ' choices</span>' : '') + '</label>' +
      (values.length > CHOICE_FILTER_FROM ? '<input type="search" class="choice-filter" data-choice-filter-for="' + esc(s.key) + '" placeholder="filter the ' + values.length + ' choices…" aria-label="filter ' + esc(s.title) + '">' : '') +
      '<select' + attrs + '>' +
      (stale ? '<option value="' + esc(s.value) + '" selected>' + esc(s.value) + ' — saved, not offered by this release</option>' : '') +
      values.map(v => '<option value="' + esc(v) + '" title="' + esc(v) + '"' + (v === current ? ' selected' : '') + '>' + esc(name(v)) + '</option>').join('') + '</select>';
  } else if (s.type === 'secret') {
    const handles = s.handles || [];
    field = '<label class="f">' + esc(s.title) + ' — a credential handle</label><select' + attrs + '><option value="">none</option>' +
      handles.map(h => '<option value="' + esc(h) + '"' + (h === current ? ' selected' : '') + '>' + esc(h) + '</option>').join('') + '</select>' +
      (handles.length ? '' : '<div class="store-hint">grant this plugin a credential handle first (plugins.grants.' + esc(id) + '.credential_handles)</div>');
  } else if (s.type === 'number' || s.type === 'integer') {
    field = '<label class="f">' + esc(s.title) + (s.type === 'integer' ? ' <span class="store-hint">— a whole number</span>' : '') + '</label><input type="number"' + attrs +
      (s.type === 'integer' ? ' step="1"' : ' step="any"') +
      (s.minimum !== undefined && s.minimum !== null ? ' min="' + esc(s.minimum) + '"' : '') +
      (s.maximum !== undefined && s.maximum !== null ? ' max="' + esc(s.maximum) + '"' : '') +
      ' value="' + (current !== undefined && current !== null ? esc(current) : '') + '">';
  } else {
    field = '<label class="f">' + esc(s.title) + '</label><input type="text"' + attrs + ' value="' + (current !== undefined && current !== null ? esc(current) : '') + '">';
  }
  const meta = [];
  if (s.required) meta.push('required');
  if (s.default !== undefined && s.default !== null && s.type !== 'boolean') meta.push('default ' + esc(shown(s, s.default)));
  // The stored value against what the plugin reads: when the stored
  // value no longer holds to this release's declaration the page says
  // so and names what is in effect instead — never a quiet default.
  const stale = s.invalid ? '<div class="store-hint setting-stale">your saved value ' + esc(JSON.stringify(s.value)) + ' ' + esc(s.invalid) + ' — in effect: ' +
    (s.effective !== undefined && s.effective !== null ? esc(shown(s, s.effective)) : 'nothing') + ' until you choose again</div>' : '';
  return '<div class="store-setting">' + field +
    (s.description || meta.length ? '<div class="store-hint">' + esc(s.description || '') + (meta.length ? ' (' + meta.join(', ') + ')' : '') + '</div>' : '') + stale + '</div>';
}
// shown renders a value the way the page names it: an enum value under
// its label when the declaration gives one.
function shown(s, v) {
  if (s.type === 'enum' && s.labels && s.labels[v]) return s.labels[v];
  return v;
}
// The choice filter narrows the options a long list SHOWS; a match is
// on the value or its label, case-insensitively; the selected option is
// always listed, and an empty filter lists every choice again. The list
// is REBUILT from the declaration's full set on each keystroke rather
// than hidden option by option: WebKit shows a native popup's options
// whatever their hidden attribute says, and a choice the page cannot
// hide reliably it should not pretend to.
function wireChoiceFilters(st) {
  st.querySelectorAll('[data-choice-filter-for]').forEach(inp => {
    const sel = inp.nextElementSibling;
    if (!sel || sel.tagName !== 'SELECT') return;
    const all = Array.from(sel.options).map(o => ({ value: o.value, text: o.textContent }));
    inp.oninput = () => {
      const q = inp.value.trim().toLowerCase();
      const current = sel.value;
      const kept = all.filter(o => !q || o.value === current || o.value.toLowerCase().includes(q) || o.text.toLowerCase().includes(q));
      sel.innerHTML = kept.map(o => '<option value="' + esc(o.value) + '" title="' + esc(o.value) + '"' + (o.value === current ? ' selected' : '') + '>' + esc(o.text) + '</option>').join('');
      sel.value = current;
      inp.title = q ? kept.length + ' of ' + all.length + ' listed' : '';
    };
  });
}
function skipsHTML(skips) {
  if (!skips || !skips.length) return '';
  return '<div class="store-skips"><label class="f">PRESENT, VERIFIED — NOT LOADED</label>' +
    skips.map(s => '<div class="store-skip"><b>' + esc(s.id) + '</b> (' + esc(s.tier) + ') — ' + esc(s.reason) + '</div>').join('') + '</div>';
}

// The list re-renders on its own when the operator searches, sorts or
// picks a category, so the search field keeps its focus and caret; a
// status frame re-renders the whole page and the store state carries.
function wireStore(st) {
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
    btn.disabled = true; btn.textContent = action === 'install' ? 'Installing…' : 'Removing…';
    send({ type: 'plugin', plugin: { action: action, id: id } });
  }; });
  const rf = st.querySelector('[data-catalog-refresh]');
  if (rf) rf.onclick = () => { rf.disabled = true; rf.textContent = 'Refreshing…'; send({ type: 'catalog_refresh' }); };
}

// connectPrefill turns a connect answer (the connector's word and the
// chosen scope) into an open New-profile form: the template whose name
// or service the word carries, that service read or modify, the rest
// for the operator.
export function connectPrefill(req, providers, installed) {
  if (!req) return null;
  const word = String(req.connector || '').toLowerCase();
  const mode = req.scope === 'modify' ? 'modify' : 'read';
  // An installed plugin's own hint outranks the word: its secret
  // setting names the authority and the services it speaks to.
  const plugin = (installed || []).find(p => p.id === req.connector);
  const hinted = plugin && (plugin.settings || []).find(s => s.type === 'secret' && s.oauth);
  if (hinted) {
    const services = {};
    (hinted.oauth.services || []).forEach(svc => { services[svc] = mode; });
    const prov = (providers || []).some(p => p.name === hinted.oauth.provider) ? hinted.oauth.provider : 'custom';
    return { name: (hinted.oauth.provider + '-' + hinted.key).replace(/[^a-z0-9._-]+/g, '-').slice(0, 40), provider: prov, services: services, connector: req.connector, scope: mode, handle: hinted.key, plugin: plugin.id };
  }
  const aliases = { gmail: ['google', 'gmail'], calendar: ['google', 'calendar'], drive: ['google', 'drive'], contacts: ['google', 'contacts'], google: ['google', ''],
    outlook: ['microsoft', 'mail'], microsoft: ['microsoft', ''], github: ['github', 'issues'], slack: ['slack', 'chat'] };
  let provider = 'custom', service = '';
  for (const key of Object.keys(aliases)) { if (word.includes(key)) { provider = aliases[key][0]; service = aliases[key][1] || service; } }
  if (!(providers || []).some(p => p.name === provider)) provider = 'custom';
  const services = {};
  if (service) services[service] = mode;
  return { name: word.replace(/[^a-z0-9._-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 40), provider: provider, services: services, connector: req.connector, scope: mode };
}

export function renderPlugins() {
  const st = $('plugins-stack');
  const c = S.config;
  if (S.connectRequest && c && c.plugins) {
    profileDraft = connectPrefill(S.connectRequest, c.plugins.providers, c.plugins.installed);
    S.connectRequest = null;
  }
  st.innerHTML = configFeedbackHTML() + storeHTML(c && c.plugins) + installedHTML(c && c.plugins.installed) + profilesHTML(c && c.plugins) + settingsHTML(c);
  wireProfiles(st);
  st.querySelectorAll('[data-save]').forEach(btn => { btn.onclick = () => { saveConfigSection(btn.dataset.save); renderPlugins(); }; });
  st.querySelectorAll('[data-act-decision]').forEach(btn => { btn.onclick = () => {
    const card = btn.closest('[data-act-card]');
    card.querySelectorAll('button').forEach(b => { b.disabled = true; });
    btn.textContent = btn.dataset.actDecision === 'deny' ? 'Dropping…' : 'Running…';
    send({ type: 'plugin', plugin: { action: btn.dataset.actDecision, id: btn.dataset.actPlugin, act: btn.dataset.actId } });
  }; });
  // Revoking a standing confirmation sends the list without it; the
  // reply is the configuration, which re-renders the card.
  st.querySelectorAll('[data-revoke-auto]').forEach(btn => { btn.onclick = () => {
    const id = btn.dataset.revokeAuto, op = btn.dataset.op;
    const p = ((S.config && S.config.plugins && S.config.plugins.installed) || []).find(x => x.id === id);
    const remaining = (((p && p.grants) || {}).auto_confirm || []).filter(x => x !== op);
    btn.disabled = true; btn.textContent = 'Revoking…';
    const ch = {}; ch['plugins.grants.' + id + '.auto_confirm'] = remaining;
    sendConfigChanges('grants:' + id, ch);
  }; });
  wireChoiceFilters(st);
  wireStore(st);
}

// ── CONNECTED ACCOUNTS: the operator's credential routes ──
//
// A profile is a name the operator gives a credential and the plugins may
// cite; the page shows what it is for and whether it is connected, never
// a value. An oauth2 profile connects by the operator's own consent in
// their browser (a tab, or the pasted redirect when the tab could not
// reach this machine), or by a code entered on the authority's page
// where the authority allows it. The form hands a client secret to the
// host exactly once; it is written to a private file and never shown.
let profileWin = null;
let profileDraft = null; // the New-profile form's state while open
function profilesHTML(pl) {
  const profiles = (pl && pl.auth_profiles) || [];
  const providers = (pl && pl.providers) || [];
  const rows = profiles.map(p => {
    const chip = '<span class="chip chip-' + esc(p.state) + '">' + esc(p.state) + '</span>';
    const used = p.handles && p.handles.length ? 'used by ' + esc(p.handles.join(', ')) : 'no plugin is granted this handle yet (plugins.grants.<id>.credential_handles)';
    if (p.scheme !== 'oauth2') {
      return '<div class="profile" data-profile="' + esc(p.name) + '"><div><b>' + esc(p.name) + '</b> <span class="store-hint">' + esc(p.scheme) + ' · ' + esc(p.host) + ':' + esc(p.port) + ' · secret from ' + esc(p.secret_source || 'nowhere') + ' · edited in the config file</span></div><div class="store-hint">' + used + '</div></div>';
    }
    const scopes = (p.scopes || []).map(sc => sc.replace(/^https?:\/\/[^/]+\/auth\//, '')).join(', ');
    let bar = '';
    if (p.device) {
      bar = '<div class="device-code">Enter <b>' + esc(p.device.user_code) + '</b> at <a href="' + esc(p.device.verification_uri_complete || p.device.verification_uri) + '" target="_blank" rel="noopener">' + esc(p.device.verification_uri) + '</a> — this page updates when the authority answers (until ' + esc(p.device.expires) + ')</div>';
    }
    bar += '<div class="savebar">' +
      '<button class="btn" data-profile-connect="' + esc(p.name) + '">' + (p.state === 'connected' ? 'Reconnect' : 'Connect') + '</button>' +
      (p.can_device ? '<button class="btn ghost" data-profile-device="' + esc(p.name) + '">Connect with a code</button>' : '') +
      (p.state !== 'disconnected' ? '<button class="btn ghost" data-profile-disconnect="' + esc(p.name) + '">Disconnect</button>' : '') +
      '<button class="btn ghost" data-profile-delete="' + esc(p.name) + '">Delete</button></div>' +
      '<div class="profile-paste"><input type="text" class="profile-paste-input" placeholder="If the tab could not reach this machine, paste the redirect URL here" data-profile-paste-input="' + esc(p.name) + '"><button class="btn ghost" data-profile-complete="' + esc(p.name) + '">Complete</button></div>';
    return '<div class="profile" data-profile="' + esc(p.name) + '"><div><b>' + esc(p.name) + '</b> ' + chip + ' <span class="store-hint">' + esc(p.provider || 'custom') + ' · client ' + esc(p.client_id) + (p.has_client_secret ? ' · secret: set' : ' · no client secret') + (p.expires_at ? ' · token until ' + esc(p.expires_at) : '') + '</span></div>' +
      '<div class="store-hint">scopes: ' + esc(scopes || 'none') + '</div><div class="store-hint">rides to: ' + esc((p.hosts || []).join(', ')) + '</div><div class="store-hint">' + used + '</div>' + bar + '</div>';
  });
  return '<div class="card"><h3>CONNECTED ACCOUNTS</h3>' +
    '<div class="dim-note">A profile names a credential the plugins may cite by handle and never see. An account connects by your own consent in your browser; the host keeps the refresh token in a private file, mints access tokens at the wire, and a plugin granted the handle rides them only to the authority\'s own hosts.</div>' +
    (rows.length ? rows.join('') : '<div class="empty">no profiles yet</div>') +
    newProfileHTML(providers) + '</div>';
}
function newProfileHTML(providers) {
  const d = profileDraft;
  if (!d) return '<div class="savebar"><button class="btn" data-profile-new>New profile</button></div>';
  const prov = providers.find(x => x.name === d.provider);
  const services = prov ? prov.services : [];
  const provOpts = providers.map(x => '<option value="' + esc(x.name) + '"' + (x.name === d.provider ? ' selected' : '') + '>' + esc(x.name) + '</option>').join('') + '<option value="custom"' + (d.provider === 'custom' ? ' selected' : '') + '>custom</option>';
  const svcRows = services.map(sv => '<div class="svc-row"><span>' + esc(sv.name) + '</span>' +
    ['', 'read', 'modify'].map(v => '<label><input type="radio" name="svc-' + esc(sv.name) + '" value="' + v + '"' + ((d.services[sv.name] || '') === v ? ' checked' : '') + '> ' + (v || 'no') + '</label>').join('') + '</div>').join('');
  const custom = d.provider === 'custom' ? '<label class="f">AUTHORIZE URL</label><input id="pf-auth" value="' + esc(d.authorize_url || '') + '"><label class="f">TOKEN URL</label><input id="pf-token" value="' + esc(d.token_url || '') + '"><label class="f">DEVICE URL (OPTIONAL)</label><input id="pf-device" value="' + esc(d.device_url || '') + '"><label class="f">REVOKE URL (OPTIONAL)</label><input id="pf-revoke" value="' + esc(d.revoke_url || '') + '">' : '';
  const hosts = d.hosts != null ? d.hosts : (prov ? prov.hosts.join(', ') : '');
  const why = d.connector ? '<div class="dim-note">Your identity asked to connect <b>' + esc(d.connector) + '</b> (' + esc(d.scope === 'modify' ? 'read and modify' : 'read only') + '). Finish the profile below, then Connect it' + (d.handle ? ', then set <b>' + esc(d.handle) + '</b> on ' + esc(d.plugin) + ' to this profile\'s name and grant it the handle' : '; the plugin that needs it cites this profile by name as its handle') + '.</div>' : '';
  return '<div class="profile-form"><h4>NEW PROFILE</h4>' + why +
    '<label class="f">NAME</label><input id="pf-name" value="' + esc(d.name || '') + '" placeholder="google-james">' +
    '<label class="f">PROVIDER</label><select id="pf-provider">' + provOpts + '</select>' +
    '<label class="f">CLIENT ID</label><input id="pf-client" value="' + esc(d.client_id || '') + '" placeholder="the client the authority knows you by">' +
    '<label class="f">CLIENT SECRET (ENTER ONCE; WRITTEN TO A PRIVATE FILE, NEVER SHOWN)</label><input id="pf-secret" type="password" autocomplete="off" value="">' +
    (services.length ? '<label class="f">SERVICES</label>' + svcRows : '') +
    (d.provider === 'custom' || !services.length ? '<label class="f">SCOPES (SPACE-SEPARATED)</label><input id="pf-scopes" value="' + esc((d.scopes || []).join(' ')) + '">' : '') +
    '<label class="f">HOSTS THE TOKEN MAY RIDE TO</label><input id="pf-hosts" value="' + esc(hosts) + '">' + custom +
    '<div class="savebar"><button class="btn" data-profile-save>Save profile</button><button class="btn ghost" data-profile-cancel>Cancel</button><span class="savenote">saved — connect it next</span></div></div>';
}
function readDraft() {
  const d = profileDraft || { services: {} };
  const val = id => { const el = $(id); return el ? el.value : ''; };
  d.name = val('pf-name').trim(); d.client_id = val('pf-client').trim();
  const sel = $('pf-provider'); if (sel) d.provider = sel.value;
  d.services = {};
  document.querySelectorAll('.profile-form input[type=radio]:checked').forEach(r => { if (r.value) d.services[r.name.replace(/^svc-/, '')] = r.value; });
  d.hosts = val('pf-hosts');
  d.scopes = val('pf-scopes').split(/\s+/).filter(Boolean);
  d.authorize_url = val('pf-auth').trim(); d.token_url = val('pf-token').trim(); d.device_url = val('pf-device').trim(); d.revoke_url = val('pf-revoke').trim();
  return d;
}
S.profileSignInNavigate = url => { if (profileWin && !profileWin.closed) { profileWin.location = url; } else { window.open(url, '_blank', 'noopener'); } profileWin = null; };
S.profileSignInAbandon = () => { if (profileWin && !profileWin.closed) profileWin.close(); profileWin = null; };
S.onProfileDevice = () => { /* the host broadcasts the configuration with the code on the profile; nothing to keep here */ };
function wireProfiles(st) {
  st.querySelectorAll('[data-profile-connect]').forEach(b => { b.onclick = () => { profileWin = window.open('', '_blank'); send({ type: 'profile_signin', profile: b.dataset.profileConnect }); }; });
  st.querySelectorAll('[data-profile-device]').forEach(b => { b.onclick = () => { b.disabled = true; b.textContent = 'Asking for a code…'; send({ type: 'profile_device', profile: b.dataset.profileDevice }); }; });
  st.querySelectorAll('[data-profile-disconnect]').forEach(b => { b.onclick = () => { b.disabled = true; send({ type: 'profile_disconnect', profile: b.dataset.profileDisconnect }); }; });
  st.querySelectorAll('[data-profile-delete]').forEach(b => { b.onclick = () => { if (!window.confirm || window.confirm('Delete profile ' + b.dataset.profileDelete + '? Its tokens and client secret are removed.')) { b.disabled = true; send({ type: 'auth_profile_delete', profile: b.dataset.profileDelete }); } }; });
  st.querySelectorAll('[data-profile-complete]').forEach(b => { b.onclick = () => {
    const inp = st.querySelector('[data-profile-paste-input="' + b.dataset.profileComplete.replace(/"/g, '\\"') + '"]');
    const v = (inp && inp.value || '').trim(); if (!v) { if (inp) inp.focus(); return; }
    b.disabled = true; send({ type: 'profile_signin_complete', profile: b.dataset.profileComplete, input: v });
  }; });
  const nb = st.querySelector('[data-profile-new]');
  if (nb) nb.onclick = () => { profileDraft = { provider: 'google', services: {} }; renderPlugins(); };
  const sel = st.querySelector('#pf-provider');
  if (sel) sel.onchange = () => { const d = readDraft(); d.hosts = null; d.services = {}; profileDraft = d; renderPlugins(); };
  const cancel = st.querySelector('[data-profile-cancel]');
  if (cancel) cancel.onclick = () => { profileDraft = null; renderPlugins(); };
  const save = st.querySelector('[data-profile-save]');
  if (save) save.onclick = () => {
    const d = readDraft();
    const secret = ($('pf-secret') && $('pf-secret').value) || '';
    const edit = { name: d.name, provider: d.provider, client_id: d.client_id, client_secret: secret, services: d.services,
      scopes: d.scopes, hosts: (d.hosts || '').split(/[,\s]+/).filter(Boolean),
      authorize_url: d.authorize_url, token_url: d.token_url, device_url: d.device_url, revoke_url: d.revoke_url };
    if ($('pf-secret')) $('pf-secret').value = '';
    save.disabled = true; save.textContent = 'Saving…';
    profileDraft = null;
    send({ type: 'auth_profile_set', profile_edit: edit });
  };
}

