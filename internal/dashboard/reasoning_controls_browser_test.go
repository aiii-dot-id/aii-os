//go:build !windows

// .
// .

package dashboard

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
const reasoningControlsPage = `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';

function render(config, providers) {
  S.config = config;
  S.providers = providers || [];
  S.providersLoaded = true;
  renderSettings();
  return document.getElementById('settings-stack').textContent;
}

const base = {
  llm: { provider: 'p', model: 'm', endpoint: 'https://x.test', api_key_masked: 'none',
         context_length: 1000, max_output_tokens: 100 },
  dashboard: {}, plugins: {}, witness: {}, prompt: {}, agency: {}, genesis: {}, tools: {},
};

run(() => {
  // A DIALECT WITH NO THINKING PARAMETER must not print a thinking
  // reading. "thinking —" reads as "not configured", not "not
  // applicable", for a concept that does not exist there.
  let text = render(Object.assign({}, base, {
    llm: Object.assign({}, base.llm, {
      thinking_applies: false, thinking_budget: 0,
      effort_plan: 'high → reasoning.effort',
    }),
  }));
  assert(text.indexOf('thinking') === -1,
    'a dialect with no thinking parameter must not print a thinking reading: ' + text);
  assert(text.indexOf('high → reasoning.effort') !== -1,
    'the panel must report where the effort actually goes: ' + text);

  // WHERE THINKING DOES APPLY it is shown.
  text = render(Object.assign({}, base, {
    llm: Object.assign({}, base.llm, {
      thinking_applies: true, thinking_budget: 8000,
      effort_plan: 'max → output_config.effort',
    }),
  }));
  assert(text.indexOf('thinking 8000') !== -1, 'anthropic dialect must show its thinking budget: ' + text);

  // A REFUSED LEVEL READS AS REFUSED. This is the reported bug: the
  // panel used to print the configured value alone, which reads as in
  // force whether or not anything was sent.
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
	// .
	// .
	// .
	// .
	// .
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { providers: [], config: null, providersLoaded: false };`)
	modules["/util.js"] = []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? ''); export const hueOf = () => 0; export const copyText = async () => true;`)
	modules["/ws.js"] = []byte(`export const frames = [];
export function send(f) { frames.push(f); return 'req-1'; }
export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`)
	modules["/pending.js"] = []byte(`export function pendingSlot() { return { arm(){}, claim(){ return false; }, drop(){}, waiting(){ return false; } }; }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, reasoningControlsPage, modules)
}
