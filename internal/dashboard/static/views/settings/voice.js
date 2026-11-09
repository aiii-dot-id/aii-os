import { heardListHTML, heardReadSentence, playSample, wirePicker } from '../settings.js';
import { readySpeechService, submittedEditor } from './model.js';
import { hinted, listKey, listed, live, renderSettings } from './parts.js';
import { HEARD_FIELDS, asking, configResult, saveSettings, savebarHTML, sendConfigChanges, speechAdd } from './save.js';
import { asked, config, draft } from './state.js';
import { echoCard, myVoiceCard, nameMyVoice, speakersCard, voiceModeCard } from './voice-speakers.js';
import { signedFetch } from '../../pagekey.js';
import { S } from '../../state.js';
import { $, esc, fill, loadingCard } from '../../util.js';
import { query } from '../../ws.js';
import { settingHTML } from '../plugin-setting.js';
import W from '../../strings/settings-voice.js';

const capital = text => text[0].toUpperCase() + text.slice(1);
export const named = text => { const n = (S.stats && S.stats.name) || ''; return fill(text, { name: n && n !== 'Unnamed' ? n : W.unnamed }); };
const SPEECH_DIRS = {
  stt: {
    kind: 'transcription model',
    none: W.sttOff, off: W.sttNone, unsetProvider: W.sttNone,
  },
  tts: {
    kind: 'model',
    none: W.ttsBrowser, off: W.repliesSpokenByBrowser + '.', unsetProvider: W.repliesSpokenByBrowser + '.',
    promise: 'Speaks every reply — to what you type, and to what you say in a conversation.',
    note: 'With nothing chosen, the engine that hears a conversation answers in its own voice. Another engine speaks a conversation\'s replies only where a page holds its speaker open; elsewhere, and whenever the voice chosen cannot speak, the browser\'s own voice does.',
  },
};
const OWN_SERVER = 'own-server:';
export const BY_ID = 'by-id:';

function speechServices(dir, sp) {
  return ((sp && sp.services) || []).filter(s => s.speech && s.speech[dir]);
}
export function speechService(value, sp) {
  return ((sp && sp.services) || []).find(s => s.name === value) || null;
}

function speechOffer(dir, value, sp) {
  if (!value) return null;
  const svc = value === OWN_SERVER ? null : speechService(value, sp);
  if (!svc || !svc.speech) return { model_required: true, voice_required: dir === 'tts' };
  return svc.speech[dir] || null;
}
export function ownServer(value, sp) {
  const svc = speechService(value, sp);
  return value === OWN_SERVER || !!(svc && svc.custom);
}
function keyFacts(value, sp) {
  if (!value) return { label: '', name: '', hint: '', note: '', stored: false };
  if (value === OWN_SERVER) return { label: 'API key (optional)', name: 'API key (optional)', stored: false, hint: 'Leave blank for a server that asks for no key.', note: 'Kept with the server\'s entry in providers.json.' };
  const entry = (S.providers || []).find(p => p.name === value);
  const svc = speechService(value, sp) || { name: value, added: !!entry, has_key: !!(entry && entry.has_key), chats: !!(entry && entry.chat) };
  const stored = !!svc.has_key;
  const env = svc.api_key_env && !stored ? '; left blank, ' + svc.api_key_env + ' is used if it is set.' : '.';
  const where = !svc.added && svc.speech ? 'Adds ' + svc.name + ' to providers.json with this key' + (svc.chats ? ', where its chat models use it too' : '')
    : svc.chats ? 'Shared with the ' + svc.name + ' chat provider'
    : 'Kept with ' + svc.name + ' in providers.json';
  return {
    label: fill(W.serviceKey, { service: svc.title || svc.name }) + ' · ' + (stored ? W.serviceKeyKept : W.serviceKeyNone), name: fill(W.serviceKey, { service: svc.title || svc.name }), stored,
    hint: stored ? 'Blank keeps the key stored for ' + svc.name + '.'
      : svc.custom ? 'Leave blank for a server that asks for no key.'
      : 'No key is stored for ' + svc.name + ' — cloud speech services refuse a request without one.',
    note: where + env,
  };
}

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
function isAsking(dir, value) {
  return !!asking[listKey(value, dir)];
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
  return '<label class="settings-voice__label" for="' + id + '">' + esc(label) + '</label>' +
    '<select id="' + id + '"' + hinted(value, hint) + '>' + optionsHTML(items, value) + '</select>' +
    '<input type="text" id="' + id + '-typed" hidden placeholder="the id the service knows it by" autocomplete="off">';
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

const keyOpen = { stt: '', tts: '' };
const field = (body, attrs) => '<div class="settings-voice__field"' + (attrs || '') + '>' + body + '</div>';
const quiet = (id, text, attrs) => '<p class="settings-voice__hint" id="' + id + '"' + (attrs || '') + '>' + esc(text) + '</p>';

export const runsEngine = (dir, sp) => speechServices(dir, sp).some(s => s.plugin);
const savedChoice = (dir, sp) => {
  const half = sp[dir] || {};
  return dir === 'stt' && half.default && sp.mode?.listen === 'off' && !draft.sttLeft ? '' : (half.provider || '');
};

let browserGone = false;
const answerSaysBrowserGone = () => { const sent = config.waiting(); return sent?.section === 'speech' && sent.editor?.choice?.tts === ''; };
export const browserGoneAnswered = () => { browserGone = false; };

function speechCard(dir, sp) {
  const d = SPEECH_DIRS[dir], inForce = sp[dir] || {};
  const provider = draft[dir] !== null ? draft[dir] : savedChoice(dir, sp);
  const cur = provider === (inForce.provider || '') ? inForce : { provider: provider };
  const services = speechServices(dir, sp);
  const known = !provider || services.some(s => s.name === provider);
  const o = speechOffer(dir, provider, sp);
  const svc = speechService(provider, sp);
  const own = ownServer(provider, sp);
  const got = listed(dir, provider);
  const engineRuns = runsEngine(dir, sp);
  const engine = field('<label class="settings-voice__label" for="sp-provider-' + dir + '">' + esc(named(dir === 'stt' ? W.sttEngine : W.ttsEngine)) + '</label>' +
    '<select id="sp-provider-' + dir + '"' + hinted(provider, engineRuns ? W.micOffHint : d.unsetProvider) + '>' +
    (dir === 'tts' && engineRuns && provider ? '' : '<option value=""' + (provider ? '' : ' selected') + '>' + esc(d.none) + '</option>') +
    services.map(s => '<option value="' + esc(s.name) + '"' + (s.name === provider ? ' selected' : '') + '>' + esc(s.title || s.name) + '</option>').join('') +
    (known ? '' : '<option value="' + esc(provider) + '" selected>' + esc(provider) + ' (not a speech service)</option>') +
    '<option value="' + OWN_SERVER + '">' + esc(W.sttOwnServer) + '</option>' +
    '</select>' + quiet('sp-engine-hint-' + dir, dir === 'stt' ? W.sttHint : W.ttsHint) +
    (dir === 'tts' && browserGone && engineRuns && !answerSaysBrowserGone() ? quiet('sp-browser-gone', fill(W.browserGone, { engine: services.find(s => s.plugin).title || services.find(s => s.plugin).name })) : ''));
  const baseValue = own && svc ? svc.endpoint : '';
  const base = field('<label class="settings-voice__label" for="sp-base-' + dir + '">' + esc(W.serverAddress) + '</label>' +
    '<input type="text" id="sp-base-' + dir + '" value="' + esc(baseValue) + '"' + hinted(baseValue, W.serverAddressHint) + '>' +
    '<p class="settings-voice__hint">' + esc(W.serverAddressHint) + '</p>', ' id="sp-base-row-' + dir + '"' + (own ? '' : ' hidden'));
  const isPlugin = !!(svc && svc.plugin);
  const k = keyFacts(provider, sp);

  const languages = languagesOf(got);
  const language = field('<label class="settings-voice__label" for="sp-language-' + dir + '">' + esc(W.language) + '</label>' +
    '<select id="sp-language-' + dir + '">' + languageOptionsHTML(languages, cur.language || '') + '</select>', ' id="sp-language-row-' + dir + '"' + (languages.length ? '' : ' hidden'));

  const wantsModel = !o || o.model_required;
  const none = provider ? '' : ' hidden';
  const model = wantsModel
    ? field(pickerHTML('sp-model-' + dir, own ? W.serverModel : W.model, cur.model || '', itemsOf(got, 'models', cur.language),
      own ? W.serverModelHint : 'Required — the ' + d.kind + ' this service is to use.') +
      quiet('sp-model-state-' + dir, stateLine(dir, provider, 'models')), none)
    : '';
  const wantsVoice = dir === 'tts' && (!o || o.voice_required);
  const searchable = !!(o && o.searches_voices);
  const voice = wantsVoice
    ? (searchable ? field('<label class="settings-voice__label" for="sp-find-tts">' + esc(W.voiceFind) + '</label><input type="search" id="sp-find-tts" autocomplete="off">' +
        '<p class="settings-voice__hint">' + esc(W.voiceHint) + '</p>') : '') +
      field(pickerHTML('sp-voice-tts', W.voice, cur.voice || '', itemsOf(got, 'voices', cur.language), W.voiceRequired) +
      quiet('sp-voice-state-tts', stateLine(dir, provider, 'voices')), none)
    : '';

  const d2 = dir === 'stt'
    ? { key: 'monthly_minutes', unit: 'minutes', of: 'listening', note: 'the microphone stops until the month turns over' }
    : { key: 'monthly_characters', unit: 'characters', of: 'speaking', note: 'replies are read by the browser\'s own voice until the month turns over' };
  const ceilingValue = (dir === 'stt' ? inForce.monthly_minutes : inForce.monthly_characters) || 0;
  const ceilingHint = 'The most ' + d2.unit + ' of ' + d2.of + ' ' + named('{name}') + ' may buy in a calendar month. Reached, ' + d2.note + '. Empty is no ceiling.';
  const ceiling = isPlugin ? '' : field('<label class="settings-voice__label" for="sp-ceiling-' + dir + '">' + esc(W.serviceCeiling) + '</label>' +
    '<input type="number" id="sp-ceiling-' + dir + '" min="0" step="1" value="' + (ceilingValue || '') + '" placeholder="no ceiling"' +
    hinted(ceilingValue ? String(ceilingValue) : '', ceilingHint) + '>' + '<p class="settings-voice__hint">' + esc(ceilingHint) + '</p>');
  const open = keyOpen[dir] === provider || !k.stored;
  const key = '<div class="settings-voice__service" id="sp-key-row-' + dir + '"' + (provider && !isPlugin ? '' : ' hidden') + '>' +
    '<div class="settings-voice__row"><span class="settings-voice__row-words" id="sp-key-label-' + dir + '">' + esc(k.label) + '</span>' +
    (k.stored ? '<button type="button" class="settings-voice__quiet" id="sp-key-change-' + dir + '" aria-expanded="' + open + '" aria-controls="sp-key-fields-' + dir + '">' + esc(W.change) + '</button>' : '') + '</div>' +
    '<div class="settings-voice__service-fields" id="sp-key-fields-' + dir + '"' + (open ? '' : ' hidden') + '>' +
    field('<label class="settings-voice__label" for="sp-key-' + dir + '">' + esc(k.name) + '</label><input type="password" id="sp-key-' + dir + '" autocomplete="off"' + hinted('', k.hint) + '>' +
      quiet('sp-key-note-' + dir, k.note)) + ceiling +
    (inForce.endpoint && provider === inForce.provider ? quiet('sp-endpoint-' + dir, W.serverAddress + ': ' + inForce.endpoint) : '') + '</div></div>';
  const spent = (sp.spent || []).some(u => u.direction === dir) || (provider && !isPlugin) ? quiet('sp-spent-' + dir, spentLine(dir, sp, ceilingValue)) : '';

  const wantScope = dir === 'stt' ? 'hearing' : 'speaking';
  const tunables = isPlugin ? (svc.settings || []).filter(x => x.scope === wantScope) : [];
  const engineSettings = tunables.length
    ? '<div id="sp-engine-settings-' + dir + '" class="engine-settings">' +
      '<span class="settings-voice__label">' + esc((svc.title || svc.name) + ' — ' + (dir === 'stt' ? 'hearing' : 'speaking')) + '</span>' +
      tunables.map(x => settingHTML(svc.name, x)).join('') + '</div>'
    : (isPlugin ? '<div id="sp-engine-settings-' + dir + '" class="engine-settings"></div>' : '');

  const chosenName = provider ? (svc && svc.title) || provider : d.none;
  const unsaved = provider !== savedChoice(dir, sp) && provider !== OWN_SERVER ? ' ' + chosenName + ' is chosen and not saved; Save to put it in force.' : '';
  const service = (speechService(inForce.provider, sp)?.title || inForce.provider) + (inForce.plugin ? ', installed on this machine' : '');
  const silent = dir === 'tts' && sp.mode?.speak === 'off';
  const inForceWords = dir === 'stt' && sp.mode?.listen === 'off' ? W.micOffHint
    : silent ? W.repliesNotSpoken
    : !inForce.provider ? d.off
    : inForce.error ? 'pointer does not resolve: ' + inForce.error
    : (dir === 'stt' ? fill(W.listeningWith, { source: service }) : fill(W.repliesSpokenBy, { service })) +
      (inForce.plugin && inForce.default ? ', and chosen because it is installed; pick a service above to use one instead.' : '.');
  const readback = '<p class="settings-voice__line" id="sp-readback-' + dir + '"' + (inForce.error ? ' style="color:var(--bad)"' : '') + '>' + esc(inForceWords + unsaved) + '</p>';
  const sample = dir === 'tts' ? '<div class="settings-voice__row"' + none + '><button class="btn ghost" id="sp-sample-tts"' + (provider && provider !== OWN_SERVER && !isPlugin ? '' : ' disabled') + '>' + esc(W.playSample) + '</button>' +
    '<span class="savesay" id="sp-sample-state" role="status" aria-live="polite"></span></div>' : '';
  const heading = dir === 'stt' ? W.hearing : named(W.theirVoice);
  return '<section class="settings-voice__part" aria-labelledby="sp-part-' + dir + '" aria-describedby="sp-engine-hint-' + dir + '" data-speech-section="speech_' + dir + '" data-edit-scope="' + esc('speech:' + dir + ':' + provider) + '">' +
    '<h3 class="settings-voice__h3" id="sp-part-' + dir + '">' + esc(heading) + '</h3>' +
    '<div class="settings-voice__fields">' + engine + base + language + model + voice + '</div>' + sample + (d.note && !silent ? quiet('sp-note-' + dir, (provider ? d.promise + ' ' : '') + d.note) : '') + key + spent + engineSettings + readback + '</section>';
}

let moreOpen = false;
export function speechHTML(c) {
  if (!c) return loadingCard('configuration');
  const sp = c.speech || { stt: {}, tts: {} };
  if (draft.tts === '' && runsEngine('tts', sp)) { draft.tts = null; browserGone = true; }
  return '<div id="speech-form" class="settings-voice">' +
    '<div class="settings-voice__head"><h2 class="settings-voice__title">' + esc(W.title) + '</h2><p class="settings-voice__intro" id="sp-talk-says">' + esc(named(W.intro)) + '</p></div>' +
    voiceModeCard(sp, !!(draft.tts !== null ? draft.tts : sp.tts?.provider)) + speechCard('stt', sp) + speechCard('tts', sp) +
    '<details class="settings-voice__more" id="sp-more"' + (moreOpen ? ' open' : '') + '><summary>' + esc(W.more) + ' <span class="settings-voice__more-hint">· ' + esc(W.moreHint) + '</span></summary>' +
    '<div class="settings-voice__more-body">' + speakersCard(sp) + myVoiceCard(sp) + heardHistoryCard(sp) + echoCard() + '</div></details>' +
    '<div class="settings-voice__save">' + savebarHTML('speech', W.saveHint, { label: W.save }) + '</div></div>';
}

function heardHistoryCard(sp) {
  const h = sp.heard_history || {};
  return '<section class="settings-voice__group" id="sp-heard-history" data-speech-section="speech_heard" aria-labelledby="sp-heard-title">' +
    '<h4 class="settings-voice__label" id="sp-heard-title">' + esc(W.whatHeard) + '</h4>' +
    '<p class="settings-voice__line" id="sp-heard-says">' + esc(capital(named(heardReadSentence(h)))) + '</p>' +
    '<p class="settings-voice__hint">' + esc(named(W.whatHeardHint)) + '</p>' +
    '<div class="settings-voice__row"><button type="button" class="btn ghost" id="sp-heard-show">' + esc(W.whatHeardShow) + '</button></div>' +
    '<div id="sp-heard"' + (heardShown ? '' : ' hidden') + '>' + heardListHTML() + '</div>' +
    '<div class="settings-voice__fields">' + HEARD_FIELDS.map(([id, key, label, min, max]) => field('<label class="settings-voice__label" for="' + id + '">' + capital(label) + '</label>' +
      '<input type="number" id="' + id + '" min="' + min + '" max="' + max + '" step="1" value="' + esc(String(h[key] || '')) + '">')).join('') + '</div>' +
    '<p class="settings-voice__hint">' + esc(named('What was heard is held in memory for {name} to read when they ask, and is cleared when {name} restarts. Empty takes the default.')) + '</p>' +
    '<p class="settings-voice__hint" id="sp-heard-readback">' + esc(heardHistoryReadback(h)) + '</p></section>';
}

export function heardHistoryReadback(h) {
  if (!h.capacity) return '';
  const plural = (n, one) => n + ' ' + one + (n === 1 ? '' : 's');
  return 'In force: the last ' + plural(h.capacity || 0, 'final') + ', up to ' + (h.text_bytes || 0) + ' text bytes each, for ' + retentionSaid(h.retention_seconds || 0) +
    '; a read returns ' + plural(h.page_rows || 0, 'row') + ' unless the identity asks for another number.';
}

export function retentionSaid(secs) {
  const plural = (n, one) => n + ' ' + one + (n === 1 ? '' : 's');
  return secs % 60 === 0 ? plural(secs / 60, 'minute') : plural(secs, 'second');
}

export let heardShown = null;
let heardAsking = false;

async function showHeard(after) {
  if (heardAsking) return;
  heardAsking = true;
  let body = null, why = '';
  try {
    const res = await signedFetch('/speech/heard' + (after ? '?after=' + encodeURIComponent(String(after)) : ''), { cache: 'no-store', credentials: 'same-origin' });
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
      none: rows.length ? '' : after ? W.whatHeardNoneBefore : W.whatHeardNone };
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
  return heard + (f || q ? ' · withheld since start: ' + f + ' final' + (f === 1 ? '' : 's') + ', ' + q + ' partial' + (q === 1 ? '' : 's') : '');
}

const tick = id => { const radio = $(id); if (radio && !radio.checked) { radio.checked = true; radio.dispatchEvent(new Event('change', { bubbles: true })); } };

export function wireSpeech(root) {
  const more = root.querySelector('#sp-more');
  if (more) more.ontoggle = () => { moreOpen = more.open; };
  root.querySelectorAll('input[name="sp-mode-listen"]').forEach(radio => radio.addEventListener('change', () => {
    if (!radio.checked) return;
    if (radio.value === 'off') { if (draft.sttLeft) { draft.sttLeft = false; renderSettings(); } return; }
    draft.sttLeft = true;
    const sel = root.querySelector('#sp-provider-stt');
    if (!sel || sel.value) return;
    draft.stt = null;
    renderSettings();
  }));
  if (root.querySelector('#sp-provider-stt')?.value === '' && (draft.stt === '' || runsEngine('stt', S.config?.speech || {}))) tick('sp-mode-listen-off');
  ['stt', 'tts'].forEach(dir => {
    const change = root.querySelector('#sp-key-change-' + dir), fields = root.querySelector('#sp-key-fields-' + dir), sel = root.querySelector('#sp-provider-' + dir);
    if (change && fields) change.onclick = () => {
      fields.hidden = !fields.hidden;
      change.setAttribute('aria-expanded', String(!fields.hidden));
      keyOpen[dir] = fields.hidden ? '' : sel.value;
    };
  });
  const modeSel = root.querySelector('#sp-speakers-mode');
  if (modeSel) modeSel.onchange = () => {
    const m = modeSel.value;
    const list = root.querySelector('#sp-speakers-list-row'), unid = root.querySelector('#sp-speakers-unid-row');
    if (list) list.hidden = m === 'all';
    if (unid) unid.hidden = m !== 'ignore';
  };
  if (modeSel) modeSel.onchange();
  root.querySelectorAll('[data-my-voice]').forEach(btn => {
    btn.onclick = () => { nameMyVoice(btn.dataset.uuid, btn.dataset.myVoice === 'name'); renderSettings(); };
  });
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
      const sp = S.config?.speech || {}, was = draft[dir] !== null ? draft[dir] : savedChoice(dir, sp);
      draft[dir] = sel.value;
      if (dir === 'tts') browserGone = false;
      if (dir === 'stt' && sel.value && !was && $('sp-mode-listen-off')?.checked) tick('sp-mode-listen-interactive');
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

export function saveSpeechSettings() {
  if (config.waiting() || speechAdd.waiting()) return;
  browserGone = false;
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
