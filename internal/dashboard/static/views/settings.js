
import { startSignIn, signInProgress, signInWanted, credentialExpired, wireSignInCompletion } from '../signin.js';
import { S } from '../state.js';
import { $, copyText, esc } from '../util.js';
import { send, query } from '../ws.js';
import { spokenAudio } from '../say.js';
import { sandboxCardHTML, wireSandboxCard } from '../sandbox.js';
import { pendingSlot } from '../pending.js';
import { providerModels } from './model-picker.js';

const SECTIONS = [
  ['substrate', 'Substrate'], ['providers', 'Providers'], ['speech', 'Speech'],
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
const speechAdd = pendingSlot();

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
  const candidates = S.providers.filter(p => p.chat !== false);
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
    cfgField('cfg-probe-timeout', 'SUBSTRATE CHECK (SECONDS)', c.llm.probe_timeout_seconds) +
    resolved +
    '<div style="font-size:11.5px;color:var(--faint);margin-top:8px">providers.json owns the provider data; this card points at an entry. Endpoint, key, context/output budgets, REASONING EFFORT, and THINKING mode are edited on the entry in the Providers section. A substrate change applies only after the candidate completes a real inference request.</div>' +
    savebarHTML('llm', 'applies live after inference check') + '</div>';
}

// Settings → Speech, in the shape every speech settings page shares: one
// engine picker per direction listing the services by name, and only the
// chosen engine's fields. Save makes the service ready — added, keyed,
// pointed at its server — and then has it answer one real request before
// anything changes.
//
// THE VENDOR IS THE ONLY SOURCE. Models, voices and languages are asked of
// the service when the operator chooses it, followed to the end of its
// paging and searched where it searches; this release ships no catalogue of
// any vendor's anything, and a direction a vendor cannot list is a
// direction it is not offered for. What the operator already chose survives
// a list that narrowed under it, and an id typed by hand is always
// reachable: the picker accelerates, it never gates.
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
  },
};
// OWN_SERVER is the engine for the operator's own OpenAI-compatible server,
// BY_ID the choice to type an id the list does not hold.
const OWN_SERVER = 'own-server:';
const BY_ID = 'by-id:';

// draft is the engine the operator has chosen but not yet saved. The card
// is drawn for THAT engine — its fields, its lists — while the readback
// keeps saying what is still in force underneath.
const draft = { stt: null, tts: null };
export function speechDraftSaved(section) {
  const dir = section === 'speech_stt' ? 'stt' : section === 'speech_tts' ? 'tts' : '';
  if (dir) draft[dir] = null;
}

function speechServices(dir, sp) {
  return ((sp && sp.services) || []).filter(s => s.speech && s.speech[dir]);
}
function speechService(value, sp) {
  return ((sp && sp.services) || []).find(s => s.name === value) || null;
}

// speechOffer is what the chosen engine reads in one direction: which
// fields its API takes, and whether it lists them. A new OpenAI-compatible
// server, or a pointer at an entry with no speech block, speaks OpenAI's
// dialect: a model both ways, and a voice when it speaks.
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
// ownServerName names an OpenAI-compatible server by where it answers, so
// the same server chosen twice is one entry and a name never outlives its
// address. An address that is not http(s) with a host names nothing.
function ownServerName(url) {
  try {
    const u = new URL(url);
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.host ? 'OpenAI-compatible · ' + u.host : '';
  } catch (e) { return ''; }
}

// hinted says on hover what a blank field means — the field itself cannot
// show it. wireUnsetHints keeps the hover in step as the field fills, so
// any Settings field can carry one.
function hinted(value, hint) {
  return ' data-unset-hint="' + esc(hint) + '"' + (String(value || '').trim() ? '' : ' title="' + esc(hint) + '"');
}
function syncUnsetHint(el) {
  if (String(el.value || '').trim()) el.removeAttribute('title');
  else el.title = el.dataset.unsetHint;
}
function wireUnsetHints(root) {
  root.querySelectorAll('[data-unset-hint]').forEach(el => {
    el.addEventListener('input', () => syncUnsetHint(el));
    el.addEventListener('change', () => syncUnsetHint(el));
  });
}

// keyFacts is what the key field says for the chosen engine: whether a key
// is stored, where a typed one is kept — shared with a chat provider of the
// same name — and the environment fallback.
function keyFacts(value, sp) {
  if (!value) return { label: 'API KEY', hint: '', note: '' };
  if (value === OWN_SERVER) return { label: 'API KEY (OPTIONAL)', hint: 'Leave blank for a server that asks for no key.', note: 'Kept with the server\'s entry in providers.json.' };
  // A pointer at a provider that is not a speech service still has its
  // entry's key facts.
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

// --- what the service answered, and what is still being asked ---

// live holds one answer per engine and direction, and the question it
// answers: a narrowed list is not the whole list, and the two must never be
// confused (a choice already made is never judged against a partial list).
const live = {};
const asking = {};
// asked keeps the last question put to each engine and direction after its
// answer came: an older answer landing later still names a question that
// is not this one (review of 0.1.5, finding 6).
const asked = {};
const listKey = (value, dir) => value + '|' + dir;

function askSpeechLists(dir, value, sp, ask) {
  if (!value || value === OWN_SERVER) return;
  const o = speechOffer(dir, value, sp);
  if (!o || !(o.lists_models || o.lists_voices)) return;
  // A KEY THE OPERATOR HAS TYPED IS A KEY THE SERVICE CAN BE ASKED WITH.
  // Waiting for a save before the lists fill makes the operator prove the
  // key twice: once to see anything, once to keep it. It is carried for
  // this question only and stored by nothing.
  const typed = $('sp-key-' + dir) ? $('sp-key-' + dir).value.trim() : '';
  const want = { search: (ask && ask.search) || '', language: (ask && ask.language) || '', typed: typed };
  const key = listKey(value, dir), had = live[key] || asking[key];
  // An answer carries the question it answers; a question with nothing in
  // it carries nothing, so the two are compared as the same emptiness.
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
// forgetSpeechLists drops what a service answered, so the next render asks
// it again — after a key is stored, or a connection came back.
function forgetSpeechLists(name) {
  ['stt', 'tts'].forEach(dir => { delete live[listKey(name, dir)]; delete asking[listKey(name, dir)]; delete asked[listKey(name, dir)]; });
}

// languagesOf and itemsOf read one answer. An item the service did not name
// for the chosen language is left out here only when the service did not
// narrow the list itself.
function languagesOf(got) {
  return (got && got.languages) || [];
}
function itemsOf(got, kind, language) {
  const items = (got && got[kind]) || [];
  if (!language || (got && got.language === language)) return items;
  const narrowed = items.filter(i => (i.languages || []).some(l => l === language || l.split('-')[0] === language.split('-')[0]));
  return narrowed.length ? narrowed : items;
}

// --- the picker ---

// pickerHTML is one field the vendor fills: its own value first so a
// choice already made is never lost, then what the service listed, then the
// way to type an id the list does not hold.
function optionsHTML(items, value) {
  // The operator's own choice comes first and stays, even when a narrowed
  // list no longer holds it; every other row reads as the vendor's name for
  // it, with what the vendor says about it behind.
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

// pickedValue is what a picker's field sends: the id typed by hand when the
// operator is typing one, and what they chose otherwise.
function pickedValue(id) {
  const typed = $(id + '-typed'), pick = $(id);
  if (typed && !typed.hidden) return typed.value.trim();
  const chosen = pick ? pick.value : '';
  return chosen === BY_ID ? '' : chosen;
}

// wirePicker lets the last option open a plain text box, so a voice minted
// a minute ago is reachable before any list has heard of it.
function wirePicker(root, id) {
  const pick = root.querySelector('#' + id), typed = root.querySelector('#' + id + '-typed');
  if (!pick || !typed) return;
  pick.addEventListener('change', () => {
    if (pick.value !== BY_ID) { typed.hidden = true; return; }
    typed.hidden = false;
    typed.focus();
  });
}

// playSample lets the operator HEAR the voice they are picking, before
// they decide to keep it: the service is asked with exactly what is on the
// card — engine, model, voice, and a key that may only have been typed.
// A refusal stands beside the button in the service's own words; nothing
// falls back to the browser's voice, which would answer a question about
// this service with a different one.
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

// spentLine is what this direction cost this month, by service, and what
// is left of any ceiling. An operator who tried three voices sees what
// each one cost; "nothing yet" is an answer, not an empty space.
function spentLine(dir, sp, ceiling) {
  const spent = (sp.spent || []).filter(u => u.direction === dir);
  const total = spent.reduce((n, u) => n + (dir === 'stt' ? (u.seconds || 0) : (u.characters || 0)), 0);
  const each = spent.map(u => u.provider + ' ' + amount(dir, dir === 'stt' ? u.seconds : u.characters) +
    ' over ' + u.requests + ' ' + (dir === 'stt' ? (u.requests === 1 ? 'utterance' : 'utterances') : (u.requests === 1 ? 'reply' : 'replies'))).join(' · ');
  // The ceiling is in minutes for listening and the meter in seconds.
  const cap = dir === 'stt' ? ceiling * 60 : ceiling;
  const left = ceiling ? ' · ' + amount(dir, Math.max(0, cap - total)) + ' of the ceiling left' : '';
  const resets = sp.resets ? ' · resets ' + sp.resets : '';
  return 'This month: ' + (each || 'nothing yet') + left + resets;
}

// amount says a count the way its bill does: seconds as a clock, and
// characters in full — a rounded character count is not a bill — grouped
// by our own hand, so the number reads the same on every machine.
function amount(dir, n) {
  n = n || 0;
  if (dir !== 'stt') return String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ',') + ' characters';
  if (n < 60) return n + 's';
  const m = Math.floor(n / 60);
  return m + 'm ' + (n % 60) + 's';
}

// stateLine says where a field's choices came from, or why there are none:
// the three states worth distinguishing are no key yet, a service that
// refused, and a list that came back narrowed.
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
  // Values belong to the engine they were saved for: a model or a voice
  // from the last engine means nothing to this one.
  const cur = provider === (inForce.provider || '') ? inForce : { provider: provider };
  const services = speechServices(dir, sp);
  const known = !provider || services.some(s => s.name === provider);
  const o = speechOffer(dir, provider, sp);
  const svc = speechService(provider, sp);
  const own = ownServer(provider, sp);
  const got = listed(dir, provider);
  const engine = '<label class="f">ENGINE</label><select id="sp-provider-' + dir + '"' + hinted(provider, d.unsetProvider) + '>' +
    '<option value=""' + (provider ? '' : ' selected') + '>' + esc(d.none) + '</option>' +
    services.map(s => '<option value="' + esc(s.name) + '"' + (s.name === provider ? ' selected' : '') + '>' + esc(s.name) + '</option>').join('') +
    (known ? '' : '<option value="' + esc(provider) + '" selected>' + esc(provider) + ' (not a speech service)</option>') +
    '<option value="' + OWN_SERVER + '">OpenAI-compatible…</option>' +
    '</select>';
  const baseValue = own && svc ? svc.endpoint : '';
  const base = '<div id="sp-base-row-' + dir + '"' + (own ? '' : ' hidden') + '><label class="f">BASE URL</label>' +
    '<input type="text" id="sp-base-' + dir + '" value="' + esc(baseValue) + '" placeholder="Base URL, such as http://localhost:8000/v1"' +
    hinted(baseValue, 'Where the OpenAI-compatible server answers, such as http://localhost:8000/v1.') + '></div>';
  const k = keyFacts(provider, sp);
  const key = '<div id="sp-key-row-' + dir + '"' + (provider ? '' : ' hidden') + '><label class="f" id="sp-key-label-' + dir + '">' + esc(k.label) + '</label>' +
    '<input type="password" id="sp-key-' + dir + '" autocomplete="off"' + hinted('', k.hint) + '>' +
    '<div id="sp-key-note-' + dir + '" style="font-size:11.5px;color:var(--faint);margin-top:6px">' + esc(k.note) + '</div></div>';

  // The language the service itself named, narrowing the voices under it.
  // THE ROW ALWAYS EXISTS, hidden until a service names languages: the
  // first answer that names them used to redraw the whole card, which
  // emptied the key the operator had just typed and asked again without
  // it.
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
  const ceiling = '<label class="f">MONTHLY CEILING</label>' +
    '<input type="number" id="sp-ceiling-' + dir + '" min="0" step="1" value="' + (ceilingValue || '') + '" placeholder="no ceiling"' +
    hinted(ceilingValue ? String(ceilingValue) : '', 'The most ' + d2.unit + ' of ' + d2.of + ' this identity may buy in a calendar month. Reached, ' + d2.note + '. Empty is no ceiling.') + '>' +
    '<div id="sp-spent-' + dir + '" style="font-size:11.5px;color:var(--faint);margin-top:6px">' + esc(spentLine(dir, sp, ceilingValue)) + '</div>';

  // WHAT IS IN FORCE AND WHAT IS ONLY CHOSEN ARE DIFFERENT THINGS. A
  // sample plays the engine on the card while this line still names the
  // one behind it, and nothing said so.
  const unsaved = provider && provider !== inForce.provider && provider !== OWN_SERVER
    ? ' — <b>' + esc(provider) + ' is chosen and not saved</b>; Save to put it in force' : '';
  const readback = !inForce.provider
    ? '<div id="sp-readback-' + dir + '" style="font-size:11.5px;color:var(--faint);margin-top:8px">' + esc(d.off) + unsaved + '</div>'
    : inForce.error
      ? '<div id="sp-readback-' + dir + '" style="font-size:11.5px;color:#c0392b;margin-top:8px">pointer does not resolve: ' + esc(inForce.error) + unsaved + '</div>'
      : '<div id="sp-readback-' + dir + '" style="font-size:11.5px;color:var(--faint);margin-top:8px;line-height:1.6">in force: <span style="font-family:var(--mono)">' + esc(inForce.endpoint || '') + '</span> · key ' + esc(inForce.api_key_masked || 'none') + unsaved + '</div>';
  return '<div class="card"><h3>' + d.title + '</h3>' + engine + base + key + language + model + voice + readback +
    ceiling + savebarHTML('speech_' + dir, 'applies live once the service answers a check',
      dir === 'tts' ? { also: '<button class="btn ghost" id="sp-sample-tts"' + (provider && provider !== OWN_SERVER ? '' : ' disabled') + '>Play sample</button>' +
        '<span class="savesay" id="sp-sample-state" role="status" aria-live="polite"></span>' } : null) + '</div>';
}

function speechHTML(c) {
  if (!c) return '<div class="card"><div class="empty">loading configuration…</div></div>';
  const sp = c.speech || { stt: {}, tts: {} };
  return speechCard('stt', sp) + speechCard('tts', sp) + speakersCard(sp);
}

// speakersCard is whose words the identity receives: one policy, read back
// with its revision and what it withheld. Ids are the speech engine's stable
// enrolled ids; a name is never an id.
function speakersCard(sp) {
  const p = sp.speakers || { mode: 'all', uids: [], unidentified: 'deliver', revision: 0 };
  const mode = p.mode || 'all';
  const opt = (v, label) => '<option value="' + v + '"' + (mode === v ? ' selected' : '') + '>' + label + '</option>';
  const unid = p.unidentified || 'deliver';
  return '<div class="card" id="sp-speakers"><h3>Speakers</h3>' +
    '<p class="muted">Whose words the identity receives. Listed speakers are named by the stable ids the speech engine enrolled; a name is never an id.</p>' +
    '<label class="f">HEARD</label><select id="sp-speakers-mode">' + opt('all', 'Everyone') + opt('only', 'Only the speakers listed') + opt('ignore', 'Everyone except the speakers listed') + '</select>' +
    '<div id="sp-speakers-list-row"' + (mode === 'all' ? ' hidden' : '') + '><label class="f">SPEAKER IDS</label>' +
    '<input type="text" id="sp-speakers-uids" value="' + esc((p.uids || []).join(' ')) + '" placeholder="ids from the speaker list, separated by spaces"></div>' +
    '<div id="sp-speakers-unid-row"' + (mode === 'ignore' ? '' : ' hidden') + '><label class="f">UNIDENTIFIED VOICES</label><select id="sp-speakers-unid">' +
    '<option value="deliver"' + (unid === 'deliver' ? ' selected' : '') + '>Delivered, marked unidentified</option>' +
    '<option value="withhold"' + (unid === 'withhold' ? ' selected' : '') + '>Withheld</option></select></div>' +
    '<div class="muted" id="sp-speakers-readback">' + esc(speakersReadback(p)) + '</div>' +
    savebarHTML('speech_speakers', '') + '</div>';
}

export function speakersReadback(p) {
  const mode = p.mode || 'all', n = (p.uids || []).length, s = n === 1 ? '' : 's';
  const heard = mode === 'all' ? 'Everyone is heard'
    : mode === 'only' ? 'Only ' + n + ' listed speaker' + s + ' heard; unidentified voices are withheld'
    : 'Everyone but ' + n + ' listed speaker' + s + '; unidentified voices are ' + (p.unidentified === 'withhold' ? 'withheld' : 'delivered');
  const f = p.withheld_finals || 0, q = p.withheld_partials || 0;
  return heard + ' · revision ' + (p.revision || 0) + ' · withheld since start: ' + f + ' final' + (f === 1 ? '' : 's') + ', ' + q + ' partial' + (q === 1 ? '' : 's');
}

// wireSpeech asks each chosen engine what it offers, follows a change of
// engine without re-rendering, and lets the operator search the vendor's own
// library rather than what happened to arrive first.
function wireSpeech(root) {
  // The Speakers card shows the rows its mode reads: a list for only and
  // ignore, the unidentified rule for ignore.
  const modeSel = root.querySelector('#sp-speakers-mode');
  if (modeSel) modeSel.onchange = () => {
    const m = modeSel.value;
    const list = root.querySelector('#sp-speakers-list-row'), unid = root.querySelector('#sp-speakers-unid-row');
    if (list) list.hidden = m === 'all';
    if (unid) unid.hidden = m !== 'ignore';
  };
  ['stt', 'tts'].forEach(dir => {
    const sel = root.querySelector('#sp-provider-' + dir);
    if (!sel) return;
    const sp = () => (S.config && S.config.speech) || {};
    const cur = () => (sp()[dir] || {});
    // A drafted engine starts from its own answers; the language in force
    // belongs to the engine in force.
    askSpeechLists(dir, sel.value, sp(), { language: draft[dir] !== null ? '' : cur().language });
    // Asking is itself worth saying: the fields were drawn before the
    // question went out, and a picker that fills a moment later is only
    // honest if the wait for it was visible.
    ['model', 'voice'].forEach(kind => {
      const state = root.querySelector('#sp-' + kind + '-state-' + dir);
      if (state) state.textContent = stateLine(dir, sel.value, kind === 'voice' ? 'voices' : 'models');
    });
    wirePicker(root, 'sp-model-' + dir);
    if (dir === 'tts') wirePicker(root, 'sp-voice-tts');
    const typed = root.querySelector('#sp-key-' + dir);
    if (typed) {
      let keyTimer = 0;
      typed.addEventListener('input', () => {
        clearTimeout(keyTimer);
        keyTimer = setTimeout(() => {
          // The card this key belongs to may be gone by now — an engine
          // change redraws it — and its question must not be asked for
          // the card that replaced it.
          if ($('sp-provider-' + dir) !== sel) return;
          askSpeechLists(dir, sel.value, sp(), { language: language ? language.value : '' });
          renderSpeechChoices(dir, sel.value, language ? language.value : '');
        }, 400);
      });
    }
    const sample = root.querySelector('#sp-sample-' + dir);
    if (sample) sample.onclick = () => playSample(sample, sel.value);
    const find = root.querySelector('#sp-find-' + dir);
    if (find) {
      let timer = 0;
      find.addEventListener('input', () => {
        clearTimeout(timer);
        // The vendor does the searching, after the operator stops typing:
        // a library of thousands is never dragged here to be filtered.
        timer = setTimeout(() => askSpeechLists(dir, sel.value, sp(), { search: find.value.trim(), language: cur().language }), 300);
      });
    }
    const language = root.querySelector('#sp-language-' + dir);
    if (language) {
      language.addEventListener('change', () => {
        const chosen = language.value;
        askSpeechLists(dir, sel.value, sp(), { language: chosen, search: find ? find.value.trim() : '' });
        renderSpeechChoices(dir, sel.value, chosen);
      });
    }
    // A NEW ENGINE STARTS FROM ITS OWN ANSWERS: which fields it reads at
    // all differ, so the card is drawn again for it — and the engine the
    // operator just chose is what it is drawn for, not what is in force.
    sel.addEventListener('change', () => {
      draft[dir] = sel.value;
      // The new card asks for itself once it is drawn, with its own key
      // field — never with the one typed for the engine before it, which
      // is another service's credential.
      renderSettings();
    });
  });
}

// languageOptionsHTML is the language picker's rows: any language first,
// then what the service named.
function languageOptionsHTML(languages, chosen) {
  return '<option value=""' + (chosen ? '' : ' selected') + '>Any language the service hears</option>' +
    languages.map(l => '<option value="' + esc(l.id) + '"' + (l.id === chosen ? ' selected' : '') + '>' +
      esc(l.name ? l.name + ' (' + l.id + ')' : l.id) + '</option>').join('');
}

// renderSpeechChoices refills one card's pickers in place, so an answer
// that arrives while the operator is typing never moves what is under them.
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
    el.innerHTML = optionsHTML(items, el.value === BY_ID ? '' : el.value);
    const state = $('sp-' + (kind === 'voices' ? 'voice' : 'model') + '-state-' + dir);
    if (state) state.textContent = stateLine(dir, value, kind);
  };
  fill(model, itemsOf(got, 'models', language), 'models');
  fill(voice, itemsOf(got, 'voices', language), 'voices');
}

// acceptSpeechLists takes one answer from a service. It belongs to the
// question it was asked; an answer to an older question is kept but changes
// nothing on screen, and an engine no longer chosen changes nothing either.
export function acceptSpeechLists(got) {
  if (!got || !got.provider || !SPEECH_DIRS[got.direction]) return false;
  const dir = got.direction, key = listKey(got.provider, dir);
  const pending = asking[key], question = pending || asked[key];
  // AN ANSWER TO AN OLDER QUESTION CHANGES NOTHING — while the newer one
  // is still being asked, and after it was answered. Each question is
  // answered on its own goroutine, so a broad search can land after the
  // narrow one it was replaced by, and even after that one's answer; kept,
  // it would fill the picker with the wrong list under the words in the
  // box.
  if (question && ((question.search || '') !== (got.search || '') || (question.language || '') !== (got.language || ''))) return true;
  live[key] = got;
  if (pending) {
    got.typed = pending.typed; // the answer belongs to the key it was asked with
    delete asking[key];
  }
  const sel = $('sp-provider-' + dir);
  if (!sel || sel.value !== got.provider) return true;
  const language = $('sp-language-' + dir) ? $('sp-language-' + dir).value : '';
  renderSpeechChoices(dir, got.provider, language);
  return true;
}

// rejectSpeechLists takes the refusal of one question, so the card says
// so instead of "Asking…" for the rest of the connection, and the same
// question can be asked again.
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

// A service is made ready first — added when this install has no entry for
// it, pointed at the operator's own server, given the key the operator
// typed — and the settings follow once it is in the directory: two acts,
// each through its own door.
function readySpeechService(section, name, key, baseURL, ch) {
  configResult = null;
  const requestID = send({ type: 'speech_service', provider: name, api_key: key, base_url: baseURL });
  if (!speechAdd.arm({ section: section, name: name, sentKey: key !== '', changes: ch }, requestID)) {
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
// THE TOKEN IS ASKED FOR, NEVER CARRIED. The config frame reaches every
// socket on every save; the field is drawn empty and filled only when the
// eye is pressed, by a question the host answers to this screen alone.
let tokenAsk = '', tokenShown = '';
function dashboardTokenHTML(required) {
  if (!required) return '';
  return '<label class="f" for="cfg-dtoken">ACCESS TOKEN</label>' +
    '<div style="display:flex;align-items:center;gap:6px"><input id="cfg-dtoken" type="password" value="' + esc(tokenShown) + '" readonly autocomplete="off" spellcheck="false" style="flex:1" placeholder="press the eye to show it">' +
    '<button type="button" class="copyb" data-token-reveal title="Show access token" aria-label="Show access token" aria-pressed="' + (tokenShown ? 'true' : 'false') + '">' + TOKEN_EYE + '</button>' +
    '<button type="button" class="copyb" data-token-copy title="' + (tokenShown ? 'Copy access token' : 'Show the token first') + '" aria-label="Copy access token"' + (tokenShown ? '' : ' disabled') + '>' + TOKEN_COPY + '</button></div>' +
    '<div class="muted" style="font-size:12px;margin-top:6px">This is the credential for another browser or device. It is stored in config.json; <span style="font-family:var(--mono)">aii dashboard-token --rotate</span> replaces it and signs every browser out.</div>';
}

// acceptDashboardToken takes the host's answer to the eye.
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
    dashboardTokenHTML(!!c.dashboard.require_token) +
    savebarHTML('dashboard', 'saved — applies next restart') + '</div>' +
    publicNameCardHTML(c.public_name || null) +
    themeCardHTML();
}

function wireDashboardToken(root) {
  const field = root.querySelector('#cfg-dtoken');
  if (!field) return;
  const reveal = root.querySelector('[data-token-reveal]');
  const copy = root.querySelector('[data-token-copy]');
  reveal.onclick = () => {
    if (!field.value) {
      // Not yet asked for: ask, and the answer fills the field.
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
    (p.can_sign_in ? signInRow(p) : '') +
    '</div>';
}

// THE ROW SAYS WHERE A SIGN-IN STANDS whenever there is one to report, and
// offers the button only when a valid token is not in hand (signInWanted in
// signin.js, the rule the birth form shares): a valid token is announced by
// the credential's own line and offers nothing to press.
function signInRow(p) {
  const progress = signInProgress(p.signin, 'provider', p.name);
  if (!signInWanted(p)) return progress ? '<div class="signin">' + progress + '</div>' : '';
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
    '<div data-update-state style="margin-top:6px">' + state + '</div>' +
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
// The way into a section from elsewhere on the page: the microphone opens
// Speech with its service field focused.
S.openSettings = (section, focusID) => {
  sec = section;
  provOpen = null;
  const nav = document.querySelector('.nav-item[data-view="settings"]');
  if (nav && S.view !== 'settings') nav.click();
  renderSettings();
  const el = focusID ? $(focusID) : null;
  if (el) el.focus();
};
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
  if (info.expires_at) bits.push((credentialExpired(info) ? 'EXPIRED ' : 'usable to ') + esc(info.expires_at.replace('T', ' ').replace('Z', ' UTC')));
  if (info.path) bits.push('from ' + esc(info.path));
  if (!bits.length) return '';
  return '<div style="font-size:11.5px;color:var(--faint);margin:2px 0 6px">' + bits.join(' &middot; ') + '</div>';
}
function providersHTML() {
  let html = '<div class="card"><h3>PROVIDERS — providers.json</h3>' +
    '<div style="font-size:11.5px;color:var(--faint);margin-bottom:8px;line-height:1.5">The operator\'s file, beside config.json — edited here or by hand. Status describes live model discovery and is never stored; activation is decided by a real inference check.</div>';
  if (prov.waiting()) html += '<div class="config-result">Saving ' + esc(prov.waiting().name) + '… waiting for the runtime to confirm.</div>';
  else if (provResult) html += '<div class="config-result ' + provResult.kind + '">' + esc(provResult.text) + '</div>';
  // SPEECH-ONLY SERVICES ARE NOT LISTED HERE. They have nothing to think
  // with; their place is Settings → Speech, where their keys are entered and
  // their models and voices asked for. The row index stays the entry's
  // index in the file, so editing and removal keep addressing the right one.
  const speechOnly = S.providers.filter(p => p.chat === false);
  const chatRows = S.providers.map((p, i) => {
      if (p.chat === false) return '';
      let row = '<div class="tool-row" data-prov-row="' + i + '" style="cursor:pointer;align-items:center">' +
        dot(p.status) +
        '<span class="tn">' + esc(p.name) + (p.default ? ' ✓' : '') + '</span>' +
        '<span class="td" style="font-family:var(--mono);font-size:11.5px">' + esc(p.endpoint) +
        (p.default_model ? ' · ' + esc(p.default_model) : '') +
        ' · ' + (p.models || []).length + ' models</span></div>' + provRowNote(p);
      if (provOpen === i) row += providerEditor(p, String(i));
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
    // This card edits chat providers; a save here declares the entry chats.
    chat: true,
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
  const added = speechAdd.claim(requestID);
  if (added) {
    const got = S.providers.find(p => p.name === added.name);
    if (got && (!added.sentKey || got.has_key)) {
      if (added.sentKey) forgetSpeechLists(added.name); // its lists are asked again with the key
      sendConfigChanges(added.section, added.changes);
    }
    else configResult = { kind: 'bad', section: added.section, text: 'Acknowledged, but ' + added.name + (got ? ' came back with no key stored' : ' is not in the registry that came back') + ' — the settings were not sent.' };
    return true;
  }

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
  const adding = speechAdd.waiting();
  if (speechAdd.claim(requestID)) { configResult = { kind: 'bad', section: adding && adding.section, text: message }; renderSettings(); return true; }
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
  else if (sec === 'speech') html += speechHTML(c);
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
  }
  st.querySelectorAll('[data-open-section]').forEach(a => { a.onclick = e => { e.preventDefault(); S.openSettings(a.dataset.openSection); }; });
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
  } else if (section === 'speech_stt' || section === 'speech_tts') {
    const dir = section.slice('speech_'.length);
    const sp = (S.config && S.config.speech) || {};
    const chosen = $('sp-provider-' + dir).value, own = ownServer(chosen, sp);
    const baseURL = own ? $('sp-base-' + dir).value.trim() : '';
    // The operator's own server is named by where it answers: a new
    // address is a new entry, and one already added is reused.
    const provider = own ? ownServerName(baseURL) : chosen;
    if (own && !provider) { configResult = { kind: 'bad', section: section, text: 'An OpenAI-compatible engine needs the address its server answers at, such as http://localhost:8000/v1.' }; return; }
    const svc = speechService(provider, sp);
    ch['speech.' + dir + '.provider'] = provider;
    // A field this engine does not read is not a field it keeps: the model
    // Cartesia never asked for is cleared, not carried from the last engine.
    ch['speech.' + dir + '.model'] = pickedValue('sp-model-' + dir);
    if (dir === 'stt') ch['speech.stt.language'] = $('sp-language-stt') ? $('sp-language-stt').value.trim() : '';
    else ch['speech.tts.voice'] = pickedValue('sp-voice-tts');
    const ceiling = $('sp-ceiling-' + dir);
    if (ceiling) {
      // A number field may carry 1e6, which parseInt reads as 1: the
      // value is read as a number, and only a whole one is a ceiling.
      const n = ceiling.value.trim() === '' ? 0 : Number(ceiling.value);
      if (!Number.isInteger(n) || n < 0) { configResult = { kind: 'bad', section: section, text: 'A monthly ceiling is a whole number, or empty for none.' }; return; }
      ch['speech.' + dir + '.' + (dir === 'stt' ? 'monthly_minutes' : 'monthly_characters')] = n;
    }
    const key = $('sp-key-' + dir).value.trim();
    const unadded = !own && provider && svc && !svc.added, moved = own && (!svc || svc.endpoint !== baseURL);
    if (unadded || moved || key) { readySpeechService(section, provider, key, baseURL, ch); return; }
  } else if (section === 'speech_speakers') {
    // The whole policy, as one object: the host accepts it whole or not at all.
    const mode = $('sp-speakers-mode').value;
    const uids = mode === 'all' ? [] : $('sp-speakers-uids').value.trim().split(/[\s,]+/).filter(Boolean);
    const pol = { mode: mode, uids: uids };
    if (mode === 'ignore') pol.unidentified = $('sp-speakers-unid').value;
    ch['speech.speakers'] = pol;
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
    configResult = { kind: 'bad', section: section, text: 'Not connected — configuration was not sent.' };
  }
}

export function saveConfigSection(section) { saveSettings(section); }
// configFeedbackHTML is the page-wide line, and it now carries only what
// belongs to no section — everything a Save button asked for is answered
// beside that button instead (savebarHTML).
export function configFeedbackHTML() {
  if (configResult && !configResult.section) return '<div class="config-result ' + configResult.kind + '">' + esc(configResult.text) + '</div>';
  return '';
}

// savebarHTML puts the answer where the hand is. A button that has been
// pressed says it is working and refuses a second press; what came back
// stands beside it. The operator who saved the second card of a long page
// was reading the top of that page for an answer that had already
// arrived — under their thumb, out of sight.
export function savebarHTML(section, note, more) {
  const adding = speechAdd.waiting() && speechAdd.waiting().section === section ? speechAdd.waiting() : null;
  const checking = config.waiting() && config.waiting().section === section;
  const said = configResult && configResult.section === section ? configResult : null;
  const middle = adding ? '<span class="savesay">Saving ' + esc(adding.name) + '… waiting for the runtime to confirm.</span>'
    : checking ? '<span class="savesay">Checking and saving… the current configuration remains active.</span>'
    : said ? '<span class="config-result ' + said.kind + '" data-said="' + esc(section) + '" role="status">' + esc(said.text) + '</span>'
    : note ? '<span class="savesay">' + esc(note) + '</span>' : '';
  // ONE SAVE AT A TIME. The pending slots are one per kind; a second save
  // while the first is being checked would take its place and orphan it
  // — its key stored and its pointer never sent. Every bar waits, and only
  // the one that asked says it is working.
  const mine = !!adding || checking;
  const busy = mine || !!speechAdd.waiting() || !!config.waiting();
  return '<div class="savebar"><button class="btn" data-save="' + esc(section) + '"' + (busy ? ' disabled' : '') + '>' +
    (mine ? 'Checking…' : ((more && more.label) || 'Save')) + '</button>' + middle + ((more && more.also) || '') + '</div>';
}

export function settingsConnectionLost() {
  let changed = false;
  // A question asked on the lost connection is never answered: ask again.
  Object.keys(asking).forEach(k => delete asking[k]);
  const lostProv = prov.drop();
  if (lostProv) {
    provDraft = lostProv.entry;
    provResult = { kind: 'bad', text: 'Connection lost before provider confirmation — check its current state after reconnect.' };
    changed = true;
  }
  if (speechAdd.drop()) {
    configResult = { kind: 'bad', text: 'Connection lost before the speech service was confirmed — check Providers after reconnect.' };
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
    configResult = { kind: 'bad', section: pending.section, text: 'Acknowledged, but ' + missed.join(', ') +
      ' did not come back as saved — the change may not have taken.' };
    return true;
  }
  speechDraftSaved(pending.section); // it is in force now, not a choice in progress
  configResult = { kind: 'good', section: pending.section, text: savedText(pending.section, (S.config && S.config.restart_required) || [], pending.changes) };
  return true;
}

export function savedText(section, restartRequired, changes) {
  if (section === 'llm') return 'Active — inference verified.';
  if (section === 'speech_stt' || section === 'speech_tts') {
    const dir = section.slice('speech_'.length), provider = changes && changes['speech.' + dir + '.provider'];
    if (!provider) return dir === 'stt' ? 'Saved — voice input is off.' : 'Saved — replies use the browser\'s built-in voice.';
    return 'Active — ' + provider + (dir === 'stt' ? ' transcribed the check.' : ' spoke the check.');
  }
  if (section === 'speech_speakers') return 'Saved — applies to the next words heard.';
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
  configResult = { kind: 'bad', section: asked && asked.section, text: message };
  renderSettings();
  return true;
}

document.addEventListener('click', (e) => {
  const btn = e.target && e.target.closest ? e.target.closest('[data-refresh-models]') : null;
  if (!btn) return;
  btn.disabled = true; btn.textContent = 'Asking…';
  query('discover', { provider: btn.dataset.refreshModels });
});
