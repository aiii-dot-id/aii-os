
import { startSignIn, signInProgress, wireSignInCompletion } from '../signin.js';
import { S } from '../state.js';
import { $, esc } from '../util.js';
import { send } from '../ws.js';
import { saveConfigSection, sendConfigChanges, configFeedbackHTML, savebarHTML, captureForm, restoreForm, forgetForm } from './settings.js';
import { settingHTML, shown, keyDrafts, keyDraftKey, choiceCache, choiceKey, accountDisclosure } from './plugin-setting.js';

const store = { q: '', cat: '', sort: 'name', open: {} };

const TIER = { T3: 'platform-signed', T2: 'reviewed', T1: 'signed', T0: 'unsigned' };
function tierLabel(t) { return TIER[t] || (t ? esc(t) : 'unsigned'); }
function metaHTML(e) {
  if (e.installed && e.installed_tier) return esc(e.installed_version || e.version) + ' · ' + tierLabel(e.installed_tier);
  return esc(e.version) + ' · the catalog says ' + tierLabel(e.tier);
}
function titleOf(e) { return e.title || e.id; }
function catOf(e) { return String(e.category || 'other').toLowerCase(); }
function sizeText(n) {
  if (!n) return '';
  if (n < 1024) return n + ' B';
  if (n < 1048576) return Math.round(n / 1024) + ' KB';
  if (n < 1073741824) return (n / 1048576).toFixed(1) + ' MB';
  return (n / 1073741824).toFixed(2) + ' GB';
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
  if (e.pending) {
    const refused = e.pending === 'refused';
    badge = chip(refused ? 'unverified' : 'provisional', (refused ? 'refused — ' : e.pending === 'starting' ? 'starting — ' : e.pending === 'retiring' ? 'stopping — ' : e.pending === 'held' ? 'held — ' : 'preparing — ') + esc(e.pending_text || ''));
    btns = (refused ? '<button class="btn sm" data-plugin="retry:' + id + '">Try again</button> ' : '') + uninstall();
  }
  else if (e.installed && e.update_available) { badge = chip('provisional', 'update to ' + esc(e.version)); btns = install('Update') + ' ' + uninstall(); }
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
      '<div class="store-detail" data-detail-of="' + id + '"' + (open ? '' : ' hidden') + '>' + detailHTMLFor(e) + '</div>' +
    '</div>' +
    '<div class="store-actions">' + btns + '</div></div>';
}

function catalogNote(pl) {
  const url = (pl && pl.catalog_url) || '';
  const dir = (pl && pl.catalog_dir) || '';
  if (pl && pl.catalog_error) return esc(pl.catalog_error);
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
    savebarHTML('plugins', 'saved — applies live') +
    skipsHTML(c.plugins.skips) +
    '<label class="f">CATALOG URL' + (isDefault ? ' <span class="store-hint">— the platform\'s catalog, by default; leave empty to keep it</span>' : '') + '</label>' +
    '<input type="text" id="cfg-caturl" value="' + esc(url) + '" placeholder="https://…/aiios-plugins.md">' +
    savebarHTML('catalog', 'saved — the index is fetched from here') +
    runtimeLimitsHTML(c.plugins.runtime) +
    '</div>';
}

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
    savebarHTML('plugin_runtime', 'saved — applies at the next activation');
}

function pendingHTML(pending) {
  if (!pending || !pending.length) return '';
  return pending.map(p => {
    const id = esc(p.id);
    const refused = p.phase === 'refused', starting = p.phase === 'starting', retiring = p.phase === 'retiring', held = p.phase === 'held';
    const label = refused ? 'refused' : starting ? 'starting' : retiring ? 'stopping' : held ? 'held' : 'preparing';
    const when = p.retry_at ? ' at ' + esc(String(p.retry_at).slice(11, 19)) + ' UTC' : '';
    const state = starting ? 'activation follows on its own'
      : p.phase === 'ready' ? 'present and verified — activating'
      : p.phase === 'waiting' ? 'waiting to retry' + when
      : 'downloading';
    let bar = '';
    if (p.bytes_total) {
      const pct = Math.min(100, Math.round(100 * p.bytes_present / p.bytes_total));
      bar = '<div class="prep-bar" role="progressbar" aria-label="download" aria-valuenow="' + pct + '" aria-valuemin="0" aria-valuemax="100"><div class="prep-fill" style="width:' + pct + '%"></div></div>';
    }
    const body = refused
      ? '<div class="prep-err">refused: ' + esc(p.summary) + '</div>' + refusalDetailHTML(p) +
        '<div class="dim-note">' + (p.refusal && p.refusal.class === 'transient' && p.retry_at ? 'It is tried again on its own at ' + esc(p.retry_at) + '.' : 'Nothing is downloading and nothing retries on its own. Fix what the reason names and try again, or remove it.') + '</div>' +
        '<div class="savebar"><button class="btn sm" data-plugin="retry:' + id + '">Try again</button> <button class="btn sm ghost" data-plugin="uninstall:' + id + '">Uninstall</button></div>'
      : held
      ? '<div class="dim-note">' + esc(p.summary) + '</div>' +
        '<div class="dim-note">Nothing to click: it is activated on its own when the hold ends.</div>'
      : retiring
      ? '<div class="dim-note">' + esc(p.summary) + '</div>' + lifecycleHTML(p) +
        '<div class="dim-note">Nothing to click: the host asks the cleanup again on its own' +
        (p.cleanup_at ? ', next at ' + esc(String(p.cleanup_at).slice(11, 19)) + ' UTC' : '') +
        ', and nothing new starts for this plugin until what it held has come back.</div>'
      : '<div class="dim-note">' + esc(p.summary) + ' · ' + state + (p.attempt > 1 ? ' · attempt ' + p.attempt : '') + '</div>' +
        (p.last_error ? '<div class="prep-err">last attempt: ' + esc(p.last_error) + '</div>' : '') +
        (p.refusal ? '<div class="prep-err">last attempt refused: ' + esc(p.refusal.cause || '') + '</div>' + refusalDetailHTML({ refusal: p.refusal }) : '') +
        '<div class="dim-note">Activation follows on its own once every declared file is present and verified; nothing to click.</div>';
    return '<div class="card prep" data-pending="' + id + '"><h3>' + id + ' <span class="soon">' + esc(p.version) + ' · ' + label + '</span></h3>' + bar + body + '</div>';
  }).join('');
}

function installedHTML(installed) {
  if (!installed || !installed.length) return '';
  return installed.map(p => {
    const named = p.title ? esc(p.title) : esc(p.id);
    const head = '<h3>' + named + ' <span class="soon">' + esc(p.version) + ' · ' + esc(p.tier) + '</span></h3>' +
      (p.title ? '<div class="dim-note pkg-id">' + esc(p.id) + '</div>' : '') +
      (p.description ? '<div class="pkg-desc">' + esc(p.description) + '</div>' : '') +
      '<div class="dim-note">' + esc(p.mode) + ', variant ' + esc(p.variant) +
      (p.tools && p.tools.length ? ' · ' + p.tools.length + ' operation' + (p.tools.length === 1 ? '' : 's') + ', reached through the tools organ, never listed in the prompt' : '') + '</div>' +
      readinessHTML(p) + startupHTML(p) + lifecycleHTML(p) + detailHTML(p) + grantsHTML(p) + actsHTML(p);
    const all = p.settings || [];
    const elsewhere = all.filter(s => s.scope === 'hearing' || s.scope === 'speaking');
    const own = all.filter(s => !(s.scope === 'hearing' || s.scope === 'speaking'));
    const pointer = elsewhere.length
      ? '<div class="dim-note">' + elsewhere.length + ' setting' + (elsewhere.length === 1 ? '' : 's') +
        ' for hearing and speaking (' + esc(elsewhere.map(s => s.title || s.key).join(', ')) +
        ') are tuned on <a href="#" data-open-section="speech">Settings → Speech</a>, beside the engine.</div>'
      : '';
    if (!own.length) {
      return '<div class="card">' + head + (pointer || '<div class="empty">no settings declared</div>') + sessionSettingsHTML(p) + '</div>';
    }
    return '<div class="card">' + head + own.map(s => settingHTML(p.id, s)).join('') + pointer + sessionSettingsHTML(p) +
      savebarHTML('plugin:' + p.id, appliesNote(p)) + '</div>';
  }).join('');
}
function lifecycleHTML(p) {
  const l = p.lifecycle;
  if (!l) return '';
  const bits = [esc(l.state) + (l.since ? ' since ' + esc(l.since) : '')];
  for (const a of (l.activations || [])) {
    let s = 'generation ' + a.gen + ' ' + esc(a.role) + (a.version ? ' (' + esc(a.version) + ')' : '');
    const t = a.timings_ms || {};
    const parts = Object.keys(t).sort().map(k => esc(k) + ' ' + t[k] + ' ms');
    if (parts.length) s += ' — ' + parts.join(', ');
    bits.push(s);
  }
  if (l.admission) bits.push(esc(l.admission));
  if (l.residue && l.residue.length) bits.push('still held: ' + esc(l.residue.join('; ')));
  if (l.retry_at) bits.push('tried again at ' + esc(l.retry_at));
  if (l.held) bits.push('held: ' + esc(l.held));
  const refusal = l.refusal ? '<div class="prep-err">last attempt refused: ' + esc(l.refusal.cause || '') + '</div>' + refusalDetailHTML({ refusal: l.refusal }) : '';
  return '<div class="dim-note lifecycle">' + bits.join('<br>') + '</div>' + refusal;
}
function refusalDetailHTML(p) {
  const r = p.refusal;
  let out = '';
  if (r) {
    out += '<div class="dim-note refusal">' + esc(r.class || '') + (r.stage ? ' at ' + esc(r.stage) : '') + (r.remedy ? ' — ' + esc(r.remedy) : '') + '</div>';
    if (r.evidence) out += '<details class="dim-note"><summary>evidence</summary><pre>' + esc(r.evidence) + '</pre></details>';
  }
  if (p.residue && p.residue.length) out += '<div class="dim-note">still held: ' + esc(p.residue.join('; ')) + '</div>';
  return out;
}
function startupHTML(p) {
  const s = p.startup;
  if (!s) return '';
  const sec = ms => ms % 1000 === 0 ? (ms / 1000) + ' s' : ms + ' ms';
  const asked = s.source === 'operator' ? 'you set ' + sec(s.requested_ms)
    : s.source === 'package' ? 'the package asked for ' + sec(s.requested_ms)
    : 'the default is ' + sec(s.requested_ms);
  return '<div class="dim-note startup-note">Allowed ' + sec(s.effective_ms) + ' to report ready — ' + esc(asked) +
    (s.capped ? '; capped at the ' + sec(s.ceiling_ms) + ' ceiling' : '; ceiling ' + sec(s.ceiling_ms)) + '.</div>';
}
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
const GRANTS = [
  ['interaction.read', 'interactions', 'interaction history', "Read this identity's operator-visible interaction history, including record content"],
  ['ring4.kv', 'kv', 'key-value store', 'a scoped namespace in the identity\'s store (ring4.kv)'],
  ['ring4.memory', 'memory', 'memory', 'remember and recall through the identity\'s memory instruments (ring4.memory)'],
  ['voice.observe', 'voice', 'voice', 'submit heard utterances into the conversation (voice.observe)'],
  ['model.embeddings', 'embeddings', 'embeddings', 'vectors from the identity\'s own model endpoint, at its cost (model.embeddings)'],
  ['tools.publish', 'tools', 'publish tools', 'grow the identity\'s tool surface at run time (tools.publish)'],
  ['fs.sandbox', 'files', 'files', 'work in this identity\'s sandbox — its home and the folders in Settings → Sandbox, never its data directory; writes only while read only is off (fs.sandbox)'],
];
function grantsHTML(p) {
  const g = p.grants || {};
  const caps = p.capabilities || [];
  const key = field => 'plugins.grants.' + esc(p.id) + '.' + field;
  const listed = (v, none) => v && v.length ? esc(v.join(', ')) : none;
  const rows = GRANTS.filter(x => caps.includes(x[0])).map(x =>
    '<label class="grant-row"><input type="checkbox" data-grant-plugin="' + esc(p.id) + '" data-grant-field="' + x[1] + '"' + (g[x[1]] ? ' checked' : '') + '>' +
    '<span><b>' + x[2] + '</b> <span class="store-hint">' + esc(x[3]) + '</span></span></label>');
  rows.push('<label class="grant-row"><input type="checkbox" data-grant-plugin="' + esc(p.id) + '" data-grant-field="read_only"' + (g.read_only ? ' checked' : '') + '>' +
    '<span><b>read only</b> <span class="store-hint">operations that write or execute are refused; the plugin may look, not act</span></span></label>');
  const lists = [];
  const standing = g.auto_confirm || [];
  if (standing.length) lists.push('always confirmed, without asking: ' + standing.map(op => esc(op) + ' <button class="btn ghost" data-revoke-auto="' + esc(p.id) + '" data-op="' + esc(op) + '">revoke</button>').join(', '));
  if (caps.some(c => c === 'net.outbound' || c.startsWith('net.outbound:'))) lists.push('outbound hosts: ' + listed(g.hosts, 'none') + ' — granted with a key pasted or an account chosen on this card (the one host the package signs for), or set as ' + key('hosts') + ' in the config file');
  if (caps.includes('net.local')) lists.push('local network: ' + listed(g.local, 'none') + ' — a list, set as ' + key('local') + ' (an address, a range like 192.168.1.0/24, or a name on your own network, each with :port or :* for any); ' + (g.plaintext_credentials ? 'credentials may be sent in the clear to these devices' : 'a credential over plain http needs ' + key('plaintext_credentials') + ': true — anyone on your network could read it'));
  if ((p.settings || []).some(s => s.type === 'secret') || (g.handles && g.handles.length)) lists.push('credential handles: ' + listed(g.handles, 'none') + ' — granted with a key pasted or an account chosen on this card, or set as ' + key('credential_handles') + ' in the config file');
  if (!rows.length && !lists.length) return '';
  return '<div class="grants"><label class="f">GRANTED BY YOU</label>' + rows.join('') +
    lists.map(l => '<div class="store-hint grant-list">' + l + '</div>').join('') +
    (rows.length ? savebarHTML('grants:' + p.id, 'saved — applies to the plugin\'s next call', { label: 'Save grants' }) : '') +
    '</div>';
}
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
function appliesNote(p) {
  return p.applies === 'next_session'
    ? 'saved — the next spoken session opens with these; a session already running keeps the values it opened with'
    : 'saved — the plugin reads it on its next call';
}
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
  const kept = skips.filter(s => !s.kind || s.kind === 'policy');
  const refused = skips.filter(s => s.kind && s.kind !== 'policy');
  return (kept.length ? '<div class="store-skips"><label class="f">PRESENT, VERIFIED — NOT LOADED</label>' +
    kept.map(s => '<div class="store-skip"><b>' + esc(s.id) + '</b> (' + esc(s.tier) + ') — ' + esc(s.reason) + '</div>').join('') + '</div>' : '') +
    (refused.length ? '<div class="store-skips refused"><label class="f">PRESENT — REFUSED</label>' +
    refused.map(s => '<div class="store-skip"><b>' + esc(s.package || s.dir) + '</b>' + (s.id ? ' (' + esc(s.id) + ')' : '') + ' — ' + esc(s.reason) + '</div>').join('') + '</div>' : '');
}

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
    btn.disabled = true; btn.textContent = action === 'install' ? 'Installing…' : action === 'retry' ? 'Retrying…' : 'Removing…';
    send({ type: 'plugin', plugin: { action: action, id: id } });
  }; });
  const rf = st.querySelector('[data-catalog-refresh]');
  if (rf) rf.onclick = () => { rf.disabled = true; rf.textContent = 'Refreshing…'; send({ type: 'catalog_refresh' }); };
}

export function connectPrefill(req, providers, installed) {
  if (!req) return null;
  const word = String(req.connector || '').toLowerCase();
  const mode = req.scope === 'modify' ? 'modify' : 'read';
  const plugin = (installed || []).find(p => p.id === req.connector);
  const hinted = plugin && (plugin.settings || []).find(s => s.type === 'secret' && s.oauth && (!req.setting || s.key === req.setting));
  if (hinted) {
    const services = {};
    (hinted.oauth.services || []).forEach(svc => { services[svc] = mode; });
    const prov = (providers || []).some(p => p.name === hinted.oauth.provider) ? hinted.oauth.provider : 'custom';
    return { name: (hinted.oauth.provider + '-' + hinted.key).replace(/[^a-z0-9._-]+/g, '-').slice(0, 40), provider: prov, services: services, connector: req.connector, scope: mode, handle: hinted.key, title: hinted.title || hinted.key, plugin: plugin.id, fromCard: !!req.fromCard };
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
  if (st.formComposing) { st.formRefreshDue = true; return; }
  const retiredForm = st.querySelector('.profile-form');
  if (!profileDraft && retiredForm) { forgetForm(retiredForm); retiredForm.remove(); }
  const activeForm = captureForm(st);
  const c = S.config;
  if (S.connectRequest && c && c.plugins) {
    profileDraft = connectPrefill(S.connectRequest, c.plugins.providers, c.plugins.installed);
    S.connectRequest = null;
  }
  const active = document.activeElement;
  const typing = active && active.dataset && active.hasAttribute('data-pkey-input') ? { k: keyDraftKey(active.dataset.pkeyPlugin, active.dataset.pkeyKey), at: active.selectionStart } : null;
  st.innerHTML = configFeedbackHTML() + storeHTML(c && c.plugins) + pendingHTML(c && c.plugins.pending) + installedHTML(c && c.plugins.installed) + profilesHTML(c && c.plugins) + settingsHTML(c);
  restoreForm(st, activeForm);
  st.oncompositionstart = () => { st.formComposing = true; };
  st.oncompositionend = () => { st.formComposing = false; if (st.formRefreshDue) { st.formRefreshDue = false; renderPlugins(); } };
  wireKeys(st, typing);
  wireAccounts(st);
  wireChoices(st);
  wireProfiles(st);
  st.querySelectorAll('[data-save]').forEach(btn => { btn.onclick = () => { saveConfigSection(btn.dataset.save); renderPlugins(); }; });
  st.querySelectorAll('[data-open-section]').forEach(a => {
    a.onclick = e => { e.preventDefault(); if (S.openSettings) S.openSettings(a.dataset.openSection, 'sp-provider-stt'); };
  });
  st.querySelectorAll('[data-act-decision]').forEach(btn => { btn.onclick = () => {
    const card = btn.closest('[data-act-card]');
    card.querySelectorAll('button').forEach(b => { b.disabled = true; });
    btn.textContent = btn.dataset.actDecision === 'deny' ? 'Dropping…' : 'Running…';
    send({ type: 'plugin', plugin: { action: btn.dataset.actDecision, id: btn.dataset.actPlugin, act: btn.dataset.actId } });
  }; });
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

let profileDraft = null;
function profilesHTML(pl) {
  const profiles = (pl && pl.auth_profiles) || [];
  const providers = (pl && pl.providers) || [];
  const rows = profiles.map(p => {
    const chip = '<span class="chip chip-' + esc(p.state) + '">' + esc(p.state) + '</span>';
    const used = p.handles && p.handles.length ? 'used by ' + esc(p.handles.join(', ')) : 'no plugin is granted this handle yet: choose it on the card of a plugin that asks for such an account, or grant it in the config file (plugins.grants.<id>.credential_handles)';
    if (p.scheme !== 'oauth2') {
      return '<div class="profile" data-profile="' + esc(p.name) + '"><div><b>' + esc(p.name) + '</b> <span class="store-hint">' + esc(p.scheme) + ' · ' + esc(p.host) + ':' + esc(p.port) + ' · secret from ' + esc(p.secret_source || 'nowhere') + ' · edited in the config file</span></div><div class="store-hint">' + used + '</div></div>';
    }
    const scopes = (p.scopes || []).map(sc => sc.replace(/^https?:\/\/[^/]+\/auth\//, '')).join(', ');
    let bar = signInProgress(p.signin, 'profile', p.name);
    bar += '<div class="savebar">' +
      (p.signin && p.signin.status === 'pending' ? '<button class="btn ghost" data-profile-cancel-signin="' + esc(p.name) + '">Cancel sign-in</button>' : '') +
      '<button class="btn" data-profile-connect="' + esc(p.name) + '">' + (p.state === 'connected' ? 'Reconnect' : 'Connect') + '</button>' +
      (p.can_device ? '<button class="btn ghost" data-profile-device="' + esc(p.name) + '">Connect with a code</button>' : '') +
      (p.state !== 'disconnected' ? '<button class="btn ghost" data-profile-disconnect="' + esc(p.name) + '">Disconnect</button>' : '') +
      '<button class="btn ghost" data-profile-delete="' + esc(p.name) + '">Delete</button></div>';
    return '<div class="profile" data-profile="' + esc(p.name) + '"><div><b>' + esc(p.name) + '</b> ' + chip + ' <span class="store-hint">' + esc(p.provider || 'custom') + ' · client ' + esc(p.client_id) + (p.has_client_secret ? ' · secret: set' : ' · no client secret') + (p.expires_at ? ' · token until ' + esc(p.expires_at) : '') + '</span></div>' +
      '<div class="store-hint">scopes: ' + esc(scopes || 'none') + '</div><div class="store-hint">rides to: ' + esc((p.hosts || []).join(', ')) + '</div><div class="store-hint">' + used + '</div>' +
      (p.revocation ? '<div class="store-hint" data-profile-revocation="' + esc(p.name) + '">' + esc(p.revocation) + '</div>' : '') + bar + '</div>';
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
  const manual = prov && prov.sign_in === 'manual';
  const returnURL = manual ? prov.redirect_uri : location.origin + '/oauth/callback';
  const services = prov ? prov.services : [];
  const provOpts = providers.map(x => '<option value="' + esc(x.name) + '"' + (x.name === d.provider ? ' selected' : '') + '>' + esc(x.name) + '</option>').join('') + '<option value="custom"' + (d.provider === 'custom' ? ' selected' : '') + '>custom</option>';
  const svcRows = services.map(sv => '<div class="svc-row"><span>' + esc(sv.name) + '</span>' +
    ['', 'read', 'modify'].map(v => '<label><input type="radio" name="svc-' + esc(sv.name) + '" value="' + v + '"' + ((d.services[sv.name] || '') === v ? ' checked' : '') + '> ' + (v || 'no') + '</label>').join('') + '</div>').join('');
  const custom = d.provider === 'custom' ? '<label class="f">AUTHORIZE URL</label><input id="pf-auth" value="' + esc(d.authorize_url || '') + '"><label class="f">TOKEN URL</label><input id="pf-token" value="' + esc(d.token_url || '') + '"><label class="f">DEVICE URL (OPTIONAL)</label><input id="pf-device" value="' + esc(d.device_url || '') + '"><label class="f">REVOKE URL (OPTIONAL)</label><input id="pf-revoke" value="' + esc(d.revoke_url || '') + '">' : '';
  const hosts = d.hosts != null ? d.hosts : (prov ? prov.hosts.join(', ') : '');
  const choose = d.handle ? 'choose it for <b>' + esc(d.title || d.handle) + '</b> on ' + esc(d.plugin) + '\'s card — saving there grants the plugin its handle and the one host its package signs for' : '';
  const why = d.fromCard ? '<div class="dim-note">An account for ' + esc(d.plugin) + ': finish the profile below and Connect it, then ' + choose + '.</div>'
    : d.connector ? '<div class="dim-note">Your identity asked to connect <b>' + esc(d.connector) + '</b> (' + esc(d.scope === 'modify' ? 'read and modify' : 'read only') + '). Finish the profile below, then Connect it' + (choose ? ', then ' + choose : '; the plugin that needs it cites this profile by name as its handle') + '.</div>' : '';
  return '<div class="profile-form"><h4>NEW PROFILE</h4>' + why +
    '<label class="f">NAME</label><input id="pf-name" value="' + esc(d.name || '') + '" placeholder="google-work">' +
    '<label class="f">PROVIDER</label><select id="pf-provider">' + provOpts + '</select>' +
    '<label class="f">REGISTERED RETURN URL</label><input id="pf-redirect" value="' + esc(d.redirect_uri || returnURL || '') + '"><div class="store-hint">' + (manual ? 'The provider shows a code at this address. Paste that code here to finish sign-in.' : 'Register this dashboard address with the provider for browser sign-in.') + '</div>' +
    '<label class="f">CLIENT ID (OPTIONAL WHEN CONFIGURED)</label><input id="pf-client" value="' + esc(d.client_id || '') + '" placeholder="the client the authority knows you by">' +
    '<label class="f">CLIENT SECRET (ENTER ONCE; WRITTEN TO A PRIVATE FILE, NEVER SHOWN)</label><input id="pf-secret" type="password" autocomplete="off" value="">' +
    (services.length ? '<label class="f">SERVICES</label>' + svcRows : '') +
    (d.provider === 'custom' || !services.length ? '<label class="f">SCOPES (SPACE-SEPARATED)</label><input id="pf-scopes" value="' + esc((d.scopes || []).join(' ')) + '">' : '') +
    '<label class="f">HOSTS THE TOKEN MAY RIDE TO</label><input id="pf-hosts" value="' + esc(hosts) + '">' + custom +
    '<div class="savebar"><button class="btn" data-profile-save>Save profile</button><button class="btn ghost" data-profile-cancel>Cancel</button><span class="savenote">saved — connect it next</span></div></div>';
}
function readDraft() {
  const d = profileDraft || { services: {} };
  const val = id => { const el = $(id); return el ? el.value : ''; };
  d.redirect_uri = val('pf-redirect').trim(); d.name = val('pf-name').trim(); d.client_id = val('pf-client').trim();
  const sel = $('pf-provider'); if (sel) d.provider = sel.value;
  d.services = {};
  document.querySelectorAll('.profile-form input[type=radio]:checked').forEach(r => { if (r.value) d.services[r.name.replace(/^svc-/, '')] = r.value; });
  d.hosts = val('pf-hosts');
  d.scopes = val('pf-scopes').split(/\s+/).filter(Boolean);
  d.authorize_url = val('pf-auth').trim(); d.token_url = val('pf-token').trim(); d.device_url = val('pf-device').trim(); d.revoke_url = val('pf-revoke').trim();
  return d;
}
function wireChoices(st) {
  const ask = (plugin, key) => {
    choiceCache.set(choiceKey(plugin, key), { pending: true });
    send({ type: 'plugin_setting_choices', plugin_key: { plugin: plugin, key: key } });
  };
  st.querySelectorAll('[data-pset-plugin]').forEach(el => {
    const card = el.closest('.store-setting');
    const marker = card && card.querySelector('[data-choices-plugin]');
    if (!marker) return;
    if (!choiceCache.has(choiceKey(marker.dataset.choicesPlugin, marker.dataset.choicesKey))) ask(marker.dataset.choicesPlugin, marker.dataset.choicesKey);
  });
  st.querySelectorAll('[data-choices-refresh]').forEach(b => { b.onclick = () => { ask(b.dataset.choicesPlugin, b.dataset.choicesKey); renderPlugins(); }; });
}
function wireKeys(st, typing) {
  st.querySelectorAll('[data-pkey-input]').forEach(inp => {
    const k = keyDraftKey(inp.dataset.pkeyPlugin, inp.dataset.pkeyKey);
    if (keyDrafts.has(k)) inp.value = keyDrafts.get(k);
    inp.oninput = () => { if (inp.value) keyDrafts.set(k, inp.value); else keyDrafts.delete(k); };
    if (typing && typing.k === k) { inp.focus(); try { inp.setSelectionRange(typing.at, typing.at); } catch (e) { } }
  });
  st.querySelectorAll('[data-pkey-remove]').forEach(b => { b.onclick = () => {
    const account = b.dataset.pkeyAccount;
    const ask = account !== undefined
      ? 'Remove ' + (account || 'the kept key') + ' from ' + b.dataset.pkeyPlugin + '? It can no longer call ' + (b.dataset.accountHost || 'its service') + ' as ' + (account || 'that key') + '; the grant of the host stays, and the account stays connected for anything else.'
      : 'Forget the key kept for ' + b.dataset.pkeyKey + '? The plugin cannot reach its service until you paste one again.';
    if (window.confirm && !window.confirm(ask)) return;
    b.disabled = true;
    keyDrafts.delete(keyDraftKey(b.dataset.pkeyPlugin, b.dataset.pkeyKey));
    send({ type: 'plugin_key_clear', plugin_key: { plugin: b.dataset.pkeyPlugin, key: b.dataset.pkeyKey } });
  }; });
}
function wireAccounts(st) {
  st.querySelectorAll('select[data-pset-type="secret"]').forEach(sel => {
    const said = sel.closest('.store-setting')?.querySelector('[data-pacct-grants]');
    const show = () => {
      const picked = sel.selectedOptions[0];
      if (said) said.textContent = accountDisclosure(sel.dataset.psetPlugin, sel.dataset.accountHost, picked && !picked.disabled ? sel.value : '', sel.dataset.psetSaved || '', sel.dataset.psetGranted === 'true');
    };
    sel.onchange = show;
    show();
  });
  st.querySelectorAll('[data-pacct-connect]').forEach(b => { b.onclick = () => {
    S.connectRequest = { connector: b.dataset.pacctPlugin, setting: b.dataset.pacctKey, scope: 'read', fromCard: true };
    renderPlugins();
    const form = st.querySelector('.profile-form');
    if (form && form.scrollIntoView) form.scrollIntoView({ block: 'nearest' });
  }; });
}
function wireProfiles(st) {
  wireSignInCompletion(st);
  st.querySelectorAll('[data-profile-cancel-signin]').forEach(b => { b.onclick = () => send({type:'profile_signin_cancel',profile:b.dataset.profileCancelSignin}); });
  st.querySelectorAll('[data-profile-connect]').forEach(b => { b.onclick = () => { startSignIn({ type: 'profile_signin', profile: b.dataset.profileConnect }); }; });
  st.querySelectorAll('[data-profile-device]').forEach(b => { b.onclick = () => { b.disabled = true; b.textContent = 'Asking for a code…'; send({ type: 'profile_device', profile: b.dataset.profileDevice }); }; });
  st.querySelectorAll('[data-profile-disconnect]').forEach(b => { b.onclick = () => { b.disabled = true; send({ type: 'profile_disconnect', profile: b.dataset.profileDisconnect }); }; });
  st.querySelectorAll('[data-profile-delete]').forEach(b => { b.onclick = () => { if (!window.confirm || window.confirm('Delete profile ' + b.dataset.profileDelete + '? Its tokens and client secret are removed.')) { b.disabled = true; send({ type: 'auth_profile_delete', profile: b.dataset.profileDelete }); } }; });
  const nb = st.querySelector('[data-profile-new]');
  if (nb) nb.onclick = () => { profileDraft = { provider: 'google', services: {} }; renderPlugins(); };
  const sel = st.querySelector('#pf-provider');
  if (sel) sel.onchange = () => { const d = readDraft(); const form = st.querySelector('.profile-form'); forgetForm(form); form.remove(); d.hosts = null; d.redirect_uri = ''; d.services = {}; profileDraft = d; renderPlugins(); };
  const cancel = st.querySelector('[data-profile-cancel]');
  if (cancel) cancel.onclick = () => { const form = st.querySelector('.profile-form'); forgetForm(form); form.remove(); profileDraft = null; renderPlugins(); };
  const save = st.querySelector('[data-profile-save]');
  if (save) save.onclick = () => {
    const d = readDraft();
    const secret = ($('pf-secret') && $('pf-secret').value) || '';
    const edit = { redirect_uri: d.redirect_uri, name: d.name, provider: d.provider, client_id: d.client_id, client_secret: secret, services: d.services,
      scopes: d.scopes, hosts: (d.hosts || '').split(/[,\s]+/).filter(Boolean),
      authorize_url: d.authorize_url, token_url: d.token_url, device_url: d.device_url, revoke_url: d.revoke_url };
    save.disabled = true; save.textContent = 'Saving…';
    const request = send({ type: 'auth_profile_set', profile_edit: edit });
    if (!request) { save.disabled = false; save.textContent = 'Not connected — try again'; return; }
    if ($('pf-secret')) $('pf-secret').value = '';
    forgetForm(st.querySelector('.profile-form'));
    profileDraft = null;
  };
}
