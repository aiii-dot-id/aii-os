//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// REFLECTION SHOWS THE RHYTHM'S SETTINGS AND EDITS THEM.
// The rhythm's numbers were compiled in, or keys in config.json that no
// page could set. Technical → Reflection now carries them: every number
// the host lists, with its bounds and its default as the host holds them
// (the card keeps no copy), the configured value where one is set and the
// default shown where none is; the rhythm's input budget and where it came
// from; what the last passes came to; and, where config.json states a
// value outside a setting's bounds, the host's sentence under that setting
// beside the value in force. A Save sends only what was changed — a blank
// field sends null, the default again — and a refusal from the door, which
// names the bounds, stands beside the Save.
func TestTheCognitionCardShowsAndEditsTheRhythmsSettings(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div><div id="technical-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { rejectSettingsConfig, acceptSettingsConfig } from './views/settings.js';
import { drawReflection } from './views/settings/technical/reflection.js';
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
    { key: 'prompt.alarm_firings', min: 1, max: 100000, default: 20, unit: 'firings', zero_is_default: true },
  ];
  S.config = { llm: { provider: 'p', model: 'm', endpoint: 'https://x.test', api_key_masked: 'none', context_length: 200000, max_output_tokens: 8192 },
    dashboard: { host: '127.0.0.1', port: 1, tls: false }, prompt: { ring3_max_chars: 9000, surfacing_max_chars: 600 }, witness: {}, logs: {}, plugins: {},
    agency: { rhythm_seconds: 600, outcome_window: 512, turn_token_budget: 600000 },
    cognition: { consolidate_batch: 25, morning_brief_at: '06:45', morning_brief_default: '07:00', pulse_seconds: 300,
      input_budget: 189760, input_budget_source: 'derived', settings,
      conditions: [{ facility: 'consolidate', kind: 'fitted', said: 'the last pass fitted the model’s input budget: 42 of 601 current beliefs were shown', at: '2026-10-11T09:00:00Z' }] },
    restart_required: [] };
  S.providers = []; S.providersLoaded = true; S.view = 'technical';
  const st = document.getElementById('technical-stack');
  S.renderTechnical = () => drawReflection(st);
  S.renderTechnical();
  const card = st;
  assert(card.querySelector('[data-technical-form="cognition"]'), 'the panel carries the form');
  // Re-found each time: a save redraws the form, and the page keeps an
  // edited control by moving it into the new one.
  const field = key => st.querySelector('[data-cog-key="' + key + '"]');
  for (const s of settings) {
    const el = field(s.key);
    assert(el, 'the card draws ' + s.key);
    // A value minutes long reads in minutes; the field holds the reading.
    const per = s.unit === 'seconds' && s.default >= 300 ? 60 : 1, shown = v => +(v / per).toFixed(3);
    assert(el.min === String(shown(s.min)) && el.max === String(shown(s.max)), s.key + ' carries the host’s bounds: ' + el.min + '..' + el.max);
    assert(el.placeholder === 'Usually ' + shown(s.default).toLocaleString(), s.key + ' shows its usual value where none is set: ' + el.placeholder);
    assert(!el.closest('.technical-form__field').textContent.includes(' to ' + s.max.toLocaleString()), s.key + ' says its bounds before they are needed');
  }
  assert(!field('cognition.consolidate_max_ops').closest('.technical-form__field').querySelector('.technical-form__unit'), 'a label that says its unit says it twice');
  assert(field('cognition.consolidate_spacing_seconds').closest('.technical-form__field').querySelector('.technical-form__unit').textContent === 'minutes', 'the spacing has no unit');
  assert(field('cognition.consolidate_batch').value === '25', 'the configured batch is shown: ' + field('cognition.consolidate_batch').value);
  assert(field('prompt.ring3_max_chars').value === '9000', 'the configured working-truth bound is shown');
  const held = card.querySelectorAll('[data-cog-held]');
  assert(!card.querySelector('[aria-invalid="true"]') && [...card.querySelectorAll('[data-range-said]')].every(p => p.hidden), 'a value the file states is judged as if it had been typed');
  assert(held.length === 1 && held[0].dataset.cogHeld === 'prompt.surfacing_max_chars' && /24 is under the least it may be/.test(held[0].textContent), 'the one setting the host held is said under it: ' + held.length);
  assert(field('prompt.surfacing_max_chars').value === '600', 'and its field shows the value in force: ' + field('prompt.surfacing_max_chars').value);
  assert(field('cognition.consolidate_spacing_seconds').value === '', 'an unset spacing is blank');
  assert(document.getElementById('cog-brief-at').value === '06:45' && !document.getElementById('cog-brief-off').checked, 'the brief’s hour is shown');
  assert(/189,760 tokens$/.test(card.querySelector('[data-cog-budget]').textContent), 'the rhythm’s input budget and its source are said: ' + card.querySelector('[data-cog-budget]').textContent);
  // Each field says when it applies: the rhythm's numbers at its next
  // pass, the turn's at the next turn, the alarms' at their next read —
  // never all at the rhythm's next pass.
  const applies = key => card.querySelector('[data-cog-applies="' + key + '"]').textContent;
  assert(!/Applies/.test(applies('cognition.consolidate_batch')) && card.textContent.includes('Applies at the next pass, unless a setting says otherwise.'), 'a rhythm setting applies at the next pass, said once: ' + applies('cognition.consolidate_batch'));
  assert(/Applies at the next read of the alarms\.$/.test(applies('prompt.alarm_firings')), 'the alarm firings apply at the next read: ' + applies('prompt.alarm_firings'));
  const said = card.querySelector('[data-cog-condition="consolidate"]');
  assert(said && /Consolidation/.test(said.textContent) && /42 of 601/.test(said.textContent), 'what the last pass came to is on the card');

  const batch = field('cognition.consolidate_batch'), spacing = field('cognition.consolidate_spacing_seconds');
  batch.value = '12'; batch.dispatchEvent(new Event('input', { bubbles: true }));
  spacing.value = '40'; spacing.dispatchEvent(new Event('input', { bubbles: true }));
  const off = document.getElementById('cog-brief-off');
  off.click();
  assert(document.getElementById('cog-brief-at').disabled, 'no brief disables its hour');
  document.querySelector('[data-save="technical"]').click();
  let sent = frames.filter(f => f.type === 'config_set');
  assert(sent.length === 1, 'the save sent ' + sent.length + ' config frame(s)');
  const got = sent[0].config;
  assert(Object.keys(got).sort().join(',') === 'cognition.consolidate_batch,cognition.consolidate_spacing_seconds,cognition.morning_brief_at', 'only what changed is sent: ' + JSON.stringify(got));
  assert(got['cognition.consolidate_batch'] === 12 && got['cognition.consolidate_spacing_seconds'] === 2400 && got['cognition.morning_brief_at'] === 'off', 'as numbers and the word: ' + JSON.stringify(got));

  assert(rejectSettingsConfig('cognition.consolidate_batch: 500 is out of bounds — between 1 and 200 experiences, or 0 for the default (30)', 'req-1'), 'the refusal is claimed by the card that asked');
  const answer = document.querySelector('[data-said="technical"]');
  assert(answer && /between 1 and 200/.test(answer.textContent), 'the refusal and its bounds stand beside the Save: ' + (answer && answer.textContent));
  assert(field('cognition.consolidate_batch').value === '12', 'a refused edit is kept for correction');

  frames.length = 0;
  const b2 = field('cognition.consolidate_batch');
  b2.value = ''; b2.dispatchEvent(new Event('input', { bubbles: true }));
  field('cognition.consolidate_spacing_seconds').value = '';
  document.getElementById('cog-brief-off').click();
  document.querySelector('[data-save="technical"]').click();
  sent = frames.filter(f => f.type === 'config_set');
  assert(sent.length === 1 && Object.keys(sent[0].config).join(',') === 'cognition.consolidate_batch' && sent[0].config['cognition.consolidate_batch'] === null, 'a cleared batch sends null and nothing else: ' + JSON.stringify(sent[0] && sent[0].config));
  delete S.config.cognition.consolidate_batch;
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  S.renderTechnical();
  const ok = document.querySelector('[data-said="technical"]');
  assert(ok && /next pass/.test(ok.textContent), 'the accepted save says when it applies: ' + (ok && ok.textContent));
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
