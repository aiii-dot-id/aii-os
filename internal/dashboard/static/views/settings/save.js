import WACCESS from '../../strings/settings-access.js';
import WPEOPLE from '../../strings/settings-people.js';
import WT from '../../strings/settings-technical-forms.js';
import WVOICE from '../../strings/settings-voice.js';
import { limitSection, pickedValue } from '../settings.js';
import { cognitionApplies } from './applies.js';
import { readySpeechService, submittedEditor } from './model.js';
import { STATUS_DOT, modelAnswered, modelSays, savesAModel } from './modelwords.js';
import { LIMIT_WORD, limitBlock, num, ownServerName, renderSettings, wireLimitRows } from './parts.js';
import { saveContacts } from './people.js';
import { collectLimits, limitSet } from './permissions.js';
import { asked, config, draft, edits } from './state.js';
import { browserGoneAnswered, ownServer, runsEngine, saveSpeechSettings, speechService } from './voice.js';
import { pendingSlot } from '../../pending.js';
import { S } from '../../state.js';
import { $, esc, fill } from '../../util.js';
import { echoMode, setEchoMode } from '../../voice-echo.js';
import { send } from '../../ws.js';
import { keyDraftKey, keyDrafts, settingValue } from '../plugin-setting.js';
export const prov = pendingSlot();
export const provBroken = pendingSlot();
export let configResult = null;
export const speechAdd = pendingSlot();

const fieldBase = new WeakMap();
export function editorFields(root) {
  return [...root.querySelectorAll('input,textarea,select')].filter(el =>
    !el.readOnly && el.type !== 'file' && (el.id || el.dataset.psetKey || el.dataset.grantField || (el.type === 'radio' && el.closest('.profile-form'))) && !el.id.startsWith('sp-provider-') &&
    !el.hasAttribute('data-immediate') &&
    (el.closest('[data-provider-editor]') || el.closest('.profile-form') || el.closest('#speech-form') || el.closest('.card')?.querySelector('[data-save]') || el.closest('[data-edit-form]')));
}
export const isEdited = el => fieldValue(el) !== fieldBase.get(el);
export function fieldValue(el) { return (el.type === 'checkbox' || el.type === 'radio') ? el.checked : el.value; }
export function fieldKey(el) {
  const scope = el.closest('[data-edit-scope]');
  let id = el.id || (el.dataset.grantField ? JSON.stringify(['grant', el.dataset.grantPlugin, el.dataset.grantField]) : el.type === 'radio' ? JSON.stringify([el.name, el.value]) : JSON.stringify([el.dataset.psetPlugin, el.dataset.psetKey, el.dataset.psetType]));
  if (scope && scope.hasAttribute('data-provider-editor')) id = id.replace(/-(\d+|new)$/, '');
  const frame = el.closest('#settings-stack, #plugins-stack');
  return JSON.stringify([frame?.id, frame?.dataset.section || 'plugins', scope ? scope.dataset.editScope : '', id, el.tagName, el.type]);
}
export function captureEdits(root, changed = null) {
  if (!root) {
    for (const id of ['settings-stack', 'plugins-stack']) if ($(id)) captureEdits($(id));
    return;
  }
  editorFields(root).forEach(el => {
    if (!fieldBase.has(el)) return;
    const key = fieldKey(el);
    if (el === changed || edits.has(key) || fieldValue(el) !== fieldBase.get(el) || (el.validity && el.validity.badInput)) edits.set(key, el);
  });
}
export function finishEditor(editor, secretsOnly = false) {
  captureEdits();
  if (!editor) return false;
  let newer = false;
  editor.fields.forEach((sent, key) => {
    const el = edits.get(key);
    if (!el || (secretsOnly && el.type !== 'password')) return;
    if (fieldValue(el) !== sent || (el.validity && el.validity.badInput)) { newer = true; return; }
    edits.delete(key);
    if (el.type === 'password') el.value = '';
    fieldBase.set(el, fieldValue(el));
  });
  return newer;
}
export function restoreEditors(root, active, twins = null) {
  editorFields(root).forEach(node => {
    const fresh = twins?.get(node) || node, inPlace = fresh !== node;
    const key = fieldKey(node), saved = fieldValue(fresh);
    const held = inPlace ? node : edits.get(key) || (active && active.key === key && active.el);
    if (!held) { fieldBase.set(fresh, saved); return; }
    const value = fieldValue(held), dirty = edits.has(key);
    [...held.attributes].forEach(a => { if (!['value', 'checked'].includes(a.name) && !fresh.hasAttribute(a.name)) held.removeAttribute(a.name); });
    [...fresh.attributes].forEach(a => { if (!['value', 'checked'].includes(a.name) && held.getAttribute(a.name) !== a.value) held.setAttribute(a.name, a.value); });
    if (held.tagName === 'SELECT') {
      held.innerHTML = fresh.innerHTML;
      if (dirty && ![...held.options].some(o => o.value === value)) held.add(new Option(value, value));
      held.value = dirty ? value : saved;
    } else if (!dirty) {
      if (held.type === 'checkbox' || held.type === 'radio') held.checked = saved; else if (held.value !== saved) held.value = saved;
    }
    fieldBase.set(held, saved);
    if (!inPlace) fresh.replaceWith(held);
  });
}

export function captureForm(root) {
  captureEdits(root);
  const el = document.activeElement;
  return el && root.contains(el) && editorFields(root).includes(el)
    ? { el, key: fieldKey(el), start: el.selectionStart, end: el.selectionEnd, direction: el.selectionDirection } : null;
}
export function restoreForm(root, active) {
  restoreEditors(root, active);
  root.oninput = root.onchange = e => captureEdits(root, e.target);
  if (active && root.contains(active.el)) {
    active.el.focus({ preventScroll: true });
    if (active.start !== null && active.start !== undefined) active.el.setSelectionRange(active.start, active.end, active.direction);
  }
}
export function forgetForm(root) {
  if (root) editorFields(root).forEach(el => edits.delete(fieldKey(el)));
}

export function speechDraftSaved(section, choice) {
  if (choice?.sttLeft) draft.sttLeft = false;
  if (section === 'speech') { ['stt', 'tts'].forEach(dir => { if (draft[dir] === choice?.[dir]) draft[dir] = null; }); return; }
  const dir = section === 'speech_stt' ? 'stt' : section === 'speech_tts' ? 'tts' : '';
  if (dir && draft[dir] === choice) draft[dir] = null;
}

export const asking = {};
export const HEARD_FIELDS = [
  ['sp-heard-capacity', 'capacity', 'finals held', 1, 2048, 256],
  ['sp-heard-retention', 'retention_seconds', 'seconds each is held', 60, 86400, 1800],
  ['sp-heard-text', 'text_bytes', 'text bytes of each', 256, 16384, 8192],
  ['sp-heard-rows', 'page_rows', 'rows a read returns', 1, 50, 20],
];

export let limitDraft = null;
export function clearLimitDraft() { limitDraft = null; }
export function wireLimits(st) {
  wireLimitRows(st);
  st.querySelectorAll('[data-cog-unl], [data-cog-none]').forEach(box => { box.onchange = () => {
    const input = st.querySelector('[data-cog-key="' + (box.dataset.cogUnl || box.dataset.cogNone) + '"]');
    if (input) { input.disabled = box.checked; input.placeholder = box.checked ? '' : input.dataset.usual || ''; }
  }; });
  st.querySelectorAll('[data-lim-remove]').forEach(btn => { btn.onclick = () => {
    const block = limitBlock(S.config);
    limitSet(block, btn.dataset.limRemove, btn.dataset.limProvider, btn.dataset.limModel, undefined);
    limitDraft = block;
    renderSettings();
  }; });
  st.querySelectorAll('[data-lim-add]').forEach(btn => { btn.onclick = () => {
    const key = btn.dataset.limAdd, slug = key.replace(/[^a-z0-9]+/g, '-');
    const field = part => st.querySelector('#lim-add-' + part + '-' + slug) || {};
    const provider = field('p').value || '';
    const model = (field('m').value || '').trim();
    const lifted = !!field('u').checked;
    const n = Number(field('v').value);
    if (!provider) { configResult = { kind: 'bad', section: st.dataset.form || limitSection(key), text: 'Choose the provider the limit is for.' }; renderSettings(); return; }
    if (!lifted && (!Number.isInteger(n) || (field('v').value || '').trim() === '')) { configResult = { kind: 'bad', section: st.dataset.form || limitSection(key), text: 'A limit is a whole number' + (st.querySelector('#lim-add-u-' + slug) ? ', or unlimited.' : '.') }; renderSettings(); return; }
    const block = limitBlock(S.config);
    limitSet(block, key, provider, model, lifted ? LIMIT_WORD : n);
    limitDraft = block;
    renderSettings();
  }; });
}
export function dot(status) {
  const d = STATUS_DOT[status] || ['#777', 'not probed yet'];
  return '<span title="' + esc(d[1]) + '" style="display:inline-block;width:9px;height:9px;border-radius:50%;background:' + d[0] + ';margin-right:8px;flex:none"></span>';
}

export const controlName = el => el.tagName + (el.id ? '#' + el.id
  : [...el.attributes].filter(a => a.name.startsWith('data-')).map(a => '[' + a.name + '=' + a.value + ']').join(''));
export const alike = (root, tag, name) => [...root.querySelectorAll(tag)].filter(el => controlName(el) === name);
export function focusOf(st, el) {
  if (!el || !st?.contains(el)) return null;
  const name = controlName(el);
  return { tag: el.tagName, name, nth: alike(st, el.tagName, name).indexOf(el),
    start: el.selectionStart ?? null, end: el.selectionEnd ?? null, direction: el.selectionDirection ?? 'none' };
}
export function saveSettings(section, collecting = false) {
  if (section === 'contacts') { saveContacts(); return; }
  if (section === 'speech') { saveSpeechSettings(); return; }
  const ch = {};
  let service = null;
  if (section === 'storage') {
    ch['identity.db_format'] = $('cfg-db-format').value;
    ch['identity.db_optimize_levels'] = $('cfg-db-levels').checked;
    ch['identity.db_learn_dictionary'] = $('cfg-db-dictionary').checked;
  } else if (section === 'llm') {
    const provider = $('cfg-provider').value, model = $('cfg-model').value.trim();
    const was = (S.config && S.config.llm) || {};
    if (provider !== (was.provider || '') || model !== (was.model || '')) { ch['llm.provider'] = provider; ch['llm.model'] = model; }
    const tmo = num($('cfg-timeout').value); if (tmo > 0) ch['llm.timeout_seconds'] = tmo;
    const bad = collectLimits(ch);
    if (bad) { configResult = { kind: 'bad', section: section, text: bad }; return; }
    const ptmo = num($('cfg-probe-timeout').value); if (ptmo > 0) ch['llm.probe_timeout_seconds'] = ptmo;
    const retry = num($('cfg-refused-retry').value);
    if (retry !== ((S.config && S.config.llm && S.config.llm.refused_retry_max_seconds) || 0)) ch['llm.refused_retry_max_seconds'] = retry === 0 ? null : retry;
  } else if (section === 'plugins') {
    ch['plugins.autoload'] = $('cfg-plevel').value;
  } else if (section === 'catalog') {
    ch['plugins.catalog_url'] = $('cfg-caturl').value.trim();
    const every = num($('cfg-catevery').value);
    if (every !== ((S.config && S.config.plugins && S.config.plugins.catalog_refresh_minutes) || 60)) ch['plugins.catalog_refresh_minutes'] = every;
  } else if (section === 'plugin_runtime') {
    const mib = id => $(id).value.trim() === '' ? null : num($(id).value) * 1048576;
    ch['plugins.runtime.max_installed_bytes'] = mib('cfg-rt-installed');
    ch['plugins.runtime.max_files'] = num($('cfg-rt-files').value);
    ch['plugins.runtime.max_file_bytes'] = mib('cfg-rt-file');
    ch['plugins.runtime.max_compressed_bytes'] = mib('cfg-rt-archive');
    ch['plugins.runtime.max_depth'] = num($('cfg-rt-depth').value);
    ch['plugins.runtime.roots_kept'] = num($('cfg-rt-kept').value);
  } else if (section === 'updates') {
    const bad = collectLimits(ch);
    if (bad) { configResult = { kind: 'bad', section: section, text: bad }; return; }
    if (!Object.keys(ch).length) { configResult = { kind: 'good', section: section, text: 'Nothing to save \u2014 no setting was changed.' }; return; }
  } else if (section.startsWith('grants:')) {
    const id = section.slice('grants:'.length);
    document.querySelectorAll('[data-grant-plugin="' + id.replace(/"/g, '\\"') + '"]').forEach(el => {
      ch['plugins.grants.' + id + '.' + el.dataset.grantField] = !!el.checked;
    });
  } else if (section.startsWith('plugin:')) {

    const id = section.slice('plugin:'.length);
    document.querySelectorAll('[data-pset-plugin="' + id.replace(/"/g, '\\"') + '"]').forEach(el => {
      const v = settingValue(el);
      if (v !== undefined) ch['plugins.settings.' + id + '.' + el.dataset.psetKey] = v;
    });
  } else if (section === 'speech_stt' || section === 'speech_tts') {
    const dir = section.slice('speech_'.length);
    const sp = (S.config && S.config.speech) || {};
    const chosen = $('sp-provider-' + dir).value, own = ownServer(chosen, sp);
    const baseURL = own ? $('sp-base-' + dir).value.trim() : '';
    const provider = own ? ownServerName(baseURL) : chosen;
    if (own && !provider) { configResult = { kind: 'bad', section: section, text: 'An OpenAI-compatible engine needs the address its server answers at, such as http://localhost:8000/v1.' }; return; }
    const svc = speechService(provider, sp);
    ch['speech.' + dir + '.provider'] = draft[dir] === null && sp[dir]?.default ? '' : provider;
    ch['speech.' + dir + '.model'] = pickedValue('sp-model-' + dir);
    if (dir === 'stt') ch['speech.stt.language'] = $('sp-language-stt') ? $('sp-language-stt').value.trim() : '';
    else ch['speech.tts.voice'] = pickedValue('sp-voice-tts');
    const ceiling = $('sp-ceiling-' + dir);
    if (ceiling) {
      const n = ceiling.value.trim() === '' ? 0 : Number(ceiling.value);
      if (!Number.isInteger(n) || n < 0) { configResult = { kind: 'bad', section: section, text: 'A monthly ceiling is a whole number, or empty for none.' }; return; }
      ch['speech.' + dir + '.' + (dir === 'stt' ? 'monthly_minutes' : 'monthly_characters')] = n;
    }
    const box = $('sp-engine-settings-' + dir);
    if (box && svc && svc.plugin) {
      box.querySelectorAll('[data-pset-plugin]').forEach(el => {
        const v = settingValue(el);
        if (v !== undefined) ch['plugins.settings.' + el.dataset.psetPlugin + '.' + el.dataset.psetKey] = v;
      });
    }
    const keyEl = $('sp-key-' + dir);
    const key = keyEl ? keyEl.value.trim() : '';
    const unadded = !own && provider && svc && !svc.added, moved = own && (!svc || svc.endpoint !== baseURL);
    if (unadded || moved || key) {
      if (collecting) service = { provider, key, baseURL };
      else { readySpeechService(section, provider, key, baseURL, ch); return; }
    }
  } else if (section === 'speech_speakers') {
    const mode = $('sp-speakers-mode').value;
    const uids = mode === 'all' ? [] : [...new Set($('sp-speakers-uids').value.trim().split(/[\s,]+/).filter(Boolean))];
    const pol = { mode: mode, uids: uids };
    if (mode === 'ignore') pol.unidentified = $('sp-speakers-unid').value;
    ch['speech.speakers'] = pol;
  } else if (section === 'speech_heard') {
    const inForce = ((S.config && S.config.speech) || {}).heard_history || {};
    for (const [id, key] of HEARD_FIELDS) {
      const raw = $(id).value.trim(), n = raw === '' ? 0 : Number(raw);
      if (!Number.isInteger(n) || n < 0) { configResult = { kind: 'bad', section: section, text: 'Each heard history number is a whole number, or empty for its default.' }; return; }
      if (n !== (inForce[key] || 0)) ch['speech.heard_history.' + key] = n;
    }
  } else if (section === 'speech_mode') {
    const picked = name => document.querySelector('input[name="' + name + '"]:checked')?.value ?? '';
    const off = $('sp-provider-stt')?.value === '' && (draft.stt === '' || runsEngine('stt', S.config?.speech || {}));
    ch['speech.mode'] = { listen: off ? 'off' : picked('sp-mode-listen'), speak: picked('sp-mode-speak') };
  } else if (section === 'route') {
    const v = $('cfg-route').value;
    ch['certificate.route_mode'] = v.startsWith('relay:') ? 'relay' : 'direct';
    if (v.startsWith('relay:')) ch['certificate.relay_endpoint'] = v.slice('relay:'.length);
    const auto = document.querySelector('input[name="cfg-route-addr"][value="auto"]');
    if (auto) {
      const have = S.config?.public_name?.direct_addresses ?? null;
      const want = auto.checked ? null : [...document.querySelectorAll('[data-route-addr]:checked')].map(e => e.value);
      if ((want === null || want.length) && JSON.stringify(want) !== JSON.stringify(have)) ch['certificate.direct_addresses'] = want;
    }
  } else if (section === 'dashboard') {
    ch['dashboard.host'] = $('cfg-dhost').value.trim();
    ch['dashboard.port'] = num($('cfg-dport').value);
    ch['dashboard.tls'] = !!$('cfg-dtls').checked;
  } else if (section === 'limits') {
    const tokens = num($('cfg-ptokens').value), turns = num($('cfg-pturns').value);
    if (tokens !== (S.config?.prompt?.max_tokens || 0)) ch['prompt.max_tokens'] = tokens;
    if (turns !== (S.config?.prompt?.recent_turns || 0)) ch['prompt.recent_turns'] = turns;
    const bad = collectLimits(ch, $('technical-stack'));
    if (bad) { configResult = { kind: 'bad', section: 'technical', text: bad }; return; }
  } else if (section === 'witness') {
    ch['witness.url'] = $('cfg-wurl').value.trim();
    ch['witness.interval_events'] = num($('cfg-wint').value);
  } else if (section === 'cognition') {
    const bad = collectLimits(ch, $('technical-stack'));
    if (bad) { configResult = { kind: 'bad', section: 'technical', text: bad }; return; }
    const at = $('cog-brief-off').checked ? 'off' : $('cog-brief-at').value.trim();
    const hadAt = (S.config && S.config.cognition && S.config.cognition.morning_brief_at) || '';
    if (at !== hadAt) ch['cognition.morning_brief_at'] = at === '' ? null : at;
  } else if (section === 'logs') {
    ch['logs.dir'] = $('cfg-ldir').value.trim();
    ch['logs.max_backups'] = $('cfg-lbackups-all').checked ? -1 : num($('cfg-lbackups').value);
    ch['logs.compress_days'] = $('cfg-lcomp-never').checked ? -1 : num($('cfg-lcomp').value);
    for (const [id, key] of [['cfg-lbytes', 'logs.max_bytes'], ['cfg-ltotal', 'logs.max_total_bytes']]) {
      const el = $(id), typed = el.value.trim();
      if (typed !== el.dataset.cogShown) ch[key] = typed === '' ? null : Math.round(Number(typed) * 1048576);
    }
  }
  if (collecting) return { changes: ch, service };
  sendConfigChanges(section, ch, submittedEditor(section));
}

export function sendConfigChanges(section, ch, editor = null, echo) {
  if (config.waiting() || speechAdd.waiting()) return;
  configResult = null;
  sendConfigStep({ section, changes: ch, editor, echo, keys: keyDraftsFor(section), keysSaved: false });
}
export function configBusy() { return !!config.waiting() || !!speechAdd.waiting(); }
export function configWaitingFor(section) { const w = config.waiting(); return w && w.section === section ? w : null; }

function keyDraftsFor(section) {
  const plugin = section.startsWith('plugin:') ? section.slice('plugin:'.length) : '';
  const drawn = section === 'speech' ? new Set([...document.querySelectorAll('#speech-form [data-pkey-input]')].map(el => keyDraftKey(el.dataset.pkeyPlugin, el.dataset.pkeyKey))) : null;
  if (!plugin && !drawn) return [];
  return [...keyDrafts].filter(([k, value]) => (plugin ? k.split('\u0000')[0] === plugin : drawn.has(k)) && value.trim())
    .map(([draftKey, value]) => ({ draftKey, plugin: draftKey.split('\u0000')[0], key: draftKey.split('\u0000')[1], value }));
}

function sendConfigStep(pending) {
  delete pending.requestID;
  pending.key = pending.keys.shift() || null;
  const key = pending.key;
  const id = send(key ? { type: 'plugin_key_set', plugin_key: { plugin: key.plugin, key: key.key, secret: key.value.trim() } }
    : { type: 'config_set', config: pending.changes });
  if (!id) {
    configResult = { kind: 'bad', section: pending.section, text: (pending.keysSaved ? 'Earlier keys were saved. ' : '') + (pending.section === 'llm' ? modelSays('notConnected') : 'Not connected — remaining settings were not sent. Unsent edits are kept.') };
    return;
  }
  if (key) {
    if (keyDrafts.get(key.draftKey) === key.value) keyDrafts.delete(key.draftKey);
    delete key.value;
  }
  config.arm(pending, id);
}

export function saveConfigSection(section) { saveSettings(section); }
export function configFeedbackHTML() {
  if (configResult && !configResult.section) return '<div class="config-result ' + configResult.kind + '" data-announce>' + esc(configResult.text) + '</div>';
  return '';
}

export function configSaidHTML(section) {
  const checking = config.waiting() && config.waiting().section === section;
  const said = configResult && configResult.section === section ? configResult : null;
  return checking ? '<span class="savesay" data-announce>Checking and saving… the current configuration remains active.</span>'
    : said ? '<span class="config-result ' + said.kind + '" data-said="' + esc(section) + '" data-announce>' + esc(said.text) + '</span>' : '';
}

export function modelSaidHTML() {
  const asked = configWaitingFor('llm');
  if (asked) return '<span class="savesay">' + esc(savesAModel(asked.changes) ? modelSays('checking', asked.changes) : 'Checking and saving\u2026 the current configuration remains active.') + '</span>';
  return configResult && configResult.section === 'llm' ? '<span class="config-result ' + configResult.kind + '">' + esc(configResult.text) + '</span>' : '';
}

export function savebarHTML(section, note, more) {
  const owns = s => s === section || (section === 'speech' && s?.startsWith('speech_'));
  const adding = speechAdd.waiting() && owns(speechAdd.waiting().section) ? speechAdd.waiting() : null;
  const checking = config.waiting() && owns(config.waiting().section);
  const said = configResult && owns(configResult.section) ? configResult : null;
  const middle = adding ? '<span class="savesay" data-announce>Saving ' + esc(adding.name) + '… waiting for the runtime to confirm.</span>'
    : checking ? '<span class="savesay" data-announce>' + esc(section === 'llm' && savesAModel(config.waiting().changes) ? modelSays('checking', config.waiting().changes) : 'Checking and saving… the current configuration remains active.') + '</span>'
    : said ? '<span class="config-result ' + said.kind + '" data-said="' + esc(section) + '" data-announce>' + esc(said.text) + '</span>'
    : note ? '<span class="savesay">' + esc(note) + '</span>' : '';
  const mine = !!adding || checking;
  const busy = mine || !!speechAdd.waiting() || !!config.waiting();
  return '<div class="savebar"><button class="btn" data-save="' + esc(section) + '"' + (busy ? ' disabled' : '') + '>' +
    (mine ? 'Checking…' : ((more && more.label) || 'Save')) + '</button>' + middle + ((more && more.also) || '') + '</div>';
}

export function configValue(state, path) {
  const parts = path.split('.');
  let v = state;
  for (const p of parts) v = v == null ? undefined : v[p];
  return v;
}

function savedValue(state, path) {
  if (path === 'identity.db_format') return state && state.database && state.database.preferred;
  if (path === 'overrides') return (state && state.cognition && state.cognition.overrides_block) || null;
  if (path === 'certificate.route_mode') return state?.public_name?.route_mode;
  if (path === 'certificate.relay_endpoint') return state?.public_name?.relay_endpoint;
  if (path === 'certificate.direct_addresses') return state?.public_name?.direct_addresses ?? null;
  if (path === 'speech.mode' && state?.speech?.mode) {
    const mode = state.speech.mode;
    return { ...mode, speak: mode.speak === 'auto' ? '' : mode.speak };
  }
  if (path === 'speech.speakers' && state?.speech?.speakers) {
    const policy = state.speech.speakers;
    return { ...policy, uids: policy.uids || [] };
  }
  const half = /^speech\.(stt|tts)\.provider$/.exec(path);
  if (half && state?.speech?.[half[1]]?.default) return '';
  if (path === 'identity.db_optimize_levels') return !!(state && state.database && state.database.optimize_levels);
  if (path === 'identity.db_learn_dictionary') return !!(state && state.database && state.database.learn_dictionary);
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
function sameSaved(saved, sent) {
  if (saved === sent) return true;
  if (Array.isArray(sent)) return Array.isArray(saved) && saved.length === sent.length && sent.every((v, i) => sameSaved(saved[i], v));
  if (sent && typeof sent === 'object') return saved && typeof saved === 'object' && Object.keys(sent).every(k => sameSaved(saved[k], sent[k]));
  return (saved === null || saved === undefined) && (sent === null || sent === undefined);
}

export function acceptSettingsConfig(requestID) {

  const pending = config.claim(requestID);
  if (!pending) return false;
  if (pending.key) {
    const key = pending.key;
    const setting = S.config?.plugins?.installed?.find(p => p.id === key.plugin)?.settings?.find(s => s.key === key.key);
    if (!setting?.key_kept) {
      configResult = { kind: 'bad', section: pending.section, text: (pending.keysSaved ? 'Earlier keys were saved. ' : '') + 'The host answered, but the key was not confirmed as kept. Remaining settings were not sent; paste the key again to retry.' };
      return true;
    }
    pending.keysSaved = true;
    sendConfigStep(pending);
    return true;
  }
  const missed = Object.keys(pending.changes).filter(p => p !== 'overrides_digest').filter(p => {
    let sent = pending.changes[p], saved = savedValue(S.config, p);
    const heard = p.startsWith('speech.heard_history.') && HEARD_FIELDS.find(f => 'speech.heard_history.' + f[1] === p);
    if (heard && sent === 0) sent = heard[5];
    if (p === 'speech.speakers' && sent && saved) {
      sent = { ...sent, uids: [...new Set(sent.uids || [])].sort() };
      saved = { ...saved, uids: [...(saved.uids || [])].sort() };
    }
    if (p === 'llm.refused_retry_max_seconds' && sent === null) sent = 0;
    return !sameSaved(saved, sent);
  });
  if (missed.length) {
    const labels = { 'speech.speakers': 'Who is heard', 'speech.mode': 'Conversation mode', 'speech.operator_speakers': 'Your voice' };
    for (const f of HEARD_FIELDS) labels['speech.heard_history.' + f[1]] = 'Heard history (' + f[2] + ')';
    configResult = { kind: 'bad', section: pending.section, text: 'Save could not be verified for ' + missed.map(p => labels[p] || p).join(', ') +
      '. Your edits are kept; review the saved values and try again.' };
    return true;
  }
  const echoMoved = pending.echo !== undefined && pending.echo !== echoMode();
  if (pending.echo !== undefined) {
    try { setEchoMode(pending.echo); }
    catch (e) {
      configResult = { kind: 'bad', section: pending.section, text: WVOICE.savedButEcho };
      return true;
    }
  }
  let newer = finishEditor(pending.editor);
  if (pending.editor?.saved?.()) newer = true;
  if (pending.editor) speechDraftSaved(pending.section, pending.editor.choice);
  if ('overrides' in pending.changes) limitDraft = null;
  configResult = { kind: 'good', section: pending.section, text: savedText(pending.section, (S.config && S.config.restart_required) || [], pending.changes, echoMoved) };
  const speaks = S.config?.speech?.tts;
  if (pending.section === 'speech' && pending.editor?.choice?.tts === '') {
    browserGoneAnswered();
    if (speaks?.default) configResult = { kind: 'bad', section: 'speech', text: fill(WVOICE.browserGone, { engine: speechService(speaks.provider, S.config.speech)?.title || speaks.provider }) };
  }
  const newerKey = pending.section.startsWith('plugin:') ? [...keyDrafts.keys()].some(k => k.split('\u0000')[0] === pending.section.slice('plugin:'.length))
    : pending.section === 'speech' && keyDraftsFor('speech').length > 0;
  if (newer || newerKey) configResult.text += ' Newer edits remain unsaved.';
  return true;
}

export function savedText(section, restartRequired, changes, echoMoved = false) {
  if (section === 'technical') {
    const keys = Object.keys(changes || {}).filter(k => k !== 'overrides_digest');
    if (keys.length && keys.every(k => k.startsWith('identity.db_'))) return WT.storage.saved;
    const when = [...new Set(keys.map(k => k.startsWith('witness.') || k.startsWith('logs.') ? 'applies at the next start'
      : k.startsWith('identity.db_') ? WT.storage.saved.replace(/^Saved\. /, '').replace(/\.$/, '')
      : k === 'cognition.morning_brief_at' ? 'applies at the next pass' : cognitionApplies(k)))];
    return 'Saved' + (when.length ? ' \u2014 ' + when.join('; ') : '') + '.';
  }
  if (section === 'speech') return echoMoved ? WVOICE.savedEcho : WVOICE.saved;
  if (section === 'storage') return WT.storage.saved;
  if (section === 'llm' && modelAnswered()) return modelSays('adopted', changes);
  if (section === 'speech_stt' || section === 'speech_tts') {
    const dir = section.slice('speech_'.length), provider = changes && changes['speech.' + dir + '.provider'];
    if (!provider) return dir === 'stt' ? WVOICE.savedVoiceOff : WVOICE.savedBrowserVoice;
    return 'Active — ' + provider + (dir === 'stt' ? ' transcribed the check.' : ' spoke the check.');
  }
  if (section === 'speech_speakers') return WVOICE.savedNextWords;
  if (section === 'speech_my_voice') return 'Saved — applies to the next words heard, and to the next change cited.';
  if (section === 'speech_heard') return 'Saved — applies to the next words heard and the next read.';
  if (section === 'contacts') return WPEOPLE.contactsSaved;
  if (section === 'route') return WACCESS.routeSaved;
  if (section === 'who') return WACCESS.whoSaved;
  if (section === 'limits') {
    const when = [...new Set(Object.keys(changes || {}).filter(k => k !== 'overrides_digest').map(cognitionApplies))];
    return 'Saved' + (when.length ? ' \u2014 ' + when.join('; ') : '') + '.';
  }
  if (section === 'cognition') {
    const when = [...new Set(Object.keys(changes || {}).filter(k => k !== 'overrides_digest').map(k => k === 'cognition.morning_brief_at' ? 'applies at the next pass' : cognitionApplies(k)))];
    return 'Saved — ' + (when.length ? when.join('; ') : 'applies at the next pass') + '.';
  }
  if (restartRequired.length) return 'Saved — takes effect after restart: ' + restartRequired.join(', ') + '.';
  return 'Saved.';
}

export function rejectSettingsConfig(message, requestID) {
  const asked = config.waiting();
  if (!config.claim(requestID)) return false;
  configResult = { kind: 'bad', section: asked && asked.section, text: (asked?.keysSaved ? 'Earlier keys were saved. ' : '') + (asked?.key ? 'Key save failed; remaining settings were not sent. ' : '') + message };
  renderSettings();
  return true;
}

export function setConfigResult(v) { configResult = v; }
