
import { dashboardHTML, dashboardWarnHTML, wirePublicName } from './settings/access.js';
import { NEW_PROVIDER, SPEND_WORDS, chooseEffort, effortAnswered, effortForgotten, forgetSpeechLists, providersHTML, readySpeechService, setAboutOpen, setMoreOpen, setSpendByDayOpen, spendAsked, spendInSpan, spendSectionHTML, submittedEditor, substrateHTML, syncEffort } from './settings/model.js';
import { setRenderSettings, wireProviderSignIn } from './settings/parts.js';
import { contactLine, peopleHTML, wireContacts } from './settings/people.js';
import { pluginToolNote } from './settings/permissions.js';
import { HEARD_FIELDS, alike, asking, captureEdits, configFeedbackHTML, controlName, editorFields, fieldKey, finishEditor, focusOf, prov, provBroken, restoreEditors, saveSettings, sendConfigChanges, setConfigResult, speechAdd, wireLimits } from './settings/save.js';
import { modelSays } from './settings/modelwords.js';
import { asked, config, draft, edits } from './settings/state.js';
import { BY_ID, heardShown, retentionSaid, speechHTML, wireSpeech } from './settings/voice.js';
import { renderKeeping } from './settings/keep.js';
import { localConnectionLost } from './settings/local.js';
import { holdReloadWhile } from '../overlay.js';
import { sandboxCardHTML, wireSandboxCard } from '../sandbox.js';
import { spokenAudio } from '../say.js';
import { wireScriptsCard } from '../scripts.js';
import { route } from '../shell/router.js';
import { S } from '../state.js';
import WAPP from '../strings/settings-app.js';
import WVOICE from '../strings/settings-voice.js';
import WBK from '../strings/settings-backups.js';
import WACCESS from '../strings/settings-access.js';
import WPEOPLE from '../strings/settings-people.js';
import { toolsCardHTML, wireToolsCard } from '../tools.js';
import { $, esc, fill, loadingCard } from '../util.js';
import WSHELL from '../strings/shell.js';
import WMODEL from '../strings/settings-model.js';
import { query, send } from '../ws.js';
import { backupsHTML, forgetBackupsSecrets, requestBackups, wireBackups } from './backups.js';
import { noticeRouteState, wireNoticeRoute } from './expert.js';
import { mailRepairDisconnected, openUnsent, requestHeldMail, wireMailRepair } from './mail-repair.js';
import { messagesDisconnected, requestMessages, wireMessages } from './messages.js';
import { providerModels } from './model-picker.js';
import { keyDrafts, keyTyping, wireSettingControls } from './plugin-setting.js';
import { appHTML, wireApp } from './settings/app.js';
import { renderPhone } from './settings/phone.js';
export { acceptAccessCode, rejectAccessCode, wirePublicName } from './settings/access.js';
export { renderPhone } from './settings/phone.js';
export { effortAnswered } from './settings/model.js';
export { localPart } from './settings/local.js';
export { limitRowsHTML, wireProviderSignIn } from './settings/parts.js';
export { acceptSettingsConfig, captureForm, configBusy, configFeedbackHTML, configSaidHTML, configWaitingFor, forgetForm, modelSaidHTML, rejectSettingsConfig, restoreForm, saveConfigSection, savebarHTML, savedText, sendConfigChanges, speechDraftSaved } from './settings/save.js';
export { acceptSpeechLists, heardHistoryReadback, rejectSpeechLists, speakersReadback } from './settings/voice.js';
export const SECTIONS = [
  ['substrate', 'Substrate', c => substrateHTML(c)],
  ['providers', 'Providers', () => providersHTML() + spendHTML()],
  ['speech', WVOICE.title, c => speechHTML(c)],
  ['dashboard', WACCESS.title, c => dashboardHTML(c)],
  ['backups', WBK.title, () => backupsHTML()],
  ['sandbox', 'Sandbox', () => sandboxCardHTML() || loadingCard('sandbox')],
  ['tools', 'Tools', () => toolsCardHTML(pluginToolNote)],
  ['messages', WPEOPLE.title, c => { drawnRoute = noticeRouteState(); return peopleHTML(c); }],
  ['updates', WAPP.title, c => appHTML(c)]];
const MOVED_TO_TECHNICAL = { witness: 'verification', prompt: 'limits', agency: 'helpers', identity: 'reflection', logs: 'logs', storage: 'storage', limits: 'limits' };
export let sec = 'substrate';
export let provOpen = null;
export let provResult = null;
holdReloadWhile(() => edits.size > 0 || keyDrafts.size > 0 || draft.stt !== null || draft.tts !== null);
function showDraftNotice(root) {
  const note = root.querySelector('[data-settings-draft]');
  if (note) note.hidden = !editorFields(root).some(el => edits.has(fieldKey(el)));
}

let spendSpan = 'month';
let spendAsking = false;
let spendMine = '';
const spendNotMine = () => !!(spendMine && S.spendRequest && S.spendRequest !== spendMine);
function spendHTML() {
  const pick = '<label class="f" for="spend-days">' + SPEND_WORDS.days + '</label><select id="spend-days">' +
    SPEND_WORDS.choices.map(([n, label]) => '<option value="' + n + '"' + (n === spendSpan ? ' selected' : '') + '>' + esc(label) + '</option>').join('') + '</select>';
  if (!S.spend || spendNotMine()) {
    if (!spendAsking && S.view === 'settings') { spendAsking = true; spendMine = query('spend', { days: spendAsked(spendSpan) }) || ''; }
    return spendSectionHTML(null, pick, spendSpan);
  }
  spendAsking = false;
  return spendSectionHTML(spendInSpan(S.spend, spendSpan), pick, spendSpan);
}
function wireSpend(root) {
  const el = root.querySelector('#spend-days');
  if (el) el.onchange = () => { spendSpan = el.value === 'month' ? 'month' : Number(el.value) || 1; S.spend = null; spendAsking = false; renderSettings(); };
  const byDay = root.querySelector('[data-spend-byday]');
  if (byDay) byDay.ontoggle = () => setSpendByDayOpen(byDay.open);
}

function syncUnsetHint(el) {
  if (String(el.value || '').trim()) el.removeAttribute('title');
  else el.title = el.dataset.unsetHint;
}
function wireUnsetHints(root) {
  root.querySelectorAll('[data-unset-hint]').forEach(syncUnsetHint);
}

export function pickedValue(id) {
  const typed = $(id + '-typed'), pick = $(id);
  if (typed && !typed.hidden) return typed.value.trim();
  const chosen = pick ? pick.value : '';
  return chosen === BY_ID ? '' : chosen;
}

export function wirePicker(root, id) {
  const pick = root.querySelector('#' + id), typed = root.querySelector('#' + id + '-typed');
  if (!pick || !typed) return;
  typed.hidden = pick.value !== BY_ID;
  pick.onchange = () => {
    if (pick.value !== BY_ID) { typed.hidden = true; return; }
    typed.hidden = false;
    typed.focus();
  };
}

export function playSample(button, provider) {
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

export function heardReadSentence(h) {
  const secs = (h && h.retention_seconds) || HEARD_FIELDS[1][5];
  return '{name} can read what was heard in the last ' + retentionSaid(secs) + ', withheld words included, when they ask to. You can read the same here.';
}

export function heardListHTML() {
  if (!heardShown) return '';
  if (heardShown.why) return '<div class="muted" data-heard-refused>' + esc(fill(WVOICE.whatHeardUnavailable, { reason: heardShown.why })) + '</div>';
  return heardShown.rows.map(heardRowHTML).join('') +
    (heardShown.none ? '<div class="muted" data-heard-none>' + esc(heardShown.none) + '</div>' : '') +
    (heardShown.next ? '<button type="button" class="btn ghost" id="sp-heard-older" style="margin-top:8px">' + esc(WVOICE.whatHeardOlder) + '</button>' : '');
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

export function limitSection(key) {
  const set = ((S.config && S.config.cognition && S.config.cognition.settings) || []).find(s => s.key === key);
  return !set || !set.card ? 'cognition' : set.card === 'substrate' ? 'llm' : set.card;
}
S.renderPublicName = (p) => {
  if (S.config) S.config.public_name = p;
  if ((S.view === 'settings' && shown('dashboard')) || S.view === 'phone') renderSettings();
  if (p && p.status === 'claiming' && !S.publicNamePoll) {
    S.publicNamePoll = setTimeout(() => { S.publicNamePoll = null; send({ type: 'public_name_state' }); }, 3000);
  }
};
S.openSettings = (section, focusID) => {
  answered = location.hash;
  enter(section);
  const nav = document.querySelector('.nav-item[data-view="settings"]');
  if (S.view !== 'settings' && S.go) S.go('settings');
  else if (nav && S.view !== 'settings') nav.click();
  else S.requestSettingsSection();
  renderSettings();
  const el = focusID ? $(focusID) : null;
  if (el) el.focus();
};
S.renderUpdate = () => { if (S.view === 'settings' && shown('updates')) renderSettings(); };
S.renderBackups = () => { if (S.view === 'settings' && shown('backups')) renderSettings(); };
S.renderSandbox = () => { if (S.view === 'settings' && shown('sandbox')) renderSettings(); };
S.renderTools = () => { if (S.view === 'settings' && shown('tools')) renderSettings(); };
let drawnRoute = null;
S.renderNoticeRoute = () => { if (S.view === 'settings' && shown('messages') && drawnRoute !== noticeRouteState()) renderSettings(); };
S.renderMailRepair = () => { if (S.view === 'settings' && shown('messages')) renderSettings(); };
S.renderMessages = () => { if (S.view === 'settings' && shown('messages')) renderSettings(); };
export function openBackups() { sec = 'backups'; }
const GROUPS = { model: ['substrate', 'providers'], voice: ['speech'], people: ['messages'], access: ['dashboard'], backups: ['backups'], app: ['updates'] };
const groupOf = id => Object.keys(GROUPS).find(g => GROUPS[g].includes(id));
const pageOf = id => (GROUPS[groupOf(id)] || [id])[0];
const shown = id => (GROUPS[groupOf(sec)] || [sec]).includes(id);
const PAGE_NAMES = { permissions: WSHELL.pluginsAndPermissions };
function pageNameHTML(body) {
  const g = S.settingsGroup();
  const name = PAGE_NAMES[g] || WSHELL[g];
  return !name || /<h2[\s>]/.test(body) ? '' : '<h2 class="settings-access__title" data-settings-page-name>' + esc(name) + '</h2>';
}
function enter(id) {
  if (pageOf(id) === 'substrate' && pageOf(sec) !== 'substrate') { S.spend = null; spendAsking = false; }
  sec = id; provOpen = null;
}
let answered = null;
S.settingsSection = name => {
  answered = location.hash;
  if (MOVED_TO_TECHNICAL[name]) { location.replace('#/settings/technical/' + MOVED_TO_TECHNICAL[name]); return false; }
  if (name === 'held-mail') { openUnsent(); name = 'messages'; }
  const [g, id] = name === 'all' ? ['model'] : name.split('/');
  const want = GROUPS[g] ? (GROUPS[g].includes(id) ? id : GROUPS[g][0]) : name;
  if (!SECTIONS.some(([s]) => s === want)) return false;
  if (sec === want) return '#/settings/' + name !== S.settingsAddress();
  enter(want);
  return true;
};
S.settingsGroup = () => groupOf(sec) || (sec === 'sandbox' || sec === 'tools' ? 'permissions' : 'technical');
S.requestSettingsSection = () => {
  if (!S.connected) return;
  if (shown('backups')) requestBackups();
  if (shown('messages')) { requestMessages(); requestHeldMail(); }
};
S.settingsAddress = () => {
  const g = groupOf(sec);
  return '#/settings/' + (!g ? sec : GROUPS[g][0] === sec ? g : g + '/' + sec);
};
function addressSection() {
  if (S.view !== 'settings') return;
  const asked = route(location.hash);
  if (location.hash && !(asked && asked.view === 'settings')) return;
  if (answered !== null && location.hash !== answered) return;
  const want = S.settingsAddress();
  if (location.hash !== want) { try { history.replaceState(null, '', want); answered = location.hash; } catch (err) {} }
  S.renderNavBadges?.();
}
S.openUpdates = () => { answered = location.hash; sec = 'updates'; provOpen = null; renderSettings(); };

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
  const localMark = $('pv-local-' + tag);
  if (localMark) entry.local = localMark.checked;

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
    else setConfigResult({ kind: 'bad', section: added.section, text: 'Acknowledged, but ' + added.name + (got ? ' came back with no key stored' : ' is not in the registry that came back') + ' — the settings were not sent.' });
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
  if (speechAdd.claim(requestID)) { setConfigResult({ kind: 'bad', section: adding && adding.section, text: message }); renderSettings(); return true; }
  if (provBroken.claim(requestID)) { provResult = { kind: 'bad', text: message }; renderSettings(); return true; }
  const pending = prov.claim(requestID);
  if (!pending) return false;
  provResult = { kind: 'bad', text: message };
  renderSettings();
  return true;
}

let focusOwed = null;
document.addEventListener('focusout', e => { if (!e.relatedTarget) focusOwed = focusOf($('settings-stack'), e.target); });
export function disableOwing(el) {
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
  if (S.settingsHeld?.()) return;
  if (S.view === 'phone') return renderPhone();
  const st = $('settings-stack');
  if (st.settingsComposing) { st.settingsRefreshDue = true; return; }
  if (st.dataset.section === 'backups' && !shown('backups')) forgetBackupsSecrets();
  captureEdits(st);
  const expanded = new Set([...st.querySelectorAll('[data-provider-editor] details[open]')]
    .map(el => el.closest('[data-edit-scope]').dataset.editScope));
  const focused = document.activeElement;
  const active = focused && st.contains(focused) && editorFields(st).includes(focused) ? { el: focused, key: fieldKey(focused) } : null;
  const had = focused === document.body ? focusOwed : focusOf(st, focused);
  const typing = keyTyping();
  if (!S.providersLoaded) query('providers');
  const c = S.config;
  let body = '';
  body += '<div class="muted" data-settings-draft hidden>Unsaved edits — not yet the running configuration.</div>';
  body += configFeedbackHTML();
  SECTIONS.forEach(([id, , draw]) => { if (shown(id)) body += '<div data-section="' + id + '">' + draw(c) + '</div>'; });
  const html = pageNameHTML(body) + body;
  const immediate = focused && st.contains(focused) && focused.hasAttribute('data-immediate') ? focused : null;
  const keep = active && active.el.list ? [active.el, active.el.list] : immediate ? [immediate] : [];
  const twins = renderKeeping(st, html, st.dataset.section !== pageOf(sec), keep);
  st.dataset.section = pageOf(sec);
  restoreEditors(st, active, twins);
  const more = st.querySelector('[data-more]');
  if (more) more.ontoggle = () => setMoreOpen(more.open);
  const about = st.querySelector('[data-more-about]');
  if (about) about.ontoggle = () => setAboutOpen(about.open);
  const effort = st.querySelector('#model-effort');
  if (effort) effort.onchange = () => chooseEffort(effort, st);
  syncEffort(st);
  st.querySelectorAll('[data-provider-editor] details').forEach(el => {
    if (expanded.has(el.closest('[data-edit-scope]').dataset.editScope)) el.open = true;
  });
  st.oninput = st.onchange = e => {
    if (e.target.hasAttribute('data-unset-hint')) syncUnsetHint(e.target);
    captureEdits(st, e.target);
    showDraftNotice(st);
    syncEffort(st);
  };
  if (!st.settingsListening) {
    st.settingsListening = true;
    st.addEventListener('compositionstart', () => { st.settingsComposing = true; });
    st.addEventListener('compositionend', () => {
      st.settingsComposing = false;
      if (st.settingsRefreshDue) {
        st.settingsRefreshDue = false;
        queueMicrotask(() => { if (S.view === 'settings') renderSettings(); });
      }
    });
  }

  st.querySelectorAll('[data-save]').forEach(btn => { btn.onclick = () => { saveSettings(btn.dataset.save); renderSettings(); }; });
  wireLimits(st);
  if (shown('messages')) { wireMessages(st); wireNoticeRoute(st); wireContacts(st); wireMailRepair(st); }
  wirePublicName(st);
  wireScriptsCard(st, () => { if (S.view === 'settings' && shown('dashboard')) renderSettings(); });
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
  const toggleProvider = i => {
    const name = S.providers[i].name;
    provOpen = provOpen === name ? null : name;
    renderSettings();
  };
  st.querySelectorAll('[data-prov-row]').forEach(row => {
    row.onclick = (e) => {
      if (e.target.closest('input,button,label')) return;
      toggleProvider(parseInt(row.dataset.provRow, 10));
    };
  });
  st.querySelectorAll('[data-prov-manage]').forEach(btn => { btn.onclick = () => {
    const i = btn.dataset.provManage;
    toggleProvider(parseInt(i, 10));
    document.querySelector('[data-prov-manage="' + i + '"]')?.focus();
  }; });
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
  wireApp(st);
  wireSpend(st);
  const openNew = $('pv-open-new');
  if (openNew) openNew.onclick = () => { provOpen = NEW_PROVIDER; renderSettings(); };

  const provEl = $('cfg-provider');
  if (provEl) {
    const syncModel = clear => {
      const p = S.providers.find(x => x.name === provEl.value) || S.providers.find(x => x.default) || null;
      const model = $('cfg-model'), list = $('cfg-model-list');
      if (!model || !list) return;
      if (clear) model.value = '';
      const hint = p && p.default_model ? 'Default: ' + p.default_model : 'Search or enter a model';
      if (model.placeholder !== hint) model.placeholder = hint;
      const options = providerModels(p).map(m => '<option value="' + esc(m) + '"></option>').join('');
      if (list.innerHTML !== options) list.innerHTML = options;
      const refresh = model.parentNode.querySelector('[data-refresh-models]');
      if (refresh) {
        const target = p ? p.name : '';
        if (refresh.dataset.refreshModels !== target) refresh.dataset.refreshModels = target;
        if (refresh.hidden !== !p) refresh.hidden = !p;
        const said = $('cfg-model-refresh-hint');
        if (said && said.hidden !== !p) said.hidden = !p;
        const asked = !!target && refreshing.has(target);
        if (asked && !refresh.disabled) disableOwing(refresh);
        else if (!asked && refresh.disabled) refresh.disabled = false;
        const words = asked ? 'Asking…' : WMODEL.modelRefresh;
        if (refresh.textContent !== words) refresh.textContent = words;
      }
    };
    provEl.onchange = () => syncModel(true);
    syncModel(false);
  }
  if (shown('sandbox')) wireSandboxCard(st);
  if (shown('backups')) wireBackups(st);
  if (shown('tools')) wireToolsCard(st);
  showDraftNotice(st);
  focusAfter(st, had);
  addressSection();
}
export const cleanContacts = lines => lines.map(c => ({ ...contactLine(c), name: c.name.trim(), channel: c.channel.trim().toLowerCase(), address: c.address.trim() }));
export function settingsConnectionLost() {
  mailRepairDisconnected();
  messagesDisconnected();
  let changed = false;
  Object.keys(asking).forEach(k => delete asking[k]);
  refreshing.clear();
  effortForgotten();
  spendAsking = false;
  if (localConnectionLost()) changed = true;
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
    setConfigResult({ kind: 'bad', text: 'Connection lost before the speech service was confirmed — check Providers after reconnect.' });
    changed = true;
  }
  const lostConfig = config.drop();
  if (lostConfig) {
    setConfigResult({ kind: 'bad', section: lostConfig.section, text: lostConfig.section === 'llm' ? modelSays('connectionLost') : 'Connection lost before configuration confirmation — keys may already be saved. Check current settings after reconnect; unsent edits are kept.' });
    changed = true;
  }
  if (changed) renderSettings();
  return changed;
}

const refreshing = new Map();
export function refreshAnswered(requestID, provider) {
  if (!provider || !requestID || refreshing.get(provider) !== requestID) return false;
  refreshing.delete(provider);
  return true;
}

export function settingsSettled(requestID, provider, refusal, toast) {
  if (!requestID || !(refreshAnswered(requestID, provider) || effortAnswered(requestID))) return false;
  if (refusal !== undefined) { toast(refusal); if (S.view === 'settings' || S.view === 'phone') renderSettings(); }
  return true;
}

document.addEventListener('click', (e) => {
  const btn = e.target && e.target.closest ? e.target.closest('[data-refresh-models]') : null;
  const target = btn && btn.dataset.refreshModels;
  if (!target) return;
  if (refreshing.has(target)) return;
  const id = query('discover', { provider: target });
  if (!id) return;
  refreshing.set(target, id);
  disableOwing(btn); btn.textContent = 'Asking…';
});

setRenderSettings(renderSettings);
