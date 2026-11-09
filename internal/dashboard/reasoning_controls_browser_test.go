//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const reasoningControlsPage = `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';

// The reading is the resolved line's own (host 2021): the rest of the
// page, More options' summary among it, may rightly say "thinking".
function render(config, providers) {
  S.config = config;
  S.providers = providers || [];
  S.providersLoaded = true;
  renderSettings();
  const line = document.querySelector('[data-resolved]');
  assert(line, 'the resolved line is not drawn');
  return line.textContent;
}

const base = {
  llm: { provider: 'p', model: 'm', endpoint: 'https://x.test', api_key_masked: 'none',
         context_length: 1000, max_output_tokens: 100 },
  dashboard: {}, plugins: {}, witness: {}, prompt: {}, agency: {}, genesis: {}, tools: {},
};

run(() => {
  let text = render(Object.assign({}, base, {
    llm: Object.assign({}, base.llm, {
      thinking_applies: false, thinking_budget: 0,
      effort_plan: 'high → reasoning.effort',
    }),
  }));
  // The reading is ' · thinking <budget>' (host 1952).
  assert(text.indexOf(' · thinking') === -1,
    'a dialect with no thinking parameter must not print a thinking reading: ' + text);
  assert(text.indexOf('high → reasoning.effort') !== -1,
    'the panel must report where the effort actually goes: ' + text);

  // LEVELS WITH NO THINKING PARAMETER (host 2021): the menu is drawn, so
  // More options' summary rightly says "thinking"; the line still prints
  // no thinking reading. effortChoice() reads only for an identity that
  // exists, so this case alone sets it.
  const hadIdentity = S.identityExists;
  S.identityExists = true;
  text = render(Object.assign({}, base, {
    llm: Object.assign({}, base.llm, {
      thinking_applies: false, thinking_budget: 0,
      effort_plan: 'high → reasoning.effort',
      effort_choice: { levels: ['low', 'high'], in_force: 'low' },
    }),
  }));
  const summary = document.querySelector('[data-more] summary');
  assert(summary && summary.textContent.indexOf('thinking, longest wait') !== -1,
    'with levels More options does not name the thinking menu: ' + (summary ? summary.textContent : 'no summary'));
  assert(text.indexOf(' · thinking') === -1,
    'with levels and no thinking parameter the line printed a thinking reading: ' + text);
  S.identityExists = hadIdentity;

  // WHERE THINKING DOES APPLY it is shown.
  text = render(Object.assign({}, base, {
    llm: Object.assign({}, base.llm, {
      thinking_applies: true, thinking_budget: 8000,
      effort_plan: 'max → output_config.effort',
    }),
  }));
  assert(text.indexOf('thinking 8000') !== -1, 'anthropic dialect must show its thinking budget: ' + text);

  text = render(Object.assign({}, base, {
    llm: Object.assign({}, base.llm, {
      thinking_applies: false,
      effort_plan: 'max — NOT SENT: this provider accepts minimal, low, medium or high',
    }),
  }));
  assert(text.indexOf('NOT SENT') !== -1,
    'a level the provider does not accept must read as NOT SENT, never as in force: ' + text);
});
</script>`

func TestTheSubstratePanelReportsEffectNotIntent(t *testing.T) {
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
	modules["/pending.js"] = []byte(`export function pendingSlot() { return { arm(){}, claim(){ return false; }, drop(){}, waiting(){ return false; } }; } export function anyPending() { return false; }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, reasoningControlsPage, modules)
}
