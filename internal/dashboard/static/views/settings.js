/* views/settings.js — operator-owned configuration, organized by TASK
   (design pass 2026-08-20): a slim section nav — the app's own idiom
   (R66) — renders ONE section at a time instead of the old full-stack
   dump. Providers are a status LIST with a single inline editor; the
   daily act (switching substrate) lives in the chat header, not here.
   R66: frame — settings stays in the binary. */
import { S } from '../state.js';
import { $, esc } from '../util.js';
import { send, query } from '../ws.js';
import { sandboxCardHTML, wireSandboxCard } from '../sandbox.js';
import { pendingSlot } from '../pending.js';
import { providerModels } from './model-picker.js';

/* --- section nav (module state; not persisted — a view concern) ---- */
const SECTIONS = [
  ['substrate', 'Substrate'], ['providers', 'Providers'],
  ['dashboard', 'Dashboard'], ['witness', 'Witness'],
  ['prompt', 'Prompt'], ['agency', 'Agency'], ['logs', 'Logs'], ['sandbox', 'Sandbox'], ['tools', 'Tools'],
];
let sec = 'substrate';
let provOpen = null; // provider index being edited, 'new', or null
const prov = pendingSlot();   // a provider save awaiting the server's answer
let provResult = null;    // what the server said about it
let provDraft = null;     // the rejected entry, so the operator's typing survives
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

/* --- sections --------------------------------------------------- */
/* The model belongs to the provider, so offer the provider's models
   rather than asking the operator to spell one. Discovery fills the list
   (live where the provider publishes one, the entry's own list
   otherwise). A model that is not in the list is still kept and shown —
   an operator who knows about a model we have not discovered must not be
   trapped by a dropdown. */
function modelField(id, providerName, current) {
  const p = S.providers.find(x => x.name === providerName) ||
            S.providers.find(x => x.default) || null;
  const models = providerModels(p);
  const placeholder = p && p.default_model ? 'Default: ' + p.default_model : 'Search or enter a model';
  return '<label class="f" id="' + id + '-label">MODEL' + (p ? ' — ' + esc(p.name) : '') + '</label>' +
    '<input type="text" id="' + id + '" list="' + id + '-list" value="' + esc(current || '') + '" placeholder="' + esc(placeholder) + '">' +
    '<datalist id="' + id + '-list">' + models.map(m => '<option value="' + esc(m) + '"></option>').join('') + '</datalist>';
}

function substrateHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';
  /* The POINTER card (2026-08-20 ruling): config.json names a
     providers.json entry + a model; the provider's data (endpoint, key,
     window, budgets) is edited in the Providers section — ONE place —
     and shown here read-only as it resolves. */
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
      ' · thinking ' + (c.llm.thinking_budget || '—') +
      ' · effort ' + esc(c.llm.reasoning_effort || 'vendor default') +
      ' · active ' + esc((c.llm.resolved_provider || '—') + ' / ' + (c.llm.resolved_model || '—')) + '</div>';
  return '<div class="card"><h3>SUBSTRATE — LLM</h3>' +
    provSel +
    modelField('cfg-model', cur, c.llm.model) +
    cfgField('cfg-timeout', 'TIMEOUT (SECONDS)', c.llm.timeout_seconds) +
    resolved +
    '<div style="font-size:11.5px;color:var(--faint);margin-top:8px">providers.json owns the provider data; this card points at an entry. Endpoint, key, context/output budgets, REASONING EFFORT, and THINKING mode are edited on the entry in the Providers section. A substrate change applies only after the candidate completes a real inference request.</div>' +
    '<div class="savebar"><button class="btn" data-save="llm">Save</button><span class="savenote">applies live after inference check</span></div></div>';
}
// A loopback bind on plain HTTP is already a secure context, so the box
// starts unchecked and nothing is lost. It stops being free the moment
// the address widens — then the conversation is on the network in the
// clear and the browser refuses the microphone — so that exact
// combination gets a warning, live, as the operator types the address.
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
    '<div class="savebar"><button class="btn" data-save="dashboard">Save</button><span class="savenote">saved — applies next restart</span></div></div>';
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
  return '<div class="card"><h3>AGENCY</h3>' +
    '<label class="f" style="display:flex;gap:6px;align-items:center;margin-top:4px"><input type="checkbox" id="cfg-plr"' + (on ? ' checked' : '') + '> USE LOCAL MODEL FOR SUB-AGENT ROLES</label>' +
    '<div style="font-size:11.5px;color:var(--faint);margin-top:8px">When checked and a provider entry marked <b>local</b> answers /models, role-tagged spawns (proposer, critic, judge…) run there — free, private evaluation on your own metal. Explicit agency.roles routes still win; untagged spawns are copies of the identity and always think with the configured model. If the local host is down, everything falls back to the configured model (logged).</div>' +
    '<div class="savebar"><button class="btn" data-save="agency">Save</button><span class="savenote">applies live — the next role-tagged spawn honors it</span></div></div>';
}
/* Logs: three next-boot fields + the inline viewer. The viewer is
   plain — a file list and a tail pane, no frame — because the
   operator asked for a viewer, not a log console. Logging is on by
   default (log/ beside data/); an EMPTY dir means deliberately
   disabled, and the viewer renders that state honestly rather than
   pretending files exist. */
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

/* --- providers: status list + one inline editor ------------------ */
const STATUS_DOT = {
  ok: ['#3fa34d', 'reachable — models listed live'],
  auth_required: ['#c9a227', 'needs an API key to list models'],
  no_credential: ['#c9a227', 'adopted credential unavailable'],
  unreachable: ['#c0392b', 'endpoint did not answer /models'],
  invalid_url: ['#c0392b', 'URL is not valid'],
};
function dot(status) {
  const d = STATUS_DOT[status] || ['#777', 'not probed yet'];
  return '<span title="' + esc(d[1]) + '" style="display:inline-block;width:9px;height:9px;border-radius:50%;background:' + d[0] + ';margin-right:8px;flex:none"></span>';
}
/* Why a provider reads the way it does, in the LIST — the reason a row
   is unusable, or the credential that makes it the one birth opens on,
   should not require opening the row to discover. */
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
  /* After a rejection, render what the operator actually typed — server
     state would silently discard it. */
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
    /* ONE api key field. There used to be a second — the env-var NAME —
       and an operator pasted a secret into it and overwrote a working
       provider's key, because two key-shaped boxes on one form is one
       too many however they are labelled. The env fallback still works
       when set in providers.json by hand; it is a deployment detail, not
       a paste target. */
    cfgField('pv-key-' + tag, 'API KEY (ENTER TO REPLACE' + (storedKey ? ', ONE STORED' : ', NONE STORED') + ')', p.api_key || '', 'password') +
    (storedKey ? '<label class="f" style="display:flex;gap:6px;align-items:center"><input type="checkbox" id="pv-keepkey-' + tag + '"' + (keepKey ? ' checked' : '') + '> KEEP STORED KEY WHEN BLANK</label>' : '') +
    credentialField('pv-cred-' + tag, p.credential || '', p.credential_info) +
    '<details style="margin-top:10px"><summary style="cursor:pointer;color:var(--dim);font-size:12px">Advanced serving settings</summary>' +
    cfgField('pv-models-' + tag, 'STATIC MODEL FALLBACK (COMMA-SEPARATED; BLANK = NONE)', (p.configured_models || []).join(', ')) +
    cfgField('pv-ctx-' + tag, 'CONTEXT LENGTH (TOKENS)', p.context_length || '') +
    cfgField('pv-maxout-' + tag, 'MAX OUTPUT TOKENS', p.max_output_tokens || '') +
    effortField('pv-effort-' + tag, p.reasoning_effort || '') +
    thinkingField(tag, p) +
    cfgField('pv-temp-' + tag, 'TEMPERATURE (BLANK = SERVER DEFAULT; 0 IS VALID)', p.temperature == null ? '' : p.temperature) +
    cfgField('pv-topp-' + tag, 'TOP_P (BLANK = SERVER DEFAULT)', p.top_p == null ? '' : p.top_p) +
    '</details>' +
    '<label class="f" style="display:flex;gap:6px;align-items:center;margin-top:6px"><input type="checkbox" id="pv-def-' + tag + '"' + (p.default ? ' checked' : '') + '> DEFAULT PROVIDER</label>' +
    '<div class="savebar"><button class="btn" data-prov-commit="' + tag + '">Save</button> ' +
    (tag !== 'new' ? '<button class="btn ghost" data-prov-del="' + esc(p.name) + '">Remove</button> ' : '') +
    '<button class="btn ghost" data-prov-cancel="1">Cancel</button></div></div>';
}
/* Reasoning effort is an enumerated value, not a spelling test. Blank
   omits it, which is what most providers want. */
function effortField(id, cur) {
  const opts = [['', '(provider default — omit)'], ['minimal', 'minimal'], ['low', 'low'], ['medium', 'medium'],
    ['high', 'high'], ['xhigh', 'xhigh (anthropic)'], ['max', 'max (anthropic)']];
  if (cur && !opts.some(o => o[0] === cur)) opts.push([cur, cur]);
  return '<label class="f">REASONING EFFORT</label><select id="' + id + '">' +
    opts.map(o => '<option value="' + esc(o[0]) + '"' + (o[0] === cur ? ' selected' : '') + '>' + esc(o[1]) + '</option>').join('') +
    '</select>';
}

/* Extended thinking is an ANTHROPIC concept. There is no OpenAI-shaped
   equivalent — we used to emit "thinking_budget" onto that path and no
   provider defines it, so the control looked live and did nothing.
   Offering it only where it works is the fix; anything provider-specific
   goes through EXTRA. */
function selectField(id, label, cur, opts) {
  return '<label class="f">' + esc(label) + '</label><select id="' + id + '">' +
    opts.map(o => '<option value="' + esc(o[0]) + '"' + (o[0] === cur ? ' selected' : '') + '>' + esc(o[1]) + '</option>').join('') +
    '</select>';
}

/* Three controls, because the vendor replaced the thinking parameter and
   the shapes are mutually exclusive: MODE picks the shape, BUDGET is
   meaningful only in the legacy one, and DISPLAY decides whether the
   reasoning comes back readable or as empty blocks. Sending the wrong
   shape is a 400 on every current model, so the mode says which
   generations accept it rather than leaving the operator to find out. */
function thinkingField(tag, p) {
  const id = 'pv-think-' + tag;
  if ((p.api_type || 'openai') !== 'anthropic') {
    return '<input type="hidden" id="' + id + '" value="' + esc(p.thinking_budget || '') + '">' +
      '<input type="hidden" id="pv-tmode-' + tag + '" value="' + esc(p.thinking_mode || '') + '">' +
      '<input type="hidden" id="pv-tdisp-' + tag + '" value="' + esc(p.thinking_display || '') + '">' +
      '<div style="font-size:11px;color:var(--faint);margin:-2px 0 8px">Extended thinking applies to the <b>anthropic</b> dialect. On an OpenAI-compatible provider use REASONING EFFORT, or a provider-specific key in EXTRA.</div>';
  }
  return selectField('pv-tmode-' + tag, 'THINKING MODE (ANTHROPIC)', p.thinking_mode || '', [
      ['', 'adaptive — current models (default)'],
      ['budget', 'budget tokens — pre-4.6 models only'],
      ['off', 'off — send no thinking'],
    ]) +
    selectField('pv-tdisp-' + tag, 'THINKING DISPLAY', p.thinking_display || '', [
      ['', 'omitted — reasoning happens, is not returned'],
      ['summarized', 'summarized — return readable reasoning'],
    ]) +
    cfgField(id, 'THINKING BUDGET (TOKENS — "budget" MODE ONLY)', p.thinking_budget || '');
}

/* A subscription you already hold, adopted rather than re-authorized:
   the runtime reads the file that tool maintains and never writes it. */
function credentialField(id, cur, info) {
  const LABEL = {
    'claude-code': 'Claude Max/Pro — adopt ~/.claude/.credentials.json',
    'codex': 'ChatGPT Plus/Pro — adopt ~/.codex/auth.json',
  };
  /* The platform says what it can adopt. Mobile returns none: another
     app's credential file is unreachable there by design. */
  const kinds = (S.config && S.config.credential_kinds) || [];
  if (!kinds.length) {
    return '<label class="f">CREDENTIAL</label>' +
      '<div style="font-size:11.5px;color:var(--faint);margin-bottom:8px">Adopted credentials are a desktop feature &mdash; the tools that hold them do not run here, and apps cannot read each other\'s files. Use an API key.</div>' +
      /* 'none' explicitly, not the current value: a provider carried over
         from a desktop install would otherwise keep re-selecting a
         credential this platform cannot use, and silently ignore the API
         key the operator just typed. */
      '<input type="hidden" id="' + id + '" value="none">';
  }
  const opts = [['none', 'API key (above)']].concat(kinds.map(k => [k, LABEL[k] || k]));
  /* A credential this identity owns (file:/path/to/token.json) is set by
     editing providers.json. Keep it as an option so selecting Save does
     not silently replace it with "none". */
  if (cur && cur !== 'none' && !opts.some(o => o[0] === cur)) {
    opts.push([cur, cur]);
  }
  const sel = cur || 'none';
  return '<label class="f">CREDENTIAL</label><select id="' + id + '">' +
    opts.map(o => '<option value="' + o[0] + '"' + (o[0] === sel ? ' selected' : '') + '>' + esc(o[1]) + '</option>').join('') +
    '</select>' + credentialState(info) +
    '<div style="font-size:11px;color:var(--faint);margin:-4px 0 8px">An adopted credential supplies its own endpoint and dialect, and replaces the key entirely.</div>';
}

/* What the credential says about itself — read now, never stored.
   Running an identity's rhythms on a subscription is a different posture
   from an API key, and the operator should be able to see which. */
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
  /* Blank sampling means server default; pointers preserve a set zero. */
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
    default: $('pv-def-' + tag).checked,
  };
  const temp = fnum('pv-temp-' + tag);
  if (temp !== undefined) entry.temperature = temp;
  const topp = fnum('pv-topp-' + tag);
  if (topp !== undefined) entry.top_p = topp;
  /* The editor stays OPEN until the server answers. Closing on send
     meant a rejected save discarded everything typed and left only a
     transient toast — you could not tell accepted from rejected from
     ignored. Same shape the config sections already use. */
  provResult = null;
  provDraft = null;
  const requestID = send({ type: 'provider_set', entry });
  if (!prov.arm({ name: entry.name, sentKey: entry.api_key !== '', entry: entry }, requestID)) {
    provDraft = entry;
    provResult = { kind: 'bad', text: 'Not connected — provider was not sent.' };
  }
  renderSettings();
}

/* The server re-sends the whole registry after a successful write, so
   VERIFY the entry came back as asked before claiming success. */
export function acceptProviderSave(requestID) {
  /* claim() clears: this registry IS our answer, so the wait ends here
     whatever it says (pending.js records the wedge this prevents). */
  const pending = prov.claim(requestID);
  if (!pending) return false;
  const got = S.providers.find(p => p.name === pending.name);
  const expectedKey = pending.sentKey || pending.entry.has_key;
  if (!got || !!got.has_key !== expectedKey) {
    provDraft = pending.entry;   // keep what was typed
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
  provDraft = pending.entry;   // keep what was typed
  provResult = { kind: 'bad', text: message };
  renderSettings();
  return true;
}

/* --- render ------------------------------------------------------ */
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
  else if (sec === 'sandbox') html += sandboxCardHTML() || '<div class="card"><div class="empty">loading sandbox…</div></div>';
  else if (sec === 'tools') html += toolsHTML();
  st.innerHTML = html;

  st.querySelectorAll('[data-sec]').forEach(btn => {
    btn.onclick = () => { sec = btn.dataset.sec; provOpen = null; renderSettings(); };
  });
  st.querySelectorAll('[data-save]').forEach(btn => { btn.onclick = () => { saveSettings(btn.dataset.save); renderSettings(); }; });
  // The unsafe combination is host-plus-unchecked, so the warning has to
  // react to BOTH — and while the operator is still typing the address,
  // not after they have saved it and restarted into the clear.
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
  st.querySelectorAll('[data-prov-cancel]').forEach(btn => { btn.onclick = () => { provOpen = null; renderSettings(); }; });
  st.querySelectorAll('[data-logfile]').forEach(row => {
    row.onclick = () => {
      S.logFile = row.dataset.logfile;
      query('logs', { name: S.logFile });
    };
  });
  const openNew = $('pv-open-new');
  if (openNew) openNew.onclick = () => { provOpen = 'new'; renderSettings(); };
  /* Changing the provider repopulates the model list IN PLACE. A full
     re-render here would discard whatever else the operator had already
     typed in this card — the reset behaviour that was called out before. */
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
    /* The pointer + the transport knob — provider data is edited on the
       providers.json entry (Providers section), never here. */
    ch['llm.provider'] = $('cfg-provider').value;
    ch['llm.model'] = $('cfg-model').value.trim();
    const tmo = num($('cfg-timeout').value); if (tmo > 0) ch['llm.timeout_seconds'] = tmo;
  } else if (section === 'plugins') {
    ch['plugins.autoload'] = $('cfg-plevel').value;
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
  } else if (section === 'logs') {
    ch['logs.dir'] = $('cfg-ldir').value.trim();
    ch['logs.max_backups'] = num($('cfg-lbackups').value);
    ch['logs.compress_days'] = num($('cfg-lcomp').value);
  }
  configResult = null;
  const requestID = send({ type: 'config_set', config: ch });
  if (!config.arm({ section: section, changes: ch }, requestID)) {
    configResult = { kind: 'bad', text: 'Not connected — configuration was not sent.' };
  }
}

// Config has one owner and TWO consumers: this view and the Plugins view.
// The owner SENDS; each view renders itself. That is what keeps the
// dependency one-way — settings.js does not import plugins.js, so neither
// module needs the other to load — and it avoids a second copy of
// pending/result, which for one field would be exactly the bureaucracy
// AGENTS.md 1.1 forbids.
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

export function acceptSettingsConfig(requestID) {
  /* claim() clears: this frame IS the answer, so the wait is over
     whatever it says. A readback that cannot carry a field — as the
     agency checkbox's could not — used to wedge the banner on every
     save (pending.js records the class). */
  const pending = config.claim(requestID);
  if (!pending) return false;
  const missed = Object.keys(pending.changes).filter(p => configValue(S.config, p) !== pending.changes[p]);
  if (missed.length) {
    configResult = { kind: 'bad', text: 'Acknowledged, but ' + missed.join(', ') +
      ' did not come back as saved — the change may not have taken.' };
    return true;
  }
  configResult = { kind: 'good', text: pending.section === 'llm' ? 'Active — inference verified.' : 'Saved.' };
  return true;
}

export function rejectSettingsConfig(message, requestID) {
  if (!config.claim(requestID)) return false;
  configResult = { kind: 'bad', text: message };
  renderSettings();
  return true;
}
