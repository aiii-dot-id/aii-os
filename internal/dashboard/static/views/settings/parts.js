import { cognitionApplies } from './applies.js';
import { configValue, limitDraft } from './save.js';
import { startSignIn, wireSignInCompletion } from '../../signin.js';
import { S } from '../../state.js';
import { esc } from '../../util.js';
import { send } from '../../ws.js';
var renderSettingsNow;
export function setRenderSettings(f) { renderSettingsNow = f; }
export function renderSettings(...a) { if (S.view === 'technical') S.renderTechnical?.(); return renderSettingsNow(...a); }
export function cfgField(id, label, value, type) {
  return '<label class="f">' + esc(label) + '</label><input type="' + (type || 'text') + '" id="' + id + '" value="' + esc(value == null ? '' : value) + '">';
}

export function ownServerName(url) {
  try {
    const u = new URL(url);
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.host ? 'OpenAI-compatible · ' + u.host : '';
  } catch (e) { return ''; }
}

export function hinted(value, hint) {
  return ' data-unset-hint="' + esc(hint) + '"' + (String(value || '').trim() ? '' : ' title="' + esc(hint) + '"');
}

export const live = {};
export const listKey = (value, dir) => value + '|' + dir;

export function listed(dir, value) {
  return live[listKey(value, dir)] || null;
}
export function cognitionField(set, c) {
  const [label, note] = LIMIT_FIELDS[set.key] || [set.key, ''];
  const configured = configValue(c, set.key);
  const lifted = configured === LIMIT_WORD;
  const id = 'cog-' + set.key.replace(/[^a-z0-9]+/g, '-');
  const dflt = set.default_unlimited ? 'no limit (default)' : set.default + ' (default' + ')';
  return '<label class="f" for="' + id + '">' + esc(label) + ' — ' + esc(set.unit) + ', ' + esc(set.min) + ' to ' + esc(set.max) + '</label>' +
    '<input type="number" id="' + id + '" data-cog-key="' + esc(set.key) + '" min="' + esc(set.min) + '" max="' + esc(set.max) + '" step="1" placeholder="' + esc(dflt) + '" value="' + esc(configured == null || lifted ? '' : configured) + '"' +
    (lifted ? ' disabled' : '') + hinted(lifted ? null : configured, 'Blank takes the default: ' + dflt) + '>' +
    (set.unlimited ? limitUnlimitedHTML(id + '-unl', 'data-cog-unl="' + esc(set.key) + '"', lifted) : '') +
    (set.held ? '<div class="dim-note" data-cog-held="' + esc(set.key) + '" style="margin:2px 0">' + esc(set.held) + '</div>' : '') +
    '<div class="muted" data-cog-applies="' + esc(set.key) + '" style="font-size:11.5px;margin:2px 0 8px">' + esc((note ? note + ' ' : '') + cognitionApplies(set.key).replace(/^./, c => c.toUpperCase()) + '.') + '</div>' +
    (set.per_model ? limitRowHTML(set, c) : '');
}

export const LIMIT_WORD = 'unlimited';
export const LIMIT_FIELDS = {
  'llm.retries': ['RETRIES', 'How many times a request the network failed is tried again; -1 for none.'],
  'llm.retry_backoff_ms': ['PAUSE BEFORE A RETRY', ''],
  'llm.model_list_timeout_seconds': ['ASKING A PROVIDER FOR ITS MODELS', 'How long the check of a provider may take: the list on the Providers card, the window an entry does not declare, a local model for a role. Starting and switching wait for it.'],
  'llm.model_list_cache_seconds': ['A PROVIDER\u2019S ANSWER STANDS FOR', 'How long what a provider said of itself is kept before it is asked again.'],
  'updates.check_interval_minutes': ['MINUTES BETWEEN UPDATE CHECKS', 'Each check also brings the newest withdrawal lists, so this is also how soon a release or plugin its publisher withdrew is known here. Check now runs one at any time.'],
};
const LIMIT_LEVELS = { model: 'the model override', provider: 'the provider override', main: 'the main setting', default: 'the default', local: 'the local model\u2019s time fence (agency.turn_seconds_local), which stands in its place' };
const LIMIT_STILL_BOUNDS = 'Unlimited lifts this fence only. A turn is still fitted to the model\u2019s window, ends at the continued turn\u2019s wall (agency.continuation_wall_seconds) and the request\u2019s silence ceiling (llm.timeout_seconds), and stops when you press Stop. A turn with no fence once spent 1,945,593 tokens over 101 calls and ran to the context wall.';
export function limitBlock(c) {
  if (limitDraft) return limitDraft;
  const raw = c && c.cognition && c.cognition.overrides_block;
  return raw ? JSON.parse(JSON.stringify(raw)) : {};
}
export function limitDrafted(c) {
  return limitDraft !== null && JSON.stringify(limitDraft) !== JSON.stringify((c && c.cognition && c.cognition.overrides_block) || {});
}
function limitUnlimitedHTML(id, attr, checked) {
  return '<label class="f" style="display:flex;gap:6px;align-items:center;margin-top:2px"><input type="checkbox" id="' + id + '" ' + attr + (checked ? ' checked' : '') + '> UNLIMITED</label>' +
    '<div class="muted" data-lim-bounds style="font-size:11.5px;margin:0 0 4px">' + esc(LIMIT_STILL_BOUNDS) + '</div>';
}
export function limitValueText(v) { return v === LIMIT_WORD ? 'no limit' : typeof v === 'number' ? v.toLocaleString() : String(v); }
function limitEntries(block, key) {
  const [section, name] = [key.slice(0, key.indexOf('.')), key.slice(key.indexOf('.') + 1)];
  const out = [];
  for (const provider of Object.keys(block).sort()) {
    const p = block[provider] || {};
    if (p[section] && p[section][name] !== undefined) out.push({ provider, model: '', value: p[section][name] });
    for (const model of Object.keys(p.models || {}).sort()) {
      const m = p.models[model] || {};
      if (m[section] && m[section][name] !== undefined) out.push({ provider, model, value: m[section][name] });
    }
  }
  return out;
}
const openAddRows = new Set();
function limitRowHTML(set, c, flat = false) {
  const slug = set.key.replace(/[^a-z0-9]+/g, '-');
  const eff = set.effective || {};
  const unused = (c.cognition && c.cognition.overrides_unused) || [];
  const where = eff.provider ? ' for ' + eff.provider + (eff.model ? ' · ' + eff.model : '') : '';
  const held = new Map((set.overrides || []).filter(o => o.held).map(o => [o.provider + '\u0000' + (o.model || ''), o.held]));
  const rows = limitEntries(limitBlock(c), set.key).map(o => {
    const path = 'overrides.' + o.provider + (o.model ? '.models.' + o.model : '');
    const gone = unused.includes(path) || unused.includes('overrides.' + o.provider);
    const said = held.get(o.provider + '\u0000' + o.model);
    return '<li class="limit-row__set" data-lim-override="' + esc(set.key) + '"><span>' + esc(o.provider + (o.model ? ' · ' + o.model : '')) + ': <b>' + esc(limitValueText(o.value)) + '</b>' +
      (gone ? ' <span class="dim-note">unused: providers.json holds no such provider or listed model</span>' : '') +
      (said ? ' <span class="dim-note">' + esc(said) + '</span>' : '') + '</span>' +
      ' <button class="c-button c-button--quiet" type="button" data-lim-remove="' + esc(set.key) + '" data-lim-provider="' + esc(o.provider) + '" data-lim-model="' + esc(o.model) + '">Remove</button></li>';
  }).join('');
  const providers = (S.providers || []).filter(p => p.chat !== false);
  const models = [...new Set(providers.flatMap(p => p.models || []))];
  const adding = '<select id="lim-add-p-' + slug + '" aria-label="provider"><option value="">add for a provider…</option>' + providers.map(p => '<option>' + esc(p.name) + '</option>').join('') + '</select>' +
    (set.per_model === 'model' ? '<input class="limit-row__input" id="lim-add-m-' + slug + '" list="lim-models-' + slug + '" placeholder="all its models, or one" aria-label="model"><datalist id="lim-models-' + slug + '">' + models.map(m => '<option value="' + esc(m) + '">').join('') + '</datalist>' : '') +
    '<input class="limit-row__input limit-row__input--value" type="number" id="lim-add-v-' + slug + '" min="' + esc(set.min) + '" max="' + esc(set.max) + '" step="1" aria-label="value">' +
    (set.unlimited ? '<label class="limit-row__unlimited"><input type="checkbox" id="lim-add-u-' + slug + '"> unlimited</label>' : '') +
    '<button class="c-button c-button--secondary" type="button" data-lim-add="' + esc(set.key) + '">Add</button>';
  if (flat) return '<div class="limit-row" data-lim-row="' + esc(set.key) + '">' + (rows ? '<ul class="limit-row__list">' + rows + '</ul>' : '') + '<div class="limit-row__fields">' + adding + '</div></div>';
  return '<div class="limit-row" data-lim-row="' + esc(set.key) + '">' +
    (eff.value === 0 ? '' : '<div data-lim-effective="' + esc(set.key) + '">In force' + esc(where) + ': <b>' + esc(limitValueText(eff.value == null ? '' : eff.value)) + '</b>, from ' + esc(LIMIT_LEVELS[eff.level] || 'the default') + '</div>') +
    (rows ? '<ul class="limit-row__list">' + rows + '</ul>' : '') +
    '<details class="limit-row__add" data-lim-more="' + esc(set.key) + '"' + (openAddRows.has(set.key) ? ' open' : '') + '><summary>Set for one provider or model</summary><div class="limit-row__fields">' + adding + '</div></details>' +
    (limitDrafted(c) ? '<div class="dim-note" data-lim-drafted>Overrides changed — Save to put them in force.</div>' : '') +
    '</div>';
}
export const moreOpen = key => (openAddRows.has(key) ? ' open' : '');
export const limitOverridesHTML = (set, c) => limitRowHTML(set, c, true);
export function wireLimitRows(st) {
  st.querySelectorAll('[data-lim-more]').forEach(d => { d.ontoggle = () => { if (d.open) openAddRows.add(d.dataset.limMore); else openAddRows.delete(d.dataset.limMore); }; });
}
const LIMIT_OWN_FIELD = new Set(['prompt.max_tokens', 'llm.timeout_seconds']);
export function limitRowsHTML(card, c) {
  return ((c.cognition && c.cognition.settings) || []).filter(set => set.card === card && !LIMIT_OWN_FIELD.has(set.key)).map(set => cognitionField(set, c)).join('');
}
export function wireProviderSignIn(root) {
  wireSignInCompletion(root);
  root.querySelectorAll('[data-prov-signin-cancel]').forEach(btn => { btn.onclick = () => send({type:'provider_signin_cancel',provider:btn.dataset.provSigninCancel}); });
  root.querySelectorAll('[data-prov-signin]').forEach(btn => { btn.onclick = () => {
    startSignIn({ type: 'provider_signin', provider: btn.dataset.provSignin });
  }; });
}


export function num(v) { const n = parseInt(v, 10); return isNaN(n) ? 0 : n; }
