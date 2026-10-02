
import { startSignIn, signInProgress, signInWanted, credentialExpired, wireSignInCompletion } from '../signin.js';
import { S } from '../state.js';
import { settingHTML, keyDrafts } from './plugin-setting.js';
import { $, copyText, esc } from '../util.js';
import { renderInto } from '../announce.js';
import { send, query } from '../ws.js';
import { spokenAudio } from '../say.js';
import { echoMode, echoModes, setEchoMode } from '../voice-echo.js';
import { sandboxCardHTML, wireSandboxCard } from '../sandbox.js';
import { pendingSlot } from '../pending.js';
import { holdReloadWhile } from '../overlay.js';
import { providerModels } from './model-picker.js';
import { backupsHTML, wireBackups, requestBackups, forgetBackupsSecrets } from './backups.js';
import { mailRepairHTML, wireMailRepair, requestHeldMail, mailRepairDisconnected } from './mail-repair.js';
import { messagesHTML, wireMessages, requestMessages, messagesDisconnected, installedChannels } from './messages.js';

const SECTIONS = [
  ['substrate', 'Substrate'], ['providers', 'Providers'], ['speech', 'Speech'],
  ['dashboard', 'Dashboard'], ['backups', 'Backups & Keys'], ['witness', 'Witness'],
  ['prompt', 'Prompt'], ['agency', 'Agency'], ['logs', 'Logs'], ['sandbox', 'Sandbox'], ['tools', 'Tools'],
  ['storage', 'Storage'], ['messages', 'Messages'], ['held-mail', 'Held mail'], ['updates', 'Updates']];
let sec = 'substrate';
let provOpen = null;
const NEW_PROVIDER = Symbol('new provider');
const prov = pendingSlot();
const provBroken = pendingSlot();
let provResult = null;
const config = pendingSlot();
let configResult = null;
const speechAdd = pendingSlot();

const edits = new Map();
const fieldBase = new WeakMap();
holdReloadWhile(() => edits.size > 0 || keyDrafts.size > 0 || draft.stt !== null || draft.tts !== null);
function editorFields(root) {
  return [...root.querySelectorAll('input,textarea,select')].filter(el =>
    !el.readOnly && el.type !== 'file' && (el.id || el.dataset.psetKey || el.dataset.grantField || (el.type === 'radio' && el.closest('.profile-form'))) && !el.id.startsWith('sp-provider-') &&
    (el.closest('[data-provider-editor]') || el.closest('.profile-form') || el.closest('#speech-form') || el.closest('.card')?.querySelector('[data-save]')));
}
function fieldValue(el) { return (el.type === 'checkbox' || el.type === 'radio') ? el.checked : el.value; }
function fieldKey(el) {
  const scope = el.closest('[data-edit-scope]');
  let id = el.id || (el.dataset.grantField ? JSON.stringify(['grant', el.dataset.grantPlugin, el.dataset.grantField]) : el.type === 'radio' ? JSON.stringify([el.name, el.value]) : JSON.stringify([el.dataset.psetPlugin, el.dataset.psetKey, el.dataset.psetType]));
  if (scope && scope.hasAttribute('data-provider-editor')) id = id.replace(/-(\d+|new)$/, '');
  const frame = el.closest('#settings-stack, #plugins-stack');
  return JSON.stringify([frame?.id, frame?.dataset.section || 'plugins', scope ? scope.dataset.editScope : '', id, el.tagName, el.type]);
}
function captureEdits(root, changed = null) {
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
function showDraftNotice(root) {
  const note = root.querySelector('[data-settings-draft]');
  if (note) note.hidden = !editorFields(root).some(el => edits.has(fieldKey(el)));
}
function submittedEditor(section, root) {
  captureEdits();
  const stack = $('settings-stack');
  if (!root && section === 'speech') root = $('speech-form');
  if (!root && section.startsWith('speech_')) root = stack?.querySelector('[data-speech-section="' + section + '"]');
  if (!root) {
    const button = [...document.querySelectorAll('#settings-stack [data-save], #plugins-stack [data-save]')].find(b => b.dataset.save === section);
    root = button && button.closest('.card');
  }
  if (!root) return null;
  const dir = section === 'speech_stt' ? 'stt' : section === 'speech_tts' ? 'tts' : '';
  const fields = editorFields(root).filter(el => {
    if (!el.closest('#plugins-stack')) return true;
    if (section.startsWith('plugin:')) return el.dataset.psetPlugin === section.slice('plugin:'.length);
    if (section.startsWith('grants:')) return el.dataset.grantPlugin === section.slice('grants:'.length);
    if (section === 'plugins') return el.id === 'cfg-plevel';
    if (section === 'catalog') return el.id === 'cfg-caturl';
    if (section === 'plugin_runtime') return el.id.startsWith('cfg-rt-');
    return false;
  });
  return { fields: new Map(fields.map(el => [fieldKey(el), fieldValue(el)])),
    choice: section === 'speech' ? { ...draft } : dir ? draft[dir] : null, owner: provOpen };
}
function finishEditor(editor, secretsOnly = false) {
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
function restoreEditors(root, active) {
  editorFields(root).forEach(fresh => {
    const key = fieldKey(fresh), saved = fieldValue(fresh);
    const held = edits.get(key) || (active && active.key === key && active.el);
    if (!held) { fieldBase.set(fresh, saved); return; }
    const value = fieldValue(held), dirty = edits.has(key);
    [...held.attributes].forEach(a => { if (!['value', 'checked'].includes(a.name) && !fresh.hasAttribute(a.name)) held.removeAttribute(a.name); });
    [...fresh.attributes].forEach(a => { if (!['value', 'checked'].includes(a.name) && held.getAttribute(a.name) !== a.value) held.setAttribute(a.name, a.value); });
    if (held.tagName === 'SELECT') {
      held.innerHTML = fresh.innerHTML;
      if (dirty && ![...held.options].some(o => o.value === value)) held.add(new Option(value, value));
      held.value = dirty ? value : saved;
    } else if (!dirty) {
      if (held.type === 'checkbox' || held.type === 'radio') held.checked = saved; else held.value = saved;
    }
    fieldBase.set(held, saved);
    fresh.replaceWith(held);
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

function cfgField(id, label, value, type) {
  return '<label class="f">' + esc(label) + '</label><input type="' + (type || 'text') + '" id="' + id + '" value="' + esc(value == null ? '' : value) + '">';
}

function storageHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';
  const db = c.database || {};
  const formats = [['', 'Keep current format'], ['sqlite', 'Ordinary SQLite'], ['zstd', 'Compressed SQLite (Zstd)']];
  return '<div class="card"><h3>DATABASE STORAGE</h3>' +
    '<p>Active format: <strong id="db-active">' + esc(db.active || 'unavailable') + '</strong></p>' +
    '<label class="f" for="cfg-db-format">FORMAT AT NEXT NORMAL STARTUP</label><select id="cfg-db-format">' +
    formats.map(([value, label]) => '<option value="' + value + '"' + ((db.preferred || '') === value ? ' selected' : '') + '>' + label + '</option>').join('') + '</select>' +
    '<p>Saving does not convert the running database or restart the app. At the next normal startup, the current data is converted and verified before use. Compression is not encryption. Routine maintenance uses the existing maintenance alarm.</p>' +
    '<h3>SELF-OPTIMIZATION AT MAINTENANCE</h3>' +
    '<label><input type="checkbox" id="cfg-db-levels"' + (db.optimize_levels ? ' checked' : '') + '> Optimize compression levels</label><br>' +
    '<label><input type="checkbox" id="cfg-db-dictionary"' + (db.learn_dictionary ? ' checked' : '') + '> Learn a local compression dictionary</label>' +
    '<p>These independent options default off and apply at the next maintenance pass on a compressed database. They can use more CPU, pause database access longer, and need temporary disk space. A rewrite is kept only when it saves space including its dictionary. Enabling learning does not mean a useful dictionary has been found.</p>' +
    '<p>Dictionary currently in the opened database: <strong id="db-dictionary-bytes">' + esc(db.dictionary_bytes == null ? 'unavailable' : String(db.dictionary_bytes) + ' bytes') + '</strong>. Dictionaries stay inside this private database, never uploaded. Turning learning off stops training; it does not remove a dictionary needed to read existing data or erase its retained fragments.</p>' +
    savebarHTML('storage', 'format: next startup; optimization: next maintenance') +
    (db.notice ? '<p data-announce>' + esc(db.notice) + '</p>' : '') +
    ((db.recovery || []).length ? '<p>Conversion working material retained for recovery; never restored automatically:</p><ul>' + db.recovery.map(path => '<li>' + esc(path) + '</li>').join('') + '</ul>' : '') +
    '<h3>EXPORT A COPY</h3><p>Downloads verified ordinary SQLite without changing the active format. This unencrypted file contains private conversations and runtime data; store it privately. It is not a complete identity backup.</p>' +
    (db.can_export ? '<form method="post" action="/database/export" style="display:inline"><button class="btn" type="submit" data-export-db>Download SQLite copy</button></form>' : '<button class="btn" disabled>Download SQLite copy unavailable</button>') + '</div>';
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

function contextSummary(llm) {
  const source = llm.prompt_budget_source || '';
  const figure = llm.context_length || llm.prompt_budget || 0;
  if (!source || !figure) return esc(llm.context_length || '—');
  if (source === 'fallback') return esc(figure) + ' (fallback — set context length on the provider)';
  return esc(figure) + ' (' + esc(source) + ')';
}

function substrateHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';

  const cur = c.llm.provider || '';
  const candidates = S.providers.filter(p => p.chat !== false);
  const provSel = '<label class="f">PROVIDER (providers.json ENTRY; BLANK = DEFAULT-FLAGGED)</label>' +
    '<select id="cfg-provider"><option value=""' + (cur === '' ? ' selected' : '') + '>(default-flagged entry)</option>' +
    candidates.map(p => '<option value="' + esc(p.name) + '"' + (p.name === cur ? ' selected' : '') + '>' + esc(p.name) + '</option>').join('') +
    '</select>';
  const resolved = c.llm.error
    ? '<div style="font-size:11.5px;color:#c0392b;margin-top:8px">pointer does not resolve: ' + esc(c.llm.error) + '</div>'
    : '<div style="font-size:11.5px;color:var(--faint);margin-top:8px;line-height:1.6">resolved: <span style="font-family:var(--mono)">' + esc(c.llm.endpoint) + '</span>' +
      ' · key ' + esc(c.llm.api_key_masked || 'none') +
      ' · context ' + contextSummary(c.llm) +
      ' · max out ' + (c.llm.max_output_tokens || '—') +

      (c.llm.thinking_applies ? ' · thinking ' + (c.llm.thinking_budget || '—') : '') +
      ' · effort ' + esc(c.llm.effort_plan || 'provider default (none set)') +
      ' · active ' + esc((c.llm.resolved_provider || '—') + ' / ' + (c.llm.resolved_model || '—')) + '</div>';
  return '<div class="card"><h3>SUBSTRATE — LLM</h3>' +
    provSel +
    modelField('cfg-model', cur, c.llm.model) +
    cfgField('cfg-timeout', 'TIMEOUT (SECONDS)', c.llm.timeout_seconds) +
    cfgField('cfg-probe-timeout', 'SUBSTRATE CHECK (SECONDS)', c.llm.probe_timeout_seconds) +
    resolved +
    '<div style="font-size:11.5px;color:var(--faint);margin-top:8px">providers.json owns the provider data; this card points at an entry. Endpoint, key, context/output budgets, REASONING EFFORT, and THINKING mode are edited on the entry in the Providers section. A substrate change applies only after the candidate completes a real inference request.</div>' +
    savebarHTML('llm', 'applies live after inference check') + '</div>';
}

const SPEECH_DIRS = {
  stt: {
    title: 'VOICE INPUT — SPEECH TO TEXT', kind: 'transcription model',
    none: 'Off', off: 'Voice input is off.',
    unsetProvider: 'No speech-to-text engine. The microphone opens this section until one is chosen.',
  },
  tts: {
    title: 'VOICE REPLIES — TEXT TO SPEECH', kind: 'model',
    none: 'Browser voice', off: 'Replies are spoken with your browser\'s built-in voice.',
    unsetProvider: 'Replies are spoken with your browser\'s built-in voice.',
    note: 'Speaks every reply — to what you type, and to what you say in a conversation. With nothing chosen, the engine that hears a conversation answers in its own voice. Another engine speaks a conversation\'s replies only where a page holds its speaker open; elsewhere, and whenever the voice chosen cannot speak, the browser\'s own voice does.',
  },
};
const OWN_SERVER = 'own-server:';
const BY_ID = 'by-id:';

const draft = { stt: null, tts: null };
export function speechDraftSaved(section, choice) {
  if (section === 'speech') { ['stt', 'tts'].forEach(dir => { if (draft[dir] === choice?.[dir]) draft[dir] = null; }); return; }
  const dir = section === 'speech_stt' ? 'stt' : section === 'speech_tts' ? 'tts' : '';
  if (dir && draft[dir] === choice) draft[dir] = null;
}

function speechServices(dir, sp) {
  return ((sp && sp.services) || []).filter(s => s.speech && s.speech[dir]);
}
function speechService(value, sp) {
  return ((sp && sp.services) || []).find(s => s.name === value) || null;
}

function speechOffer(dir, value, sp) {
  if (!value) return null;
  const svc = value === OWN_SERVER ? null : speechService(value, sp);
  if (!svc || !svc.speech) return { model_required: true, voice_required: dir === 'tts' };
  return svc.speech[dir] || null;
}
function ownServer(value, sp) {
  const svc = speechService(value, sp);
  return value === OWN_SERVER || !!(svc && svc.custom);
}
function ownServerName(url) {
  try {
    const u = new URL(url);
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.host ? 'OpenAI-compatible · ' + u.host : '';
  } catch (e) { return ''; }
}

function hinted(value, hint) {
  return ' data-unset-hint="' + esc(hint) + '"' + (String(value || '').trim() ? '' : ' title="' + esc(hint) + '"');
}
function syncUnsetHint(el) {
  if (String(el.value || '').trim()) el.removeAttribute('title');
  else el.title = el.dataset.unsetHint;
}
function wireUnsetHints(root) {
  root.querySelectorAll('[data-unset-hint]').forEach(syncUnsetHint);
}

function keyFacts(value, sp) {
  if (!value) return { label: 'API KEY', hint: '', note: '' };
  if (value === OWN_SERVER) return { label: 'API KEY (OPTIONAL)', hint: 'Leave blank for a server that asks for no key.', note: 'Kept with the server\'s entry in providers.json.' };
  const entry = (S.providers || []).find(p => p.name === value);
  const svc = speechService(value, sp) || { name: value, added: !!entry, has_key: !!(entry && entry.has_key), chats: !!(entry && entry.chat) };
  const stored = !!svc.has_key;
  const env = svc.api_key_env && !stored ? '; left blank, ' + svc.api_key_env + ' is used if it is set.' : '.';
  const where = !svc.added && svc.speech ? 'Adds ' + svc.name + ' to providers.json with this key' + (svc.chats ? ', where its chat models use it too' : '')
    : svc.chats ? 'Shared with the ' + svc.name + ' chat provider'
    : 'Kept with ' + svc.name + ' in providers.json';
  return {
    label: stored ? 'API KEY (ONE STORED — ENTER TO REPLACE)' : 'API KEY (NONE STORED)',
    hint: stored ? 'Blank keeps the key stored for ' + svc.name + '.'
      : svc.custom ? 'Leave blank for a server that asks for no key.'
      : 'No key is stored for ' + svc.name + ' — cloud speech services refuse a request without one.',
    note: where + env,
  };
}

const live = {};
const asking = {};
const asked = {};
const listKey = (value, dir) => value + '|' + dir;

function askSpeechLists(dir, value, sp, ask) {
  const local = speechService(value, sp);
  if (local && local.plugin) return;
  if (!value || value === OWN_SERVER) return;
  const o = speechOffer(dir, value, sp);
  if (!o || !(o.lists_models || o.lists_voices)) return;
  const typed = $('sp-key-' + dir) ? $('sp-key-' + dir).value.trim() : '';
  const want = { search: (ask && ask.search) || '', language: (ask && ask.language) || '', typed: typed };
  const key = listKey(value, dir), had = live[key] || asking[key];
  if (had && (had.search || '') === want.search && (had.language || '') === want.language && (had.typed || '') === typed) return;
  const request = query('speech_lists', { provider: value, direction: dir, search: want.search, language: want.language, api_key: typed });
  if (request) {
    want.request = request;
    asking[key] = want;
    asked[key] = { search: want.search, language: want.language };
  }
}
function listed(dir, value) {
  return live[listKey(value, dir)] || null;
}
function isAsking(dir, value) {
  return !!asking[listKey(value, dir)];
}
function forgetSpeechLists(name) {
  ['stt', 'tts'].forEach(dir => { delete live[listKey(name, dir)]; delete asking[listKey(name, dir)]; delete asked[listKey(name, dir)]; });
}

function languagesOf(got) {
  return (got && got.languages) || [];
}
function itemsOf(got, kind, language) {
  const items = (got && got[kind]) || [];
  if (!language || (got && got.language === language)) return items;
  const narrowed = items.filter(i => (i.languages || []).some(l => l === language || l.split('-')[0] === language.split('-')[0]));
  return narrowed.length ? narrowed : items;
}

function optionsHTML(items, value) {
  const known = items.some(i => i.id === value);
  return (value ? '' : '<option value="" selected>— none —</option>') +
    (value && !known ? '<option value="' + esc(value) + '" selected>' + esc(value) + '</option>' : '') +
    items.map(i => '<option value="' + esc(i.id) + '"' + (i.id === value ? ' selected' : '') + '>' +
      esc(i.name || i.id) + (i.detail ? esc(' — ' + i.detail) : '') + '</option>').join('') +
    '<option value="' + BY_ID + '">enter an id…</option>';
}

function pickerHTML(id, label, value, items, hint) {
  return '<label class="f">' + esc(label) + '</label>' +
    '<select id="' + id + '"' + hinted(value, hint) + '>' + optionsHTML(items, value) + '</select>' +
    '<input type="text" id="' + id + '-typed" hidden placeholder="the id the service knows it by" autocomplete="off">';
}

function pickedValue(id) {
  const typed = $(id + '-typed'), pick = $(id);
  if (typed && !typed.hidden) return typed.value.trim();
  const chosen = pick ? pick.value : '';
  return chosen === BY_ID ? '' : chosen;
}

function wirePicker(root, id) {
  const pick = root.querySelector('#' + id), typed = root.querySelector('#' + id + '-typed');
  if (!pick || !typed) return;
  typed.hidden = pick.value !== BY_ID;
  pick.onchange = () => {
    if (pick.value !== BY_ID) { typed.hidden = true; return; }
    typed.hidden = false;
    typed.focus();
  };
}

function playSample(button, provider) {
  const state = $('sp-sample-state');
  const said = m => { if (state) state.textContent = m; };
  const words = button.textContent;
  button.disabled = true;
  button.textContent = 'Playing…';
  said('');
  const done = () => { button.disabled = false; button.textContent = words; };
  spokenAudio({
    sample: true,
    provider: provider,
    model: pickedValue('sp-model-tts'),
    voice: pickedValue('sp-voice-tts'),
    api_key: $('sp-key-tts') ? $('sp-key-tts').value.trim() : '',
  }).then(() => { said('Playing ' + provider + '.'); done(); }, err => { said((err && err.message) || String(err)); done(); });
}

function spentLine(dir, sp, ceiling) {
  const spent = (sp.spent || []).filter(u => u.direction === dir);
  const total = spent.reduce((n, u) => n + (dir === 'stt' ? (u.seconds || 0) : (u.characters || 0)), 0);
  const each = spent.map(u => u.provider + ' ' + amount(dir, dir === 'stt' ? u.seconds : u.characters) +
    ' over ' + u.requests + ' ' + (dir === 'stt' ? (u.requests === 1 ? 'utterance' : 'utterances') : (u.requests === 1 ? 'reply' : 'replies'))).join(' · ');
  const cap = dir === 'stt' ? ceiling * 60 : ceiling;
  const left = ceiling ? ' · ' + amount(dir, Math.max(0, cap - total)) + ' of the ceiling left' : '';
  const resets = sp.resets ? ' · resets ' + sp.resets : '';
  return 'This month: ' + (each || 'nothing yet') + left + resets;
}

function amount(dir, n) {
  n = n || 0;
  if (dir !== 'stt') return String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ',') + ' characters';
  if (n < 60) return n + 's';
  const m = Math.floor(n / 60);
  return m + 'm ' + (n % 60) + 's';
}

function stateLine(dir, value, kind) {
  const got = listed(dir, value);
  if (!value) return '';
  if (value === OWN_SERVER) return 'Type the id your server expects.';
  if (isAsking(dir, value)) return 'Asking ' + value + '…';
  if (!got) return '';
  if (got.needs_key) return value + ' lists its ' + kind + ' once a key is stored above.';
  const why = kind === 'voices' ? got.voices_error : got.models_error;
  if (why) return why + (why.includes('(401') || why.includes('(403') ? ' — check the key.' : '');
  const items = (got[kind] || []).length;
  const whole = kind === 'voices' ? got.voices_complete : got.models_complete;
  if (!items) return value + ' listed no ' + kind + '.';
  return items + ' ' + (items === 1 ? kind.replace(/s$/, '') : kind) + ' from ' + value +
    (whole ? '' : ' — narrow the search to see the rest') + '.';
}

function speechCard(dir, sp) {
  const d = SPEECH_DIRS[dir], inForce = sp[dir] || {};
  const provider = draft[dir] !== null ? draft[dir] : (inForce.provider || '');
  const cur = provider === (inForce.provider || '') ? inForce : { provider: provider };
  const services = speechServices(dir, sp);
  const known = !provider || services.some(s => s.name === provider);
  const o = speechOffer(dir, provider, sp);
  const svc = speechService(provider, sp);
  const own = ownServer(provider, sp);
  const got = listed(dir, provider);
  const engine = '<label class="f">ENGINE</label><select id="sp-provider-' + dir + '"' + hinted(provider, d.unsetProvider) + '>' +
    '<option value=""' + (provider ? '' : ' selected') + '>' + esc(d.none) + '</option>' +
    services.map(s => '<option value="' + esc(s.name) + '"' + (s.name === provider ? ' selected' : '') + '>' + esc(s.title || s.name) + '</option>').join('') +
    (known ? '' : '<option value="' + esc(provider) + '" selected>' + esc(provider) + ' (not a speech service)</option>') +
    '<option value="' + OWN_SERVER + '">OpenAI-compatible…</option>' +
    '</select>';
  const baseValue = own && svc ? svc.endpoint : '';
  const base = '<div id="sp-base-row-' + dir + '"' + (own ? '' : ' hidden') + '><label class="f">BASE URL</label>' +
    '<input type="text" id="sp-base-' + dir + '" value="' + esc(baseValue) + '" placeholder="Base URL, such as http://localhost:8000/v1"' +
    hinted(baseValue, 'Where the OpenAI-compatible server answers, such as http://localhost:8000/v1.') + '></div>';
  const isPlugin = !!(svc && svc.plugin);
  const k = keyFacts(provider, sp);
  const key = '<div id="sp-key-row-' + dir + '"' + (provider && !isPlugin ? '' : ' hidden') + '><label class="f" id="sp-key-label-' + dir + '">' + esc(k.label) + '</label>' +
    '<input type="password" id="sp-key-' + dir + '" autocomplete="off"' + hinted('', k.hint) + '>' +
    '<div id="sp-key-note-' + dir + '" style="font-size:11.5px;color:var(--faint);margin-top:6px">' + esc(k.note) + '</div></div>';

  const languages = languagesOf(got);
  const language = '<div id="sp-language-row-' + dir + '"' + (languages.length ? '' : ' hidden') + '><label class="f">LANGUAGE</label>' +
    '<select id="sp-language-' + dir + '">' + languageOptionsHTML(languages, cur.language || '') + '</select></div>';

  const wantsModel = !o || o.model_required;
  const model = wantsModel
    ? pickerHTML('sp-model-' + dir, 'MODEL', cur.model || '', itemsOf(got, 'models', cur.language),
      'Required — the ' + d.kind + ' this service is to use.') +
      '<div id="sp-model-state-' + dir + '" style="font-size:11.5px;color:var(--faint);margin-top:6px">' + esc(stateLine(dir, provider, 'models')) + '</div>'
    : '';
  const wantsVoice = dir === 'tts' && (!o || o.voice_required);
  const searchable = !!(o && o.searches_voices);
  const voice = wantsVoice
    ? (searchable ? '<label class="f">FIND A VOICE</label><input type="search" id="sp-find-tts" autocomplete="off" placeholder="' + esc('Search ' + (provider || 'the service') + '’s voices') + '">' : '') +
      pickerHTML('sp-voice-tts', 'VOICE', cur.voice || '', itemsOf(got, 'voices', cur.language), 'Required — the voice this service is to speak in.') +
      '<div id="sp-voice-state-tts" style="font-size:11.5px;color:var(--faint);margin-top:6px">' + esc(stateLine(dir, provider, 'voices')) + '</div>'
    : '';

  const d2 = dir === 'stt'
    ? { key: 'monthly_minutes', unit: 'minutes', of: 'listening', note: 'the microphone stops until the month turns over' }
    : { key: 'monthly_characters', unit: 'characters', of: 'speaking', note: 'replies are read by the browser\'s own voice until the month turns over' };
  const ceilingValue = (dir === 'stt' ? inForce.monthly_minutes : inForce.monthly_characters) || 0;
  const ceiling = isPlugin ? '' : '<label class="f">MONTHLY CEILING</label>' +
    '<input type="number" id="sp-ceiling-' + dir + '" min="0" step="1" value="' + (ceilingValue || '') + '" placeholder="no ceiling"' +
    hinted(ceilingValue ? String(ceilingValue) : '', 'The most ' + d2.unit + ' of ' + d2.of + ' this identity may buy in a calendar month. Reached, ' + d2.note + '. Empty is no ceiling.') + '>' +
    '<div id="sp-spent-' + dir + '" style="font-size:11.5px;color:var(--faint);margin-top:6px">' + esc(spentLine(dir, sp, ceilingValue)) + '</div>';

  const wantScope = dir === 'stt' ? 'hearing' : 'speaking';
  const tunables = isPlugin ? (svc.settings || []).filter(x => x.scope === wantScope) : [];
  const engineSettings = tunables.length
    ? '<div id="sp-engine-settings-' + dir + '" class="engine-settings">' +
      '<label class="f">' + esc((svc.title || svc.name).toUpperCase()) + ' — ' + (dir === 'stt' ? 'HEARING' : 'SPEAKING') + '</label>' +
      tunables.map(x => settingHTML(svc.name, x)).join('') + '</div>'
    : (isPlugin ? '<div id="sp-engine-settings-' + dir + '" class="engine-settings"></div>' : '');

  const unsaved = provider && provider !== inForce.provider && provider !== OWN_SERVER
    ? ' — <b>' + esc(provider) + ' is chosen and not saved</b>; Save to put it in force' : '';
  const readback = inForce.plugin
    ? '<div id="sp-readback-' + dir + '" style="font-size:11.5px;color:var(--faint);margin-top:8px;line-height:1.6">in force: <b>' + esc((svc && svc.title) || inForce.provider) + '</b> — installed on this machine' +
      (inForce.default ? ', and chosen because it is installed; pick a service above to use one instead' : '') + unsaved + '</div>'
    : !inForce.provider
    ? '<div id="sp-readback-' + dir + '" style="font-size:11.5px;color:var(--faint);margin-top:8px">' + esc(d.off) + unsaved + '</div>'
    : inForce.error
      ? '<div id="sp-readback-' + dir + '" style="font-size:11.5px;color:#c0392b;margin-top:8px">pointer does not resolve: ' + esc(inForce.error) + unsaved + '</div>'
      : '<div id="sp-readback-' + dir + '" style="font-size:11.5px;color:var(--faint);margin-top:8px;line-height:1.6">in force: <span style="font-family:var(--mono)">' + esc(inForce.endpoint || '') + '</span> · key ' + esc(inForce.api_key_masked || 'none') + unsaved + '</div>';
  const note = d.note ? '<p class="muted" id="sp-note-' + dir + '">' + esc(d.note) + '</p>' : '';
  return '<div class="card" data-speech-section="speech_' + dir + '" data-edit-scope="' + esc('speech:' + dir + ':' + provider) + '"><h3>' + d.title + '</h3>' + note + engine + base + key + language + model + voice + engineSettings + readback +
    ceiling +
      (dir === 'tts' ? '<button class="btn ghost" id="sp-sample-tts"' + (provider && provider !== OWN_SERVER && !isPlugin ? '' : ' disabled') + '>Play sample</button>' +
        '<span class="savesay" id="sp-sample-state" role="status" aria-live="polite"></span>' : '') + '</div>';
}

function speechHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';
  const sp = c.speech || { stt: {}, tts: {} };
  return '<div id="speech-form"><div class="card" style="position:sticky;top:0;z-index:2">' +
    '<h3>Speech settings</h3><p>Make your changes below, then save once. Listening rules apply immediately; engine and echo settings apply to the next voice session.</p>' +
    savebarHTML('speech', '', { label: 'Save speech settings' }) + '</div>' +
    voiceModeCard(sp) + speakersCard(sp) + speechCard('stt', sp) + speechCard('tts', sp) + '</div>';
}

function voiceModeCard(sp) {
  const m = sp.mode || { listen: 'interactive', speak: 'auto', revision: 0, name: 'interactive', set: false };
  const listen = m.listen || 'interactive';
  const speak = m.speak === 'on' || m.speak === 'off' ? m.speak : '';
  const opt = (v, cur, label) => '<option value="' + v + '"' + (cur === v ? ' selected' : '') + '>' + label + '</option>';
  return '<div class="card" id="sp-voice-mode" data-speech-section="speech_mode"><h3>Conversation</h3>' +
    '<p class="muted">What the microphone does and whether replies are spoken. One pair for this identity: the control in the composer shows the same mode, and the identity is told which one it is in.</p>' +
    '<label class="f">MICROPHONE</label><select id="sp-mode-listen">' +
    opt('interactive', listen, 'Interactive — the operator speaks to the identity') +
    opt('meeting', listen, 'Meeting — record the room; what is heard is not addressed to the identity') +
    opt('off', listen, 'Off — the operator types') +
    '</select>' +
    '<label class="f">SPOKEN REPLIES</label><select id="sp-mode-speak">' +
    opt('', speak, 'Automatic — spoken when a voice is configured and the operator spoke') +
    opt('on', speak, 'Spoken') +
    opt('off', speak, 'Text only') +
    '</select>' +
    '<div class="muted" id="sp-mode-readback">' + esc(voiceModeReadback(m)) + '</div>' +
    '</div>' +
    '<div class="card"><h3>Echo processing — this browser tab</h3><select id="sp-echo-mode">' +
    echoModes.map(([value, label]) => opt(value, echoMode(), label)).join('') + '</select>' +
    '<p class="muted">Saved for this browser with Save speech settings; retained after refresh. Applies on the next Listen or Meeting session. Other browsers keep their own choice. Push-to-talk uses browser cancellation.</p>' +
    '<div class="muted" id="sp-echo-readback">Saved: ' + esc(echoModes.find(([v]) => v === echoMode())[1]) + '</div></div>';
}

export function voiceModeReadback(m) {
  const listen = m.listen || 'interactive', speak = m.speak || 'auto';
  return (m.name || '') + ' — listen: ' + listen + ', speak: ' + speak +
    ' · revision ' + (m.revision || 0) +
    (m.set ? '' : ' · nothing chosen: the default is in force');
}

function speakersCard(sp) {
  const p = sp.speakers || { mode: 'all', uids: [], unidentified: 'deliver', revision: 0 };
  const mode = p.mode || 'all';
  const opt = (v, label) => '<option value="' + v + '"' + (mode === v ? ' selected' : '') + '>' + label + '</option>';
  const unid = p.unidentified || 'deliver';
  return '<div class="card" id="sp-speakers" data-speech-section="speech_speakers"><h3>Who is heard</h3>' +
    '<p class="muted">Whose words the identity receives. Use stable speaker UUIDs or enrolled ids from the speech engine\'s speaker list, not display names. Provisional or unresolved speakers do not match an allow-list. Identification does not grant authority.</p>' +
    '<label class="f">HEARD</label><select id="sp-speakers-mode">' + opt('all', 'Everyone') + opt('only', 'Only the speakers listed') + opt('ignore', 'Everyone except the speakers listed') + '</select>' +
    '<div id="sp-speakers-list-row"' + (mode === 'all' ? ' hidden' : '') + '><label class="f">SPEAKER IDS</label>' +
    '<input type="text" id="sp-speakers-uids" value="' + esc((p.uids || []).join(' ')) + '" placeholder="ids from the speaker list, separated by spaces"></div>' +
    '<div id="sp-speakers-unid-row"' + (mode === 'ignore' ? '' : ' hidden') + '><label class="f">UNIDENTIFIED VOICES</label><select id="sp-speakers-unid">' +
    '<option value="deliver"' + (unid === 'deliver' ? ' selected' : '') + '>Delivered, marked unidentified</option>' +
    '<option value="withhold"' + (unid === 'withhold' ? ' selected' : '') + '>Withheld</option></select></div>' +
    '<div class="muted" id="sp-speakers-readback">' + esc(speakersReadback(p)) + '</div>' +
    '</div>';
}

export function speakersReadback(p) {
  const mode = p.mode || 'all', n = (p.uids || []).length, s = n === 1 ? '' : 's';
  const heard = mode === 'all' ? 'Everyone is heard'
    : mode === 'only' ? 'Only ' + n + ' listed speaker' + s + ' heard; unidentified voices are withheld'
    : 'Everyone but ' + n + ' listed speaker' + s + '; unidentified voices are ' + (p.unidentified === 'withhold' ? 'withheld' : 'delivered');
  const f = p.withheld_finals || 0, q = p.withheld_partials || 0;
  return heard + ' · revision ' + (p.revision || 0) + ' · withheld since start: ' + f + ' final' + (f === 1 ? '' : 's') + ', ' + q + ' partial' + (q === 1 ? '' : 's');
}

function wireSpeech(root) {
  const modeSel = root.querySelector('#sp-speakers-mode');
  if (modeSel) modeSel.onchange = () => {
    const m = modeSel.value;
    const list = root.querySelector('#sp-speakers-list-row'), unid = root.querySelector('#sp-speakers-unid-row');
    if (list) list.hidden = m === 'all';
    if (unid) unid.hidden = m !== 'ignore';
  };
  if (modeSel) modeSel.onchange();
  ['stt', 'tts'].forEach(dir => {
    const sel = root.querySelector('#sp-provider-' + dir);
    if (!sel) return;
    const sp = () => (S.config && S.config.speech) || {};
    const cur = () => (sp()[dir] || {});
    const adding = speechAdd.waiting();
    if (!adding || adding.section !== 'speech_' + dir) {
      askSpeechLists(dir, sel.value, sp(), { language: draft[dir] !== null ? '' : cur().language });
    }
    ['model', 'voice'].forEach(kind => {
      const state = root.querySelector('#sp-' + kind + '-state-' + dir);
      if (state) state.textContent = stateLine(dir, sel.value, kind === 'voice' ? 'voices' : 'models');
    });
    wirePicker(root, 'sp-model-' + dir);
    if (dir === 'tts') wirePicker(root, 'sp-voice-tts');
    const typed = root.querySelector('#sp-key-' + dir);
    if (typed) {
      let keyTimer = 0;
      typed.oninput = () => {
        clearTimeout(keyTimer);
        keyTimer = setTimeout(() => {
          if ($('sp-provider-' + dir) !== sel) return;
          askSpeechLists(dir, sel.value, sp(), { language: language ? language.value : '' });
          renderSpeechChoices(dir, sel.value, language ? language.value : '');
        }, 400);
      };
    }
    const sample = root.querySelector('#sp-sample-' + dir);
    if (sample) sample.onclick = () => playSample(sample, sel.value);
    const find = root.querySelector('#sp-find-' + dir);
    if (find) {
      let timer = 0;
      find.oninput = () => {
        clearTimeout(timer);
        timer = setTimeout(() => {
          if ($('sp-provider-' + dir) === sel) askSpeechLists(dir, sel.value, sp(), { search: find.value.trim(), language: cur().language });
        }, 300);
      };
    }
    const language = root.querySelector('#sp-language-' + dir);
    if (language) {
      language.onchange = () => {
        const chosen = language.value;
        askSpeechLists(dir, sel.value, sp(), { language: chosen, search: find ? find.value.trim() : '' });
        renderSpeechChoices(dir, sel.value, chosen);
      };
    }
    sel.onchange = () => {
      draft[dir] = sel.value;
      renderSettings();
    };
  });
}

function languageOptionsHTML(languages, chosen) {
  return '<option value=""' + (chosen ? '' : ' selected') + '>Any language the service hears</option>' +
    languages.map(l => '<option value="' + esc(l.id) + '"' + (l.id === chosen ? ' selected' : '') + '>' +
      esc(l.name ? l.name + ' (' + l.id + ')' : l.id) + '</option>').join('');
}

function renderSpeechChoices(dir, value, language) {
  const got = listed(dir, value);
  const model = $('sp-model-' + dir), voice = dir === 'tts' ? $('sp-voice-tts') : null;
  const row = $('sp-language-row-' + dir), pick = $('sp-language-' + dir);
  if (row && pick) {
    const langs = languagesOf(got);
    row.hidden = !langs.length;
    pick.innerHTML = languageOptionsHTML(langs, langs.some(l => l.id === (language || '')) ? language : '');
  }
  const fill = (el, items, kind) => {
    if (!el) return;
    const chosen = el.value;
    el.innerHTML = optionsHTML(items, chosen === BY_ID ? '' : chosen);
    el.value = chosen;
    const state = $('sp-' + (kind === 'voices' ? 'voice' : 'model') + '-state-' + dir);
    if (state) state.textContent = stateLine(dir, value, kind);
  };
  fill(model, itemsOf(got, 'models', language), 'models');
  fill(voice, itemsOf(got, 'voices', language), 'voices');
}

export function acceptSpeechLists(got) {
  if (!got || !got.provider || !SPEECH_DIRS[got.direction]) return false;
  const dir = got.direction, key = listKey(got.provider, dir);
  const pending = asking[key], question = pending || asked[key];
  if (question && ((question.search || '') !== (got.search || '') || (question.language || '') !== (got.language || ''))) return true;
  live[key] = got;
  if (pending) {
    got.typed = pending.typed;
    delete asking[key];
  }
  const sel = $('sp-provider-' + dir);
  if (!sel || sel.value !== got.provider) return true;
  const language = $('sp-language-' + dir) ? $('sp-language-' + dir).value : '';
  renderSpeechChoices(dir, got.provider, language);
  return true;
}

export function rejectSpeechLists(requestID, text) {
  for (const key of Object.keys(asking)) {
    if (asking[key].request !== requestID) continue;
    const [provider, dir] = key.split('|');
    delete asking[key];
    live[key] = { provider: provider, direction: dir, models_error: text, voices_error: text, search: '', language: '' };
    const sel = $('sp-provider-' + dir);
    if (sel && sel.value === provider) renderSpeechChoices(dir, provider, $('sp-language-' + dir) ? $('sp-language-' + dir).value : '');
    return true;
  }
  return false;
}

function readySpeechService(section, name, key, baseURL, ch, editor = submittedEditor(section), remaining = [], echo) {
  configResult = null;
  const requestID = send({ type: 'speech_service', provider: name, api_key: key, base_url: baseURL });
  if (!speechAdd.arm({ section: section, name: name, sentKey: key !== '', changes: ch, editor, remaining, echo }, requestID)) {
    configResult = { kind: 'bad', section: section, text: 'Not connected — ' + name + ' was not saved.' };
  }
}

function isLoopbackHost(h) {
  h = (h || '').trim();
  return h === '' || h === '127.0.0.1' || h === '::1' || h === 'localhost';
}
function dashboardWarnHTML(host, tls) {
  if (tls || isLoopbackHost(host)) return '';
  return '<div class="fb-hint" id="cfg-dwarn">WITHOUT HTTPS ON THIS ADDRESS, EVERY WORD BETWEEN YOU AND THIS IDENTITY CROSSES THE NETWORK IN THE CLEAR — AND THE BROWSER WILL REFUSE THE MICROPHONE.</div>';
}
const TOKEN_EYE = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M1.5 8s2.4-4 6.5-4 6.5 4 6.5 4-2.4 4-6.5 4S1.5 8 1.5 8Z"/><circle cx="8" cy="8" r="2"/></svg>';
const TOKEN_COPY = '<svg viewBox="0 0 16 16" aria-hidden="true"><rect x="5.5" y="5.5" width="8" height="8" rx="1.8"/><path d="M10.5 3.5v-.3A1.7 1.7 0 0 0 8.8 1.5H4.2a1.7 1.7 0 0 0-1.7 1.7v4.6a1.7 1.7 0 0 0 1.7 1.7h.3"/></svg>';
const TOKEN_COPIED = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="m3.5 8.5 3 3 6-7"/></svg>';
let tokenAsk = '', tokenShown = '';
let tokenBusy = '', tokenSaid = null;
const TOKEN_OFF_WHERE = '<div class="muted" data-token-off-where style="font-size:12px;margin-top:6px">Asking for a token stops only in config.json on this machine (dashboard.require_token: false), never from this page \u2014 and a dashboard other machines can reach always asks.</div>';
function tokenActHTML(act, label, busyLabel, secretsOK, privateNote) {
  if (!secretsOK) return '<div class="muted" data-token-' + act + '-private style="font-size:12px;margin-top:6px">' + privateNote + '</div>';
  return '<div class="savebar"><button type="button" class="btn ghost" data-token-' + act + (tokenBusy ? ' disabled' : '') + '>' + (tokenBusy === act ? busyLabel : label) + '</button>' +
    (tokenSaid ? '<span class="config-result ' + tokenSaid.kind + '" data-token-' + tokenSaid.act + '-said data-announce>' + esc(tokenSaid.text) + '</span>' : '') + '</div>';
}
function dashboardTokenHTML(required, secretsOK) {
  if (!required) {
    return '<label class="f">ACCESS TOKEN</label>' +
      '<div data-token-open>This dashboard asks no browser for a token: any browser on this machine that opens it is its operator.</div>' +
      tokenActHTML('require', 'Ask for an access token', 'Turning on\u2026', secretsOK, 'Asking for a token is turned on over a private connection only \u2014 open this page on the machine itself, or over HTTPS.') +
      TOKEN_OFF_WHERE;
  }
  return '<label class="f" for="cfg-dtoken">ACCESS TOKEN</label>' +
    '<div style="display:flex;align-items:center;gap:6px"><input id="cfg-dtoken" type="password" value="' + esc(tokenShown) + '" readonly autocomplete="off" spellcheck="false" style="flex:1" placeholder="press the eye to show it">' +
    '<button type="button" class="copyb" data-token-reveal title="Show access token" aria-label="Show access token" aria-pressed="' + (tokenShown ? 'true' : 'false') + '">' + TOKEN_EYE + '</button>' +
    '<button type="button" class="copyb" data-token-copy title="' + (tokenShown ? 'Copy access token' : 'Show the token first') + '" aria-label="Copy access token"' + (tokenShown ? '' : ' disabled') + '>' + TOKEN_COPY + '</button></div>' +
    '<div class="muted" style="font-size:12px;margin-top:6px">This is the credential for another browser or device, kept in config.json. Rotating replaces it: every other browser and device signs in again with the new one, and this one stays signed in.</div>' +
    tokenActHTML('rotate', 'Rotate access token', 'Rotating\u2026', secretsOK, 'Rotating the token is offered over a private connection only \u2014 open this page on the machine itself, or over HTTPS.') +
    TOKEN_OFF_WHERE;
}

const TOKEN_ACTS = {
  rotate: {
    confirm: 'Rotate the access token? Every other browser and device signed in to this dashboard signs in again with the new token; this one stays signed in.',
    done: 'Rotated \u2014 every other browser and device signs in again with the new token. Press the eye to show it.',
    failed: 'The token was not rotated.',
  },
  require: {
    confirm: 'Ask for an access token? From now on every browser and device must sign in with it to use this dashboard: every other open page is cut off and asks for the token, and this one stays signed in. Only config.json on this machine turns this off again.',
    done: 'Asked for \u2014 every other browser and device must now sign in with the token. Press the eye to show it.',
    failed: 'No token is asked for yet.',
  },
};
async function runTokenAct(act) {
  if (tokenBusy) return;
  const a = TOKEN_ACTS[act];
  if (window.confirm && !window.confirm(a.confirm)) return;
  tokenBusy = act; tokenSaid = null; renderSettings();
  const res = await (act === 'rotate' ? S.rotateAccessToken() : S.requireAccessToken());
  tokenBusy = '';
  if (res.ok) {
    tokenAsk = ''; tokenShown = '';
    if (S.config && S.config.dashboard) S.config.dashboard.require_token = true;
    tokenSaid = { act, kind: 'good', text: a.done };
  } else tokenSaid = { act, kind: 'bad', text: res.text || a.failed };
  if (S.view === 'settings' && sec === 'dashboard') renderSettings();
}

export function acceptDashboardToken(requestID, token) {
  if (!tokenAsk || requestID !== tokenAsk) return false;
  tokenAsk = '';
  tokenShown = token || '';
  const field = $('cfg-dtoken');
  if (field) { field.value = tokenShown; field.type = 'text'; }
  const reveal = document.querySelector('[data-token-reveal]'), copy = document.querySelector('[data-token-copy]');
  if (reveal) { reveal.title = 'Hide access token'; reveal.setAttribute('aria-label', reveal.title); reveal.setAttribute('aria-pressed', 'true'); }
  if (copy) { copy.disabled = false; copy.title = 'Copy access token'; }
  return true;
}
function dashboardHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';
  const tls = !!c.dashboard.tls;
  return '<div class="card"><h3>DASHBOARD — BIND ADDRESS</h3>' +
    cfgField('cfg-dhost', 'HOST (127.0.0.1 = THIS MACHINE ONLY; A LAN IP OR 0.0.0.0 EXPOSES THE DASHBOARD TO THAT NETWORK)', c.dashboard.host) +
    cfgField('cfg-dport', 'PORT', c.dashboard.port) +
    '<label class="f" style="display:flex;gap:6px;align-items:center;margin-top:8px"><input type="checkbox" id="cfg-dtls"' + (tls ? ' checked' : '') + '> HTTPS (REQUIRED FOR THE MICROPHONE, AND FOR ANY ADDRESS OTHER THAN THIS MACHINE)</label>' +
    dashboardWarnHTML(c.dashboard.host, tls) +
    dashboardTokenHTML(!!c.dashboard.require_token, !!c.secrets_ok) +
    savebarHTML('dashboard', 'saved — applies next restart') + '</div>' +
    publicNameCardHTML(c.public_name || null, c) +
    themeCardHTML();
}

function wireDashboardToken(root) {
  root.querySelectorAll('[data-token-rotate],[data-token-require]').forEach(btn => {
    btn.onclick = () => runTokenAct(btn.hasAttribute('data-token-rotate') ? 'rotate' : 'require');
  });
  const field = root.querySelector('#cfg-dtoken');
  if (!field) return;
  const reveal = root.querySelector('[data-token-reveal]');
  const copy = root.querySelector('[data-token-copy]');
  reveal.onclick = () => {
    if (!field.value) {
      tokenAsk = query('dashboard_token') || '';
      return;
    }
    const shown = field.type === 'text';
    field.type = shown ? 'password' : 'text';
    reveal.title = shown ? 'Show access token' : 'Hide access token';
    reveal.setAttribute('aria-label', reveal.title);
    reveal.setAttribute('aria-pressed', String(!shown));
  };
  copy.onclick = async () => {
    if (!field.value) { copy.title = 'Show the token first'; return; }
    if (!(await copyText(field.value))) {
      copy.title = 'The browser refused clipboard access';
      return;
    }
    copy.innerHTML = TOKEN_COPIED;
    copy.title = 'Copied';
    setTimeout(() => { copy.innerHTML = TOKEN_COPY; copy.title = 'Copy access token'; }, 1200);
  };
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
    savebarHTML('witness', 'saved — applies next boot') + '</div>';
}
function promptHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';
  return '<div class="card"><h3>PROMPT</h3>' +
    cfgField('cfg-ptokens', 'MAX TOKENS', c.prompt.max_tokens) +
    cfgField('cfg-pturns', 'RECENT TURNS', c.prompt.recent_turns) +
    savebarHTML('prompt', 'saved — applies next boot') + '</div>';
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
    savebarHTML('agency', 'role routing applies live; coaching prompts after restart') + '</div>';
}

function logsHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';
  const cfg = c.logs || {};
  let html = '<div class="card"><h3>LOGS</h3>' +
    cfgField('cfg-ldir', 'DIRECTORY (RELATIVE TO IDENTITY HOME; EMPTY = DISABLED)', cfg.dir) +
    cfgField('cfg-lbackups', 'MAX BACKUPS (-1 = KEEP ALL)', cfg.max_backups == null ? '' : cfg.max_backups) +
    cfgField('cfg-lcomp', 'COMPRESS AFTER DAYS (-1 = NEVER)', cfg.compress_days == null ? '' : cfg.compress_days) +
    savebarHTML('logs', 'saved — applies next restart') + '</div>';
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
  broken: ['#c0392b', 'broken entry in providers.json — not used until repaired or removed'],
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
    if (ci.expires_at) s.push((credentialExpired(ci) ? 'EXPIRED ' : 'usable to ') + esc(ci.expires_at.slice(0, 16).replace('T', ' ')) + ' UTC');
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
  p = p || {};
  const apiType = p.api_type || 'openai';
  const nameField = tag === 'new'
    ? cfgField('pv-name-' + tag, 'NAME', '')
    : '<label class="f">NAME</label><input type="text" id="pv-name-' + tag + '" value="' + esc(p.name || '') + '" readonly>';
  return '<div data-provider-editor data-edit-scope="' + esc(tag === 'new' ? 'new-provider' : 'provider:' + p.name) + '" style="padding:10px 0 4px">' +
    nameField +
    '<label class="f">API TYPE (DIALECT)</label><select id="pv-type-' + tag + '">' +
    ['openai', 'anthropic'].map(t => '<option value="' + t + '"' + (t === apiType ? ' selected' : '') + '>' + t + '</option>').join('') +
    '</select>' +
    cfgField('pv-url-' + tag, 'ENDPOINT URL', p.endpoint || '') +
    cfgField('pv-model-' + tag, 'DEFAULT MODEL', p.default_model || '') +

    cfgField('pv-key-' + tag, 'API KEY (ENTER TO REPLACE' + (storedKey ? ', ONE STORED' : ', NONE STORED') + ')', '', 'password') +
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
    (p.can_sign_in ? signInRow(p) : '') +
    '</div>';
}

function signInRow(p) {
  const progress = signInProgress(p.signin, 'provider', p.name);
  if (!signInWanted(p, S.skipSignInWithValidToken !== false)) return progress ? '<div class="signin">' + progress + '</div>' : '';
  return '<div class="signin">' + progress +
    '<button class="btn" data-prov-signin="' + esc(p.name) + '">Sign in with ' + esc(p.name.replace(/\s*\(.*\)\s*$/, '')) + '</button>' +
    (p.signin && p.signin.status === 'pending' ? '<button class="btn ghost" data-prov-signin-cancel="' + esc(p.name) + '">Cancel sign-in</button>' : '') + '</div>';
}
export function wireProviderSignIn(root) {
  wireSignInCompletion(root);
  root.querySelectorAll('[data-prov-signin-cancel]').forEach(btn => { btn.onclick = () => send({type:'provider_signin_cancel',provider:btn.dataset.provSigninCancel}); });
  root.querySelectorAll('[data-prov-signin]').forEach(btn => { btn.onclick = () => {
    startSignIn({ type: 'provider_signin', provider: btn.dataset.provSignin });
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
    '<div data-update-state data-announce style="margin-top:6px">' + state + '</div>' +
    '<label class="f" style="margin-top:8px"><input type="checkbox" id="cfg-uauto"' + (auto ? ' checked' : '') + '> Install updates automatically — a signed release is downloaded, verified and installed on a check; you choose when to relaunch</label>' +
    '<div class="muted" style="font-size:12px;margin-top:6px">Checks run once an hour.' +
    (managed ? ' This install is managed by your package manager (' + esc(u.stage_refusal) + '): updates are reported here and installed with it.' : '') +
    ' Mobile builds only report.</div>' +
    savebarHTML('updates', '', { also: '<button class="btn ghost" data-update-check' + (canCheck ? '' : ' disabled') + '>Check now</button>' +
      (u && u.needs_restart ? '<button class="btn" data-update-restart>Relaunch now</button>' : '') }) +
    '</div>';
}
export function wireThemeChoice(root) {
  root.querySelectorAll('[data-theme-choice]').forEach(sel => { sel.onchange = () => { if (typeof S.setThemeChoice === 'function') S.setThemeChoice(sel.value); }; });
}

function routeHTML(p, c) {
  if (!p || !p.name) return '';
  const mode = p.route_mode || 'direct';
  const now = mode === 'relay' ? 'through the relay ' + esc(p.relay_endpoint || '(none named)')
    : mode === 'disabled' ? 'nowhere \u2014 the route is withdrawn (set in config.json), and the name and its certificate are kept'
    : mode === 'direct' ? 'directly at the addresses this machine is reached at' : 'as config.json says: ' + esc(mode);
  let html = '<div data-route style="margin-top:12px"><label class="f" for="cfg-route">ROUTE</label><div data-route-now>The name points ' + now + '.</div>';
  if (!c || !c.secrets_ok) {
    return html + '<div class="muted" data-route-private style="font-size:12px;margin-top:6px">The route is chosen over a private connection only \u2014 open this page on the machine itself, or over HTTPS.</div></div>';
  }
  const relays = p.relays, token = !!(c.dashboard && c.dashboard.require_token);
  const chosen = mode === 'relay' ? 'relay:' + (p.relay_endpoint || '').replace(/:\d+$/, '') : 'direct';
  const opt = (value, label, off) => '<option value="' + esc(value) + '"' + (value === chosen ? ' selected' : '') + (off ? ' disabled' : '') + '>' + esc(label) + '</option>';
  let options = opt('direct', 'Direct \u2014 the addresses this machine is reached at', false);
  (relays || []).forEach(r => { options += opt('relay:' + r.name, 'Relay \u2014 through ' + r.name + ' (port ' + r.port + ')', !token); });
  let note = '';
  if (relays == null) note = 'The certificate service has not answered yet; the relays it offers are listed here when it does.';
  else if (!relays.length) note = 'The certificate service advertises no relay.';
  else if (!token) note = 'A relay carries the public internet to this dashboard, so one can be chosen only while the dashboard asks for its access token, and it does not now \u2014 turn it on above with Ask for an access token, then choose one.';
  else note = 'A relay serves one port, and the name is reached on the port in its address: a relay on another port is refused.';
  return html + '<select id="cfg-route">' + options + '</select>' +
    '<div class="muted" data-route-note style="font-size:12px;margin-top:6px">' + note + '</div>' +
    savebarHTML('route', '', { label: 'Use this route' }) + '</div>';
}

export function publicNameCardHTML(p, c) {
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
    (control ? '<div class="savebar">' + control + '</div>' : '') + routeHTML(p, c) + '</div>';
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
S.openSettings = (section, focusID) => {
  sec = section;
  provOpen = null;
  const nav = document.querySelector('.nav-item[data-view="settings"]');
  if (nav && S.view !== 'settings') nav.click();
  else S.requestSettingsSection();
  renderSettings();
  const el = focusID ? $(focusID) : null;
  if (el) el.focus();
};
S.renderUpdate = () => { if (S.view === 'settings' && sec === 'updates') renderSettings(); };
S.renderBackups = () => { if (S.view === 'settings' && sec === 'backups') renderSettings(); };
S.renderSandbox = () => { if (S.view === 'settings' && sec === 'sandbox') renderSettings(); };
S.renderMailRepair = () => { if (S.view === 'settings' && sec === 'held-mail') renderSettings(); };
S.renderMessages = () => { if (S.view === 'settings' && sec === 'messages') renderSettings(); };
export function openBackups() { sec = 'backups'; }
S.settingsSection = name => {
  if (!SECTIONS.some(([id]) => id === name) || sec === name) return false;
  sec = name; provOpen = null;
  return true;
};
S.requestSettingsSection = () => {
  if (!S.connected) return;
  if (sec === 'backups') requestBackups();
  if (sec === 'held-mail') requestHeldMail();
  if (sec === 'messages') requestMessages();
};
S.settingsAddress = () => '#/settings/' + sec;
function addressSection() {
  if (S.view !== 'settings') return;
  const want = S.settingsAddress();
  if (location.hash !== want) { try { history.replaceState(null, '', want); } catch (err) {} }
}
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
  if (info.expires_at) bits.push((credentialExpired(info) ? 'EXPIRED ' : 'usable to ') + esc(info.expires_at.replace('T', ' ').replace('Z', ' UTC')));
  if (info.path) bits.push('from ' + esc(info.path));
  if (!bits.length) return '';
  return '<div style="font-size:11.5px;color:var(--faint);margin:2px 0 6px">' + bits.join(' &middot; ') + '</div>';
}
function providersHTML() {
  let html = '<div class="card"><h3>PROVIDERS — providers.json</h3>' +
    '<div style="font-size:11.5px;color:var(--faint);margin-bottom:8px;line-height:1.5">The operator\'s file, beside config.json — edited here or by hand. Status describes live model discovery and is never stored; activation is decided by a real inference check.</div>';
  if (prov.waiting()) html += '<div class="config-result" data-announce>Saving ' + esc(prov.waiting().name) + '… waiting for the runtime to confirm.</div>';
  else if (provBroken.waiting()) html += '<div class="config-result" data-announce>' + (provBroken.waiting().type === 'provider_repair' ? 'Repairing ' : 'Removing ') + esc(provBroken.waiting().label) + '… waiting for the runtime to confirm.</div>';
  else if (provResult) html += '<div class="config-result ' + provResult.kind + '" data-announce>' + esc(provResult.text) + '</div>';
  const speechOnly = S.providers.filter(p => p.chat === false);
  const chatRows = S.providers.map((p, i) => {
      if (p.chat === false) return '';
      let row = '<div class="tool-row" data-prov-row="' + i + '" style="cursor:pointer;align-items:center">' +
        dot(p.status) +
        '<span class="tn">' + esc(p.name) + (p.default ? ' ✓' : '') + '</span>' +
        '<span class="td" style="font-family:var(--mono);font-size:11.5px">' + esc(p.endpoint) +
        (p.default_model ? ' · ' + esc(p.default_model) : '') +
        ' · ' + (p.models || []).length + ' models</span></div>' + provRowNote(p);
      if (provOpen === p.name) row += providerEditor(p, String(i));
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
  html += provOpen === NEW_PROVIDER
    ? providerEditor(null, 'new')
    : '<div class="savebar" style="margin-top:10px"><button class="btn" id="pv-open-new">Add provider</button></div>';
  html += '</div>';
  return html;
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
function brokenProviderAction(type, position, sha256) {
  const b = (S.brokenProviders || []).find(x => x.position === position && x.sha256 === sha256);
  const label = b && b.name ? b.name : 'entry ' + (position + 1);
  provResult = null;
  const requestID = send({ type, position, entry_sha256: sha256 });
  if (!provBroken.arm({ type, label, sha256 }, requestID)) provResult = { kind: 'bad', text: 'Not connected — ' + label + ' was not changed.' };
  renderSettings();
}
function commitProvider(tag) {
  const base = (tag !== 'new' && S.providers[parseInt(tag, 10)]) || {};
  const editor = submittedEditor('', $('pv-key-' + tag).closest('[data-provider-editor]'));

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
    chat: true,
  };
  const temp = fnum('pv-temp-' + tag);
  if (temp !== undefined) entry.temperature = temp;
  const topp = fnum('pv-topp-' + tag);
  if (topp !== undefined) entry.top_p = topp;

  provResult = null;
  const requestID = send({ type: 'provider_set', entry });
  if (!prov.arm({ name: entry.name, sentKey: entry.api_key !== '', entry: entry, editor }, requestID)) {
    provResult = { kind: 'bad', text: 'Not connected — provider was not sent.' };
  }
  renderSettings();
}

export function acceptProviderSave(requestID) {
  const acted = provBroken.claim(requestID);
  if (acted) {
    const still = (S.brokenProviders || []).some(b => b.sha256 === acted.sha256);
    provResult = still
      ? { kind: 'bad', text: 'Acknowledged, but ' + acted.label + ' is still listed as broken.' }
      : { kind: 'good', text: (acted.type === 'provider_repair' ? 'Repaired — ' : 'Removed — ') + acted.label + '.' };
    return true;
  }
  const added = speechAdd.claim(requestID);
  if (added) {
    const got = S.providers.find(p => p.name === added.name);
    if (got && (!added.sentKey || got.has_key)) {
      if (added.sentKey) forgetSpeechLists(added.name);
      if (added.remaining.length) {
        const [next, ...remaining] = added.remaining;
        readySpeechService(added.section, next.provider, next.key, next.baseURL, added.changes, added.editor, remaining, added.echo);
      } else {
        finishEditor(added.editor, true);
        sendConfigChanges(added.section, added.changes, added.editor, added.echo);
      }
    }
    else configResult = { kind: 'bad', section: added.section, text: 'Acknowledged, but ' + added.name + (got ? ' came back with no key stored' : ' is not in the registry that came back') + ' — the settings were not sent.' };
    return true;
  }

  const pending = prov.claim(requestID);
  if (!pending) return false;
  const got = S.providers.find(p => p.name === pending.name);
  const expectedKey = pending.sentKey || pending.entry.has_key;
  if (!got || !!got.has_key !== expectedKey) {
    provResult = { kind: 'bad', text: 'Acknowledged, but ' + pending.name +
      (got ? ' came back with a different stored-key state' : ' is not in the registry that came back') +
      ' — the save did not take.' };
    return true;
  }
  provResult = { kind: 'good', text: 'Saved — ' + pending.name +
    (pending.sentKey ? ', key stored.' : (expectedKey ? '.' : ', no stored key.')) };
  const newer = finishEditor(pending.editor);
  if (!newer && pending.editor && provOpen === pending.editor.owner) provOpen = null;
  if (newer) provResult.text += ' Newer edits remain unsaved.';
  return true;
}

export function rejectProviderSave(message, requestID) {
  const adding = speechAdd.waiting();
  if (speechAdd.claim(requestID)) { configResult = { kind: 'bad', section: adding && adding.section, text: message }; renderSettings(); return true; }
  if (provBroken.claim(requestID)) { provResult = { kind: 'bad', text: message }; renderSettings(); return true; }
  const pending = prov.claim(requestID);
  if (!pending) return false;
  provResult = { kind: 'bad', text: message };
  renderSettings();
  return true;
}

let focusOwed = null;
const controlName = el => el.tagName + (el.id ? '#' + el.id
  : [...el.attributes].filter(a => a.name.startsWith('data-')).map(a => '[' + a.name + '=' + a.value + ']').join(''));
const alike = (root, tag, name) => [...root.querySelectorAll(tag)].filter(el => controlName(el) === name);
function focusOf(st, el) {
  if (!el || !st?.contains(el)) return null;
  const name = controlName(el);
  return { tag: el.tagName, name, nth: alike(st, el.tagName, name).indexOf(el),
    start: el.selectionStart ?? null, end: el.selectionEnd ?? null, direction: el.selectionDirection ?? 'none' };
}
document.addEventListener('focusout', e => { if (!e.relatedTarget) focusOwed = focusOf($('settings-stack'), e.target); });
function focusAfter(st, had) {
  focusOwed = null;
  if (!had || st.contains(document.activeElement)) return;
  const el = alike(st, had.tag, had.name)[had.nth];
  if (!el) return;
  el.focus({ preventScroll: true });
  if (document.activeElement !== el) focusOwed = had;
  else if (had.start !== null) el.setSelectionRange(had.start, had.end, had.direction);
}

export function renderSettings() {
  const st = $('settings-stack');
  if (st.settingsComposing) { st.settingsRefreshDue = true; return; }
  if (st.dataset.section === 'backups' && sec !== 'backups') forgetBackupsSecrets();
  captureEdits(st);
  const expanded = new Set([...st.querySelectorAll('[data-provider-editor] details[open]')]
    .map(el => el.closest('[data-edit-scope]').dataset.editScope));
  const focused = document.activeElement;
  const active = focused && st.contains(focused) && editorFields(st).includes(focused) ? { el: focused, key: fieldKey(focused) } : null;
  const had = focused === document.body ? focusOwed : focusOf(st, focused);
  if (!S.providersLoaded) query('providers');
  const c = S.config;
  let html = navHTML();
  html += '<div class="muted" data-settings-draft hidden>Unsaved edits — not yet the running configuration.</div>';
  html += configFeedbackHTML();
  if (sec === 'substrate') html += substrateHTML(c);
  else if (sec === 'providers') html += providersHTML();
  else if (sec === 'speech') html += speechHTML(c);
  else if (sec === 'dashboard') html += dashboardHTML(c);
  else if (sec === 'backups') html += backupsHTML();
  else if (sec === 'witness') html += witnessHTML(c);
  else if (sec === 'prompt') html += promptHTML(c);
  else if (sec === 'agency') html += agencyHTML(c);
  else if (sec === 'logs') html += logsHTML(c);
  else if (sec === 'storage') html += storageHTML(c);
  else if (sec === 'messages') html += messagesHTML() + contactsHTML(c);
  else if (sec === 'held-mail') html += mailRepairHTML();
  else if (sec === 'updates') html += updateCardHTML(S.update || null);
  else if (sec === 'sandbox') html += sandboxCardHTML() || '<div class="card"><div class="empty">loading sandbox…</div></div>';
  else if (sec === 'tools') html += toolsHTML();
  renderInto(st, html, st.dataset.section !== sec);
  st.dataset.section = sec;
  restoreEditors(st, active);
  st.querySelectorAll('[data-provider-editor] details').forEach(el => {
    if (expanded.has(el.closest('[data-edit-scope]').dataset.editScope)) el.open = true;
  });
  st.oninput = st.onchange = e => {
    if (e.target.hasAttribute('data-unset-hint')) syncUnsetHint(e.target);
    captureEdits(st, e.target);
    showDraftNotice(st);
  };
  st.oncompositionstart = () => { st.settingsComposing = true; };
  st.oncompositionend = () => {
    st.settingsComposing = false;
    if (st.settingsRefreshDue) {
      st.settingsRefreshDue = false;
      queueMicrotask(() => { if (S.view === 'settings') renderSettings(); });
    }
  };

  st.querySelectorAll('[data-sec]').forEach(btn => {
    btn.onclick = () => { sec = btn.dataset.sec; provOpen = null; S.requestSettingsSection(); renderSettings(); };
  });
  st.querySelectorAll('[data-save]').forEach(btn => { btn.onclick = () => { saveSettings(btn.dataset.save); renderSettings(); }; });
  if (sec === 'held-mail') wireMailRepair(st);
  if (sec === 'messages') { wireMessages(st); wireContacts(st); }
  wirePublicName(st);
  wireDashboardToken(st);
  wireUnsetHints(st);
  wireSpeech(st);

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
    sync();
  }
  st.querySelectorAll('[data-open-section]').forEach(a => { a.onclick = e => { e.preventDefault(); S.openSettings(a.dataset.openSection); }; });
  st.querySelectorAll('[data-prov-row]').forEach(row => {
    row.onclick = (e) => {
      if (e.target.closest('input,button,label')) return;
      const i = parseInt(row.dataset.provRow, 10);
      const name = S.providers[i].name;
      provOpen = provOpen === name ? null : name;
      renderSettings();
    };
  });
  st.querySelectorAll('[data-prov-commit]').forEach(btn => { btn.onclick = () => commitProvider(btn.dataset.provCommit); });
  st.querySelectorAll('[data-prov-repair]').forEach(btn => { btn.onclick = () => brokenProviderAction('provider_repair', parseInt(btn.dataset.provRepair, 10), btn.dataset.provSha); });
  st.querySelectorAll('[data-prov-remove-broken]').forEach(btn => { btn.onclick = () => brokenProviderAction('provider_remove_broken', parseInt(btn.dataset.provRemoveBroken, 10), btn.dataset.provSha); });
  st.querySelectorAll('[data-prov-del]').forEach(btn => { btn.onclick = () => { provOpen = null; send({ type: 'provider_delete', provider: btn.dataset.provDel }); }; });
  wireProviderSignIn(st);
  st.querySelectorAll('[data-prov-cancel]').forEach(btn => { btn.onclick = () => {
    const editor = btn.closest('[data-provider-editor]');
    editorFields(editor).forEach(el => edits.delete(fieldKey(el)));
    editor.remove();
    provOpen = null; renderSettings();
  }; });
  wireUpdateCheck(st);
  wireThemeChoice(st);
  st.querySelectorAll('[data-logfile]').forEach(row => {
    row.onclick = () => {
      S.logFile = row.dataset.logfile;
      query('logs', { name: S.logFile });
    };
  });
  const openNew = $('pv-open-new');
  if (openNew) openNew.onclick = () => { provOpen = NEW_PROVIDER; renderSettings(); };

  const provEl = $('cfg-provider');
  if (provEl) {
    const syncModel = clear => {
      const p = S.providers.find(x => x.name === provEl.value) || S.providers.find(x => x.default) || null;
      const model = $('cfg-model'), list = $('cfg-model-list'), label = $('cfg-model-label');
      if (!model || !list || !label) return;
      if (clear) model.value = '';
      model.placeholder = p && p.default_model ? 'Default: ' + p.default_model : 'Search or enter a model';
      list.innerHTML = providerModels(p).map(m => '<option value="' + esc(m) + '"></option>').join('');
      label.textContent = 'MODEL' + (p ? ' — ' + p.name : '');
    };
    provEl.onchange = () => syncModel(true);
    syncModel(false);
  }
  if (sec === 'sandbox') wireSandboxCard(st);
  if (sec === 'backups') wireBackups(st);
  st.querySelectorAll('[data-tool]').forEach(sw => {
    sw.onchange = () => send({ type: 'tool_toggle', tool: sw.dataset.tool, enabled: sw.checked });
  });
  showDraftNotice(st);
  focusAfter(st, had);
  addressSection();
}
let contactDraft = null;
const contactLine = c => ({ name: c.name || '', channel: c.channel || '', address: c.address || '', wake: !!c.wake, operator: !!c.operator });
const contactLines = () => contactDraft || ((S.config && S.config.contacts) || []).map(contactLine);
const cleanContacts = lines => lines.map(c => ({ ...contactLine(c), name: c.name.trim(), channel: c.channel.trim().toLowerCase(), address: c.address.trim() }));
function contactsHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';
  const lines = contactLines();
  const field = (l, key, label) => '<input type="text" data-contact-field="' + key + '" aria-label="' + label + '" placeholder="' + label + '"' +
    (key === 'channel' ? ' list="contact-channels"' : '') + ' value="' + esc(l[key]) + '">';
  const tick = (l, key, label) => '<label><input type="checkbox" data-contact-field="' + key + '"' + (l[key] ? ' checked' : '') + '> ' + label + '</label>';
  return '<div class="card" data-contacts><h3>CONTACTS</h3><p>Who the identity can write to, and whose messages may wake it. A person\u2019s first line is tried first, then the next. Mark the lines that are yours as you: your notices arrive on this page, and with \u201cSend my notices to me off the dashboard\u201d on (Chat, under Expert) they go to these lines while no page is open. Changes apply at once.</p>' +
    '<datalist id="contact-channels">' + installedChannels().map(n => '<option value="' + esc(n) + '"></option>').join('') + '</datalist>' +
    (lines.length ? lines.map((l, i) => '<div class="item" data-contact-line="' + i + '">' +
      field(l, 'name', 'Name') + ' ' + field(l, 'channel', 'Channel') + ' ' + field(l, 'address', 'Address') + ' ' +
      tick(l, 'wake', 'May wake the identity') + ' ' + tick(l, 'operator', 'This is me') + ' ' +
      (i ? '<button class="btn ghost" data-contact-up="' + i + '">Move up</button> ' : '') +
      '<button class="btn ghost" data-contact-remove="' + i + '">Remove</button></div>').join('')
      : '<p class="muted">No contacts: the identity can write only to you, here.</p>') +
    '<button class="btn ghost" data-contact-add>Add a line</button>' +
    '<p class="muted" data-contacts-draft' + (contactDraft ? '' : ' hidden') + '>Unsaved edits \u2014 the saved contacts are still in force.</p>' +
    savebarHTML('contacts', 'applies at once') + '</div>';
}
function readContacts(card) {
  return [...card.querySelectorAll('[data-contact-line]')].map(row => {
    const f = key => row.querySelector('[data-contact-field="' + key + '"]');
    return { name: f('name').value, channel: f('channel').value, address: f('address').value, wake: f('wake').checked, operator: f('operator').checked };
  });
}
function wireContacts(st) {
  const card = st.querySelector('[data-contacts]');
  if (!card) return;
  card.addEventListener('input', () => { contactDraft = readContacts(card); card.querySelector('[data-contacts-draft]').hidden = false; });
  const edit = change => () => { const lines = readContacts(card); change(lines); contactDraft = lines; renderSettings(); };
  const add = card.querySelector('[data-contact-add]');
  if (add) add.onclick = edit(lines => lines.push(contactLine({})));
  card.querySelectorAll('[data-contact-remove]').forEach(b => { b.onclick = edit(lines => lines.splice(Number(b.dataset.contactRemove), 1)); });
  card.querySelectorAll('[data-contact-up]').forEach(b => { b.onclick = edit(lines => { const i = Number(b.dataset.contactUp); lines.splice(i - 1, 2, lines[i], lines[i - 1]); }); });
}
function saveContacts() {
  const card = document.querySelector('#settings-stack [data-contacts]');
  if (card) contactDraft = readContacts(card);
  const sent = cleanContacts(contactLines());
  sendConfigChanges('contacts', { contacts: sent }, { fields: new Map(), saved() {
    const newer = !!contactDraft && JSON.stringify(cleanContacts(contactDraft)) !== JSON.stringify(sent);
    if (!newer) contactDraft = null;
    requestMessages();
    return newer;
  } });
}
function num(v) { const n = parseInt(v, 10); return isNaN(n) ? 0 : n; }
function saveSettings(section, collecting = false) {
  if (section === 'contacts') { saveContacts(); return; }
  if (section === 'speech') { saveSpeechSettings(); return; }
  const ch = {};
  let service = null;
  if (section === 'storage') {
    ch['identity.db_format'] = $('cfg-db-format').value;
    ch['identity.db_optimize_levels'] = $('cfg-db-levels').checked;
    ch['identity.db_learn_dictionary'] = $('cfg-db-dictionary').checked;
  } else if (section === 'llm') {

    ch['llm.provider'] = $('cfg-provider').value;
    ch['llm.model'] = $('cfg-model').value.trim();
    const tmo = num($('cfg-timeout').value); if (tmo > 0) ch['llm.timeout_seconds'] = tmo;
    const ptmo = num($('cfg-probe-timeout').value); if (ptmo > 0) ch['llm.probe_timeout_seconds'] = ptmo;
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
    const id = section.slice('grants:'.length);
    document.querySelectorAll('[data-grant-plugin="' + id.replace(/"/g, '\\"') + '"]').forEach(el => {
      ch['plugins.grants.' + id + '.' + el.dataset.grantField] = !!el.checked;
    });
  } else if (section.startsWith('plugin:')) {

    const id = section.slice('plugin:'.length);
    document.querySelectorAll('[data-pset-plugin="' + id.replace(/"/g, '\\"') + '"]').forEach(el => {
      const key = 'plugins.settings.' + id + '.' + el.dataset.psetKey;
      const type = el.dataset.psetType;
      if (type === 'forget') { if (el.checked) ch[key] = null; }
      else if (type === 'boolean') ch[key] = !!el.checked;
      else if (type === 'number' || type === 'integer') {
        const n = el.value.trim() === '' ? NaN : Number(el.value); ch[key] = isNaN(n) ? null : n;
      }
      else if (type === 'secret') { const v = accountChoice(el); if (v !== undefined) ch[key] = v; }
      else ch[key] = el.value === '' ? null : el.value;
    });
  } else if (section === 'speech_stt' || section === 'speech_tts') {
    const dir = section.slice('speech_'.length);
    const sp = (S.config && S.config.speech) || {};
    const chosen = $('sp-provider-' + dir).value, own = ownServer(chosen, sp);
    const baseURL = own ? $('sp-base-' + dir).value.trim() : '';
    const provider = own ? ownServerName(baseURL) : chosen;
    if (own && !provider) { configResult = { kind: 'bad', section: section, text: 'An OpenAI-compatible engine needs the address its server answers at, such as http://localhost:8000/v1.' }; return; }
    const svc = speechService(provider, sp);
    ch['speech.' + dir + '.provider'] = provider;
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
        const k = 'plugins.settings.' + el.dataset.psetPlugin + '.' + el.dataset.psetKey;
        const t = el.dataset.psetType;
        if (t === 'forget') { if (el.checked) ch[k] = null; }
        else if (t === 'boolean') ch[k] = !!el.checked;
        else if (t === 'number' || t === 'integer') {
          const n = el.value.trim() === '' ? NaN : Number(el.value); ch[k] = isNaN(n) ? null : n;
        } else if (t === 'secret') { const v = accountChoice(el); if (v !== undefined) ch[k] = v; }
        else ch[k] = el.value === '' ? null : el.value;
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
  } else if (section === 'speech_mode') {
    ch['speech.mode'] = { listen: $('sp-mode-listen').value, speak: $('sp-mode-speak').value };
  } else if (section === 'route') {
    const v = $('cfg-route').value;
    ch['certificate.route_mode'] = v.startsWith('relay:') ? 'relay' : 'direct';
    if (v.startsWith('relay:')) ch['certificate.relay_endpoint'] = v.slice('relay:'.length);
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
  if (collecting) return { changes: ch, service };
  sendConfigChanges(section, ch, submittedEditor(section));
}

function accountChoice(el) {
  const picked = el.selectedOptions && el.selectedOptions[0];
  if (!el.value || !picked || picked.disabled) return undefined;
  return el.value !== (el.dataset.psetSaved || '') || el.dataset.psetGranted !== 'true' ? el.value : undefined;
}

function saveSpeechSettings() {
  if (config.waiting() || speechAdd.waiting()) return;
  const changes = {}, services = [];
  for (const section of ['speech_stt', 'speech_tts', 'speech_speakers', 'speech_mode']) {
    const row = saveSettings(section, true);
    if (!row) { configResult.section = 'speech'; return; }
    Object.assign(changes, row.changes);
    if (row.service) services.push(row.service);
  }
  const editor = submittedEditor('speech'), echo = $('sp-echo-mode').value;
  if (services.length) {
    const [first, ...remaining] = services;
    readySpeechService('speech', first.provider, first.key, first.baseURL, changes, editor, remaining, echo);
  } else sendConfigChanges('speech', changes, editor, echo);
}

export function sendConfigChanges(section, ch, editor = null, echo) {
  if (config.waiting() || speechAdd.waiting()) return;
  configResult = null;
  const plugin = section.startsWith('plugin:') ? section.slice('plugin:'.length) : '';
  const keys = plugin ? [...keyDrafts].filter(([k, value]) => k.split('\u0000')[0] === plugin && value.trim()).map(([draftKey, value]) => ({ draftKey, plugin, key: draftKey.split('\u0000')[1], value })) : [];
  sendConfigStep({ section, changes: ch, editor, echo, keys, keysSaved: false });
}

function sendConfigStep(pending) {
  delete pending.requestID;
  pending.key = pending.keys.shift() || null;
  const key = pending.key;
  const id = send(key ? { type: 'plugin_key_set', plugin_key: { plugin: key.plugin, key: key.key, secret: key.value.trim() } }
    : { type: 'config_set', config: pending.changes });
  if (!id) {
    configResult = { kind: 'bad', section: pending.section, text: (pending.keysSaved ? 'Earlier keys were saved. ' : '') + 'Not connected — remaining settings were not sent. Unsent edits are kept.' };
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

export function savebarHTML(section, note, more) {
  const owns = s => s === section || (section === 'speech' && s?.startsWith('speech_'));
  const adding = speechAdd.waiting() && owns(speechAdd.waiting().section) ? speechAdd.waiting() : null;
  const checking = config.waiting() && owns(config.waiting().section);
  const said = configResult && owns(configResult.section) ? configResult : null;
  const middle = adding ? '<span class="savesay" data-announce>Saving ' + esc(adding.name) + '… waiting for the runtime to confirm.</span>'
    : checking ? '<span class="savesay" data-announce>Checking and saving… the current configuration remains active.</span>'
    : said ? '<span class="config-result ' + said.kind + '" data-said="' + esc(section) + '" data-announce>' + esc(said.text) + '</span>'
    : note ? '<span class="savesay">' + esc(note) + '</span>' : '';
  const mine = !!adding || checking;
  const busy = mine || !!speechAdd.waiting() || !!config.waiting();
  return '<div class="savebar"><button class="btn" data-save="' + esc(section) + '"' + (busy ? ' disabled' : '') + '>' +
    (mine ? 'Checking…' : ((more && more.label) || 'Save')) + '</button>' + middle + ((more && more.also) || '') + '</div>';
}

export function settingsConnectionLost() {
  mailRepairDisconnected();
  messagesDisconnected();
  let changed = false;
  Object.keys(asking).forEach(k => delete asking[k]);
  const lostProv = prov.drop();
  if (lostProv) {
    provResult = { kind: 'bad', text: 'Connection lost before provider confirmation — check its current state after reconnect.' };
    changed = true;
  }
  const lostBroken = provBroken.drop();
  if (lostBroken) {
    provResult = { kind: 'bad', text: 'Connection lost before ' + lostBroken.label + ' was confirmed ' + (lostBroken.type === 'provider_repair' ? 'repaired' : 'removed') + ' — check Providers after reconnect.' };
    changed = true;
  }
  if (speechAdd.drop()) {
    configResult = { kind: 'bad', text: 'Connection lost before the speech service was confirmed — check Providers after reconnect.' };
    changed = true;
  }
  const lostConfig = config.drop();
  if (lostConfig) {
    configResult = { kind: 'bad', section: lostConfig.section, text: 'Connection lost before configuration confirmation — keys may already be saved. Check current settings after reconnect; unsent edits are kept.' };
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

function savedValue(state, path) {
  if (path === 'identity.db_format') return state && state.database && state.database.preferred;
  if (path === 'certificate.route_mode') return state?.public_name?.route_mode;
  if (path === 'certificate.relay_endpoint') return state?.public_name?.relay_endpoint;
  if (path === 'speech.mode' && state?.speech?.mode) {
    const mode = state.speech.mode;
    return { ...mode, speak: mode.speak === 'auto' ? '' : mode.speak };
  }
  if (path === 'speech.speakers' && state?.speech?.speakers) {
    const policy = state.speech.speakers;
    return { ...policy, uids: policy.uids || [] };
  }
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
  const missed = Object.keys(pending.changes).filter(p => {
    let sent = pending.changes[p], saved = savedValue(S.config, p);
    if (p === 'speech.speakers' && sent && saved) {
      sent = { ...sent, uids: [...new Set(sent.uids || [])].sort() };
      saved = { ...saved, uids: [...(saved.uids || [])].sort() };
    }
    return !sameSaved(saved, sent);
  });
  if (missed.length) {
    const labels = { 'speech.speakers': 'Who is heard', 'speech.mode': 'Conversation mode' };
    configResult = { kind: 'bad', section: pending.section, text: 'Save could not be verified for ' + missed.map(p => labels[p] || p).join(', ') +
      '. Your edits are kept; review the saved values and try again.' };
    return true;
  }
  if (pending.echo !== undefined) {
    try { setEchoMode(pending.echo); }
    catch (e) {
      configResult = { kind: 'bad', section: pending.section, text: 'Speech settings saved, but this browser could not save echo processing. Allow site storage and save again.' };
      return true;
    }
  }
  let newer = finishEditor(pending.editor);
  if (pending.editor?.saved?.()) newer = true;
  if (pending.editor) speechDraftSaved(pending.section, pending.editor.choice);
  configResult = { kind: 'good', section: pending.section, text: savedText(pending.section, (S.config && S.config.restart_required) || [], pending.changes) };
  const newerKey = pending.section.startsWith('plugin:') && [...keyDrafts.keys()].some(k => k.split('\u0000')[0] === pending.section.slice('plugin:'.length));
  if (newer || newerKey) configResult.text += ' Newer edits remain unsaved.';
  return true;
}

export function savedText(section, restartRequired, changes) {
  if (section === 'speech') return 'Speech settings saved. Echo processing is saved for this browser; open a new voice session to apply it.';
  if (section === 'storage') return 'Preferences saved — format at the next normal startup; optimization at the next maintenance pass. The active format has not changed.';
  if (section === 'llm') return 'Active — inference verified.';
  if (section === 'speech_stt' || section === 'speech_tts') {
    const dir = section.slice('speech_'.length), provider = changes && changes['speech.' + dir + '.provider'];
    if (!provider) return dir === 'stt' ? 'Saved — voice input is off.' : 'Saved — replies use the browser\'s built-in voice.';
    return 'Active — ' + provider + (dir === 'stt' ? ' transcribed the check.' : ' spoke the check.');
  }
  if (section === 'speech_speakers') return 'Saved — applies to the next words heard.';
  if (section === 'contacts') return 'Saved — the contacts are in force now.';
  if (section === 'route') return 'Saved — the name is re-routed on the route owner\'s next pass, within the minute.';
  if (section === 'agency') {
    const coaching = restartRequired.includes('agency.heuristic_nudges');
    return 'Saved — role routing applies live to the next role-tagged spawn; coaching prompts ' +
      (coaching ? 'take effect after restart.' : 'unchanged.');
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

document.addEventListener('click', (e) => {
  const btn = e.target && e.target.closest ? e.target.closest('[data-refresh-models]') : null;
  if (!btn) return;
  btn.disabled = true; btn.textContent = 'Asking…';
  query('discover', { provider: btn.dataset.refreshModels });
});
