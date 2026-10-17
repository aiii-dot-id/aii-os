//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestTheCognitionCardShowsAndEditsTheRhythmsSettings(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, rejectSettingsConfig, acceptSettingsConfig } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  const settings = [
    { key: 'agency.rhythm_seconds', min: 1, max: 86400, default: 600, unit: 'seconds' },
    { key: 'cognition.consolidate_spacing_seconds', min: 2, max: 259200, default: 1800, unit: 'seconds', zero_is_default: true },
    { key: 'cognition.consolidate_batch', min: 1, max: 200, default: 30, unit: 'experiences', zero_is_default: true },
    { key: 'cognition.consolidate_max_ops', min: 2, max: 512, default: 32, unit: 'operations', zero_is_default: true },
    { key: 'prompt.ring3_max_chars', min: 600, max: 10000000, default: 8000, unit: 'characters', zero_is_default: true },
    { key: 'prompt.surfacing_max_chars', min: 600, max: 10000000, default: 2400, unit: 'characters', zero_is_default: true,
      held: 'prompt.surfacing_max_chars 24 is under the least it may be (600 characters): 600 is in force, and the file is left as it is' },
    { key: 'agency.turn_checkpoint_calls', min: 1, max: 1000000000, default: 32, unit: 'tool calls', zero_is_default: true },
    { key: 'prompt.alarm_firings', min: 1, max: 100000, default: 20, unit: 'firings', zero_is_default: true },
  ];
  S.config = { llm: { provider: 'p', model: 'm', endpoint: 'https://x.test', api_key_masked: 'none', context_length: 200000, max_output_tokens: 8192 },
    dashboard: { host: '127.0.0.1', port: 1, tls: false }, prompt: { ring3_max_chars: 9000, surfacing_max_chars: 600 }, witness: {}, logs: {}, plugins: {},
    agency: { prefer_local_for_roles: false, rhythm_seconds: 600, outcome_window: 512, turn_token_budget: 600000 },
    cognition: { consolidate_batch: 25, morning_brief_at: '06:45', morning_brief_default: '07:00', pulse_seconds: 300,
      input_budget: 189760, input_budget_source: 'derived', settings,
      conditions: [{ facility: 'consolidate', kind: 'fitted', said: 'the last pass fitted the model’s input budget: 42 of 601 current beliefs were shown', at: '2026-10-11T09:00:00Z' }] },
    restart_required: [] };
  S.providers = []; S.providersLoaded = true; S.view = 'settings';
  renderSettings();
  const nav = document.querySelector('[data-sec="identity"]');
  assert(nav && nav.textContent === 'Identity', 'Settings has an Identity section');
  nav.click();
  const card = document.querySelector('[data-cognition]');
  assert(card && card.querySelector('h3').textContent === 'COGNITION', 'the section carries the Cognition card');
  const field = key => document.querySelector('[data-cognition] [data-cog-key="' + key + '"]');
  for (const s of settings) {
    const el = field(s.key);
    assert(el, 'the card draws ' + s.key);
    assert(el.min === String(s.min) && el.max === String(s.max), s.key + ' carries the host’s bounds: ' + el.min + '..' + el.max);
    assert(el.placeholder.indexOf(String(s.default)) === 0, s.key + ' shows its default where none is set: ' + el.placeholder);
    assert(card.querySelector('label[for="' + el.id + '"]').textContent.indexOf(s.min + ' to ' + s.max) > 0, s.key + '’s label states its bounds');
  }
  assert(field('cognition.consolidate_batch').value === '25', 'the configured batch is shown: ' + field('cognition.consolidate_batch').value);
  assert(field('prompt.ring3_max_chars').value === '9000', 'the configured working-truth bound is shown');
  const held = card.querySelectorAll('[data-cog-held]');
  assert(held.length === 1 && held[0].dataset.cogHeld === 'prompt.surfacing_max_chars' && /24 is under the least it may be/.test(held[0].textContent), 'the one setting the host held is said under it: ' + held.length);
  assert(field('prompt.surfacing_max_chars').value === '600', 'and its field shows the value in force: ' + field('prompt.surfacing_max_chars').value);
  assert(field('cognition.consolidate_spacing_seconds').value === '' && /three ticks/.test(field('cognition.consolidate_spacing_seconds').placeholder), 'an unset spacing is blank and says it follows the cadence');
  assert(document.getElementById('cog-brief-at').value === '06:45' && !document.getElementById('cog-brief-off').checked, 'the brief’s hour is shown');
  assert(/189760 tokens \(derived from the model/.test(card.querySelector('[data-cog-budget]').textContent), 'the rhythm’s input budget and its source are said: ' + card.querySelector('[data-cog-budget]').textContent);
  const applies = key => card.querySelector('[data-cog-applies="' + key + '"]').textContent;
  assert(/the rhythm reads it at its next pass\.$/i.test(applies('cognition.consolidate_batch')), 'a rhythm setting applies at the next pass: ' + applies('cognition.consolidate_batch'));
  assert(/Applies from the next turn\.$/.test(applies('agency.turn_checkpoint_calls')), 'the turn checkpoint applies from the next turn: ' + applies('agency.turn_checkpoint_calls'));
  assert(/Applies at the next read of the alarms\.$/.test(applies('prompt.alarm_firings')), 'the alarm firings apply at the next read: ' + applies('prompt.alarm_firings'));
  assert(!/Each applies at the rhythm/.test(card.textContent), 'the card no longer says every number applies at the rhythm\u2019s next pass');
  const said = card.querySelector('[data-cog-condition="consolidate"]');
  assert(said && /Consolidation/.test(said.textContent) && /42 of 601/.test(said.textContent), 'what the last pass came to is on the card');

  const batch = field('cognition.consolidate_batch'), spacing = field('cognition.consolidate_spacing_seconds');
  batch.value = '12'; batch.dispatchEvent(new Event('input', { bubbles: true }));
  spacing.value = '2400'; spacing.dispatchEvent(new Event('input', { bubbles: true }));
  const off = document.getElementById('cog-brief-off');
  off.click();
  assert(document.getElementById('cog-brief-at').disabled, 'no brief disables its hour');
  card.querySelector('[data-save="cognition"]').click();
  let sent = frames.filter(f => f.type === 'config_set');
  assert(sent.length === 1, 'the save sent ' + sent.length + ' config frame(s)');
  const got = sent[0].config;
  assert(Object.keys(got).sort().join(',') === 'cognition.consolidate_batch,cognition.consolidate_spacing_seconds,cognition.morning_brief_at', 'only what changed is sent: ' + JSON.stringify(got));
  assert(got['cognition.consolidate_batch'] === 12 && got['cognition.consolidate_spacing_seconds'] === 2400 && got['cognition.morning_brief_at'] === 'off', 'as numbers and the word: ' + JSON.stringify(got));

  assert(rejectSettingsConfig('cognition.consolidate_batch: 500 is out of bounds — between 1 and 200 experiences, or 0 for the default (30)', 'req-1'), 'the refusal is claimed by the card that asked');
  const answer = document.querySelector('[data-said="cognition"]');
  assert(answer && /between 1 and 200/.test(answer.textContent) && answer.closest('[data-cognition]'), 'the refusal and its bounds stand beside the card’s Save: ' + (answer && answer.textContent));
  assert(field('cognition.consolidate_batch').value === '12', 'a refused edit is kept for correction');

  frames.length = 0;
  const b2 = field('cognition.consolidate_batch');
  b2.value = ''; b2.dispatchEvent(new Event('input', { bubbles: true }));
  field('cognition.consolidate_spacing_seconds').value = '';
  document.getElementById('cog-brief-off').click();
  document.querySelector('[data-cognition] [data-save="cognition"]').click();
  sent = frames.filter(f => f.type === 'config_set');
  assert(sent.length === 1 && Object.keys(sent[0].config).join(',') === 'cognition.consolidate_batch' && sent[0].config['cognition.consolidate_batch'] === null, 'a cleared batch sends null and nothing else: ' + JSON.stringify(sent[0] && sent[0].config));
  delete S.config.cognition.consolidate_batch;
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings();
  const ok = document.querySelector('[data-said="cognition"]');
  assert(ok && /next pass/.test(ok.textContent), 'the accepted save says when it applies: ' + (ok && ok.textContent));

  frames.length = 0;
  const calls = field('agency.turn_checkpoint_calls');
  calls.value = '500'; calls.dispatchEvent(new Event('input', { bubbles: true }));
  document.querySelector('[data-cognition] [data-save="cognition"]').click();
  sent = frames.filter(f => f.type === 'config_set');
  assert(sent.length === 1 && sent[0].config['agency.turn_checkpoint_calls'] === 500, 'the turn checkpoint is sent: ' + JSON.stringify(sent[0] && sent[0].config));
  S.config.agency.turn_checkpoint_calls = 500;
  assert(acceptSettingsConfig('req-1'), 'the second save is claimed');
  renderSettings();
  const turnOK = document.querySelector('[data-said="cognition"]');
  assert(turnOK && turnOK.textContent === 'Saved — applies from the next turn.', 'the accepted turn setting says when it applies: ' + (turnOK && turnOK.textContent));
});
</script>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { providers: [], config: null, providersLoaded: false };`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? ''); export const hueOf = () => 0; export const copyText = async () => true;`)
	modules["/ws.js"] = []byte(`export const frames = [];
export function send(f) { frames.push(f); return 'req-1'; }
export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, page, modules)
}
