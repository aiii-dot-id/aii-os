//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// limitsCardPage draws Settings → Agency over a read-back that carries the
// per-model limits and runs the
// script in check. __CSS__ is the page's stylesheet, for the width check.
const limitsCardPage = `<!doctype html>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>__CSS__</style>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, acceptSettingsConfig } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  const settings = [
    { key: 'agency.max_tool_calls', min: 1, max: 1000000000, default: 0, unit: 'tool calls', zero_is_default: true, card: 'agency', per_model: 'model', unlimited: true, default_unlimited: true,
      effective: { value: 'unlimited', level: 'provider', provider: 'Local server' },
      overrides: [{ provider: 'Local server', value: 'unlimited' }] },
    { key: 'agency.max_tool_rounds', min: 1, max: 1000000, default: 30, unit: 'rounds', card: 'agency', per_model: 'model', unlimited: true, value: 30,
      effective: { value: 200, level: 'model', provider: 'Local server', model: 'big' },
      overrides: [{ provider: 'Local server', model: 'big', value: 200 }, { provider: 'Gone', value: 5 }] },
    { key: 'agency.subagent_wall_seconds_local', min: 1, max: 604800, default: 1800, unit: 'seconds', card: 'agency', effective: { value: 1800, level: 'default' } },
  ];
  S.config = { llm: { provider: 'Local server', model: 'big' }, dashboard: { host: '127.0.0.1', port: 1, tls: false }, prompt: {}, witness: {}, logs: {}, plugins: {},
    agency: { prefer_local_for_roles: false, max_tool_rounds: 30 },
    cognition: { settings, conditions: [], overrides_digest: 'd1', overrides_unused: ['overrides.Gone'],
      overrides_block: { 'Local server': { agency: { max_tool_calls: 'unlimited' }, models: { big: { agency: { max_tool_rounds: 200 } } } }, Gone: { agency: { max_tool_rounds: 5 } } } },
    restart_required: [] };
  S.providers = [{ name: 'Local server', models: ['big', 'small'], chat: true }, { name: 'Anthropic', models: ['claude-opus-5'], chat: true }];
  S.providersLoaded = true; S.view = 'settings';
  renderSettings();
  document.querySelector('[data-sec="agency"]').click();
  const st = document.getElementById('settings-stack');
  const row = key => st.querySelector('[data-lim-row="' + key + '"]');

  // Each limit once, on its card, with what is in force and where from.
  assert(st.querySelector('[data-cog-key="agency.max_tool_calls"]'), 'the call limit is drawn on the Agency card');
  const eff = st.querySelector('[data-lim-effective="agency.max_tool_rounds"]').textContent;
  assert(eff === 'In force for Local server · big: 200, from the model override', 'the rounds in force: ' + eff);
  assert(/no limit, from the provider override/.test(st.querySelector('[data-lim-effective="agency.max_tool_calls"]').textContent), 'a lifted limit reads as no limit');
  assert(st.querySelector('[data-cog-key="agency.max_tool_calls"]').placeholder.startsWith('no limit'), 'the call limit’s default is no limit');
  assert(!row('agency.subagent_wall_seconds_local'), 'a global key offers no override');
  // The overrides, each with Remove; an unused one is said.
  const items = [...row('agency.max_tool_rounds').querySelectorAll('[data-lim-override]')].map(li => li.textContent);
  assert(items.length === 2 && /Gone: 5/.test(items[0]) && /unused/.test(items[0]) && /Local server · big: 200/.test(items[1]), 'the overrides of the rounds: ' + JSON.stringify(items));
  // The Unlimited box, and beside it what still bounds a turn, verbatim.
  const bounds = st.querySelector('[data-lim-bounds]').textContent;
  assert(bounds === 'Unlimited lifts this fence only. A turn is still fitted to the model’s window, ends at the continued turn’s wall (agency.continuation_wall_seconds) and the request’s silence ceiling (llm.timeout_seconds), and stops when you press Stop. A turn with no fence once spent 1,945,593 tokens over 101 calls and ran to the context wall.', 'the line beside the box: ' + bounds);

  // Add one for a model, remove the unused one, lift the main rounds: one save
  // carries the main value and the whole block with its digest.
  const slug = 'agency-max-tool-rounds';
  document.getElementById('lim-add-p-' + slug).value = 'Anthropic';
  document.getElementById('lim-add-m-' + slug).value = 'claude-opus-5';
  document.getElementById('lim-add-v-' + slug).value = '12';
  st.querySelector('[data-lim-add="agency.max_tool_rounds"]').click();
  assert(st.querySelector('[data-lim-drafted]'), 'an edited block says it is not saved yet');
  [...st.querySelectorAll('[data-lim-remove="agency.max_tool_rounds"]')].find(b => b.dataset.limProvider === 'Gone').click();
  const box = st.querySelector('[data-cog-unl="agency.max_tool_rounds"]');
  box.checked = true; box.dispatchEvent(new Event('change', { bubbles: true }));
  assert(st.querySelector('[data-cog-key="agency.max_tool_rounds"]').disabled, 'a lifted main value has no number to type');
  st.querySelector('[data-save="agency"]').click();
  const sent = frames.filter(f => f.type === 'config_set').at(-1).config;
  assert(sent['agency.max_tool_rounds'] === 'unlimited', 'the main rounds are sent lifted: ' + JSON.stringify(sent));
  assert(sent.overrides_digest === 'd1', 'the block is sent with the digest it was read with');
  const want = { 'Local server': { agency: { max_tool_calls: 'unlimited' }, models: { big: { agency: { max_tool_rounds: 200 } } } }, Anthropic: { models: { 'claude-opus-5': { agency: { max_tool_rounds: 12 } } } } };
  assert(JSON.stringify(sent.overrides) === JSON.stringify(want), 'the block as edited: ' + JSON.stringify(sent.overrides));
  // Read back as saved: the draft is the host's again.
  S.config.agency.max_tool_rounds = 'unlimited';
  S.config.cognition.overrides_block = want; S.config.cognition.overrides_digest = 'd2';
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings();
  assert(!st.querySelector('[data-lim-drafted]'), 'a saved block is still called a draft');
  assert(/Saved/.test(st.querySelector('[data-said="agency"]').textContent), 'the save says it was saved: ' + st.querySelector('[data-said="agency"]').textContent);

  // At phone width the card does not scroll sideways.
  assert(document.documentElement.scrollWidth <= window.innerWidth + 1, 'the card is wider than the screen: ' + document.documentElement.scrollWidth + ' > ' + window.innerWidth);
});
</script>`

func limitsCardModules(t *testing.T) map[string][]byte {
	t.Helper()
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
	return modules
}

func limitsCardPageWithCSS(t *testing.T) string {
	t.Helper()
	css, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Replace(limitsCardPage, "__CSS__", string(css), 1)
}

// THE CARD SHOWS WHERE EACH LIMIT CAME FROM, AND EDITS THE OVERRIDES. One
// renderer draws each per-model limit on the card where it lives: what is
// in force for the identity's own provider and model and where from (the
// model override, the provider override, the main setting, the default),
// the overrides config.json holds for it with Remove, an unused one said,
// an Unlimited box with the line the operator approved beside it, and a
// control to add one for a provider or one of its models. A Save sends the
// main values it changed and the whole block with its digest; read back as
// sent, the block is the host's again.
func TestTheCardShowsWhereEachLimitCameFrom(t *testing.T) {
	runPageInEngines(t, limitsCardPageWithCSS(t), limitsCardModules(t))
}

// THE LIMITS IN A REAL BROWSER, AT PHONE WIDTH: the same card at 375 pixels
// wide, where the add control wraps and nothing scrolls sideways.
func TestTheLimitsInARealBrowser(t *testing.T) {
	runPageInEnginesAtSize(t, limitsCardPageWithCSS(t), limitsCardModules(t), 375, 812)
}
