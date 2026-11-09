//go:build !windows

package dashboard

import "testing"

// modelSaveRig is the Model page with two providers, one in use, and a helper to press its Save.
const modelSaveRig = `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, acceptSettingsConfig } from './views/settings.js';
import { assert, run } from './__harness.js';
const llm = { provider: 'alpha', model: 'a1', resolved_provider: 'alpha', resolved_model: 'a1', endpoint: 'https://x.test', api_key_masked: 'none',
  timeout_seconds: 120, probe_timeout_seconds: 45, refused_retry_max_seconds: 0, refused_retry_max_default: 4321 };
const open = () => {
  S.config = { llm: { ...llm }, dashboard: { host: '127.0.0.1', port: 1, tls: false }, prompt: {}, witness: {}, logs: {}, plugins: {}, agency: {}, cognition: {}, restart_required: [] };
  S.providers = [{ name: 'alpha', chat: true, models: ['a1', 'a2'], default_model: 'a1' }, { name: 'beta', chat: true, models: ['b1'], default_model: 'b1' }];
  S.providersLoaded = true; S.view = 'settings'; S.stats = { name: 'Mira' };
  renderSettings();
};
const el = id => document.getElementById(id);
const save = () => { document.querySelector('[data-save="llm"]').click(); const sent = frames.filter(f => f.type === 'config_set'); frames.length = 0; return sent; };
const waiting = () => document.querySelector('.savesay')?.textContent || '';
const said = () => document.querySelector('[data-said="llm"]')?.textContent || '';
`

// A SAVE THAT CHANGES NEITHER THE PROVIDER NOR THE MODEL says what Save says for any other
// setting. The host checks a model only when one of those changes, so "answered" would say a
// check happened that did not.
func TestASaveOfAWaitSaysSavedAndNothingAnswered(t *testing.T) {
	page := modelSaveRig + `
run(() => {
  open();
  el('cfg-refused-retry').value = '600';
  const sent = save();
  assert(sent.length === 1 && sent[0].config['llm.refused_retry_max_seconds'] === 600, 'the wait was not sent: ' + JSON.stringify(sent[0]));
  assert(!('llm.provider' in sent[0].config) && !('llm.model' in sent[0].config), 'a wait alone sent the model pointers: ' + JSON.stringify(sent[0].config));
  renderSettings();
  assert(!waiting().includes('answers'), 'while it saves the page says: ' + waiting());
  S.config.llm.refused_retry_max_seconds = 600;
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings();
  assert(said() === 'Saved.', 'a saved wait reads as: ' + said());
});
</script>`
	runPageInEngines(t, page, settingsFieldModules(t))
}

// A PROVIDER CHOSEN WITH ITS DEFAULT MODEL names that model: its own default while the host
// checks it, and the one the host resolved when it has answered. Never an empty name.
func TestAProviderKeptOnItsDefaultModelIsNamed(t *testing.T) {
	page := modelSaveRig + `
run(() => {
  open();
  el('cfg-provider').value = 'beta';
  el('cfg-model').value = '';
  const sent = save();
  assert(sent.length === 1 && sent[0].config['llm.provider'] === 'beta' && sent[0].config['llm.model'] === '', 'the pointers were not sent: ' + JSON.stringify(sent[0]));
  renderSettings();
  assert(waiting().startsWith('Checking that b1 answers.'), 'while it checks the page says: ' + waiting());
  S.config.llm = { ...S.config.llm, provider: 'beta', model: '', resolved_provider: 'beta', resolved_model: 'b1-latest', answered: true };
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings();
  assert(said().startsWith('Active. b1-latest answered'), 'the answer reads as: ' + said());
});
</script>`
	runPageInEngines(t, page, settingsFieldModules(t))
}

// A MODEL ASKED FOR BY NAME is still checked and named.
func TestAModelAskedForByNameIsChecked(t *testing.T) {
	page := modelSaveRig + `
run(() => {
  open();
  el('cfg-model').value = 'a2';
  const sent = save();
  assert(sent.length === 1 && sent[0].config['llm.model'] === 'a2' && sent[0].config['llm.provider'] === 'alpha', 'the pointers were not sent: ' + JSON.stringify(sent[0]));
  renderSettings();
  assert(waiting().startsWith('Checking that a2 answers.'), 'while it checks the page says: ' + waiting());
  S.config.llm = { ...S.config.llm, model: 'a2', resolved_model: 'a2', answered: true };
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings();
  assert(said().startsWith('Active. a2 answered'), 'the answer reads as: ' + said());
});
</script>`
	runPageInEngines(t, page, settingsFieldModules(t))
}

// "SIGNED IN" ONLY WHEN IT IS. The row's first words use the check its detail and its button use:
// a credential past its published expiry, before the next probe, or one whose details could not
// be read, is not signed in, whatever the last probe said.
func TestARowSaysSignedInOnlyWhenItIs(t *testing.T) {
	page := modelSaveRig + `
run(() => {
  open();
  const row = (name, credential_info, status) => ({ name, chat: true, credential: 'claude-code', models: ['m'], default_model: 'm', status, credential_info });
  const soon = new Date(Date.now() + 3600000).toISOString(), gone = new Date(Date.now() - 3600000).toISOString();
  S.providers = [
    row('valid', { kind: 'subscription', plan: 'Max', expires_at: soon }, 'ok'),
    row('past', { kind: 'subscription', plan: 'Max', expires_at: gone }, 'ok'),
    row('flagged', { kind: 'subscription', expired: true }, 'ok'),
    row('unread', { error: 'the file could not be read' }, 'ok'),
    row('refused', { kind: 'subscription', expires_at: soon }, 'no_credential'),
  ];
  renderSettings();
  S.settingsSection('providers'); S.requestSettingsSection(); renderSettings();
  const rowText = name => document.querySelector('[data-prov-row="' + S.providers.findIndex(p => p.name === name) + '"]')?.textContent || '';
  assert(rowText('valid').includes('Signed in'), 'a valid credential does not say so: ' + rowText('valid'));
  for (const name of ['past', 'flagged', 'unread', 'refused']) assert(rowText(name) && !rowText(name).includes('Signed in'), name + ' says it is signed in: ' + rowText(name));
});
</script>`
	runPageInEngines(t, page, settingsFieldModules(t))
}

// REFRESH DOES NOT SIT INSIDE A SENTENCE. The field's hint is whole, then the button, then what
// the button does as its own hint. The field stays described by its own sentence alone.
func TestRefreshComesAfterTheFieldsWholeSentence(t *testing.T) {
	page := modelSaveRig + `
run(() => {
  open();
  const hint = el('cfg-model-hint'), press = document.querySelector('[data-refresh-models]'), says = el('cfg-model-refresh-hint');
  assert(hint.textContent === "Type to search, or paste a model's id.", 'the hint reads: ' + hint.textContent);
  assert(!hint.contains(press) && !hint.contains(says), 'the button or what it does sits inside the hint');
  assert(hint.compareDocumentPosition(press) & Node.DOCUMENT_POSITION_FOLLOWING, 'the button does not come after the sentence');
  assert(press.compareDocumentPosition(says) & Node.DOCUMENT_POSITION_FOLLOWING, 'what the button does does not come after it');
  assert(press.parentElement === says.parentElement && says.textContent === 'Asks the provider for its list now.', 'the button is not beside its own hint: ' + says.textContent);
  assert(el('cfg-model').parentNode.contains(press), 'the field no longer finds Refresh beside it');
});
</script>`
	runPageInEngines(t, page, settingsFieldModules(t))
}

// "ANSWERED" ONLY WHEN THE HOST SAYS IT ASKED. A save can send a model the host already holds, from
// a screen that had not heard of it: the host asks nothing, and its reply says so. The page reads
// that reply, not the keys it sent.
func TestAnAnswerIsSaidOnlyWhenTheHostSaysItAsked(t *testing.T) {
	page := modelSaveRig + `
run(() => {
  open();
  el('cfg-model').value = 'a2';
  const sent = save();
  assert(sent.length === 1 && sent[0].config['llm.model'] === 'a2', 'the model was not sent: ' + JSON.stringify(sent[0]));
  S.config.llm = { ...S.config.llm, model: 'a2', resolved_model: 'a2' };
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings();
  assert(said() === 'Saved.', 'a save the host checked nothing for reads as: ' + said());
});
</script>`
	runPageInEngines(t, page, settingsFieldModules(t))
}

// A WAIT NEVER MOVES THE PROVIDER. A configured provider the list does not hold, while the list
// loads or after its entry is gone, is still the one configured: the menu shows it, and a save of
// anything else sends no pointer. A model asked for beside it keeps it. Only a person's own choice
// of the blank sends the blank.
func TestAWaitNeverMovesAProviderTheListDoesNotHold(t *testing.T) {
	page := modelSaveRig + `
run(() => {
  const ack = sent => { for (const [k, v] of Object.entries(sent[0].config)) if (k.startsWith('llm.')) S.config.llm[k.slice(4)] = v; assert(acceptSettingsConfig('req-1'), 'the save is claimed'); renderSettings(); };
  const beta = { name: 'beta', chat: true, models: ['b1'], default_model: 'b1', default: true };
  for (const [what, providers, loaded] of [['the list still loading', [], false], ['its entry gone', [beta], true]]) {
    open();
    S.providers = providers; S.providersLoaded = loaded;
    renderSettings();
    assert(el('cfg-provider').value === 'alpha', what + ': the menu shows ' + JSON.stringify(el('cfg-provider').value));
    el('cfg-refused-retry').value = '600';
    const sent = save();
    assert(sent.length === 1 && sent[0].config['llm.refused_retry_max_seconds'] === 600, what + ': the wait was not sent: ' + JSON.stringify(sent[0]));
    assert(!('llm.provider' in sent[0].config) && !('llm.model' in sent[0].config), what + ': a wait sent the pointers: ' + JSON.stringify(sent[0].config));
    ack(sent);
  }
  // A model asked for beside a provider the list does not hold keeps that provider.
  open();
  S.providers = [beta];
  renderSettings();
  el('cfg-model').value = 'x1';
  let sent = save();
  assert(sent.length === 1 && sent[0].config['llm.provider'] === 'alpha' && sent[0].config['llm.model'] === 'x1', 'a model beside it sent: ' + JSON.stringify(sent[0] && sent[0].config));
  ack(sent);
  // A person's own choice of the blank sends it.
  open();
  el('cfg-provider').value = '';
  sent = save();
  assert(sent.length === 1 && sent[0].config['llm.provider'] === '', 'choosing the blank sent: ' + JSON.stringify(sent[0] && sent[0].config));
  ack(sent);
  // A blank already configured, saved with a wait, stays unsent.
  open();
  S.config.llm.provider = '';
  renderSettings();
  el('cfg-refused-retry').value = '600';
  sent = save();
  assert(sent.length === 1 && !('llm.provider' in sent[0].config) && !('llm.model' in sent[0].config), 'a wait under the blank sent: ' + JSON.stringify(sent[0] && sent[0].config));
});
</script>`
	runPageInEngines(t, page, settingsFieldModules(t))
}
