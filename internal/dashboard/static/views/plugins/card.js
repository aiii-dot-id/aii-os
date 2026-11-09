import { S } from '../../state.js';
import { esc } from '../../util.js';
import { send } from '../../ws.js';
import { pendingSlot, pendingKeyed, notTaken, NOT_SENT, LOST } from '../../pending.js';
import { savebarHTML } from '../settings.js';
import W from '../../strings/settings-permissions.js';
import { settingHTML, shown } from '../plugin-setting.js';
import { renderPlugins } from '../plugins.js';

const revoking = pendingSlot();
let revokeSaid = null;
S.revokeAnswered = (requestID, text) => {
  const asked = revoking.claim(requestID);
  if (!asked) return false;
  revokeSaid = text === null
    ? { plugin: asked.plugin, kind: 'good', text: 'Revoked: ' + asked.operation + ' asks you again before it runs.' }
    : { plugin: asked.plugin, kind: 'bad', text };
  if (S.view === 'plugins') renderPlugins();
  return true;
};
S.revokeLost = () => {
  const asked = revoking.drop();
  if (asked) revokeSaid = { plugin: asked.plugin, kind: 'bad', text: 'Connection lost — it is not known whether ' + asked.operation + ' was revoked. The card shows what the runtime last reported; nothing is retried.' };
};

function primaryToolsHTML(p) {
  if (!p.primary_tools || !p.primary_tools.length) return '';
  return '<div class="dim-note">Primary tools, in the identity\'s tool list: ' + esc(p.primary_tools.join(', ')) + '</div>';
}

export function framePagesHTML(p) {
  const pages = S.framePagesOf ? S.framePagesOf(p.id) : [];
  if (!pages.length) return '';
  return '<div class="frame-pages">' + pages.map(f => '<button class="c-button" data-open-frame="' + esc(f.id) + '">Open ' + esc(f.title) + '</button>').join(' ') + '</div>';
}
S.framePagesChanged = () => { if (S.view === 'plugins') renderPlugins(); };
export function assetCardHTML(p) {
  return '<div class="card" data-asset-card="' + esc(p.id) + '"><h3>' + esc(p.title || p.id) + '</h3>' +
    (p.title ? '<div class="dim-note pkg-id">' + esc(p.id) + '</div>' : '') +
    (p.description ? '<div class="pkg-desc">' + esc(p.description) + '</div>' : '') +
    '<div class="dim-note">' + esc(S.framePagesOf && S.framePagesOf(p.id).length ? W.addsAPage : W.runsNoProgram) + '</div>' + lifecycleHTML(p) + detailHTML(p) + '</div>';
}
export function installedHTML(installed) {
  if (!installed || !installed.length) return '';
  return installed.map(p => {
    const named = p.title ? esc(p.title) : esc(p.id);
    const head = '<h3>' + named + '</h3>' +
      (p.title ? '<div class="dim-note pkg-id">' + esc(p.id) + '</div>' : '') +
      (p.description ? '<div class="pkg-desc">' + esc(p.description) + '</div>' : '') +
      (p.tools && p.tools.length ? '<div class="dim-note">' + p.tools.length + ' tool' + (p.tools.length === 1 ? '' : 's') + '</div>' : '') +
      primaryToolsHTML(p) + readinessHTML(p) + checksHTML(p) + startupHTML(p) + lifecycleHTML(p) + componentSetsHTML(servingSelection(p)) + detailHTML(p) + operationsHTML(p) + grantsHTML(p) + actsHTML(p);
    const all = p.settings || [];
    const elsewhere = all.filter(s => s.scope === 'hearing' || s.scope === 'speaking');
    const own = all.filter(s => !(s.scope === 'hearing' || s.scope === 'speaking'));
    const pointer = elsewhere.length
      ? '<div class="dim-note">' + elsewhere.length + ' setting' + (elsewhere.length === 1 ? '' : 's') +
        ' for hearing and speaking (' + esc(elsewhere.map(s => s.title || s.key).join(', ')) +
        ') are tuned on <a href="#" data-open-section="speech">Settings → Speech</a>, beside the engine.</div>'
      : '';
    if (!own.length) {
      return '<div class="card">' + head + (pointer || '<div class="empty">No settings.</div>') + sessionSettingsHTML(p) + '</div>';
    }
    return '<div class="card">' + head + own.map(s => settingHTML(p.id, s)).join('') + pointer + sessionSettingsHTML(p) +
      savebarHTML('plugin:' + p.id, appliesNote(p)) + '</div>';
  }).join('');
}
export function lifecycleHTML(p) {
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
export function componentSetsHTML(sel) {
  if (!sel || !sel.sets || !sel.sets.length) return '';
  const rows = sel.sets.map(s => {
    const role = s.chosen ? esc(s.why || 'chosen') : s.arriving ? esc(s.why || 'arriving') : 'passed over';
    const head = '<div class="set-head"><b>' + esc(s.variant) + '</b> — ' + role + '</div>';
    const facts = [['disk', s.storage], ['download', s.bandwidth], ['this machine', s.resources]]
      .filter(f => f[1]).map(f => '<div>' + f[0] + ': ' + esc(f[1]) + '</div>').join('');
    const why = (s.reasons || []).map(r => '<li>' + esc(r.words) +
      (r.remedy ? ' — <span class="set-remedy">to admit it: ' + esc(r.remedy) + '</span>' : '') + '</li>').join('');
    return '<div class="set' + (s.chosen ? ' is-chosen' : s.arriving ? ' is-arriving' : '') + '" data-set="' + esc(s.variant) + '">' + head + facts +
      (why ? '<ul class="set-why">' + why + '</ul>' : '') + '</div>';
  }).join('');
  const none = sel.variant ? '' : '<div class="prep-err">no component set fits this machine</div>';
  const needs = sel.needs ? '<div class="set-needs">' + esc(sel.needs) + '</div>' : '';
  return '<div class="dim-note sets"><div class="set-title">COMPONENT SETS</div>' + needs + none + rows + '</div>';
}
function servingSelection(p) {
  const acts = (p.lifecycle && p.lifecycle.activations) || [];
  const serving = acts.find(a => a.role === 'active' && a.selection);
  if (serving) return serving.selection;
  for (let i = acts.length - 1; i >= 0; i--) if (acts[i].selection) return acts[i].selection;
  return null;
}
export function refusalDetailHTML(p) {
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
  const differs = r.differs ? '<div class="dim-note engine-differs">' + esc(r.differs) + '</div>' : '';
  return '<div class="dim-note engine-ready">the engine reported itself ready: ' + bits.join(' · ') + '</div>' + differs;
}
function checksHTML(p) {
  const acts = (p.lifecycle && p.lifecycle.activations) || [];
  const a = acts.find(x => x.role === 'active' && x.checks) || [...acts].reverse().find(x => x.checks);
  const c = a && a.checks;
  if (p.probes && (!c || c.state === 'none')) return probesHTML(p.probes);
  if (!c) return '';
  const which = c.check ? ' — \u201c' + esc(c.check) + '\u201d: ' + esc(c.words || '') : '';
  let line;
  switch (c.state) {
    case 'passed':
      line = 'its publisher\'s checks passed on this machine ' + esc(c.at || '');
      break;
    case 'pending':
      line = 'checks not yet passed: ' + (c.outcome === 'timed_out' ? 'timed out' : 'not answered') + which +
        (c.timeouts > 1 ? ' (' + c.timeouts + ' starts in a row within a day)' : '');
      break;
    case 'failed':
      line = 'its publisher\'s checks failed on this machine' + which;
      break;
    default:
      line = 'the plugin declares no checks; it is used on its readiness alone';
  }
  return '<div class="dim-note checks-note" data-checks="' + esc(c.state) + '">' + line + '</div>';
}
function probesHTML(v) {
  let tail;
  switch (v.state) {
    case 'passed':
      tail = 'they passed on this machine ' + esc(v.at || '');
      break;
    case 'failed':
      tail = 'they failed on this machine: ' + esc(v.words || '') + '; memory does not use its vectors until it starts again';
      break;
    case 'pending':
      tail = 'not yet passed: ' + esc(v.words || '') + '; they are asked again shortly';
      break;
    case 'checking':
      tail = 'they are being checked';
      break;
    default:
      tail = 'they have not been checked yet';
  }
  return '<div class="dim-note checks-note" data-checks="probes" data-probes="' + esc(v.state) + '">the plugin declares no checks of its own; ' +
    'its signed probes are checked before memory uses its vectors \u2014 ' + tail + '</div>';
}
const actWaits = pendingKeyed();
const actPressed = new Map();
const actSaid = new Map();
const deciding = decision => decision === 'deny' ? 'Dropping…' : 'Running…';
S.actRefused = (requestID, text) => {
  const id = actWaits.refused(requestID);
  if (id === null) return false;
  actPressed.delete(id);
  actSaid.set(id, notTaken(text));
  if (S.view === 'plugins') renderPlugins();
  return true;
};
S.actsLost = () => { actWaits.lost().forEach(id => { actPressed.delete(id); actSaid.set(id, LOST); }); };

function actsHTML(p) {
  const acts = p.acts || [];
  if (!acts.length) return '';
  const argLine = (k, v) => '<div class="act-arg"><span>' + esc(k) + '</span><b>' + esc(typeof v === 'string' ? v : JSON.stringify(v)) + '</b></div>';
  return '<div class="acts"><label class="f">AWAITING YOUR CONFIRMATION</label>' + acts.map(a => {
    const args = Object.keys(a.args || {}).sort().filter(k => k !== 'finals' && k !== 'sequences' || !(a.finals && a.finals.length)).map(k => argLine(k, a.args[k])).join('');
    const finals = (a.finals || []).map(f => '<div class="act-final">#' + esc(f.sequence) + ' ' + (f.heard ? '“' + esc(f.text) + '”' : '<span class="setting-stale">never heard on this session</span>') + '</div>').join('');
    const quiet = actWaits.waiting(a.id) ? ' disabled' : '';
    const label = (decision, text) => actWaits.waiting(a.id) && actPressed.get(a.id) === decision ? deciding(decision) : text;
    return '<div class="act" data-act-card="' + esc(a.id) + '"><div><b>' + esc(a.summary || a.operation) + '</b> <span class="store-hint">' + esc(a.operation) + (a.effects ? ' · ' + esc(a.effects) : '') + (a.session ? ' · session ' + esc(a.session) : '') + '</span></div>' +
      args + finals +
      '<div class="store-hint">proposed ' + esc(a.proposed) + ' · expires ' + esc(a.expires) + ' · runs once, with exactly these arguments, when you confirm; Always runs it now and every time from now on, until you revoke it below</div>' +
      '<div class="savebar"><button class="btn" data-act-plugin="' + esc(p.id) + '" data-act-id="' + esc(a.id) + '" data-act-decision="confirm"' + quiet + '>' + label('confirm', 'Confirm') + '</button> ' +
      '<button class="btn" data-act-plugin="' + esc(p.id) + '" data-act-id="' + esc(a.id) + '" data-act-decision="always"' + quiet + '>' + label('always', 'Always') + '</button> ' +
      '<button class="btn ghost" data-act-plugin="' + esc(p.id) + '" data-act-id="' + esc(a.id) + '" data-act-decision="deny"' + quiet + '>' + label('deny', 'Deny') + '</button>' +
      (actSaid.has(a.id) ? ' <span class="savesay" data-act-said data-announce>' + esc(actSaid.get(a.id)) + '</span>' : '') + '</div></div>';
  }).join('') + '</div>';
}
const GRANTS = [
  ['interaction.read', 'interactions', 'Read your conversations', ''],
  ['ring4.kv', 'kv', 'Its own storage', ''],
  ['ring4.memory', 'memory', 'Remember and recall', ''],
  ['voice.observe', 'voice', 'Hear what is said', ''],
  ['model.embeddings', 'embeddings', 'Use the model to find meaning', ''],
  ['tools.publish', 'tools', 'Add tools while it runs', ''],
  ['fs.sandbox', 'files', 'Files in their folders', ''],
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
    '<span><b>Read only</b> <span class="store-hint">it can look, not change anything</span></span></label>');
  const lists = [];
  const standing = g.auto_confirm || [];
  const held = revoking.waiting();
  if (standing.length) lists.push('always confirmed, without asking: ' + standing.map(op => esc(op) + ' <button class="btn ghost" data-revoke-auto="' + esc(p.id) + '" data-op="' + esc(op) + '"' + (held ? ' disabled' : '') + '>' +
    (held && held.plugin === p.id && held.operation === op ? 'Revoking…' : 'revoke') + '</button>').join(', '));
  if (revokeSaid && revokeSaid.plugin === p.id) lists.push('<span class="config-result ' + revokeSaid.kind + '" data-revoke-said data-announce>' + esc(revokeSaid.text) + '</span>');
  if (caps.some(c => c === 'net.outbound' || c.startsWith('net.outbound:'))) lists.push('outbound hosts: ' + listed(g.hosts, 'none') + ' — granted with a key pasted or an account chosen on this card (the one host the package signs for), or set as ' + key('hosts') + ' in the config file');
  if (caps.includes('net.local')) lists.push('local network: ' + listed(g.local, 'none') + ' — a list, set as ' + key('local') + ' (an address, a range like 192.168.1.0/24, or a name on your own network, each with :port or :* for any); ' + (g.plaintext_credentials ? 'credentials may be sent in the clear to these devices' : 'a credential over plain http needs ' + key('plaintext_credentials') + ': true — anyone on your network could read it'));
  if ((p.settings || []).some(s => s.type === 'secret') || (g.handles && g.handles.length)) lists.push('credential handles: ' + listed(g.handles, 'none') + ' — granted with a key pasted or an account chosen on this card, or set as ' + key('credential_handles') + ' in the config file');
  if (!rows.length && !lists.length) return '';
  return '<div class="grants"><label class="f">WHAT YOU ALLOW</label>' + rows.join('') +
    lists.map(l => '<div class="store-hint grant-list">' + l + '</div>').join('') +
    (rows.length ? savebarHTML('grants:' + p.id, 'Applies from its next use.', { label: 'Save' }) : '') +
    '</div>';
}
function operationsHTML(p) {
  const ops = p.operations || [];
  if (!ops.length) return '';
  return '<div class="plugin-ops"><label class="f">OPERATIONS, IN THE AUTHOR\'S WORDS</label>' +
    '<div class="store-hint">Each summary below is what this plugin\'s author wrote, whole. The identity is given one quoted line of it, cut when it is long (Settings, Tools shows that line).</div>' +
    ops.map(o => '<div class="plugin-op"><b>' + esc(o.operation || o.tool) + '</b> <span class="store-hint">' + esc(o.tool) + '</span>' +
      (o.summary ? '<div class="pkg-desc author-words">' + esc(o.summary) + '</div>'
        : '<div class="store-hint">' + (o.published ? 'the plugin gave no summary for it' : 'the package declared no summary for it') + '</div>') +
      (o.published ? '<div class="store-hint op-published">Published by the plugin while it runs, not declared in its package: the summary is what the plugin gave then.</div>' : '') +
      (o.confirms ? '<div class="store-hint op-confirms">Each call waits for your confirmation here.</div>' : '') +
      (o.unconfirmed_by_design ? '<div class="store-hint op-unconfirmed">It writes or runs a command, and its calls do not wait for your confirmation. The plugin says why:</div><div class="pkg-desc author-words op-unconfirmed-why">' + esc(o.unconfirmed_by_design) + '</div>' : '') +
      '</div>').join('') +
    '</div>';
}
function detailHTML(p) {
  const row = (k, v) => v ? '<div class="sp-kv"><span>' + k + '</span><b>' + v + '</b></div>' : '';
  const publisher = p.publisher ? esc(p.publisher) + (p.publisher_id ? ' <span class="store-hint">(certified ' + esc(p.publisher_id) + ')</span>' : '') : '';
  const caps = p.capabilities && p.capabilities.length ? p.capabilities.map(esc).join(', ') : 'none';
  return '<div class="plugin-detail">' +
    row('Publisher', publisher) +
    row('Version', esc(p.version)) +
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

export function wireCard(st) {
  st.querySelectorAll('[data-act-decision]').forEach(btn => { btn.onclick = () => {
    const act = btn.dataset.actId, decision = btn.dataset.actDecision;
    const requestID = send({ type: 'plugin', plugin: { action: decision, id: btn.dataset.actPlugin, act } });
    if (!actWaits.sent(act, requestID)) { actSaid.set(act, NOT_SENT); renderPlugins(); return; }
    actPressed.set(act, decision); actSaid.delete(act);
    btn.closest('[data-act-card]').querySelectorAll('button').forEach(b => { b.disabled = true; });
    btn.textContent = deciding(decision);
    btn.closest('[data-act-card]').querySelector('[data-act-said]')?.remove();
  }; });
  st.querySelectorAll('[data-revoke-auto]').forEach(btn => { btn.onclick = () => {
    if (revoking.waiting()) return;
    const plugin = btn.dataset.revokeAuto, operation = btn.dataset.op;
    const requestID = send({ type: 'plugin', plugin: { action: 'revoke', id: plugin, act: operation } });
    revokeSaid = revoking.arm({ plugin, operation }, requestID) ? null
      : { plugin, kind: 'bad', text: 'Not connected — ' + operation + ' was not revoked.' };
    renderPlugins();
  }; });
  wireChoiceFilters(st);
}
