import { provOpen, provResult } from '../settings.js';
import { cfgField, listKey, live } from './parts.js';
import { effortName } from '../../components/reply-box/reply-box.js';
import { asking, captureEdits, dot, editorFields, fieldKey, fieldValue, prov, provBroken, savebarHTML, setConfigResult, speechAdd } from './save.js';
import { nameOf, statusWords } from './modelwords.js';
import { asked, draft, edits } from './state.js';
import { credentialExpired, credentialInHand, signInProgress, signInUnmetRow, signInWanted } from '../../signin.js';
import WM from '../../strings/settings-model.js';
import { S } from '../../state.js';
import { $, esc, fill, loadingCard } from '../../util.js';
import { send } from '../../ws.js';
import W from '../../strings/settings-model.js';
import { providerModels } from '../model-picker.js';
import { capabilitiesHTML } from './capabilities.js';
import { localMarkField, localModelHTML } from './local.js';
export const NEW_PROVIDER = Symbol('new provider');
export function submittedEditor(section, root) {
  captureEdits();
  const stack = $('settings-stack');
  if (!root && section === 'speech') root = $('speech-form');
  if (!root && section.startsWith('speech_')) root = stack?.querySelector('[data-speech-section="' + section + '"]');
  if (!root) {
    const button = [...document.querySelectorAll('#settings-stack [data-save], #plugins-stack [data-save], #technical-stack [data-save]')].find(b => b.dataset.save === section);
    root = button && (button.closest('.card') || button.closest('[data-edit-form]'));
  }
  if (!root) return null;
  const dir = section === 'speech_stt' ? 'stt' : section === 'speech_tts' ? 'tts' : '';
  const fields = editorFields(root).filter(el => {
    if (!el.closest('#plugins-stack')) return true;
    if (section.startsWith('plugin:')) return el.dataset.psetPlugin === section.slice('plugin:'.length);
    if (section.startsWith('grants:')) return el.dataset.grantPlugin === section.slice('grants:'.length);
    if (section === 'plugins') return el.id === 'cfg-plevel';
    if (section === 'catalog') return el.id === 'cfg-caturl' || el.id === 'cfg-catevery';
    if (section === 'plugin_runtime') return el.id.startsWith('cfg-rt-');
    return false;
  });
  return { fields: new Map(fields.map(el => [fieldKey(el), fieldValue(el)])),
    choice: section === 'speech' ? { ...draft } : dir ? draft[dir] : null, owner: provOpen };
}
function modelField(id, providerName, current) {
  const p = S.providers.find(x => x.name === providerName) ||
            S.providers.find(x => x.default) || null;
  const models = providerModels(p);
  const placeholder = p && p.default_model ? 'Default: ' + p.default_model : 'Search or enter a model';
  const off = p ? '' : ' hidden';
  return '<div class="settings-model__model"><label class="f" id="' + id + '-label" for="' + id + '">' + esc(W.model) + '</label>' +
    '<input type="text" id="' + id + '" list="' + id + '-list" value="' + esc(current || '') + '" placeholder="' + esc(placeholder) + '" aria-describedby="' + id + '-hint">' +
    '<div class="muted settings-model__hint" id="' + id + '-hint">' + esc(W.modelSearch) + '.</div>' +
    '<div class="muted settings-model__refresh"><button type="button" class="btn" data-refresh-models="' + esc(p ? p.name : '') + '"' + off + '>' + esc(W.modelRefresh) + '</button>' +
    '<span id="' + id + '-refresh-hint"' + off + '>' + esc(W.modelRefreshHint) + '</span></div>' +
    '<datalist id="' + id + '-list">' + models.map(m => '<option value="' + esc(m) + '"></option>').join('') + '</datalist></div>';
}

function contextSummary(llm) {
  const source = llm.prompt_budget_source || '';
  const figure = llm.context_length || llm.prompt_budget || 0;
  if (!source || !figure) return esc(llm.context_length || '—');
  if (source === 'fallback') return esc(figure) + ' (fallback — set context length on the provider)';
  return esc(figure) + ' (' + esc(source) + ')';
}

export function modelHeadHTML() {
  return '<header class="settings-access__head" data-model-head><h2 class="settings-access__title" data-settings-page-name>' + esc(W.title) + '</h2>' +
    '<p class="settings-access__intro" id="m-intro">' + esc(fill(W.intro, { name: nameOf() || W.unnamed })) + '</p></header>';
}

let moreOpen = false;
export const setMoreOpen = open => { moreOpen = !!open; };
let effortPending = null;
export function effortAnswered(requestID) {
  if (!effortPending || effortPending.requestID !== requestID) return false;
  effortPending = null;
  return true;
}
export function effortForgotten() { effortPending = null; }
const effortShown = choice => effortPending ? effortPending.level : (choice.in_force || '');
const effortChoice = () => (S.identityExists && S.config && S.config.llm && S.config.llm.effort_choice) || null;
const drafted = id => [...edits.values()].some(el => el.id === id);
const effortInert = () => drafted('cfg-provider') || drafted('cfg-model');
const effortLine = () => effortInert() ? W.effortInert
  : effortPending && effortPending.checked ? fill(W.effortChecking, { level: effortName(effortPending.level) }) : W.effortHint;
function effortHTML(choice) {
  if (!choice || !choice.levels || !choice.levels.length) return '';
  const inForce = effortShown(choice), entries = ['', ...choice.levels];
  if (!entries.includes(inForce)) entries.push(inForce);
  return '<div data-effort><label class="f" for="model-effort">' + esc(W.effort) + '</label>' +
    '<select id="model-effort" data-immediate' + (effortInert() ? ' disabled' : '') + '>' +
    entries.map(l => '<option value="' + esc(l) + '"' + (l === inForce ? ' selected' : '') + '>' + esc(effortName(l)) + '</option>').join('') +
    '</select><div class="muted settings-model__effort" data-effort-line data-announce>' + esc(effortLine()) + '</div></div>';
}
export function syncEffort(root) {
  const sel = root && root.querySelector('#model-effort');
  if (!sel) return;
  const off = effortInert();
  if (sel.disabled !== off) sel.disabled = off;
  const want = effortShown(effortChoice() || {});
  if (sel.value !== want && [...sel.options].some(o => o.value === want)) sel.value = want;
  const line = root.querySelector('[data-effort-line]'), text = effortLine();
  if (line && line.textContent !== text) line.textContent = text;
}
export function chooseEffort(sel, root) {
  const choice = effortChoice() || {}, level = sel.value;
  if (effortInert()) { sel.value = choice.in_force || ''; syncEffort(root); return; }
  const requestID = send({ type: 'effort_set', effort: level });
  effortPending = requestID ? { level, requestID, checked: (choice.checked || []).includes(level) } : null;
  syncEffort(root);
}
const hintHTML = text => '<div class="muted settings-model__note">' + esc(text) + '</div>';
function moreHTML(c) {
  const name = nameOf(), retryDefault = c.llm.refused_retry_max_default;
  const effort = effortHTML(effortChoice());
  return '<details class="settings-model__more" data-more' + (moreOpen ? ' open' : '') + '>' +
    '<summary>' + esc(W.more) + ' <span class="muted">· ' + esc(effort ? W.moreHint : W.moreHintNoLevels) + '</span></summary>' +
    effort +
    cfgField('cfg-timeout', W.timeout + ' (' + W.seconds + ')', c.llm.timeout_seconds) +
    hintHTML(name ? fill(W.timeoutHint, { name }) : W.timeoutHintUnnamed) +
    cfgField('cfg-probe-timeout', W.checkSeconds + ' (' + W.seconds + ')', c.llm.probe_timeout_seconds) +
    cfgField('cfg-refused-retry', W.retryAfter + ' (' + W.seconds + ')', c.llm.refused_retry_max_seconds || '') +
    (retryDefault ? hintHTML(fill(W.retryAfterHint, { seconds: retryDefault })) : '') +
    '</details>';
}

export function substrateHTML(c) {
  if (!c) return modelHeadHTML() + loadingCard('configuration');

  const cur = c.llm.provider || '';
  const picked = [...edits.values()].find(el => el.id === 'cfg-provider');
  const shownProvider = picked ? picked.value : cur;
  const candidates = S.providers.filter(p => p.chat !== false);
  const def = S.providers.find(p => p.default);
  const provSel = '<div class="settings-model__provider"><label class="f" for="cfg-provider">' + esc(W.provider) + '</label>' +
    '<select id="cfg-provider"><option value=""' + (cur === '' ? ' selected' : '') + '>' + esc(def ? fill(W.providerDefault, { provider: def.name }) : W.providerNoDefault) + '</option>' +
    (cur && !candidates.some(p => p.name === cur) ? '<option value="' + esc(cur) + '" selected>' + esc(cur) + '</option>' : '') +
    candidates.map(p => '<option value="' + esc(p.name) + '"' + (p.name === cur ? ' selected' : '') + '>' + esc(p.name) + '</option>').join('') +
    '</select></div>';
  const failed = c.llm.error
    ? '<div style="font-size:11.5px;color:#c0392b;margin-top:8px">pointer does not resolve: ' + esc(c.llm.error) + '</div>' : '';
  const resolved = c.llm.error ? ''
    : '<div data-resolved style="font-size:11.5px;color:var(--faint);margin-top:8px;line-height:1.6">resolved: <span style="font-family:var(--mono)">' + esc(c.llm.endpoint) + '</span>' +
      ' · key ' + esc(c.llm.api_key_masked || 'none') +
      ' · context ' + contextSummary(c.llm) +
      ' · max out ' + (c.llm.max_output_tokens || '—') +

      (c.llm.thinking_applies ? ' · thinking ' + (c.llm.thinking_budget || '—') : '') +
      ' · effort ' + esc(c.llm.effort_plan || 'provider default (none set)') +
      ' · active ' + esc((c.llm.resolved_provider || '—') + ' / ' + (c.llm.resolved_model || '—')) + '</div>';
  return modelHeadHTML() + '<section class="settings-access__part" aria-labelledby="m-model" aria-describedby="m-intro" data-model data-edit-form>' +
    '<h3 class="sr-only" id="m-model">' + esc(W.modelSection) + '</h3>' +
    '<div class="settings-model__fields">' + provSel + modelField('cfg-model', shownProvider, c.llm.model) + '</div>' +
    failed +
    moreHTML(c) +
    aboutHTML(resolved + capabilitiesHTML(c.llm.capabilities) +
      '<div style="font-size:11.5px;color:var(--faint);margin-top:8px">providers.json owns the provider data; this card points at an entry. Endpoint, key, context/output budgets, REASONING EFFORT, and THINKING mode are edited on the entry in the Providers section. A substrate change applies only after the candidate completes a real inference request.</div>') +
    savebarHTML('llm', W.useModelHint + ' ' + fill(W.changedNote, { name: nameOf() || W.unnamed }), { label: W.useModel }) + '</section>';
}

let aboutOpen = false;
export const setAboutOpen = open => { aboutOpen = !!open; };
const aboutHTML = body => '<details class="settings-model__more" data-more-about' + (aboutOpen ? ' open' : '') + '>' +
  '<summary>' + esc(W.moreAbout) + '</summary>' + body + '</details>';

export const SPEND_WORDS = {
  title: 'LANGUAGE MODEL SPEND',
  tip: 'The tokens the providers reported, by day, by who spent them and on which model.',
  intro: 'Tokens the providers reported, by day, by who spent them and on which model. No price is known to AII OS, and nothing here stops a turn.',
  days: 'SHOW',
  choices: [['month', 'this month'], [1, 'today'], [7, 'the last 7 days'], [30, 'the last 30 days']],
  none: 'Nothing was spent on these days.',
  turns: 'turns',
  facility: 'facility',
  subagent: 'sub-agent',
  atLeast: 'at least ',
  unknown: n => n + (n === 1 ? ' record' : ' records') + ' of unknown spend',
  calls: n => n + (n === 1 ? ' call' : ' calls'),
  split: (i, o, c) => 'in ' + i.toLocaleString() + ', out ' + o.toLocaleString() + (c ? ', ' + c.toLocaleString() + ' read from the cache' : ''),
  zone: z => 'Days are counted in ' + z + '.',
};
function spendRole(role) {
  if (role === 'turns') return SPEND_WORDS.turns;
  if (role.startsWith('facility:')) return SPEND_WORDS.facility + ' ' + role.slice(9);
  if (role.startsWith('sub-agent:')) return SPEND_WORDS.subagent + ' ' + role.slice(10);
  return SPEND_WORDS.subagent;
}
export function spendLineHTML(l) {
  const where = [l.provider, l.model].filter(Boolean).join(' \u00b7 ');
  const tokens = (l.floor ? SPEND_WORDS.atLeast : '') + (l.tokens || 0).toLocaleString();
  const parts = [];
  if (l.calls) parts.push(SPEND_WORDS.calls(l.calls));
  if (l.input || l.output) parts.push(SPEND_WORDS.split(l.input || 0, l.output || 0, l.cached || 0));
  if (l.unknown) parts.push(SPEND_WORDS.unknown(l.unknown));
  return '<li data-spend-line="' + esc(l.role) + '"><b>' + esc(spendRole(l.role)) + '</b>' + (where ? ' \u2014 ' + esc(where) : '') +
    ': <b data-spend-tokens>' + esc(tokens) + '</b>' + (parts.length ? ' <span class="muted">(' + esc(parts.join('; ')) + ')</span>' : '') + '</li>';
}

let byDayOpen = false;
export const setSpendByDayOpen = open => { byDayOpen = !!open; };
const sentence = s => s.charAt(0).toUpperCase() + s.slice(1);
export function spendByProvider(days) {
  const by = new Map();
  for (const d of days) for (const l of d.lines || []) {
    if (!l.provider && !(l.role || '').startsWith('sub-agent')) continue;
    const t = by.get(l.provider || '') || { tokens: 0, floor: false };
    t.tokens += l.tokens || 0;
    t.floor = t.floor || !!l.floor;
    by.set(l.provider || '', t);
  }
  return [...by].sort((a, b) => b[1].tokens - a[1].tokens || a[0].localeCompare(b[0]));
}
function spendDayHTML(d) {
  return '<div data-spend-day="' + esc(d.day) + '" style="margin-top:10px"><div class="kv"><span>' + esc(d.day) + '</span><b data-spend-total>' +
    esc((d.floor ? SPEND_WORDS.atLeast : '') + (d.tokens || 0).toLocaleString()) + '</b></div>' +
    (d.unknown ? '<div class="dim-note">' + esc(SPEND_WORDS.unknown(d.unknown)) + '</div>' : '') +
    '<ul style="margin:4px 0 0 18px;font-size:12.5px;line-height:1.6">' + (d.lines || []).map(spendLineHTML).join('') + '</ul></div>';
}
export const spendAsked = span => span === 'month' ? 31 : span;
export function spendInSpan(spend, span) {
  if (span !== 'month') return spend;
  const month = (spend.today || '').slice(0, 7);
  return { ...spend, days: month ? (spend.days || []).filter(d => String(d.day).slice(0, 7) === month) : [] };
}
const SPEND_HEADS = { month: 'spend', 1: 'spendToday', 7: 'spendWeek', 30: 'spendMonthDays' };
export function spendSectionHTML(spend, pick, span) {
  const head = '<h3 class="settings-access__h3" id="m-spend">' + esc(W[SPEND_HEADS[span] || 'spend']) + '</h3>';
  const hint = '<p class="settings-access__intro" id="m-spend-hint">' + esc(W.spendHint) + '</p>';
  const open = body => '<section class="settings-access__part" aria-labelledby="m-spend" aria-describedby="m-spend-hint" data-spend>' + head + body + '</section>';
  if (!spend) return open(hint + pick + loadingCard('spend'));
  const days = spend.days || [];
  const unknown = days.reduce((n, d) => n + (d.unknown || 0), 0);
  const sums = days.length
    ? '<ul class="settings-model__spend-list" data-spend-providers>' + spendByProvider(days).map(([p, t]) => {
        const tokens = (t.floor ? SPEND_WORDS.atLeast : '') + t.tokens.toLocaleString();
        return '<li data-spend-provider="' + esc(p) + '">' + esc(sentence(fill(p ? W.spendNoPrice : W.spendNoProvider, { tokens, provider: p }))) + '</li>';
      }).join('') + '</ul>' +
      (unknown ? '<p class="dim-note" data-spend-unknown>' + esc(sentence(SPEND_WORDS.unknown(unknown))) + '</p>' : '')
    : '<p class="empty">' + esc(W.spendNothing) + '</p>';
  const byDay = days.length
    ? '<details data-spend-byday' + (byDayOpen ? ' open' : '') + '><summary>' + esc(W.spendByDay) + '</summary>' + days.map(spendDayHTML).join('') + '</details>'
    : '';
  const zone = spend.zone ? '<p class="dim-note">' + esc(fill(W.spendDaysIn, { zone: spend.zone })) + '</p>' : '';
  return open(sums + hint + pick + byDay + zone);
}
export function forgetSpeechLists(name) {
  ['stt', 'tts'].forEach(dir => { delete live[listKey(name, dir)]; delete asking[listKey(name, dir)]; delete asked[listKey(name, dir)]; });
}

export function readySpeechService(section, name, key, baseURL, ch, editor = submittedEditor(section), remaining = [], echo) {
  setConfigResult(null);
  const requestID = send({ type: 'speech_service', provider: name, api_key: key, base_url: baseURL });
  if (!speechAdd.arm({ section: section, name: name, sentKey: key !== '', changes: ch, editor, remaining, echo }, requestID)) {
    setConfigResult({ kind: 'bad', section: section, text: 'Not connected — ' + name + ' was not saved.' });
  }
}

function provRowNote(p) {
  const bits = [];
  if (p.preselect && p.preselect_why) bits.push(esc(p.preselect_why));
  const ci = p.credential_info;
  if (ci && !ci.error) {
    const s = [];
    if (ci.plan) s.push('plan ' + esc(ci.plan));
    if (ci.expires_at) s.push((credentialExpired(ci) ? 'EXPIRED ' : 'usable to ') + esc(ci.expires_at.slice(0, 16).replace('T', ' ')) + ' UTC');
    if (s.length) bits.push(esc(ci.kind) + ' · ' + s.join(' · '));
  }
  if (p.status_reason) bits.push(esc(p.status_reason));
  else if (ci && ci.error) bits.push(esc(ci.error));
  if (!bits.length) return '';
  return '<span class="settings-access__hint">' + bits.join(' — ') + '</span>';
}

function providerEditor(p, tag) {

  const storedKey = !!(p && p.has_key);
  let keepKey = storedKey;
  p = p || {};
  const apiType = p.api_type || 'openai';
  const nameField = tag === 'new'
    ? cfgField('pv-name-' + tag, 'NAME', '')
    : '<label class="f">NAME</label><input type="text" id="pv-name-' + tag + '" value="' + esc(p.name || '') + '" readonly>';
  return '<div data-provider-editor data-keep-open="' + esc(tag === 'new' ? 'new-provider' : 'provider:' + p.name) + '" data-edit-scope="' + esc(tag === 'new' ? 'new-provider' : 'provider:' + p.name) + '" style="padding:10px 0 4px">' +
    nameField +
    '<label class="f">API TYPE (DIALECT)</label><select id="pv-type-' + tag + '">' +
    ['openai', 'anthropic', 'gemini', 'responses'].map(t => '<option value="' + t + '"' + (t === apiType ? ' selected' : '') + '>' + t + '</option>').join('') +
    '</select>' +
    cfgField('pv-url-' + tag, 'ENDPOINT URL', p.endpoint || '') +
    localMarkField(tag, p) +
    cfgField('pv-model-' + tag, 'DEFAULT MODEL', p.default_model || '') +
    capabilitiesHTML(p.capabilities) +

    cfgField('pv-key-' + tag, 'API KEY (ENTER TO REPLACE' + (storedKey ? ', ONE STORED' : ', NONE STORED') + ')', '', 'password') +
    (storedKey ? '<label class="f" style="display:flex;gap:6px;align-items:center"><input type="checkbox" id="pv-keepkey-' + tag + '"' + (keepKey ? ' checked' : '') + '> KEEP STORED KEY WHEN BLANK</label>' : '') +
    credentialField('pv-cred-' + tag, p.credential || '', p.credential_info) +
    '<details style="margin-top:10px"><summary style="cursor:pointer;color:var(--dim);font-size:12px">Advanced serving settings</summary>' +
    cfgField('pv-models-' + tag, 'STATIC MODEL FALLBACK (COMMA-SEPARATED; BLANK = NONE)', (p.configured_models || []).join(', ')) +
    cfgField('pv-embed-' + tag, 'EMBEDDINGS MODEL (MEMORY\'S MEANING LAYER; BLANK = NONE)', p.embeddings_model || '') +
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
    (p.can_sign_in ? signInRow(p) : signInUnmetRow(p)) +
    '</div>';
}

function signInRow(p) {
  const progress = signInProgress(p.signin, 'provider', p.name);
  if (!signInWanted(p, S.skipSignInWithValidToken !== false)) return progress ? '<div class="signin">' + progress + '</div>' : '';
  return '<div class="signin">' + progress +
    '<button class="btn" data-prov-signin="' + esc(p.name) + '">Sign in with ' + esc(p.name.replace(/\s*\(.*\)\s*$/, '')) + '</button>' +
    (p.signin && p.signin.status === 'pending' ? '<button class="btn ghost" data-prov-signin-cancel="' + esc(p.name) + '">' + esc(WM.signInCancel) + '</button>' : '') + '</div>';
}
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
    'claude-code': WM.adoptClaude,
    'codex': WM.adoptChatGPT,
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
    '<div style="font-size:11px;color:var(--faint);margin:-4px 0 8px">' + esc(WM.adoptHint) + ' An adopted credential replaces the key entirely. To use the claude-code credential at another address, name that address on the entry in providers.json (credential_options.base_url).</div>';
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
  if (info.expires_at) bits.push((credentialExpired(info) ? 'EXPIRED ' : 'usable to ') + esc(info.expires_at.replace('T', ' ').replace('Z', ' UTC')));
  if (info.path) bits.push('from ' + esc(info.path));
  if (!bits.length) return '';
  return '<div style="font-size:11.5px;color:var(--faint);margin:2px 0 6px">' + bits.join(' &middot; ') + '</div>';
}
export function providersHTML() {
  let html = '<section class="settings-access__part" aria-labelledby="m-providers" aria-describedby="m-providers-hint" data-providers>' +
    '<h3 class="settings-access__h3" id="m-providers">' + esc(W.providers) + '</h3>' +
    '<p class="settings-access__hint" id="m-providers-hint">' + esc(fill(W.providersHint, { name: nameOf() || W.unnamed })) + '</p>' +
    '<p class="settings-access__hint">The operator\'s file, beside config.json — edited here or by hand. Status describes live model discovery and is never stored; activation is decided by a real inference check.</p>';
  if (prov.waiting()) html += '<div class="config-result" data-announce>Saving ' + esc(prov.waiting().name) + '… waiting for the runtime to confirm.</div>';
  else if (provBroken.waiting()) html += '<div class="config-result" data-announce>' + (provBroken.waiting().type === 'provider_repair' ? 'Repairing ' : 'Removing ') + esc(provBroken.waiting().label) + '… waiting for the runtime to confirm.</div>';
  else if (provResult) html += '<div class="config-result ' + provResult.kind + '" data-announce>' + esc(provResult.text) + '</div>';
  const speechOnly = S.providers.filter(p => p.chat === false);
  let drawn = 0;
  const chatRows = S.providers.map((p, i) => {
      if (p.chat === false) return '';
      const open = provOpen === p.name;
      const status = p.local && p.status === 'unreachable' ? 'not_running' : p.status;
      let row = '<div class="settings-access__row settings-model__prov' + (drawn++ ? ' settings-model__prov--next' : '') + '" data-prov-row="' + i + '">' +
        '<div class="settings-access__row-words">' +
        '<span class="settings-access__h4 settings-model__name">' + dot(status) + esc(p.name) + (p.default ? ' ✓' : '') + '</span>' +
        '<span class="settings-access__hint">' + esc(providerState(p, status)) + '</span>' +
        '<span class="td settings-access__hint settings-model__endpoint">' + esc(p.endpoint) +
        (p.default_model ? ' · ' + esc(p.default_model) : '') +
        (p.local ? '' : ' · ' + esc(modelCount((p.models || []).length))) + '</span>' +
        provRowNote(p) + '</div>' +
        '<button type="button" class="c-button" data-prov-manage="' + i + '" data-keep-press="provider:' + esc(p.name) + '" aria-expanded="' + open + '">' + esc(W.manage) + '</button></div>';
      if (open) row += providerEditor(p, String(i));
      return row;
    }).join('');
  if (chatRows) html += chatRows;
  else if (S.providers.length) html += '<div class="empty">no chat providers — add one below</div>';
  else html += '<div class="empty">no providers — add one below</div>';
  if (speechOnly.length) {
    const one = speechOnly.length === 1;
    html += '<div class="muted" style="font-size:11.5px;margin:6px 0 2px">' + speechOnly.length + ' speech-only service' + (one ? '' : 's') +
      ' (' + esc(speechOnly.map(p => p.name).join(', ')) + ') ' + (one ? 'is' : 'are') + ' on <a href="#" data-open-section="speech">Settings → Speech</a>.</div>';
  }
  html += brokenProvidersHTML();
  html += localModelHTML(S.config);
  html += provOpen === NEW_PROVIDER
    ? providerEditor(null, 'new')
    : '<button type="button" class="c-button settings-model__add" id="pv-open-new" data-keep-press="new-provider">' + esc(W.addProvider) + '</button>';
  html += '</section>';
  return html;
}
const modelCount = n => n === 1 ? W.modelCountOne : fill(W.modelCount, { n });
function providerState(p, status) {
  const bits = [];
  if (p.local) bits.push((p.models || []).length === 1 ? W.localModelsOne : fill(W.localModels, { n: (p.models || []).length }));
  else if (p.credential && credentialInHand({ ...p, status })) bits.push(W.signedIn);
  else if (status !== 'key_refused' && p.has_key) bits.push(W.keyKept);
  else if (status === 'auth_required') bits.push(W.noKey);
  if (status === 'key_refused') bits.push(W.keyRefused);
  else if (status === 'ok') bits.push(W.answering);
  else if (status) bits.push(fill(W.notAnswering, { reason: statusWords(status) }));
  return bits.join(' · ');
}
function brokenProvidersHTML() {
  const broken = S.brokenProviders || [];
  if (!broken.length) return '';
  return '<div class="muted" style="font-size:11.5px;margin:10px 0 4px">' + broken.length + ' broken entr' + (broken.length === 1 ? 'y' : 'ies') +
    ' in providers.json, not used until repaired or removed:</div>' +
    broken.map(b => {
      const label = b.name ? esc(b.name) : 'unnamed entry';
      const sha = esc(b.sha256);
      return '<div class="tool-row" data-prov-broken="' + b.position + '" style="align-items:center">' + dot('broken') +
        '<span class="tn">' + label + ' · broken</span>' +
        '<span class="td">entry ' + (b.position + 1) + ' in providers.json</span></div>' +
        '<div style="font-size:11px;color:var(--warn,#c66);margin:-6px 0 4px 22px">' + esc(b.reason) + '</div>' +
        '<div class="savebar" style="margin:0 0 10px 22px">' +
        (b.repair ? '<button class="btn" data-prov-repair="' + b.position + '" data-prov-sha="' + sha + '">Repair</button> ' +
          '<span class="muted" style="font-size:11px">' + esc(b.repair) + '</span> ' : '') +
        '<button class="btn ghost" data-prov-remove-broken="' + b.position + '" data-prov-sha="' + sha + '">Remove</button></div>';
    }).join('');
}
