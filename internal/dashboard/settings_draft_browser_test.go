//go:build !windows

package dashboard

import (
	"strconv"
	"strings"
	"testing"
)

const settingsDraftPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames, setConnected } from './ws.js';
import { renderSettings, acceptSettingsConfig, rejectSettingsConfig, settingsConnectionLost } from './views/settings.js';
import { assert, run } from './__harness.js';

run(() => {
  const el = id => document.getElementById(id);
  const edit = (id, value) => { const node = el(id); node.value = value; node.dispatchEvent(new Event('input', { bubbles: true })); return node; };
  const nav = name => document.querySelector('[data-sec="' + name + '"]').click();
  const save = name => { document.querySelector('[data-save="' + name + '"]').click(); return frames.filter(f => f.type === 'config_set').at(-1); };
  S.identityExists = true;
  S.view = 'settings';
  S.providersLoaded = true;
  S.providers = [{ name: 'fixture', default: true, models: ['base', 'draft'], chat: true }];
  S.config = { llm: { provider: 'fixture', model: 'base', resolved_provider: 'fixture', resolved_model: 'base', timeout_seconds: 120, probe_timeout_seconds: 20 },
    prompt: { max_tokens: 1000, recent_turns: 5 }, dashboard: { host: '127.0.0.1', port: 8180, tls: false } };
  renderSettings();
  let model = edit('cfg-model', 'unsaved-model');
  model.focus(); model.setSelectionRange(3, 6);
  S.config.llm.resolved_model = 'live-status-changed';
  S.config.llm.timeout_seconds = 321;
  renderSettings();
  assert(el('cfg-model').value === 'unsaved-model', 'broadcast ate typed model');
  assert(document.activeElement === el('cfg-model') && el('cfg-model').selectionStart === 3 && el('cfg-model').selectionEnd === 6, 'broadcast lost caret/selection');
  assert(el('cfg-timeout').value === '321', 'untouched field did not follow host truth');
  assert(el('settings-stack').textContent.includes('live-status-changed'), 'draft froze live readback');
  el('cfg-model').blur();
  renderSettings();
  assert(el('cfg-model').value === 'unsaved-model', 'blur discarded draft');
  nav('prompt'); edit('cfg-ptokens', '2345'); nav('substrate');
  assert(el('cfg-model').value === 'unsaved-model', 'section switch discarded draft');
  nav('prompt');
  assert(el('cfg-ptokens').value === '2345', 'another section stole the draft');
  nav('substrate');
  const sent = save('llm');
  assert(sent.config['llm.model'] === 'unsaved-model', 'save did not use the draft');
  assert(el('cfg-model').value === 'unsaved-model', 'pending save erased draft');
  edit('cfg-model', 'newer-edit');
  assert(!acceptSettingsConfig('another-tabs-request'), 'foreign acknowledgement consumed our save');
  S.config.llm.model = 'unsaved-model';
  assert(acceptSettingsConfig(sent.request_id), 'matching save was not acknowledged');
  renderSettings();
  assert(el('cfg-model').value === 'newer-edit', 'acknowledgement A erased edit B');
  const newer = save('llm');
  S.config.llm.model = 'newer-edit';
  assert(acceptSettingsConfig(newer.request_id), 'newer save not acknowledged');
  renderSettings();
  S.config.llm.model = 'other-tab-saved';
  renderSettings();
  assert(el('cfg-model').value === 'other-tab-saved', 'acknowledged draft hid later host state');
  edit('cfg-model', 'save-in-flight');
  const back = save('llm');
  edit('cfg-model', 'other-tab-saved');
  S.config.llm.model = 'save-in-flight';
  acceptSettingsConfig(back.request_id); renderSettings();
  assert(el('cfg-model').value === 'other-tab-saved', 'returning to the old value was mistaken for no newer edit');
  edit('cfg-model', 'rejected-edit');
  const rejected = save('llm');
  assert(rejectSettingsConfig('fixture refusal', rejected.request_id), 'refusal not correlated');
  assert(el('cfg-model').value === 'rejected-edit', 'refusal erased editable draft');
  const mismatched = save('llm');
  acceptSettingsConfig(mismatched.request_id); renderSettings();
  assert(el('cfg-model').value === 'rejected-edit' && el('settings-stack').textContent.includes('Save could not be verified'), 'mismatched readback erased or falsely confirmed draft');
  save('llm'); edit('cfg-model', 'offline-edit');
  const before = frames.filter(f => f.type === 'config_set').length;
  settingsConnectionLost();
  renderSettings();
  assert(el('cfg-model').value === 'offline-edit', 'connection loss erased newer draft');
  assert(!document.querySelector('[data-settings-draft]').hidden, 'local intent was not labelled unsaved');
  assert(frames.filter(f => f.type === 'config_set').length === before, 'recovery replayed a save');
  setConnected(false); save('llm');
  assert(el('cfg-model').value === 'offline-edit' && frames.filter(f => f.type === 'config_set').length === before, 'failed send discarded or transmitted the draft');
  setConnected(true); renderSettings();
  edit('cfg-model', ''); renderSettings();
  assert(el('cfg-model').value === '', 'explicit clear became the saved value');
  nav('prompt'); edit('cfg-ptokens', '0'); renderSettings();
  assert(el('cfg-ptokens').value === '0', 'zero draft became the saved value');
  S.config.dashboard.tls = true; nav('dashboard');
  el('cfg-dtls').checked = false; el('cfg-dtls').dispatchEvent(new Event('change', { bubbles: true }));
  renderSettings();
  assert(!el('cfg-dtls').checked && S.config.dashboard.tls, 'false draft was lost or changed host truth');
});
</script>`

func TestSettingsDraftSurvivesReadbackAndAcknowledgesOnlySubmittedEdits(t *testing.T) {
	runPageInEngines(t, settingsDraftPage, settingsDraftModules(t))
}

func settingsDraftModules(t *testing.T) map[string][]byte {
	t.Helper()
	modules := substrateModules(t)
	util, err := staticFS.ReadFile("static/util.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/util.js"] = util
	return modules
}

const settingsProviderDraftPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, acceptProviderSave, rejectProviderSave, settingsConnectionLost } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  const el = id => document.getElementById(id);
  const edit = (id, v) => { const n = el(id); n.value = v; n.dispatchEvent(new Event('input', { bubbles: true })); return n; };
  const save = tag => { document.querySelector('[data-prov-commit="' + tag + '"]').click(); return frames.filter(f => f.type === 'provider_set').at(-1); };
  let storageWrites = 0;
  const setItem = Storage.prototype.setItem;
  Storage.prototype.setItem = function(...args) { storageWrites++; return setItem.apply(this, args); };
  S.identityExists = true; S.providersLoaded = true;
  const a = { name: 'Alpha', endpoint: 'https://alpha.test', default_model: 'base', has_key: false, chat: true, models: [] };
  const b = { name: 'Beta', endpoint: 'https://beta.test', default_model: 'base', has_key: false, chat: true, models: [] };
  S.providers = [a, b]; S.config = { llm: {} };
  renderSettings(); document.querySelector('[data-sec="providers"]').click();
  document.querySelector('[data-prov-row="0"]').click();
  edit('pv-model-0', 'alpha-draft'); edit('pv-key-0', 'fixture-secret-A');
  document.querySelector('[data-provider-editor] details').open = true;
  edit('pv-temp-0', '0.7');
  S.providers = [b, a]; renderSettings();
  assert(el('pv-name-1')?.value === 'Alpha' && el('pv-model-1')?.value === 'alpha-draft', 'reordering attached edits to the wrong provider');
  assert(el('pv-key-1').value === 'fixture-secret-A', 'provider broadcast lost unsaved key');
  assert(el('pv-temp-1').value === '0.7' && el('pv-temp-1').closest('details').open, 'broadcast hid or lost advanced edits');
  assert(!el('settings-stack').innerHTML.includes('fixture-secret-A'), 'credential draft copied into markup');
  const request = save('1');
  edit('pv-model-1', 'newer-alpha'); edit('pv-key-1', 'fixture-secret-B');
  a.has_key = true; a.default_model = 'alpha-draft';
  assert(!acceptProviderSave('foreign'), 'foreign provider acknowledgement consumed the save');
  assert(acceptProviderSave(request.request_id), 'provider acknowledgement not accepted'); renderSettings();
  assert(el('pv-model-1')?.value === 'newer-alpha' && el('pv-key-1')?.value === 'fixture-secret-B', 'provider acknowledgement erased newer edits');
  const second = save('1');
  rejectProviderSave('fixture refusal', second.request_id);
  assert(el('pv-key-1').value === 'fixture-secret-B', 'provider refusal lost key');
  const third = save('1');
  settingsConnectionLost(); renderSettings();
  assert(el('pv-model-1').value === 'newer-alpha', 'provider disconnect lost draft');
  assert(!acceptProviderSave(third.request_id), 'reply from lost request was accepted');
  const last = save('1');
  a.default_model = 'newer-alpha';
  assert(acceptProviderSave(last.request_id), 'last provider acknowledgement missing'); renderSettings();
  document.querySelector('[data-prov-row="1"]').click();
  assert(el('pv-key-1').value === '' && el('pv-model-1').value === 'newer-alpha', 'successful save retained its secret or lost readback');
  edit('pv-key-1', 'cancel-this-secret'); document.querySelector('[data-prov-cancel]').click();
  document.querySelector('[data-prov-row="1"]').click();
  assert(el('pv-key-1').value === '', 'explicit cancellation retained credential draft');
  assert(storageWrites === 0, 'Settings persisted a transient draft');
});
</script>`

func TestSettingsProviderDraftStaysWithItsOwnerAndSecretNeverEntersMarkup(t *testing.T) {
	runPageInEngines(t, settingsProviderDraftPage, settingsDraftModules(t))
}

const settingsSpeechDraftPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, acceptSettingsConfig } from './views/settings.js';
import { assert, run } from './__harness.js';
run(async () => {
  const el = id => document.getElementById(id);
  const choose = (id, v) => { const n = el(id); n.value = v; n.dispatchEvent(new Event('change', { bubbles: true })); };
  const edit = (id, v) => { const n = el(id); n.value = v; n.dispatchEvent(new Event('input', { bubbles: true })); return n; };
  S.identityExists = true; S.view = 'settings'; S.providersLoaded = true; S.providers = [];
  const svc = name => ({ name, added: true, has_key: true, speech: { stt: { model_required: true } } });
  S.config = { llm: {}, speech: { services: [svc('Alpha'), svc('Beta')], stt: { provider: 'Alpha', model: 'base', monthly_minutes: 100 } } };
  renderSettings(); document.querySelector('[data-sec="speech"]').click();
  choose('sp-model-stt', 'by-id:');
  edit('sp-model-stt-typed', 'typed-model');
  edit('sp-key-stt', 'alpha-only-secret');
  renderSettings();
  assert(!el('sp-model-stt-typed').hidden && el('sp-model-stt-typed').value === 'typed-model', 'typed picker mode did not survive readback');
  choose('sp-provider-stt', 'Beta');
  assert(el('sp-key-stt').value === '' && el('sp-model-stt-typed').value === '', 'another engine inherited draft model/key');
  choose('sp-model-stt', 'by-id:'); edit('sp-model-stt-typed', 'beta-model');
  document.querySelector('[data-save="speech"]').click();
  const request = frames.filter(f => f.type === 'config_set').at(-1);
  assert(request.config['speech.stt.provider'] === 'Beta', 'save did not name chosen engine');
  choose('sp-provider-stt', 'Alpha');
  S.config.speech.stt = { provider: 'Beta', model: 'beta-model', language: '', monthly_minutes: 100 };
  assert(acceptSettingsConfig(request.request_id), 'speech acknowledgement missing'); renderSettings();
  assert(el('sp-provider-stt').value === 'Alpha' && el('sp-key-stt').value === 'alpha-only-secret', 'old acknowledgement stole newer engine choice');
  assert(!el('settings-stack').innerHTML.includes('alpha-only-secret'), 'speech draft key entered markup');
  const typed = el('sp-model-stt-typed'); typed.focus();
  typed.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true }));
  typed.value = 'composing'; renderSettings();
  assert(el('sp-model-stt-typed') === typed && document.activeElement === typed, 'readback interrupted composition');
  typed.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true }));
  await Promise.resolve();
  assert(el('sp-model-stt-typed').value === 'composing', 'composition end lost draft');
});
</script>`

func TestSettingsSpeechDraftAndCompositionStayWithTheChosenEngine(t *testing.T) {
	runPageInEngines(t, settingsSpeechDraftPage, settingsDraftModules(t))
}

const settingsDraftClient = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, acceptSettingsConfig } from './views/settings.js';
S.view = 'settings'; S.providersLoaded = true; S.identityExists = true;
S.providers = [{ name: 'fixture', default: true, chat: true, models: [] }];
window.client = {
  receive(config, requestID) { S.config = config; acceptSettingsConfig(requestID); renderSettings(); },
  edit(value) { const e = document.getElementById('cfg-model'); e.value = value; e.dispatchEvent(new Event('input', { bubbles: true })); e.blur(); },
  save() { document.querySelector('[data-save="llm"]').click(); return frames.filter(f => f.type === 'config_set').at(-1); },
  value(id) { return document.getElementById(id).value; },
  get fact() { return S.config.llm.model; }
};
</script>`

const settingsTwoClientsPage = `<!doctype html>
<script type="module">
import { assert, run } from './__harness.js';
run(async () => {
  const tabs = await Promise.all(['one', 'two'].map(id => new Promise(resolve => {
    const frame = document.createElement('iframe');
    frame.id = id;
    frame.addEventListener('load', () => resolve(frame.contentWindow.client), { once: true });
    frame.srcdoc = CLIENT_DOCUMENT;
    document.body.append(frame);
  })));
  assert(tabs.every(Boolean), 'independent editors did not load');
  const [a, b] = tabs;
  const saved = { llm: { provider: 'fixture', model: 'base', timeout_seconds: 120, probe_timeout_seconds: 20 } };
  tabs.forEach(c => c.receive(structuredClone(saved)));
  a.edit('from-a'); b.edit('from-b');
  const request = a.save();
  saved.llm.model = 'from-a';
  a.receive(structuredClone(saved), request.request_id);
  b.receive(structuredClone(saved));
  assert(a.fact === 'from-a' && b.fact === 'from-a', 'clients disagree about host truth');
  assert(a.value('cfg-model') === 'from-a' && b.value('cfg-model') === 'from-b', 'one client erased the other client\'s unsaved intent');
  saved.llm.timeout_seconds = 456;
  tabs.forEach(c => c.receive(structuredClone(saved)));
  const next = b.save();
  assert(next.config['llm.model'] === 'from-b' && next.config['llm.timeout_seconds'] === 456, 'saving a draft overwrote an untouched field from stale readback');
});
</script>`

func TestSettingsClientsKeepIndependentDraftsOverTheSameReadback(t *testing.T) {
	modules := settingsDraftModules(t)
	child := strings.ReplaceAll(strconv.Quote(settingsDraftClient), "</", `<\/`)
	page := strings.Replace(settingsTwoClientsPage, "CLIENT_DOCUMENT", child, 1)
	runPageInEngines(t, page, modules)
}
