
import { startSignIn, signInProgress, signInWanted, signInUnmetRow, credentialExpired, wireSignInCompletion } from '../signin.js';
import { S } from '../state.js';
import { settingHTML, settingValue, keyDrafts, keyDraftKey, keyTyping, wireSettingControls } from './plugin-setting.js';
import { $, copyText, esc, loadingCard } from '../util.js';
import { renderInto } from '../announce.js';
import { send, query } from '../ws.js';
import { spokenAudio } from '../say.js';
import { echoMode, echoModes, setEchoMode } from '../voice-echo.js';
import { sandboxCardHTML, wireSandboxCard } from '../sandbox.js';
import { scriptsCardHTML, wireScriptsCard, scriptsCardStale } from '../scripts.js';
import { pendingSlot } from '../pending.js';
import { holdReloadWhile } from '../overlay.js';
import { providerModels } from './model-picker.js';
import { backupsHTML, wireBackups, requestBackups, forgetBackupsSecrets } from './backups.js';
import { mailRepairHTML, wireMailRepair, requestHeldMail, mailRepairDisconnected } from './mail-repair.js';
import { messagesHTML, wireMessages, requestMessages, messagesDisconnected, installedChannels } from './messages.js';

const SECTIONS = [
  ['substrate', 'Substrate', c => substrateHTML(c)], ['providers', 'Providers', () => providersHTML()], ['speech', 'Speech', c => speechHTML(c)],
  ['dashboard', 'Dashboard', c => dashboardHTML(c)], ['backups', 'Backups & Keys', () => backupsHTML()], ['witness', 'Witness', c => witnessHTML(c)],
  ['prompt', 'Prompt', c => promptHTML(c)], ['agency', 'Agency', c => agencyHTML(c)], ['identity', 'Identity', c => cognitionHTML(c)], ['logs', 'Logs', c => logsHTML(c)],
  ['sandbox', 'Sandbox', () => sandboxCardHTML() || loadingCard('sandbox')], ['tools', 'Tools', () => toolsHTML()],
  ['storage', 'Storage', c => storageHTML(c)], ['messages', 'Messages', c => messagesHTML() + contactsHTML(c)],
  ['held-mail', 'Held mail', () => mailRepairHTML()],
  ['updates', 'Updates', () => updateCardHTML(S.update || null) + uninstallCardHTML(!!(S.stats && S.stats.uninstall_offered))]];
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
  if (!c) return loadingCard('configuration');
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
  if (!c) return loadingCard('configuration');

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
  const held = document.activeElement === button;
  disableOwing(button);
  button.textContent = 'Playing…';
  said('');
  const done = () => {
    button.disabled = false; button.textContent = words;
    if (!held) return;
    if (focusOwed && focusOwed.name === controlName(button)) focusOwed = null;
    if (button.isConnected && document.activeElement === document.body) button.focus({ preventScroll: true });
  };
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
  if (!c) return loadingCard('configuration');
  const sp = c.speech || { stt: {}, tts: {} };
  return '<div id="speech-form"><div class="card" style="position:sticky;top:0;z-index:2">' +
    '<h3>Speech settings</h3><p>Make your changes below, then save once. Listening rules apply immediately; engine and echo settings apply to the next voice session.</p>' +
    savebarHTML('speech', '', { label: 'Save speech settings' }) + '</div>' +
    voiceModeCard(sp) + speakersCard(sp) + heardHistoryCard(sp) + speechCard('stt', sp) + speechCard('tts', sp) + '</div>';
}

const HEARD_FIELDS = [
  ['sp-heard-capacity', 'capacity', 'finals held', 1, 2048, 256],
  ['sp-heard-retention', 'retention_seconds', 'seconds each is held', 60, 86400, 1800],
  ['sp-heard-text', 'text_bytes', 'text bytes of each', 256, 16384, 8192],
  ['sp-heard-rows', 'page_rows', 'rows a read returns', 1, 50, 20],
];

function heardHistoryCard(sp) {
  const h = sp.heard_history || {};
  return '<div class="card" id="sp-heard-history" data-speech-section="speech_heard">' + '<h3 title="How much of what was heard the host holds for the identity to read when they ask, and for how long.">Heard history</h3>' +
    '<p class="muted">What was heard is held in memory for the identity to read when they ask, and is cleared when the identity restarts. Empty takes the default.</p>' +
    HEARD_FIELDS.map(([id, key, label, min, max]) => '<label class="f sub">' + label +
      '<input type="number" id="' + id + '" min="' + min + '" max="' + max + '" step="1" value="' + esc(String(h[key] || '')) + '"></label>').join('') +
    '<div class="muted" id="sp-heard-readback">' + esc(heardHistoryReadback(h)) + '</div></div>';
}

export function heardHistoryReadback(h) {
  if (!h.capacity) return '';
  const plural = (n, one) => n + ' ' + one + (n === 1 ? '' : 's');
  return 'In force: the last ' + plural(h.capacity || 0, 'final') + ', up to ' + (h.text_bytes || 0) + ' text bytes each, for ' + retentionSaid(h.retention_seconds || 0) +
    '; a read returns ' + plural(h.page_rows || 0, 'row') + ' unless the identity asks for another number.';
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
    '<p class="muted" id="sp-heard-says">' + esc(heardReadSentence(sp.heard_history)) + '</p>' +
    '<button type="button" class="btn ghost" id="sp-heard-show">Show what was heard</button>' +
    '<div id="sp-heard"' + (heardShown ? '' : ' hidden') + '>' + heardListHTML() + '</div>' +
    '</div>';
}

function retentionSaid(secs) {
  const plural = (n, one) => n + ' ' + one + (n === 1 ? '' : 's');
  return secs % 60 === 0 ? plural(secs / 60, 'minute') : plural(secs, 'second');
}

function heardReadSentence(h) {
  const secs = (h && h.retention_seconds) || HEARD_FIELDS[1][5];
  return 'The identity can read what was heard in the last ' + retentionSaid(secs) + ', withheld words included, when they ask to. You can read the same here.';
}

let heardShown = null;
let heardAsking = false;

function heardListHTML() {
  if (!heardShown) return '';
  if (heardShown.why) return '<div class="muted" data-heard-refused>' + esc('What was heard cannot be shown: ' + heardShown.why) + '</div>';
  return heardShown.rows.map(heardRowHTML).join('') +
    (heardShown.none ? '<div class="muted" data-heard-none>' + esc(heardShown.none) + '</div>' : '') +
    (heardShown.next ? '<button type="button" class="btn ghost" id="sp-heard-older" style="margin-top:8px">Older</button>' : '');
}

function heardRowHTML(r) {
  const held = r.delivery === 'withheld';
  const was = r.initial_attribution && r.initial_attribution !== r.attribution ? ' (at first: ' + r.initial_attribution + ')' : '';
  const where = 'session ' + (r.session || '') + ', final ' + (r.final == null ? '' : r.final) + (r.track ? ', track ' + r.track : '') +
    (r.speaker_uuid ? ', speaker ' + r.speaker_uuid : '');
  return '<div class="heard-row" data-heard-seq="' + esc(String(r.seq)) + '" style="margin-top:8px">' +
    '<div class="muted" style="font-size:12px">' + esc(r.observed_at || '') + ' · ' + esc(r.delivery || '') +
    (r.delivery_reason ? ' (' + esc(r.delivery_reason) + ')' : '') + ' · ' + esc((r.attribution || '') + was) + ' · ' + esc(where) + '</div>' +
    '<div data-heard-text' + (held ? ' data-heard-withheld' : '') + ' style="white-space:pre-wrap">' + esc(r.text || '') + '</div>' +
    (r.text_truncated ? '<div class="muted" data-heard-cut style="font-size:12px">[cut at the host\'s limit for one final]</div>' : '') + '</div>';
}

async function showHeard(after) {
  if (heardAsking) return;
  heardAsking = true;
  let body = null, why = '';
  try {
    const res = await fetch('/speech/heard' + (after ? '?after=' + encodeURIComponent(String(after)) : ''), { cache: 'no-store', credentials: 'same-origin' });
    const text = await res.text();
    try { body = JSON.parse(text); } catch (e) { body = null; }
    if (!res.ok) why = (body && body.error) || text.trim() || ('the host answered ' + res.status);
    else if (!body || typeof body !== 'object') why = 'the host\'s answer was not heard speech';
  } catch (e) {
    why = 'the host could not be asked: ' + (e && e.message ? e.message : e);
  }
  heardAsking = false;
  if (why) {
    heardShown = { rows: [], next: 0, why, none: '' };
  } else {
    const rows = Array.isArray(body.rows) ? body.rows : [];
    const before = after && heardShown && !heardShown.why ? heardShown.rows : [];
    heardShown = { rows: before.concat(rows), next: body.next_after_seq || 0, why: '',
      none: rows.length ? '' : after ? 'Nothing heard is held before that.' : 'Nothing heard is held.' };
  }
  drawHeard(document);
}

function drawHeard(root) {
  const box = root.querySelector('#sp-heard');
  if (!box) return;
  box.hidden = !heardShown;
  box.innerHTML = heardListHTML();
  wireHeard(root);
}

function wireHeard(root) {
  const show = root.querySelector('#sp-heard-show'), older = root.querySelector('#sp-heard-older');
  if (show) show.onclick = () => showHeard(0);
  if (older) older.onclick = () => showHeard(heardShown && heardShown.next);
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
  wireHeard(root);
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
  const revealWaiting = !tokenShown && (!S.connected || !!tokenBusy);
  const revealTitle = revealWaiting ? (tokenBusy ? 'Wait for the access-token change' : 'Reconnect to show access token') : 'Show access token';
  if (!required) {
    return '<label class="f">ACCESS TOKEN</label>' +
      '<div data-token-open>This dashboard asks no browser for a token: any browser on this machine that opens it is its operator.</div>' +
      tokenActHTML('require', 'Ask for an access token', 'Turning on\u2026', secretsOK, 'Asking for a token is turned on over a private connection only \u2014 open this page on the machine itself, or over HTTPS.') +
      TOKEN_OFF_WHERE;
  }
  return '<label class="f" for="cfg-dtoken">ACCESS TOKEN</label>' +
    '<div style="display:flex;align-items:center;gap:6px"><input id="cfg-dtoken" type="password" value="' + esc(tokenShown) + '" readonly autocomplete="off" spellcheck="false" style="flex:1" placeholder="press the eye to show it">' +
    '<button type="button" class="copyb" data-token-reveal title="' + revealTitle + '" aria-label="' + revealTitle + '" aria-pressed="' + (tokenShown ? 'true' : 'false') + '"' + (revealWaiting ? ' disabled' : '') + '>' + TOKEN_EYE + '</button>' +
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
    scriptsCardStale();
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
  if (!c) return loadingCard('configuration');
  const tls = !!c.dashboard.tls;
  return '<div class="card"><h3>DASHBOARD — BIND ADDRESS</h3>' +
    cfgField('cfg-dhost', 'HOST (127.0.0.1 = THIS MACHINE ONLY; A LAN IP OR 0.0.0.0 EXPOSES THE DASHBOARD TO THAT NETWORK)', c.dashboard.host) +
    cfgField('cfg-dport', 'PORT', c.dashboard.port) +
    '<label class="f" style="display:flex;gap:6px;align-items:center;margin-top:8px"><input type="checkbox" id="cfg-dtls"' + (tls ? ' checked' : '') + '> HTTPS (REQUIRED FOR THE MICROPHONE, AND FOR ANY ADDRESS OTHER THAN THIS MACHINE)</label>' +
    dashboardWarnHTML(c.dashboard.host, tls) +
    dashboardTokenHTML(!!c.dashboard.require_token, !!c.secrets_ok) +
    savebarHTML('dashboard', 'saved — applies next restart') + '</div>' +
    scriptsCardHTML() +
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
      if (!S.connected || tokenBusy) return;
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
  if (!c) return loadingCard('configuration');
  return '<div class="card"><h3>WITNESS</h3>' +
    cfgField('cfg-wurl', 'URL', c.witness.url) +
    cfgField('cfg-wint', 'INTERVAL (EVENTS)', c.witness.interval_events) +
    savebarHTML('witness', 'saved — applies next boot') + '</div>';
}
function promptHTML(c) {
  if (!c) return loadingCard('configuration');
  return '<div class="card"><h3>PROMPT</h3>' +
    cfgField('cfg-ptokens', 'MAX TOKENS', c.prompt.max_tokens) +
    cfgField('cfg-pturns', 'RECENT TURNS', c.prompt.recent_turns) +
    savebarHTML('prompt', 'saved — applies next boot') + '</div>';
}
function agencyHTML(c) {
  if (!c) return loadingCard('configuration');
  const on = !!(c.agency && c.agency.prefer_local_for_roles);
  return '<div class="card"><h3>AGENCY</h3>' +
    '<label class="f" style="display:flex;gap:6px;align-items:center;margin-top:4px"><input type="checkbox" id="cfg-plr"' + (on ? ' checked' : '') + '> USE LOCAL MODEL FOR SUB-AGENT ROLES</label>' +
    '<div data-lifecycle="routing" style="font-size:11.5px;margin-top:4px">Applies live &mdash; the next role-tagged spawn honors it. Explicit agency.roles routes still win.</div>' +
    '<div style="font-size:11.5px;color:var(--faint);margin-top:8px">When checked and a provider entry marked <b>local</b> answers /models, role-tagged spawns (proposer, critic, judge…) run there — free, private evaluation on your own metal. Explicit agency.roles routes still win; untagged spawns are copies of the identity and always think with the configured model. If the local host is down, everything falls back to the configured model (logged).</div>' +
    savebarHTML('agency', 'role routing applies live') + '</div>';
}

const COGNITION_FIELDS = {
  'agency.rhythm_seconds': ['METABOLISM CADENCE', 'How often the rhythm looks for material to digest, operator present or not.'],
  'cognition.consolidate_spacing_seconds': ['CONSOLIDATION SPACING', 'The least time between consolidation passes, and between outcome intakes; a dream pass that advanced nothing waits as long. More than the cadence, or dreaming gets no tick while experiences wait. Blank follows the cadence: three ticks of it, and never less than thirty minutes.'],
  'cognition.consolidate_batch': ['EXPERIENCES PER CONSOLIDATION', 'Fewer leaves more room for the beliefs each pass weighs.'],
  'cognition.consolidate_max_ops': ['OPERATIONS PER CONSOLIDATION', 'A reply with more mints nothing, and the next pass reads half as many experiences.'],
  'agency.outcome_window': ['OUTCOME PROBE WINDOW', 'Records of the identity\u2019s own ledger one tick reads, looking for what became of an intention or a commitment.'],
  'cognition.self_model_every_minutes': ['SELF-PORTRAIT EVERY', 'Lived time between portraits: minutes with you present, counted in pulses.'],
  'cognition.identity_review_every_minutes': ['IDENTITY REVIEW EVERY', 'Lived time between reviews of the record\u2019s structure.'],
  'cognition.self_model_evidence_rows': ['PORTRAIT: RECENT EXPERIENCES', 'The most recent experiences a portrait is drawn from.'],
  'cognition.self_model_spoken_rows': ['PORTRAIT: WHERE OTHERS SPOKE', 'Experiences where someone else spoke, which enter a portrait whatever their age.'],
  'prompt.self_model_max_tokens': ['PORTRAIT BUDGET', 'The longest a new portrait may be; it rides in every prompt. Portraits already accepted stand.'],
  'prompt.ring3_max_chars': ['WORKING TRUTH BOUND', 'The longest view of working truth consolidation may write.'],
  'prompt.surfacing_max_chars': ['DREAM NOTE BOUND', 'The longest note a dream pass may write; an outcome observation is held to it too.'],
  'prompt.dream_conversation_max_chars': ['CONVERSATION ONE DREAM READS', 'How much unread conversation one dream pass reads.'],
  'prompt.tensions_max_chars': ['CONTRADICTION VIEW BOUND', 'How much of the standing contradictions a pass is shown.'],
  'prompt.brief_max_chars': ['MORNING BRIEF BOUND', 'The longest brief kept; a longer one is refused and yesterday\u2019s stands.'],
  'prompt.excerpt_chars': ['QUOTED TEXT SHOWN', 'How much of one quoted text the identity\u2019s own passes are shown \u2014 a note or belief in a contradiction, a retired belief, an outcome \u2014 and working truth shows of each recent experience. Past it the text is cut, and the cut is said.'],
  'prompt.ring3_recent_experiences': ['RECENT EXPERIENCES IN WORKING TRUTH', 'How many of the newest processed experiences working truth lists; it rides in every prompt.'],
  'prompt.alarm_firings': ['ALARM FIRINGS LISTED', 'How many of the newest firings the identity is shown when they read their alarms; one line says how many came earlier and how to ask for them.'],
  'agency.turn_token_budget': ['TURN TOKEN FENCE', 'The tokens one conversation turn may spend across all its calls before it wraps up.'],
  'agency.tool_schema_refusals': ['TOOLS LEFT OUT A TURN', 'How many tools one turn may leave out because the model provider would not accept their definitions; past it the turn ends on the refusal.'],
  'agency.turn_checkpoint_calls': ['TURN CHECKPOINT', 'The tool calls a turn may make before it is asked to say where it stands, when it declared no estimate of its own; a turn that declared one is asked at its estimate times the slack below.'],
  'agency.turn_checkpoint_slack': ['SLACK ON A DECLARED ESTIMATE', 'How many times its own estimate of tool calls a turn that declared one may make before it is asked to say where it stands. Estimates run low, by about two and a half times as measured.'],
  'agency.turn_checkpoint_grace_rounds': ['ROUNDS AFTER A CHECKPOINT', 'The rounds a turn has after its checkpoint before it ends; while its work is unfinished the work continues in a fresh turn.'],
  'agency.max_turn_continuations': ['CONTINUED TURNS IN A ROW', 'How many turns in a row may continue unfinished work unattended before the identity stands down for you to look. A message from you, typed or spoken, starts a new chain.'],
  'agency.continuation_wall_seconds': ['WALL OF A CONTINUED TURN', 'The longest one continued turn of unfinished work may run; at it the turn is cut, and nothing continues it by itself.'],
  'agency.alarm_wall_seconds': ['WALL OF AN ALARM\u2019S TURN', 'The longest the turn an alarm wakes the identity for may run. How long the alarm waits for the identity to be free is not bounded.'],
  'agency.harvest_wall_seconds': ['WALL OF A HARVEST\u2019S TURN', 'The longest the turn that takes in a finished sub-agent\u2019s work may run.'],
};
const COGNITION_APPLIES = {
  'agency.turn_token_budget': 'applies from the next turn',
  'agency.tool_schema_refusals': 'applies from the next turn',
  'agency.turn_checkpoint_calls': 'applies from the next turn',
  'agency.turn_checkpoint_grace_rounds': 'applies from the next turn',
  'agency.turn_checkpoint_slack': 'applies from the next turn',
  'agency.max_turn_continuations': 'applies when the next turn ends',
  'agency.continuation_wall_seconds': 'applies from the next continued turn',
  'agency.alarm_wall_seconds': 'applies from the next turn an alarm wakes',
  'agency.harvest_wall_seconds': 'applies from the next harvest\u2019s turn',
  'prompt.alarm_firings': 'applies at the next read of the alarms',
};
function cognitionApplies(key) { return COGNITION_APPLIES[key] || 'the rhythm reads it at its next pass'; }
const FACILITY_NAMES = { consolidate: 'Consolidation', dream: 'Dreaming', self_model: 'Self-portrait', outcomes: 'Outcome intake', brief: 'Morning brief' };
function cognitionField(set, c) {
  const [label, note] = COGNITION_FIELDS[set.key] || [set.key, ''];
  const configured = configValue(c, set.key);
  const id = 'cog-' + set.key.replace(/[^a-z0-9]+/g, '-');
  const dflt = set.default + ' (default' + (set.key === 'cognition.consolidate_spacing_seconds' ? ', three ticks of the cadence and at least thirty minutes' : '') + ')';
  return '<label class="f" for="' + id + '">' + esc(label) + ' — ' + esc(set.unit) + ', ' + esc(set.min) + ' to ' + esc(set.max) + '</label>' +
    '<input type="number" id="' + id + '" data-cog-key="' + esc(set.key) + '" min="' + esc(set.min) + '" max="' + esc(set.max) + '" step="1" placeholder="' + esc(dflt) + '" value="' + esc(configured == null ? '' : configured) + '"' +
    hinted(configured, 'Blank takes the default: ' + dflt) + '>' +
    (set.held ? '<div class="dim-note" data-cog-held="' + esc(set.key) + '" style="margin:2px 0">' + esc(set.held) + '</div>' : '') +
    '<div class="muted" data-cog-applies="' + esc(set.key) + '" style="font-size:11.5px;margin:2px 0 8px">' + esc((note ? note + ' ' : '') + cognitionApplies(set.key).replace(/^./, c => c.toUpperCase()) + '.') + '</div>';
}
function cognitionHTML(c) {
  if (!c) return loadingCard('configuration');
  const cog = c.cognition || {};
  const at = cog.morning_brief_at || '';
  const off = at === 'off';
  const budget = cog.input_budget
    ? esc(cog.input_budget) + ' tokens (' + (cog.input_budget_source === 'derived' ? 'derived from the model\u2019s window' : cog.input_budget_source === 'fallback' ? 'fallback \u2014 set context length on the provider to give the rhythm the model\u2019s window' : 'the conversation\u2019s ceiling \u2014 no window is known for this model') + ')'
    : '\u2014';
  const conditions = (cog.conditions || []).map(k =>
    '<li data-cog-condition="' + esc(k.facility) + '" data-kind="' + esc(k.kind) + '"><b>' + esc(FACILITY_NAMES[k.facility] || k.facility) + '</b> \u2014 ' + esc(k.said) +
    (k.at ? ' <span class="muted">(' + esc(new Date(k.at).toLocaleString()) + ')</span>' : '') + '</li>').join('');
  return '<div class="card" data-cognition><h3>COGNITION</h3>' +
    '<p class="muted">The rhythm\u2019s own numbers, and the turn\u2019s: how the identity digests, reflects and is briefed, and how far a turn goes. Each says under it when it applies. A blank field takes the default shown in it.</p>' +
    '<div class="kv"><span>input budget</span><b data-cog-budget>' + budget + '</b></div>' +
    (conditions ? '<div class="dim-note" style="margin:8px 0">What the last passes came to:</div><ul data-cog-conditions style="margin:0 0 10px 18px;font-size:12.5px;line-height:1.6">' + conditions + '</ul>' : '<div class="dim-note" data-cog-conditions style="margin:8px 0">No facility\u2019s last pass reported carrying less than it was asked.</div>') +
    (cog.settings || []).map(set => cognitionField(set, c)).join('') +
    '<label class="f" for="cog-brief-at">MORNING BRIEF AT \u2014 local time</label>' +
    '<input type="time" id="cog-brief-at" value="' + esc(off ? '' : at) + '" placeholder="' + esc(cog.morning_brief_default || '') + '"' + (off ? ' disabled' : '') + hinted(off ? 'off' : at, 'Blank takes the default: ' + (cog.morning_brief_default || '')) + '>' +
    '<label class="f" style="display:flex;gap:6px;align-items:center;margin-top:4px"><input type="checkbox" id="cog-brief-off"' + (off ? ' checked' : '') + '> NO MORNING BRIEF</label>' +
    '<div class="muted" style="font-size:11.5px;margin:2px 0 8px">Blank is ' + esc(cog.morning_brief_default || '') + '. With no brief, the last one is taken away rather than left standing as this morning\u2019s.</div>' +
    savebarHTML('cognition', 'each applies as its field says') + '</div>';
}

function logsHTML(c) {
  if (!c) return loadingCard('configuration');
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
    html += loadingCard('log files');
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
  credential_expired: ['#c9a227', 'adopted credential expired — sign in again, or refresh it with the tool it is borrowed from'],
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
  if (p.status_reason) bits.push(esc(p.status_reason));
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
    ['openai', 'anthropic', 'gemini'].map(t => '<option value="' + t + '"' + (t === apiType ? ' selected' : '') + '>' + t + '</option>').join('') +
    '</select>' +
    cfgField('pv-url-' + tag, 'ENDPOINT URL', p.endpoint || '') +
    cfgField('pv-model-' + tag, 'DEFAULT MODEL', p.default_model || '') +

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
    (p.signin && p.signin.status === 'pending' ? '<button class="btn ghost" data-prov-signin-cancel="' + esc(p.name) + '">Cancel sign-in</button>' : '') + '</div>';
}
export function wireProviderSignIn(root) {
  wireSignInCompletion(root);
  root.querySelectorAll('[data-prov-signin-cancel]').forEach(btn => { btn.onclick = () => send({type:'provider_signin_cancel',provider:btn.dataset.provSigninCancel}); });
  root.querySelectorAll('[data-prov-signin]').forEach(btn => { btn.onclick = () => {
    startSignIn({ type: 'provider_signin', provider: btn.dataset.provSignin });
  }; });
}

const shellWord = (s) => "'" + String(s).replace(/'/g, "'\\''") + "'";

export function updateCardHTML(u) {
  let state;
  const managed = !!(u && u.stage_refusal);
  if (!u || !u.enabled) state = 'Unavailable — this build carries no release version, so it does not check for updates.';
  else if (u.checking) state = 'Checking…';
  else if (u.error) state = 'Last check failed: ' + esc(u.error);
  else if (u.needs_restart) state = 'Update ' + esc(u.installed_version) + ' is installed — relaunch to run it.';
  else if (u.available_version && managed && u.package && u.package.path) state = 'Update ' + esc(u.available_version) + ' is downloaded and verified against the release\'s signature. Install it with <code>sudo dpkg -i ' + esc(shellWord(u.package.path)) + '</code>; an identity running as a service restarts on the new build.';
  else if (u.available_version && managed && u.package && u.package.refusal) state = 'Update ' + esc(u.available_version) + ' is announced, and its package is not offered: ' + esc(String(u.package.refusal).replace(/[.\s]+$/, '')) + '. Nothing was verified, so there is nothing to install.';
  else if (u.available_version && managed) state = 'Update available: ' + esc(u.available_version) + ' — this program cannot replace itself where it is installed. Update it the way it was installed' + (u.release_url ? '; <a href="' + esc(u.release_url) + '" target="_blank" rel="noopener">the release</a> is here' : '') + '.';
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
  const short = !!(c.dashboard && c.dashboard.token_short);
  const relayValue = r => 'relay:' + r.name + ':' + r.port;
  const held = (relays || []).find(r => r.name === (p.relay_endpoint || '').replace(/:\d+$/, ''));
  const chosen = mode === 'relay' ? (held ? relayValue(held) : 'relay:' + (p.relay_endpoint || '')) : 'direct';
  const opt = (value, label, off, to, relay) => '<option value="' + esc(value) + '"' + (value === chosen ? ' selected' : '') + (off ? ' disabled' : '') +
    ' data-move="' + esc(routeMoveText(p, c, to, relay)) + '">' + esc(label) + '</option>';
  let options = opt('direct', 'Direct \u2014 the addresses this machine is reached at', false, p.direct_origin, false);
  (relays || []).forEach(r => { options += opt(relayValue(r), 'Relay \u2014 through ' + r.name + ' (port ' + r.port + ')', !token || (short && relayValue(r) !== chosen), r.origin, true); });
  let note = '';
  let ask = '';
  if (relays == null) {
    note = 'The certificate service has not been asked which relays it offers.';
    ask = '<div class="savebar"><button class="btn ghost" data-public-name="look">Ask which relays are offered</button></div>';
  }
  else if (!relays.length) note = 'The certificate service advertises no relay.';
  else if (!token) note = 'A relay carries the public internet to this dashboard, so one can be chosen only while the dashboard asks for its access token, and it does not now \u2014 turn it on above with Ask for an access token, then choose one.';
  else if (short) note = 'A relay exposes this dashboard\u2019s sign-in to the internet, and its access token is short \u2014 Rotate access token above makes a new one, then choose a relay.';
  else note = 'A relay serves one port, and behind it the name is reached on that port: a relay on another port than the address has now moves the address.';
  if (short && mode === 'relay') html += '<div class="config-result bad" data-route-token-short data-announce style="margin-top:6px">The access token is short, and the relay exposes this dashboard\u2019s sign-in to the internet. Rotate access token above makes a new one.</div>';
  const expert = !!(S.stats && S.stats.expert);
  return html + '<div data-route-expert' + (expert ? '' : ' hidden') + '>' + '<select id="cfg-route">' + options + '</select>' +
    '<div class="muted" data-route-note style="font-size:12px;margin-top:6px">' + note + '</div>' + ask +
    '<div data-route-move data-announce style="font-size:12px;margin-top:6px" hidden></div>' +
    (expert ? directAddressesHTML(p) : '') +
    savebarHTML('route', '', { label: 'Use this route' }) + '</div></div>';
}

let openFlow = null;
const accessCodeAsk = pendingSlot();
let offSaid = null, offAsked = false;

function reachesOtherDevices(p) {
  return p.route_mode === 'relay' || (!!p.address && p.address !== 'withdrawn');
}

function nameServiceSaid(c) {
  const row = ((c && c.dashboard && c.dashboard.settings) || []).find(r => r.key === 'certificate.server_url');
  return row && row.value ? 'the name service at ' + row.value : 'the name service';
}

const declinedSaid = 'The certificate service is declined in this identity\u2019s configuration, so no name can be claimed.';
function claimDeclined(p) {
  return !!(p && p.service_declined) && (!p.name || p.status === 'none');
}

const relaySees = 'can see that you connected, from which address, when and how much';

function pressDoesSaid(lead) {
  return lead + ' turns on the access token if it is off, has a certificate made for the name (certificates are listed in public logs), and makes a one-time access code for the other device. ' +
    'When nothing else leads here from outside this machine\u2019s network, it also asks the name service which relays it offers, saves one as the route and connects through it: a relay cannot read what it carries, and it ' + relaySees + '.';
}

function openElsewhereHTML(p, c) {
  const f = openFlow || {};
  let html = '<div data-open-elsewhere style="margin-top:14px"><label class="f">OPEN ON ANOTHER DEVICE</label>';
  if (f.code) {
    const life = f.code.seconds % 60 ? f.code.seconds + ' seconds' : (f.code.seconds / 60) + (f.code.seconds === 60 ? ' minute' : ' minutes');
    html += '<div data-open-code>' + (f.code.qr ? '<div data-open-qr style="width:220px;max-width:100%;background:#fff;padding:4px">' + f.code.qr + '</div>' : '') +
      '<div style="margin-top:8px">On a phone, point its camera at the picture. On a device with no camera, open <b>' + esc(p.origin || '') + '</b> and type this code where it asks you to sign in:</div>' +
      '<div data-open-code-text style="font-size:22px;letter-spacing:2px;margin:6px 0;user-select:all"><b>' + esc(f.code.code) + '</b></div>' +
      '<div class="muted" style="font-size:12px">It works once, and for ' + esc(life) + (f.code.own ? '. It is your own code: it is as strong as what you typed, and no count of wrong tries is kept.' : '.') + '</div></div>';
  } else {
    html += '<div class="muted" style="font-size:12px">A phone, a laptop, any device with a browser. Nothing is installed on it.</div>';
    if (claimDeclined(p)) {
      html += '<div data-open-claims style="font-size:12px;margin-top:6px">This identity has no public name. ' + declinedSaid + '</div>';
    } else if (!p || !p.name || p.status === 'none') {
      html += '<div data-open-claims style="font-size:12px;margin-top:6px">This identity has no public name. Pressing this claims one for it: its public key is sent to ' + esc(nameServiceSaid(c)) +
        ', which gives it a name, and the claim is written in its record. Nothing is sent until you press.</div>' +
        '<div data-open-does style="font-size:12px;margin-top:6px">' + pressDoesSaid('The same press') + '</div>';
    } else if (p.status === 'prepared') {
      html += '<div data-open-does style="font-size:12px;margin-top:6px">' + pressDoesSaid('Pressing this') + '</div>';
    }
  }
  if (f.said) html += '<div class="' + (f.bad ? 'config-result bad' : 'savesay') + '" data-open-said data-announce style="margin-top:6px">' + esc(f.said) + '</div>';
  if (f.needsMove) html += '<div data-open-move data-announce style="font-size:12px;margin-top:6px">' + esc(f.needsMove) + '</div>';
  const busy = !!f.running;
  html += '<div class="savebar">' +
    (f.needsMove ? '<button class="btn" data-open-elsewhere-act="move">Move the address and continue</button>'
      : '<button class="btn" data-open-elsewhere-act="start"' + (busy || claimDeclined(p) ? ' disabled' : '') + '>' + (f.code ? 'New code' : 'Open on another device') + '</button>') +
    (f.code || busy || f.needsMove ? '<button class="btn ghost" data-open-elsewhere-act="done">Done</button>' : '') + '</div>';
  if (f.code || busy) {
    html += '<div style="margin-top:8px"><label class="f" for="cfg-open-own">YOUR OWN CODE, IF YOU PREFER ONE</label>' +
      '<input type="text" id="cfg-open-own" autocomplete="off" spellcheck="false" placeholder="12 characters or more">' +
      '<div class="savebar"><button class="btn ghost" data-open-elsewhere-act="own">Use my own code</button></div></div>';
  }
  if (p.route_mode === 'relay' && !busy && !f.needsMove && c && c.secrets_ok) {
    html += '<div class="savebar"><button class="btn ghost" data-open-elsewhere-act="off">Stop using the relay</button></div>' +
      '<div class="muted" data-open-off-note style="font-size:12px">After this no new connection reaches this dashboard from outside its own network. The name and its certificate are kept, devices stay signed in, and Open on another device brings it back.</div>';
  }
  if (offSaid) html += '<div class="' + (offSaid.bad ? 'config-result bad' : 'savesay') + '" data-open-off-said data-announce style="margin-top:6px">' + esc(offSaid.text) + '</div>';
  if (p.route_mode === 'relay' || busy || f.code) {
    html += '<div class="muted" style="font-size:12px;margin-top:8px">' +
      (p.route_mode === 'relay' ? 'A relay brings the other device to this one, and carries the page and its files. It cannot read any of it. It ' + relaySees + '. The live session moves off the relay when the two devices can reach each other. ' : '') +
      'Every device shares one sign-in: if a device is lost, Rotate access token above signs all of them out at once.</div>';
  }
  return html + '</div>';
}

function openElsewhereAdvance() {
  const f = openFlow;
  if (!f || !f.running || f.busy) return;
  const c = S.config, p = (c && c.public_name) || {};
  const settle = () => { clearTimeout(f.poll); f.poll = null; };
  const stop = (text, bad) => { settle(); f.running = false; f.bad = bad !== false; f.said = text; };
  const wait = text => {
    f.bad = false; f.said = text;
    if (!f.poll) f.poll = setTimeout(() => { f.poll = null; send({ type: 'public_name_state' }); send({ type: 'query', query: 'config' }); }, 2000);
  };
  if (!c || !c.dashboard) return wait('Reading this dashboard\u2019s settings\u2026');
  if (!c.secrets_ok) return stop('This connection is not private, so nothing was set up. Open this page on the machine itself, or over HTTPS.');
  if (claimDeclined(p)) return stop(declinedSaid);
  if (!c.dashboard.require_token) {
    f.busy = true; f.bad = false; f.said = 'Turning on the access token\u2026';
    S.requireAccessToken().then(res => {
      f.busy = false;
      if (!res || !res.ok) stop((res && res.text) || 'The access token could not be turned on.');
      else if (S.config && S.config.dashboard) S.config.dashboard.require_token = true;
      renderSettings();
    });
    return;
  }
  if (!p.name || p.status === 'none') {
    if (!f.claimed) { f.claimed = true; send({ type: 'public_name_claim' }); }
    return wait('Claiming this identity\u2019s name\u2026');
  }
  if (p.status === 'claiming') return wait('Obtaining the certificate for ' + p.name + '\u2026');
  if (p.status !== 'issued' && p.status !== 'prepared') return stop('The certificate for ' + p.name + ' could not be obtained: ' + (p.last_error || 'unknown') + '. Retry now, above, tries again.');
  if (!reachesOtherDevices(p)) {
    if (c.dashboard.token_short) return stop('The access token is short, and a relay exposes this dashboard\u2019s sign-in to the internet. Rotate access token above makes a new one; then press this again.');
    if (p.relays == null) {
      if (!f.lookedAt || Date.now() - f.lookedAt > 15000) { f.lookedAt = Date.now(); send({ type: 'public_name_look' }); }
      return wait('Asking which relays are offered\u2026');
    }
    const r = p.relays.find(x => x.suggested);
    if (!r) return stop('No relay is offered yet, so nothing leads to this machine from outside its own network. A technical operator can reach it over their own VPN: Expert shows how.');
    if (r.origin && p.origin && r.origin !== p.origin && (p.moves_with || []).length && !f.acceptMove) {
      settle(); f.running = false; f.bad = false; f.said = ''; f.needsMove = routeMoveText(p, c, r.origin, true);
      return;
    }
    if (!f.routed) {
      f.routed = true;
      const select = document.getElementById('cfg-route');
      if (!select) return stop('The route could not be chosen from this page.');
      select.value = 'relay:' + r.name + ':' + r.port;
      saveSettings('route');
    }
    if (configResult && configResult.section === 'route' && configResult.kind === 'bad') return stop(configResult.text);
    return wait('Choosing the relay\u2026');
  }
  if (p.status === 'prepared') {
    if (!f.ordered) { f.ordered = true; send({ type: 'public_name_retry' }); }
    return wait('Obtaining the certificate for ' + p.name + '\u2026');
  }
  if (p.route_mode === 'relay' && !p.relay_connected) return wait(p.relay_error ? 'The relay is not carrying this name yet: ' + p.relay_error : 'Connecting to the relay\u2026');
  if (!f.code && !accessCodeAsk.waiting()) {
    settle(); f.bad = false; f.said = 'Making a code\u2026';
    if (!accessCodeAsk.arm({}, send({ type: 'access_code', access_code: f.own || '' }))) stop('Not connected \u2014 no code was made.');
  }
}

function openElsewhereEnd() {
  const f = openFlow;
  if (!f) return;
  clearTimeout(f.poll);
  accessCodeAsk.drop();
  if (f.code) send({ type: 'access_code_cancel', access_code: f.code.id });
  openFlow = null;
}

export function acceptAccessCode(requestID, code) {
  if (!accessCodeAsk.claim(requestID)) return false;
  if (openFlow && code) { openFlow.code = code; openFlow.own = ''; openFlow.running = false; openFlow.bad = false; openFlow.said = ''; }
  renderSettings();
  return true;
}

export function rejectAccessCode(message, requestID) {
  if (!accessCodeAsk.claim(requestID)) return false;
  if (openFlow) { openFlow.running = false; openFlow.bad = true; openFlow.said = message; openFlow.own = ''; }
  renderSettings();
  return true;
}

function wireOpenElsewhere(root) {
  if (offAsked && configResult && configResult.section === 'route' && configResult.kind) {
    offAsked = false;
    offSaid = configResult.kind === 'bad' ? { bad: true, text: configResult.text } : { bad: false, text: 'This dashboard is off the relay.' };
    const was = root.querySelector('[data-open-elsewhere]');
    if (was && S.config) was.outerHTML = openElsewhereHTML(S.config.public_name || {}, S.config);
  }
  const told = () => JSON.stringify(openFlow && [openFlow.said, openFlow.bad, openFlow.running, openFlow.needsMove, openFlow.code && openFlow.code.id]);
  const before = told();
  openElsewhereAdvance();
  const box = root.querySelector('[data-open-elsewhere]');
  if (box && told() !== before && S.config) box.outerHTML = openElsewhereHTML(S.config.public_name || {}, S.config);
  root.querySelectorAll('[data-open-elsewhere-act]').forEach(btn => {
    btn.onclick = () => {
      const act = btn.dataset.openElsewhereAct;
      if (act === 'done') { openElsewhereEnd(); renderSettings(); return; }
      if (act === 'off') {
        const select = document.getElementById('cfg-route');
        openElsewhereEnd();
        if (!select) { offSaid = { bad: true, text: 'The route could not be changed from this page.' }; renderSettings(); return; }
        select.value = 'direct';
        offSaid = { bad: false, text: 'Taking this dashboard off the relay\u2026' }; offAsked = true;
        saveSettings('route');
        renderSettings();
        return;
      }
      if (claimDeclined(S.config && S.config.public_name)) return;
      offSaid = null; offAsked = false;
      const own = act === 'own' ? ((document.getElementById('cfg-open-own') || {}).value || '').trim() : '';
      if (act === 'own' && !own) return;
      if (openFlow && openFlow.code) send({ type: 'access_code_cancel', access_code: openFlow.code.id });
      const kept = openFlow || {};
      clearTimeout(kept.poll);
      accessCodeAsk.drop();
      openFlow = { running: true, own: own, acceptMove: act === 'move' || !!kept.acceptMove, claimed: !!kept.claimed, routed: act === 'move' ? false : !!kept.routed };
      renderSettings();
    };
  });
}

function directAddressesHTML(p) {
  const machine = p.machine_addresses || [], set = p.direct_addresses, named = set != null;
  const listed = [...new Set([...(set || []), ...machine])];
  const pick = a => '<br><label style="margin-left:18px"><input type="checkbox" data-route-addr value="' + esc(a) + '"' + ((set || []).includes(a) ? ' checked' : '') + '> ' +
    esc(a) + (machine.includes(a) ? '' : ' (not an address this machine reports)') + '</label>';
  return '<div data-route-addresses style="margin-top:10px"><label class="f">DIRECT ADDRESSES</label>' +
    '<label><input type="radio" name="cfg-route-addr" value="auto"' + (named ? '' : ' checked') + '> Work them out from the address the dashboard is bound to</label><br>' +
    '<label><input type="radio" name="cfg-route-addr" value="list"' + (named ? ' checked' : '') + '> Publish exactly these</label>' + listed.map(pick).join('') +
    (named && !set.length ? '<div class="muted" data-route-addr-empty style="font-size:12px;margin-top:6px">config.json names an empty list: the direct route publishes nothing.</div>' : '') +
    '<div class="muted" style="font-size:12px;margin-top:6px">Used while the route is direct. For your own VPN, tick the VPN address: the name then leads there, and nothing passes through a relay.</div></div>';
}

function routeMoveText(p, c, to, relay) {
  const moves = !!to && !!p.origin && to !== p.origin;
  const same = (p.route_mode || 'direct') === (relay ? 'relay' : 'direct');
  if (!moves && (!relay || same)) return '';
  let text = '';
  if (moves) {
    text = 'This moves the address from ' + p.origin + ' to ' + to + '. The old address stops answering, bookmarks included. At the new one each browser signs in again and is asked for the microphone again.';
    if (p.moves_with && p.moves_with.length) text += ' Registered at the old address, to register again at the new one: ' + p.moves_with.join(', ') + '.';
    const front = c && c.dashboard && c.dashboard.origin;
    if (front && front !== p.origin) text += ' Links and webhooks advertise ' + front + ' (dashboard.origin), which this choice neither changes nor checks.';
  }
  if (relay) text += (text ? ' ' : '') + 'The relay carries every use of the name, at home too; when it is down, this machine\u2019s own address still works.';
  return text;
}

export function publicNameCardHTML(p, c) {
  let state, control = '';
  if (!p || p.status === 'none') {
    state = 'Reachable by address only. ' + (claimDeclined(p) ? declinedSaid
      : 'Claim a public name to reach this identity as https://… from any browser, with the microphone. Claiming sends this identity’s public key to ' + esc(nameServiceSaid(c)) + '.');
    control = '<button class="btn" data-public-name="claim"' + (p && p.can_claim && !claimDeclined(p) ? '' : ' disabled') + '>Claim a public name</button>';
  } else if (p.status === 'prepared') {
    state = '<b>' + esc(p.name) + '</b> — this identity’s name, held and not in use. Nothing leads to it yet. Open on another device, below, puts it to use; its certificate is obtained then.';
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
  const phone = S.stats && S.stats.hosted_on_phone
    ? '<div class="muted" data-public-name-phone style="font-size:12px;margin-top:6px">This identity runs on a phone. Another device can open it only while this app is open on the screen; when the phone sleeps or the app is closed, the other device waits and reconnects by itself. To be reachable at any time, the identity has to live on a computer that stays on.</div>'
    : '';
  return '<div class="card"><h3>PUBLIC NAME</h3><div data-public-name-state style="margin-top:6px">' + state + '</div>' + phone +
    '<div class="muted" style="font-size:12px;margin-top:6px">A public name is claimed once and never changes; the certificate for it renews itself. Changing the bind address above does not touch it.</div>' +
    (control ? '<div class="savebar">' + control + '</div>' : '') +
    (c && c.secrets_ok ? openElsewhereHTML(p || {}, c) : '') + routeHTML(p, c) + settingsReadHTML(c) + '</div>';
}

function settingsReadHTML(c) {
  const rows = (c && c.dashboard && c.dashboard.settings) || [];
  if (!rows.length) return '';
  return '<details data-settings-read style="margin-top:14px"><summary>Every setting of this card, as it is in force (' + rows.length + ')</summary>' +
    '<table style="width:100%;font-size:12px;margin-top:6px"><thead><tr><th style="text-align:left">Setting</th><th style="text-align:left">In force</th><th style="text-align:left">From</th></tr></thead><tbody>' +
    rows.map(r => '<tr data-setting="' + esc(r.key) + '"><td><code>' + esc(r.key) + '</code></td><td>' + esc(r.value) + '</td><td class="muted">' + (r.set ? 'config.json' : 'the default') + '</td></tr>').join('') +
    '</tbody></table><div class="muted" style="font-size:12px;margin-top:6px">Those with no control above are set in config.json.</div></details>';
}
export function wirePublicName(root) {
  wireOpenElsewhere(root);
  const route = root.querySelector('#cfg-route'), move = root.querySelector('[data-route-move]');
  if (route && move) {
    const say = () => { const o = route.selectedOptions[0]; move.textContent = (o && o.dataset.move) || ''; move.hidden = !move.textContent; };
    route.addEventListener('change', say);
    say();
  }
  root.querySelectorAll('[data-public-name]').forEach(btn => { btn.onclick = () => {
    const kind = btn.dataset.publicName;
    if (kind === 'claim' && claimDeclined(S.config && S.config.public_name)) return;
    disableOwing(btn);
    send({ type: kind === 'retry' ? 'public_name_retry' : kind === 'move' ? 'public_name_move' : kind === 'look' ? 'public_name_look' : 'public_name_claim' });
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
  root.querySelectorAll('[data-update-check]').forEach(btn => { btn.onclick = () => { disableOwing(btn); send({ type: 'update_check' }); }; });
  root.querySelectorAll('[data-update-restart]').forEach(btn => { btn.onclick = () => { disableOwing(btn); btn.textContent = 'Relaunching\u2026'; send({ type: 'restart' }); }; });
  root.querySelectorAll('[data-uninstall]').forEach(btn => { btn.onclick = () => {
    disableOwing(btn);
    const note = root.querySelector('[data-uninstall-note]');
    const asked = send({ type: 'uninstall' });
    if (!asked) btn.disabled = false;
    if (note) note.textContent = asked ? 'AII OS opens on this Mac and asks you there' : 'not connected — try again';
  }; });
}
export function uninstallCardHTML(offered) {
  if (!offered) return '';
  return '<div class="card"><h3>UNINSTALL</h3>' +
    '<div>Uninstall AII OS from this account. AII OS opens on this Mac and asks you there; every identity stops, and each one\'s data stays exactly where it is.</div>' +
    '<div style="margin-top:8px"><button class="btn ghost" data-uninstall>Uninstall AII OS\u2026</button> <span class="savenote" data-uninstall-note></span></div>' +
    '</div>';
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
    '<div style="font-size:11px;color:var(--faint);margin:-4px 0 8px">An adopted credential replaces the key entirely. To use the claude-code credential at another address, name that address on the entry in providers.json (credential_options.base_url).</div>';
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
    embeddings_model: $('pv-embed-' + tag).value.trim(),
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
function disableOwing(el) {
  if (document.activeElement === el) focusOwed = focusOf($('settings-stack'), el);
  el.disabled = true;
}
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
  const typing = keyTyping();
  if (!S.providersLoaded) query('providers');
  const c = S.config;
  let html = navHTML();
  html += '<div class="muted" data-settings-draft hidden>Unsaved edits — not yet the running configuration.</div>';
  html += configFeedbackHTML();
  const body = SECTIONS.find(([id]) => id === sec);
  if (body) html += body[2](c);
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
  wireScriptsCard(st, () => { if (S.view === 'settings' && sec === 'dashboard') renderSettings(); });
  wireUnsetHints(st);
  wireSpeech(st);
  wireSettingControls(st, { send, rerender: renderSettings, typing, connect: () => { if (S.go) S.go('plugins'); } });

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
  const briefOff = $('cog-brief-off');
  if (briefOff) briefOff.onchange = () => { $('cog-brief-at').disabled = briefOff.checked; };
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
  if (!c) return loadingCard('configuration');
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
    const mib = id => $(id).value.trim() === '' ? null : num($(id).value) * 1048576;
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
    ch['speech.mode'] = { listen: $('sp-mode-listen').value, speak: $('sp-mode-speak').value };
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
  } else if (section === 'prompt') {
    ch['prompt.max_tokens'] = num($('cfg-ptokens').value);
    ch['prompt.recent_turns'] = num($('cfg-pturns').value);
  } else if (section === 'witness') {
    ch['witness.url'] = $('cfg-wurl').value.trim();
    ch['witness.interval_events'] = num($('cfg-wint').value);
  } else if (section === 'agency') {
    ch['agency.prefer_local_for_roles'] = !!$('cfg-plr').checked;
  } else if (section === 'cognition') {
    let bad = '';
    document.querySelectorAll('[data-cog-key]').forEach(el => {
      const key = el.dataset.cogKey, raw = el.value.trim(), had = configValue(S.config, key);
      if (raw === '') { if (had != null) ch[key] = null; return; }
      const n = Number(raw);
      if (!Number.isInteger(n)) { bad = bad || (COGNITION_FIELDS[key] ? COGNITION_FIELDS[key][0] : key) + ' is a whole number, or blank for the default.'; return; }
      if (n !== had) ch[key] = n;
    });
    if (bad) { configResult = { kind: 'bad', section: section, text: bad }; return; }
    const at = $('cog-brief-off').checked ? 'off' : $('cog-brief-at').value.trim();
    const hadAt = (S.config && S.config.cognition && S.config.cognition.morning_brief_at) || '';
    if (at !== hadAt) ch['cognition.morning_brief_at'] = at === '' ? null : at;
    if (!Object.keys(ch).length) { configResult = { kind: 'good', section: section, text: 'Nothing to save \u2014 no setting was changed.' }; return; }
  } else if (section === 'logs') {
    ch['logs.dir'] = $('cfg-ldir').value.trim();
    ch['logs.max_backups'] = num($('cfg-lbackups').value);
    ch['logs.compress_days'] = num($('cfg-lcomp').value);
  }
  if (collecting) return { changes: ch, service };
  sendConfigChanges(section, ch, submittedEditor(section));
}

function saveSpeechSettings() {
  if (config.waiting() || speechAdd.waiting()) return;
  const changes = {}, services = [];
  for (const section of ['speech_stt', 'speech_tts', 'speech_speakers', 'speech_heard', 'speech_mode']) {
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
  sendConfigStep({ section, changes: ch, editor, echo, keys: keyDraftsFor(section), keysSaved: false });
}

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
  tokenAsk = '';
  const reveal = document.querySelector('[data-token-reveal]');
  if (reveal && !tokenShown) {
    reveal.disabled = true;
    reveal.title = 'Reconnect to show access token';
    reveal.setAttribute('aria-label', reveal.title);
  }
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
  if (path === 'certificate.direct_addresses') return state?.public_name?.direct_addresses ?? null;
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
    const heard = p.startsWith('speech.heard_history.') && HEARD_FIELDS.find(f => 'speech.heard_history.' + f[1] === p);
    if (heard && sent === 0) sent = heard[5];
    if (p === 'speech.speakers' && sent && saved) {
      sent = { ...sent, uids: [...new Set(sent.uids || [])].sort() };
      saved = { ...saved, uids: [...(saved.uids || [])].sort() };
    }
    return !sameSaved(saved, sent);
  });
  if (missed.length) {
    const labels = { 'speech.speakers': 'Who is heard', 'speech.mode': 'Conversation mode' };
    for (const f of HEARD_FIELDS) labels['speech.heard_history.' + f[1]] = 'Heard history (' + f[2] + ')';
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
  const newerKey = pending.section.startsWith('plugin:') ? [...keyDrafts.keys()].some(k => k.split('\u0000')[0] === pending.section.slice('plugin:'.length))
    : pending.section === 'speech' && keyDraftsFor('speech').length > 0;
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
  if (section === 'speech_heard') return 'Saved — applies to the next words heard and the next read.';
  if (section === 'contacts') return 'Saved — the contacts are in force now.';
  if (section === 'route') return 'Saved — the name is re-routed on the route owner\'s next pass, within the minute.';
  if (section === 'agency') return 'Saved — role routing applies live to the next role-tagged spawn.';
  if (section === 'cognition') {
    const when = [...new Set(Object.keys(changes || {}).map(k => k === 'cognition.morning_brief_at' ? 'the rhythm reads it at its next pass' : cognitionApplies(k)))];
    return 'Saved — ' + (when.length ? when.join('; ') : 'the rhythm reads it at its next pass') + '.';
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
  disableOwing(btn); btn.textContent = 'Asking…';
  query('discover', { provider: btn.dataset.refreshModels });
});
