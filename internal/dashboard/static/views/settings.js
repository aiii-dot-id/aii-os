
import { S } from '../state.js';
import { $, esc } from '../util.js';
import { send, query } from '../ws.js';
import { sandboxCardHTML, wireSandboxCard } from '../sandbox.js';
import { pendingSlot } from '../pending.js';
import { providerModels } from './model-picker.js';

const SECTIONS = [
  ['substrate', 'Substrate'], ['providers', 'Providers'],
  ['dashboard', 'Dashboard'], ['witness', 'Witness'],
  ['prompt', 'Prompt'], ['agency', 'Agency'], ['logs', 'Logs'], ['sandbox', 'Sandbox'], ['tools', 'Tools'],
  ['updates', 'Updates']];
let sec = 'substrate';
let provOpen = null;
const prov = pendingSlot();
let provResult = null;
let provDraft = null;
const config = pendingSlot();
let configResult = null;

function cfgField(id, label, value, type) {
  return '<label class="f">' + esc(label) + '</label><input type="' + (type || 'text') + '" id="' + id + '" value="' + esc(value == null ? '' : value) + '">';
}
function navHTML() {
  return '<div class="card" style="display:flex;gap:6px;flex-wrap:wrap;padding:10px">' +
    SECTIONS.map(([id, label]) =>
      '<button class="btn' + (sec === id ? '' : ' ghost') + '" data-sec="' + id + '" style="padding:6px 14px;font-size:12.5px">' + label + '</button>').join('') +
    '</div>';
}

function modelField(id, providerName, current) {
  const p = S.providers.find(x => x.name === providerName) ||
            S.providers.find(x => x.default) || null;
  const models = providerModels(p);
  const placeholder = p && p.default_model ? 'Default: ' + p.default_model : 'Search or enter a model';
  return '<label class="f" id="' + id + '-label">MODEL' + (p ? ' — ' + esc(p.name) : '') + '</label>' +
    '<input type="text" id="' + id + '" list="' + id + '-list" value="' + esc(current || '') + '" placeholder="' + esc(placeholder) + '">' +
    (p ? '<button type="button" class="btn" data-refresh-models="' + esc(p.name) + '" style="margin-top:6px" title="Ask the provider for its model list now">Refresh models</button>' : '') +
    '<datalist id="' + id + '-list">' + models.map(m => '<option value="' + esc(m) + '"></option>').join('') + '</datalist>';
}

function substrateHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';

  const cur = c.llm.provider || '';
  const candidates = S.providers;
  const provSel = '<label class="f">PROVIDER (providers.json ENTRY; BLANK = DEFAULT-FLAGGED)</label>' +
    '<select id="cfg-provider"><option value=""' + (cur === '' ? ' selected' : '') + '>(default-flagged entry)</option>' +
    candidates.map(p => '<option value="' + esc(p.name) + '"' + (p.name === cur ? ' selected' : '') + '>' + esc(p.name) + '</option>').join('') +
    '</select>';
  const resolved = c.llm.error
    ? '<div style="font-size:11.5px;color:#c0392b;margin-top:8px">pointer does not resolve: ' + esc(c.llm.error) + '</div>'
    : '<div style="font-size:11.5px;color:var(--faint);margin-top:8px;line-height:1.6">resolved: <span style="font-family:var(--mono)">' + esc(c.llm.endpoint) + '</span>' +
      ' · key ' + esc(c.llm.api_key_masked || 'none') +
      ' · context ' + (c.llm.context_length || '—') +
      ' · max out ' + (c.llm.max_output_tokens || '—') +

      (c.llm.thinking_applies ? ' · thinking ' + (c.llm.thinking_budget || '—') : '') +
      ' · effort ' + esc(c.llm.effort_plan || 'provider default (none set)') +
      ' · active ' + esc((c.llm.resolved_provider || '—') + ' / ' + (c.llm.resolved_model || '—')) + '</div>';
  return '<div class="card"><h3>SUBSTRATE — LLM</h3>' +
    provSel +
    modelField('cfg-model', cur, c.llm.model) +
    cfgField('cfg-timeout', 'TIMEOUT (SECONDS)', c.llm.timeout_seconds) +
    resolved +
    '<div style="font-size:11.5px;color:var(--faint);margin-top:8px">providers.json owns the provider data; this card points at an entry. Endpoint, key, context/output budgets, REASONING EFFORT, and THINKING mode are edited on the entry in the Providers section. A substrate change applies only after the candidate completes a real inference request.</div>' +
    '<div class="savebar"><button class="btn" data-save="llm">Save</button><span class="savenote">applies live after inference check</span></div></div>';
}

function isLoopbackHost(h) {
  h = (h || '').trim();
  return h === '' || h === '127.0.0.1' || h === '::1' || h === 'localhost';
}
function dashboardWarnHTML(host, tls) {
  if (tls || isLoopbackHost(host)) return '';
  return '<div class="fb-hint" id="cfg-dwarn">WITHOUT HTTPS ON THIS ADDRESS, EVERY WORD BETWEEN YOU AND THIS IDENTITY CROSSES THE NETWORK IN THE CLEAR — AND THE BROWSER WILL REFUSE THE MICROPHONE.</div>';
}
function dashboardHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';
  const tls = !!c.dashboard.tls;
  return '<div class="card"><h3>DASHBOARD — BIND ADDRESS</h3>' +
    cfgField('cfg-dhost', 'HOST (127.0.0.1 = THIS MACHINE ONLY; A LAN IP OR 0.0.0.0 EXPOSES THE DASHBOARD TO THAT NETWORK)', c.dashboard.host) +
    cfgField('cfg-dport', 'PORT', c.dashboard.port) +
    '<label class="f" style="display:flex;gap:6px;align-items:center;margin-top:8px"><input type="checkbox" id="cfg-dtls"' + (tls ? ' checked' : '') + '> HTTPS (REQUIRED FOR THE MICROPHONE, AND FOR ANY ADDRESS OTHER THAN THIS MACHINE)</label>' +
    dashboardWarnHTML(c.dashboard.host, tls) +
    '<div class="savebar"><button class="btn" data-save="dashboard">Save</button><span class="savenote">saved — applies next restart</span></div></div>' +
    publicNameCardHTML(c.public_name || null) +
    themeCardHTML();
}

export function themeCardHTML() {
  const choice = (typeof S.themeChoice === 'function') ? S.themeChoice() : 'system';
  const opt = (v, label) => '<option value="' + v + '"' + (choice === v ? ' selected' : '') + '>' + label + '</option>';
  return '<div class="card"><h3>THEME</h3>' +
    '<label class="f" for="cfg-theme">APPEARANCE (THIS BROWSER ONLY — APPLIES AT ONCE)</label>' +
    '<select id="cfg-theme" data-theme-choice>' + opt('system', 'Follow this browser') + opt('light', 'Light') + opt('dark', 'Dark') + '</select>' +
    '<div class="muted" style="font-size:12px;margin-top:6px">A theme file (theme.json) still overrides either palette.</div></div>';
}
function witnessHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';
  return '<div class="card"><h3>WITNESS</h3>' +
    cfgField('cfg-wurl', 'URL', c.witness.url) +
    cfgField('cfg-wint', 'INTERVAL (EVENTS)', c.witness.interval_events) +
    '<div class="savebar"><button class="btn" data-save="witness">Save</button><span class="savenote">saved — applies next boot</span></div></div>';
}
function promptHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';
  return '<div class="card"><h3>PROMPT</h3>' +
    cfgField('cfg-ptokens', 'MAX TOKENS', c.prompt.max_tokens) +
    cfgField('cfg-pturns', 'RECENT TURNS', c.prompt.recent_turns) +
    '<div class="savebar"><button class="btn" data-save="prompt">Save</button><span class="savenote">saved — applies next boot</span></div></div>';
}
function agencyHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';
  const on = !!(c.agency && c.agency.prefer_local_for_roles);
    const nudges = !!(c.agency && c.agency.heuristic_nudges);
  return '<div class="card"><h3>AGENCY</h3>' +
    '<label class="f" style="display:flex;gap:6px;align-items:center;margin-top:4px"><input type="checkbox" id="cfg-hn"' + (nudges ? ' checked' : '') + '> COACHING PROMPTS DURING A TURN</label>' +
    '<div data-lifecycle="coaching" style="font-size:11.5px;margin-top:4px">Saved now; takes effect for the resident after restart &mdash; the running loop keeps the setting it started with. New sub-agent runs read it live.</div>' +
    '<div style="font-size:11.5px;color:var(--faint);margin-top:8px">Off by default. When on, the loop adds short written prompts to the identity mid-turn &mdash; asking it to record a plan, to say how many steps it expects, to fan work out to sub-agents, or noting a tool returned the same result several times. These shape behaviour with words. They are separate from the limits that always apply: turn and token budgets, the tool-call ceiling, declared truncation, and the checkpoint that continues long work in a fresh turn &mdash; those run either way.</div>' +
    '<label class="f" style="display:flex;gap:6px;align-items:center;margin-top:10px"><input type="checkbox" id="cfg-plr"' + (on ? ' checked' : '') + '> USE LOCAL MODEL FOR SUB-AGENT ROLES</label>' +
    '<div data-lifecycle="routing" style="font-size:11.5px;margin-top:4px">Applies live &mdash; the next role-tagged spawn honors it. Explicit agency.roles routes still win.</div>' +
    '<div style="font-size:11.5px;color:var(--faint);margin-top:8px">When checked and a provider entry marked <b>local</b> answers /models, role-tagged spawns (proposer, critic, judge…) run there — free, private evaluation on your own metal. Explicit agency.roles routes still win; untagged spawns are copies of the identity and always think with the configured model. If the local host is down, everything falls back to the configured model (logged).</div>' +
    '<div class="savebar"><button class="btn" data-save="agency">Save</button><span class="savenote">role routing applies live; coaching prompts after restart</span></div></div>';
}

function logsHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';
  const cfg = c.logs || {};
  let html = '<div class="card"><h3>LOGS</h3>' +
    cfgField('cfg-ldir', 'DIRECTORY (RELATIVE TO IDENTITY HOME; EMPTY = DISABLED)', cfg.dir) +
    cfgField('cfg-lbackups', 'MAX BACKUPS (-1 = KEEP ALL)', cfg.max_backups == null ? '' : cfg.max_backups) +
    cfgField('cfg-lcomp', 'COMPRESS AFTER DAYS (-1 = NEVER)', cfg.compress_days == null ? '' : cfg.compress_days) +
    '<div class="savebar"><button class="btn" data-save="logs">Save</button><span class="savenote">saved — applies next restart</span></div></div>';
  if (cfg.dir === '') {
    html += '<div class="card"><div class="empty">logging disabled — empty dir</div></div>';
    return html;
  }
  if (!S.logsList) {
    query('logs');
    html += '<div class="card"><div class="empty">loading log files…</div></div>';
    return html;
  }
  html += '<div class="card"><h3>VIEWER</h3>';
  if (!S.logsList.length) {
    html += '<div class="empty">no log files yet — the engine has not restarted since logging was enabled</div>';
  } else {
    html += S.logsList.map(f =>
      '<div class="tool-row" data-logfile="' + esc(f.name) + '" style="cursor:pointer;align-items:center">' +
      '<span class="tn" style="font-family:var(--mono);font-size:11.5px">' + esc(f.name) + '</span>' +
      '<span class="td" style="font-family:var(--mono);font-size:11.5px">' + esc(f.size) + (f.modified ? ' · ' + esc(f.modified) : '') + '</span></div>').join('');
    if (S.logTail) {
      html += '<div style="margin-top:10px"><label class="f">TAIL — ' + esc(S.logFile) + '</label>' +
        '<pre style="max-height:320px;overflow:auto;background:var(--bg2,#111);padding:10px;font-size:11px;line-height:1.5;border:1px solid var(--line)">' + esc(S.logTail.lines) + '</pre></div>';
    }
  }
  html += '</div>';
  return html;
}
function toolsHTML() {
  return '<div class="card"><h3>TOOLS — RING 5 REACH</h3>' +
    (S.tools.length ? S.tools.map(t =>
      '<div class="tool-row"><span class="tn">' + esc(t.name) + '</span><span class="td">' + esc(t.description) + '</span>' +
      '<label class="switch"><input type="checkbox" data-tool="' + esc(t.name) + '"' + (t.enabled ? ' checked' : '') + '><span class="tk"></span></label></div>').join('') :
      '<div class="empty">no tools</div>') + '</div>';
}

const STATUS_DOT = {
  ok: ['#3fa34d', 'reachable — models listed live'],
  auth_required: ['#c9a227', 'needs an API key to list models'],
  no_credential: ['#c9a227', 'adopted credential unavailable'],
  credential_expired: ['#c9a227', 'adopted credential expired — refresh it with its own tool'],
  unreachable: ['#c0392b', 'endpoint did not answer /models'],
  invalid_url: ['#c0392b', 'URL is not valid'],
};
function dot(status) {
  const d = STATUS_DOT[status] || ['#777', 'not probed yet'];
  return '<span title="' + esc(d[1]) + '" style="display:inline-block;width:9px;height:9px;border-radius:50%;background:' + d[0] + ';margin-right:8px;flex:none"></span>';
}

function provRowNote(p) {
  const bits = [];
  if (p.preselect && p.preselect_why) bits.push(esc(p.preselect_why));
  const ci = p.credential_info;
  if (ci && !ci.error) {
    const s = [];
    if (ci.plan) s.push('plan ' + esc(ci.plan));
    if (ci.expires_at) s.push((ci.expired ? 'EXPIRED ' : 'usable to ') + esc(ci.expires_at.slice(0, 16).replace('T', ' ')) + ' UTC');
    if (s.length) bits.push(esc(ci.kind) + ' · ' + s.join(' · '));
  }
  if (p.status !== 'ok' && p.status_reason) bits.push(esc(p.status_reason));
  else if (ci && ci.error) bits.push(esc(ci.error));
  if (!bits.length) return '';
  return '<div style="font-size:11px;color:var(--faint);margin:-6px 0 6px 22px">' + bits.join(' — ') + '</div>';
}

function providerEditor(p, tag) {

  const storedKey = !!(p && p.has_key);
  let keepKey = storedKey;
  if (provDraft && (provDraft.name === (p && p.name) || tag === 'new')) {
    keepKey = !!provDraft.has_key;
    p = Object.assign({}, p || {}, provDraft, { has_key: storedKey });
  }
  p = p || {};
  const apiType = p.api_type || 'openai';
  const nameField = tag === 'new'
    ? cfgField('pv-name-' + tag, 'NAME', '')
    : '<label class="f">NAME</label><input type="text" id="pv-name-' + tag + '" value="' + esc(p.name || '') + '" readonly>';
  return '<div style="padding:10px 0 4px">' +
    nameField +
    '<label class="f">API TYPE (DIALECT)</label><select id="pv-type-' + tag + '">' +
    ['openai', 'anthropic'].map(t => '<option value="' + t + '"' + (t === apiType ? ' selected' : '') + '>' + t + '</option>').join('') +
    '</select>' +
    cfgField('pv-url-' + tag, 'ENDPOINT URL', p.endpoint || '') +
    cfgField('pv-model-' + tag, 'DEFAULT MODEL', p.default_model || '') +

    cfgField('pv-key-' + tag, 'API KEY (ENTER TO REPLACE' + (storedKey ? ', ONE STORED' : ', NONE STORED') + ')', p.api_key || '', 'password') +
    (storedKey ? '<label class="f" style="display:flex;gap:6px;align-items:center"><input type="checkbox" id="pv-keepkey-' + tag + '"' + (keepKey ? ' checked' : '') + '> KEEP STORED KEY WHEN BLANK</label>' : '') +
    credentialField('pv-cred-' + tag, p.credential || '', p.credential_info) +
    '<details style="margin-top:10px"><summary style="cursor:pointer;color:var(--dim);font-size:12px">Advanced serving settings</summary>' +
    cfgField('pv-models-' + tag, 'STATIC MODEL FALLBACK (COMMA-SEPARATED; BLANK = NONE)', (p.configured_models || []).join(', ')) +
    cfgField('pv-ctx-' + tag, 'CONTEXT LENGTH (TOKENS)', p.context_length || '') +
    cfgField('pv-maxout-' + tag, 'MAX OUTPUT TOKENS', p.max_output_tokens || '') +
    effortField('pv-effort-' + tag, p.reasoning_effort || '', p.effort_levels, p.effort_model) +
    thinkingField(tag, p) +
    cacheFields(tag, p) +
    cfgField('pv-temp-' + tag, 'TEMPERATURE (BLANK = SERVER DEFAULT; 0 IS VALID)', p.temperature == null ? '' : p.temperature) +
    cfgField('pv-topp-' + tag, 'TOP_P (BLANK = SERVER DEFAULT)', p.top_p == null ? '' : p.top_p) +
    '</details>' +
    '<label class="f" style="display:flex;gap:6px;align-items:center;margin-top:6px"><input type="checkbox" id="pv-def-' + tag + '"' + (p.default ? ' checked' : '') + '> DEFAULT PROVIDER</label>' +
    '<div class="savebar"><button class="btn" data-prov-commit="' + tag + '">Save</button> ' +
    (tag !== 'new' ? '<button class="btn ghost" data-prov-del="' + esc(p.name) + '">Remove</button> ' : '') +
    '<button class="btn ghost" data-prov-cancel="1">Cancel</button></div>' +
    (p.can_sign_in ? signInRow(p, tag) : '') +
    '</div>';
}

let signInWin = null;
function signInRow(p, tag) {
  return '<div class="signin" style="margin-top:8px;padding-top:8px;border-top:1px solid var(--rule,#334)">' +
    '<button class="btn" data-prov-signin="' + esc(p.name) + '">Sign in with ' + esc(p.name.replace(/\s*\(.*\)\s*$/, '')) + '</button> ' +
    '<span class="muted" style="font-size:12px">opens the provider\'s sign-in in a new tab</span>' +
    '<div style="display:flex;gap:6px;margin-top:6px"><input id="pv-signin-' + tag + '" placeholder="If the tab could not reach this machine, paste the redirect URL here" style="flex:1">' +
    '<button class="btn ghost" data-prov-signin-complete="' + esc(p.name) + '" data-tag="' + tag + '">Complete</button></div></div>';
}
function navigateSignIn(url) {
  if (signInWin && !signInWin.closed) { signInWin.location = url; } else { window.open(url, '_blank', 'noopener'); }
  signInWin = null;
}
function abandonSignInWindow() { if (signInWin && !signInWin.closed) signInWin.close(); signInWin = null; }

S.signInNavigate = navigateSignIn;
S.signInAbandon = abandonSignInWindow;

export function wireProviderSignIn(root) {
  root.querySelectorAll('[data-prov-signin]').forEach(btn => { btn.onclick = () => {
    signInWin = window.open('', '_blank');
    send({ type: 'provider_signin', provider: btn.dataset.provSignin });
  }; });
}

export function updateCardHTML(u) {
  let state;
  const managed = !!(u && u.stage_refusal);
  if (!u || !u.enabled) state = 'Unavailable — this build carries no release version, so it does not check for updates.';
  else if (u.checking) state = 'Checking…';
  else if (u.error) state = 'Last check failed: ' + esc(u.error);
  else if (u.needs_restart) state = 'Update ' + esc(u.installed_version) + ' is installed — relaunch to run it.';
  else if (u.available_version && managed) state = 'Update available: ' + esc(u.available_version) + ' — this install is managed by your package manager. Download <code>aii-os_' + esc(u.available_version) + '_amd64.deb</code>' + (u.release_url ? ' from <a href="' + esc(u.release_url) + '" target="_blank" rel="noopener">the release</a>' : '') + ' and run <code>sudo dpkg -i aii-os_' + esc(u.available_version) + '_amd64.deb</code>; the identity restarts on the new build.';
  else if (u.available_version) state = 'Update available: ' + esc(u.available_version) + (u.automatic ? ' — it is downloaded, verified and installed on the next check.' : ' — automatic apply is off; turn it on and check again to install it.');
  else if (u.checked_at) state = 'Up to date (checked ' + esc(u.checked_at) + ').';
  else state = 'Not checked yet.';
  const canCheck = !!(u && u.enabled && !u.checking);
  const auto = !!(u && u.automatic);
  return '<div class="card"><h3>UPDATES</h3>' +
    '<div class="sp-kv"><span>running</span><b>' + esc((u && u.current_version) || 'dev') + '</b></div>' +
    '<div data-update-state style="margin-top:6px">' + state + '</div>' +
    '<label class="f" style="margin-top:8px"><input type="checkbox" id="cfg-uauto"' + (auto ? ' checked' : '') + '> Install updates automatically — a signed release is downloaded, verified and installed on a check; you choose when to relaunch</label>' +
    '<div class="muted" style="font-size:12px;margin-top:6px">Checks run once an hour.' +
    (managed ? ' This install is managed by your package manager (' + esc(u.stage_refusal) + '): updates are reported here and installed with it.' : '') +
    ' Mobile builds only report.</div>' +
    '<div class="savebar"><button class="btn" data-save="updates">Save</button>' +
    '<button class="btn ghost" data-update-check' + (canCheck ? '' : ' disabled') + '>Check now</button>' +
    (u && u.needs_restart ? '<button class="btn" data-update-restart>Relaunch now</button>' : '') +
    '</div></div>';
}
export function wireThemeChoice(root) {
  root.querySelectorAll('[data-theme-choice]').forEach(sel => { sel.onchange = () => { if (typeof S.setThemeChoice === 'function') S.setThemeChoice(sel.value); }; });
}

export function publicNameCardHTML(p) {
  let state, control = '';
  if (!p || p.status === 'none') {
    state = 'Reachable by address only. Claim a public name to reach this identity as https://… from any browser, with the microphone.' +
      (p && !p.tls ? ' Turn on HTTPS above first.' : '');
    control = '<button class="btn" data-public-name="claim"' + (p && p.can_claim ? '' : ' disabled') + '>Claim a public name</button>';
  } else if (p.status === 'claiming') {
    state = '<b>' + esc(p.name) + '</b> — obtaining the certificate…';
  } else if (p.status === 'issued') {
    state = '<b>' + esc(p.name) + '</b> — certificate issued' + (p.not_after ? ', expires ' + esc(p.not_after) : '') +
      (p.renew_at ? ', renews around ' + esc(p.renew_at) : '') + '. Bookmark <b>' + esc(p.origin) + '</b>.';
    if (p.relay) state += p.relay_connected ? ' Reachable from anywhere through ' + esc(p.relay) + '.' : ' The relay ' + esc(p.relay) + ' is not carrying it' + (p.relay_error ? ': ' + esc(p.relay_error) : '') + '.';
    if (p.can_move) {
      state += ' The certificate service now serves <b>' + esc(p.service_zone) + '</b>; this name is under ' + esc(p.zone) + '.';
      control = '<button class="btn" data-public-name="move">Move to ' + esc(p.service_zone) + '</button>';
    }
  } else {
    state = '<b>' + esc(p.name) + '</b> — the certificate could not be obtained: ' + esc(p.last_error || 'unknown') +
      (p.days_left ? ' (' + Math.floor(p.days_left) + ' days left on the current one)' : '');
    control = '<button class="btn" data-public-name="retry">Retry now</button>';
  }
  return '<div class="card"><h3>PUBLIC NAME</h3><div data-public-name-state style="margin-top:6px">' + state + '</div>' +
    '<div class="muted" style="font-size:12px;margin-top:6px">A public name is claimed once and never changes; the certificate for it renews itself. Changing the bind address above does not touch it.</div>' +
    (control ? '<div class="savebar">' + control + '</div>' : '') + '</div>';
}
export function wirePublicName(root) {
  root.querySelectorAll('[data-public-name]').forEach(btn => { btn.onclick = () => {
    btn.disabled = true;
    const kind = btn.dataset.publicName;
    send({ type: kind === 'retry' ? 'public_name_retry' : kind === 'move' ? 'public_name_move' : 'public_name_claim' });
  }; });
}
S.renderPublicName = (p) => {
  if (S.config) S.config.public_name = p;
  if (S.view === 'settings' && sec === 'dashboard') renderSettings();
  if (p && p.status === 'claiming' && !S.publicNamePoll) {
    S.publicNamePoll = setTimeout(() => { S.publicNamePoll = null; send({ type: 'public_name_state' }); }, 3000);
  }
};
export function wireUpdateCheck(root) {
  root.querySelectorAll('[data-update-check]').forEach(btn => { btn.onclick = () => { btn.disabled = true; send({ type: 'update_check' }); }; });
  root.querySelectorAll('[data-update-restart]').forEach(btn => { btn.onclick = () => { btn.disabled = true; btn.textContent = 'Relaunching\u2026'; send({ type: 'restart' }); }; });
}
S.renderUpdate = () => { if (S.view === 'settings' && sec === 'updates') renderSettings(); };
S.openUpdates = () => { sec = 'updates'; provOpen = null; renderSettings(); };

function effortField(id, cur, levels, levelsModel) {

  const opts = [['', '(provider default — omit)']].concat(
    (levels || []).map((l) => [l, l]));
  if (cur && !opts.some(o => o[0] === cur)) {
    opts.push([cur, cur + (levels && levels.length ? ' — not accepted by this provider' : ' (unverified)')]);
  }

  const forModel = levelsModel ? ' (' + esc(levelsModel) + ')' : '';
  return '<label class="f">REASONING EFFORT' + forModel + '</label><select id="' + id + '">' +
    opts.map(o => '<option value="' + esc(o[0]) + '"' + (o[0] === cur ? ' selected' : '') + '>' + esc(o[1]) + '</option>').join('') +
    '</select>';
}

function selectField(id, label, cur, opts) {
  return '<label class="f">' + esc(label) + '</label><select id="' + id + '">' +
    opts.map(o => '<option value="' + esc(o[0]) + '"' + (o[0] === cur ? ' selected' : '') + '>' + esc(o[1]) + '</option>').join('') +
    '</select>';
}

function thinkingField(tag, p) {
  const id = 'pv-think-' + tag;
  const anthropic = (p.api_type || 'openai') === 'anthropic';

  let show = '';
  if (p.summary_field) {
    const opts = [['', 'omitted — reasoning happens, is not returned']].concat(
      (p.summary_levels || []).map((v) => [v, v + ' — return readable reasoning']));
    show = selectField('pv-tdisp-' + tag, 'SHOW REASONING (' + esc(p.summary_field) + ')',
      p.thinking_display || '', opts);
  } else {
    show = '<input type="hidden" id="pv-tdisp-' + tag + '" value="' + esc(p.thinking_display || '') + '">' +
      '<div style="font-size:11px;color:var(--faint);margin:-2px 0 8px">This dialect has no readable-reasoning parameter.</div>';
  }

  if (!anthropic) {
    return show +
      '<input type="hidden" id="' + id + '" value="' + esc(p.thinking_budget || '') + '">' +
      '<input type="hidden" id="pv-tmode-' + tag + '" value="' + esc(p.thinking_mode || '') + '">';
  }
  return show +
    selectField('pv-tmode-' + tag, 'THINKING SHAPE (ANTHROPIC)', p.thinking_mode || '', [
      ['', 'adaptive — current models (default)'],
      ['budget', 'budget tokens — pre-4.6 models only'],
      ['off', 'off — send no thinking'],
    ]) +
    cfgField(id, 'THINKING BUDGET (TOKENS — "budget" SHAPE ONLY)', p.thinking_budget || '');
}

function cacheFields(tag, p) {
  const cache = p.cache || {};
  const modes = p.cache_modes || ['auto'];
  const labels = { auto: 'Automatic', explicit: 'Stable prefix only', off: 'Off' };
  const modeOptions = [['', 'Provider default']].concat(modes.map(m => [m, labels[m] || m]));
  if (cache.mode && !modeOptions.some(o => o[0] === cache.mode)) modeOptions.push([cache.mode, cache.mode + ' (not supported by this model)']);
  const ttls = [['', 'Provider default']].concat((p.cache_ttls || []).map(v => [v, v]));
  if (cache.ttl && !ttls.some(o => o[0] === cache.ttl)) ttls.push([cache.ttl, cache.ttl + ' (verify model support)']);
  let html = '<div class="muted" style="margin-top:12px">Prompt caching reduces repeated input work. Cache writes may cost more than ordinary input; choose retention for the gaps between your requests.</div>' +
    selectField('pv-cache-mode-' + tag, 'PROMPT CACHING', cache.mode || '', modeOptions) +
    selectField('pv-cache-ttl-' + tag, 'CACHE RETENTION', cache.ttl || '', ttls);
  if (p.cache_diagnostics) {
    html += selectField('pv-cache-tail-' + tag, 'CONVERSATION CACHE RETENTION', cache.tail_ttl || '', [['', 'Same as stable prefix'], ['5m', '5 minutes'], ['1h', '1 hour (stable prefix must also use 1 hour)']]) +
      '<label class="f"><input type="checkbox" id="pv-cache-diag-' + tag + '"' + (cache.diagnostics ? ' checked' : '') + '> CACHE DIAGNOSTICS IN LOGS</label>';
  }
  if (p.cache_key_supported) html += cfgField('pv-cache-key-' + tag, 'CACHE ROUTING KEY (BLANK = AUTOMATIC)', cache.key || '');
  return html;
}

function credentialField(id, cur, info) {
  const LABEL = {
    'claude-code': 'Claude Max/Pro — adopt ~/.claude/.credentials.json',
    'codex': 'ChatGPT Plus/Pro — adopt ~/.codex/auth.json',
  };

  const kinds = (S.config && S.config.credential_kinds) || [];
  if (!kinds.length) {
    return '<label class="f">CREDENTIAL</label>' +
      '<div style="font-size:11.5px;color:var(--faint);margin-bottom:8px">Adopted credentials are a desktop feature &mdash; the tools that hold them do not run here, and apps cannot read each other\'s files. Use an API key.</div>' +

      '<input type="hidden" id="' + id + '" value="none">';
  }
  const opts = [['none', 'API key (above)']].concat(kinds.map(k => [k, LABEL[k] || k]));

  if (cur && cur !== 'none' && !opts.some(o => o[0] === cur)) {
    opts.push([cur, cur]);
  }
  const sel = cur || 'none';
  return '<label class="f">CREDENTIAL</label><select id="' + id + '">' +
    opts.map(o => '<option value="' + o[0] + '"' + (o[0] === sel ? ' selected' : '') + '>' + esc(o[1]) + '</option>').join('') +
    '</select>' + credentialState(info) +
    '<div style="font-size:11px;color:var(--faint);margin:-4px 0 8px">An adopted credential supplies its own endpoint and dialect, and replaces the key entirely.</div>';
}

function credentialState(info) {
  if (!info) return '';
  if (info.error) {
    return '<div style="font-size:11.5px;color:var(--warn,#c66);margin:2px 0 6px">' + esc(info.error) + '</div>';
  }
  const bits = [];
  if (info.is_api_key) bits.push('holds an API key, not a token');
  if (info.plan) bits.push('plan ' + esc(info.plan));
  if (info.tier) bits.push(esc(info.tier));
  if (info.expires_at) bits.push((info.expired ? 'EXPIRED ' : 'usable to ') + esc(info.expires_at.replace('T', ' ').replace('Z', ' UTC')));
  if (info.path) bits.push('from ' + esc(info.path));
  if (!bits.length) return '';
  return '<div style="font-size:11.5px;color:var(--faint);margin:2px 0 6px">' + bits.join(' &middot; ') + '</div>';
}
function providersHTML() {
  let html = '<div class="card"><h3>PROVIDERS — providers.json</h3>' +
    '<div style="font-size:11.5px;color:var(--faint);margin-bottom:8px;line-height:1.5">The operator\'s file, beside config.json — edited here or by hand. Status describes live model discovery and is never stored; activation is decided by a real inference check.</div>';
  if (prov.waiting()) html += '<div class="config-result">Saving ' + esc(prov.waiting().name) + '… waiting for the runtime to confirm.</div>';
  else if (provResult) html += '<div class="config-result ' + provResult.kind + '">' + esc(provResult.text) + '</div>';
  if (S.providers.length) {
    html += S.providers.map((p, i) => {
      let row = '<div class="tool-row" data-prov-row="' + i + '" style="cursor:pointer;align-items:center">' +
        dot(p.status) +
        '<span class="tn">' + esc(p.name) + (p.default ? ' ✓' : '') + '</span>' +
        '<span class="td" style="font-family:var(--mono);font-size:11.5px">' + esc(p.endpoint) +
        (p.default_model ? ' · ' + esc(p.default_model) : '') +
        ' · ' + (p.models || []).length + ' models</span></div>' + provRowNote(p);
      if (provOpen === i) row += providerEditor(p, String(i));
      return row;
    }).join('');
  } else {
    html += '<div class="empty">no providers — add one below</div>';
  }
  html += provOpen === 'new'
    ? providerEditor(null, 'new')
    : '<div class="savebar" style="margin-top:10px"><button class="btn" id="pv-open-new">Add provider</button></div>';
  html += '</div>';
  return html;
}
function commitProvider(tag) {
  const base = (tag !== 'new' && S.providers[parseInt(tag, 10)]) || {};

  const fnum = (id) => {
    const raw = $(id).value.trim();
    if (raw === '') return undefined;
    const n = parseFloat(raw);
    return isNaN(n) ? undefined : n;
  };
  const keepKey = $('pv-keepkey-' + tag);
  const entry = {
    name: $('pv-name-' + tag).value.trim(),
    api_type: $('pv-type-' + tag).value,
    endpoint: $('pv-url-' + tag).value.trim(),
    default_model: $('pv-model-' + tag).value.trim(),
    api_key: $('pv-key-' + tag).value.trim(),
    has_key: !!(keepKey && keepKey.checked),
    api_key_env: base.api_key_env || '',
    credential: $('pv-cred-' + tag).value,
    subscribe_url: base.subscribe_url || '',
    configured_models: $('pv-models-' + tag).value.split(',').map(s => s.trim()).filter(Boolean),
    context_length: parseInt($('pv-ctx-' + tag).value, 10) || 0,
    max_output_tokens: parseInt($('pv-maxout-' + tag).value, 10) || 0,
    reasoning_effort: $('pv-effort-' + tag).value.trim(),
    thinking_budget: parseInt($('pv-think-' + tag).value, 10) || 0,
    thinking_mode: $('pv-tmode-' + tag).value.trim(),
    thinking_display: $('pv-tdisp-' + tag).value.trim(),
    extra: base.extra,
    cache: {
      mode: $('pv-cache-mode-' + tag)?.value || '',
      ttl: $('pv-cache-ttl-' + tag)?.value || '',
      tail_ttl: $('pv-cache-tail-' + tag)?.value || '',
      key: $('pv-cache-key-' + tag)?.value || '',
      diagnostics: !!$('pv-cache-diag-' + tag)?.checked,
    },
    default: $('pv-def-' + tag).checked,
  };
  const temp = fnum('pv-temp-' + tag);
  if (temp !== undefined) entry.temperature = temp;
  const topp = fnum('pv-topp-' + tag);
  if (topp !== undefined) entry.top_p = topp;

  provResult = null;
  provDraft = null;
  const requestID = send({ type: 'provider_set', entry });
  if (!prov.arm({ name: entry.name, sentKey: entry.api_key !== '', entry: entry }, requestID)) {
    provDraft = entry;
    provResult = { kind: 'bad', text: 'Not connected — provider was not sent.' };
  }
  renderSettings();
}

export function acceptProviderSave(requestID) {

  const pending = prov.claim(requestID);
  if (!pending) return false;
  const got = S.providers.find(p => p.name === pending.name);
  const expectedKey = pending.sentKey || pending.entry.has_key;
  if (!got || !!got.has_key !== expectedKey) {
    provDraft = pending.entry;
    provResult = { kind: 'bad', text: 'Acknowledged, but ' + pending.name +
      (got ? ' came back with a different stored-key state' : ' is not in the registry that came back') +
      ' — the save did not take.' };
    return true;
  }
  provResult = { kind: 'good', text: 'Saved — ' + pending.name +
    (pending.sentKey ? ', key stored.' : (expectedKey ? '.' : ', no stored key.')) };
  provDraft = null;
  provOpen = null;
  return true;
}

export function rejectProviderSave(message, requestID) {
  const pending = prov.claim(requestID);
  if (!pending) return false;
  provDraft = pending.entry;
  provResult = { kind: 'bad', text: message };
  renderSettings();
  return true;
}

export function renderSettings() {
  if (!S.providersLoaded) query('providers');
  const st = $('settings-stack');
  const c = S.config;
  let html = navHTML();
  html += configFeedbackHTML();
  if (sec === 'substrate') html += substrateHTML(c);
  else if (sec === 'providers') html += providersHTML();
  else if (sec === 'dashboard') html += dashboardHTML(c);
  else if (sec === 'witness') html += witnessHTML(c);
  else if (sec === 'prompt') html += promptHTML(c);
  else if (sec === 'agency') html += agencyHTML(c);
  else if (sec === 'logs') html += logsHTML(c);
  else if (sec === 'updates') html += updateCardHTML(S.update || null);
  else if (sec === 'sandbox') html += sandboxCardHTML() || '<div class="card"><div class="empty">loading sandbox…</div></div>';
  else if (sec === 'tools') html += toolsHTML();
  st.innerHTML = html;

  st.querySelectorAll('[data-sec]').forEach(btn => {
    btn.onclick = () => { sec = btn.dataset.sec; provOpen = null; renderSettings(); };
  });
  st.querySelectorAll('[data-save]').forEach(btn => { btn.onclick = () => { saveSettings(btn.dataset.save); renderSettings(); }; });
  wirePublicName(st);

  const dHost = $('cfg-dhost'), dTLS = $('cfg-dtls');
  if (dHost && dTLS) {
    const sync = () => {
      const old = $('cfg-dwarn');
      if (old) old.remove();
      const html = dashboardWarnHTML(dHost.value, dTLS.checked);
      if (html) dTLS.closest('label').insertAdjacentHTML('afterend', html);
    };
    dHost.oninput = sync;
    dTLS.onchange = sync;
  }
  st.querySelectorAll('[data-prov-row]').forEach(row => {
    row.onclick = (e) => {
      if (e.target.closest('input,button,label')) return;
      const i = parseInt(row.dataset.provRow, 10);
      provOpen = provOpen === i ? null : i;
      renderSettings();
    };
  });
  st.querySelectorAll('[data-prov-commit]').forEach(btn => { btn.onclick = () => commitProvider(btn.dataset.provCommit); });
  st.querySelectorAll('[data-prov-del]').forEach(btn => { btn.onclick = () => { provOpen = null; send({ type: 'provider_delete', provider: btn.dataset.provDel }); }; });
  wireProviderSignIn(st);
  st.querySelectorAll('[data-prov-signin-complete]').forEach(btn => { btn.onclick = () => {
    const input = document.getElementById('pv-signin-' + btn.dataset.tag);
    const v = input ? input.value.trim() : '';
    if (!v) return;
    send({ type: 'provider_signin_complete', provider: btn.dataset.provSigninComplete, input: v });
    input.value = '';
  }; });
  st.querySelectorAll('[data-prov-cancel]').forEach(btn => { btn.onclick = () => { provOpen = null; renderSettings(); }; });
  wireUpdateCheck(st);
  wireThemeChoice(st);
  st.querySelectorAll('[data-logfile]').forEach(row => {
    row.onclick = () => {
      S.logFile = row.dataset.logfile;
      query('logs', { name: S.logFile });
    };
  });
  const openNew = $('pv-open-new');
  if (openNew) openNew.onclick = () => { provOpen = 'new'; renderSettings(); };

  const provEl = $('cfg-provider');
  if (provEl) {
    provEl.onchange = () => {
      const p = S.providers.find(x => x.name === provEl.value) || S.providers.find(x => x.default) || null;
      const model = $('cfg-model'), list = $('cfg-model-list'), label = $('cfg-model-label');
      if (!model || !list || !label) return;
      model.value = '';
      model.placeholder = p && p.default_model ? 'Default: ' + p.default_model : 'Search or enter a model';
      list.innerHTML = providerModels(p).map(m => '<option value="' + esc(m) + '"></option>').join('');
      label.textContent = 'MODEL' + (p ? ' — ' + p.name : '');
    };
  }
  if (sec === 'sandbox') wireSandboxCard(st);
  st.querySelectorAll('[data-tool]').forEach(sw => {
    sw.onchange = () => send({ type: 'tool_toggle', tool: sw.dataset.tool, enabled: sw.checked });
  });
}
function num(v) { const n = parseInt(v, 10); return isNaN(n) ? 0 : n; }
function saveSettings(section) {
  const ch = {};
  if (section === 'llm') {

    ch['llm.provider'] = $('cfg-provider').value;
    ch['llm.model'] = $('cfg-model').value.trim();
    const tmo = num($('cfg-timeout').value); if (tmo > 0) ch['llm.timeout_seconds'] = tmo;
  } else if (section === 'plugins') {
    ch['plugins.autoload'] = $('cfg-plevel').value;
  } else if (section === 'catalog') {
    ch['plugins.catalog_url'] = $('cfg-caturl').value.trim();
  } else if (section === 'plugin_runtime') {
    const mib = id => num($(id).value) * 1048576;
    ch['plugins.runtime.max_installed_bytes'] = mib('cfg-rt-installed');
    ch['plugins.runtime.max_files'] = num($('cfg-rt-files').value);
    ch['plugins.runtime.max_file_bytes'] = mib('cfg-rt-file');
    ch['plugins.runtime.max_compressed_bytes'] = mib('cfg-rt-archive');
    ch['plugins.runtime.max_depth'] = num($('cfg-rt-depth').value);
    ch['plugins.runtime.roots_kept'] = num($('cfg-rt-kept').value);
  } else if (section === 'updates') {
    ch['updates.automatic'] = !!$('cfg-uauto').checked;
  } else if (section.startsWith('grants:')) {
    // One plugin's boolean grants (plugins.js grantsHTML): every box is
    // sent, so an unchecked one withdraws.
    const id = section.slice('grants:'.length);
    document.querySelectorAll('[data-grant-plugin="' + id.replace(/"/g, '\\"') + '"]').forEach(el => {
      ch['plugins.grants.' + id + '.' + el.dataset.grantField] = !!el.checked;
    });
  } else if (section.startsWith('plugin:')) {

    const id = section.slice('plugin:'.length);
    document.querySelectorAll('[data-pset-plugin="' + id.replace(/"/g, '\\"') + '"]').forEach(el => {
      const key = 'plugins.settings.' + id + '.' + el.dataset.psetKey;
      const type = el.dataset.psetType;
      // A forget box sends a clear ONLY when it is ticked: an untouched
      // orphan is left alone, never discarded by saving the card.
      if (type === 'forget') { if (el.checked) ch[key] = null; }
      else if (type === 'boolean') ch[key] = !!el.checked;
      else if (type === 'number' || type === 'integer') {
        // An integer is sent as the number typed, whole or not: the
        // host holds it to the declaration and names a fraction by
        // key, the same refusal a value outside its bounds gets.
        const n = el.value.trim() === '' ? NaN : Number(el.value); ch[key] = isNaN(n) ? null : n;
      }
      else ch[key] = el.value === '' ? null : el.value;
    });
  } else if (section === 'dashboard') {
    ch['dashboard.host'] = $('cfg-dhost').value.trim();
    ch['dashboard.port'] = num($('cfg-dport').value);
    ch['dashboard.tls'] = !!$('cfg-dtls').checked;
  } else if (section === 'prompt') {
    ch['prompt.max_tokens'] = num($('cfg-ptokens').value);
    ch['prompt.recent_turns'] = num($('cfg-pturns').value);
  } else if (section === 'witness') {
    ch['witness.url'] = $('cfg-wurl').value.trim();
    ch['witness.interval_events'] = num($('cfg-wint').value);
  } else if (section === 'agency') {
    ch['agency.prefer_local_for_roles'] = !!$('cfg-plr').checked;
    ch['agency.heuristic_nudges'] = !!$('cfg-hn').checked;
  } else if (section === 'logs') {
    ch['logs.dir'] = $('cfg-ldir').value.trim();
    ch['logs.max_backups'] = num($('cfg-lbackups').value);
    ch['logs.compress_days'] = num($('cfg-lcomp').value);
  }
  sendConfigChanges(section, ch);
}

// sendConfigChanges sends one config_set and arms the pending slot, so
// the reply lands on the section that asked — the page's own saves and
// the plugins page's revoke of a standing confirmation use the same path.
export function sendConfigChanges(section, ch) {
  configResult = null;
  const requestID = send({ type: 'config_set', config: ch });
  if (!config.arm({ section: section, changes: ch }, requestID)) {
    configResult = { kind: 'bad', text: 'Not connected — configuration was not sent.' };
  }
}

export function saveConfigSection(section) { saveSettings(section); }
export function configFeedbackHTML() {
  if (config.waiting()) return '<div class="config-result">Checking and saving… the current configuration remains active.</div>';
  if (configResult) return '<div class="config-result ' + configResult.kind + '">' + esc(configResult.text) + '</div>';
  return '';
}

export function settingsConnectionLost() {
  let changed = false;
  const lostProv = prov.drop();
  if (lostProv) {
    provDraft = lostProv.entry;
    provResult = { kind: 'bad', text: 'Connection lost before provider confirmation — check its current state after reconnect.' };
    changed = true;
  }
  if (config.drop()) {
    configResult = { kind: 'bad', text: 'Connection lost before configuration confirmation — check current settings after reconnect.' };
    changed = true;
  }
  if (changed) renderSettings();
  return changed;
}

function configValue(state, path) {
  const parts = path.split('.');
  let v = state;
  for (const p of parts) v = v == null ? undefined : v[p];
  return v;
}

// A plugin's settings and grants come back on its card, not at the
// dotted path they were sent under (the plugin id itself has dots):
// plugins.settings.<id>.<key> is installed[id].settings[key].value and
// plugins.grants.<id>.<field> is installed[id].grants[field]. The
// read-back looks where the state puts them (found live:
// every plugin-settings save said "did not come back as saved" while
// the card showed the saved values).
function savedValue(state, path) {
  const plugins = (state && state.plugins && state.plugins.installed) || [];
  const under = prefix => {
    if (!path.startsWith(prefix)) return null;
    const rest = path.slice(prefix.length), dot = rest.lastIndexOf('.');
    if (dot <= 0) return null;
    return { plugin: plugins.find(x => x.id === rest.slice(0, dot)), key: rest.slice(dot + 1) };
  };
  const s = under('plugins.settings.');
  if (s) { const d = s.plugin && (s.plugin.settings || []).find(x => x.key === s.key); return d ? d.value : undefined; }
  const g = under('plugins.grants.');
  if (g) return g.plugin && g.plugin.grants ? g.plugin.grants[g.key] : undefined;
  return configValue(state, path);
}
// A cleared value comes back absent: null sent and nothing saved agree.
function sameSaved(saved, sent) {
  if (saved === sent) return true;
  return (saved === null || saved === undefined) && (sent === null || sent === undefined);
}

export function acceptSettingsConfig(requestID) {

  const pending = config.claim(requestID);
  if (!pending) return false;
  const missed = Object.keys(pending.changes).filter(p => !sameSaved(savedValue(S.config, p), pending.changes[p]));
  if (missed.length) {
    configResult = { kind: 'bad', text: 'Acknowledged, but ' + missed.join(', ') +
      ' did not come back as saved — the change may not have taken.' };
    return true;
  }
  configResult = { kind: 'good', text: savedText(pending.section, (S.config && S.config.restart_required) || []) };
  return true;
}

export function savedText(section, restartRequired) {
  if (section === 'llm') return 'Active — inference verified.';
  if (section === 'agency') {
    const coaching = restartRequired.includes('agency.heuristic_nudges');
    return 'Saved — role routing applies live to the next role-tagged spawn; coaching prompts ' +
      (coaching ? 'take effect after restart.' : 'unchanged.');
  }
  if (restartRequired.length) return 'Saved — takes effect after restart: ' + restartRequired.join(', ') + '.';
  return 'Saved.';
}

export function rejectSettingsConfig(message, requestID) {
  if (!config.claim(requestID)) return false;
  configResult = { kind: 'bad', text: message };
  renderSettings();
  return true;
}

document.addEventListener('click', (e) => {
  const btn = e.target && e.target.closest ? e.target.closest('[data-refresh-models]') : null;
  if (!btn) return;
  btn.disabled = true; btn.textContent = 'Asking…';
  query('discover', { provider: btn.dataset.refreshModels });
});
