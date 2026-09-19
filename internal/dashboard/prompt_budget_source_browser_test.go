//go:build !windows

package dashboard

import "testing"

// .
// .
// .
// .
// .
// .
const promptBudgetSourcePage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  S.identityExists = true; S.providersLoaded = true;
  S.providers = [{ name: 'Local (oMLX)', models: ['m1'], default_model: 'm1', chat: true }];
  const show = llm => {
    S.config = { llm: Object.assign({ provider: 'Local (oMLX)', model: 'm1', resolved_provider: 'Local (oMLX)', resolved_model: 'm1', endpoint: 'http://127.0.0.1:10240/v1', timeout_seconds: 120 }, llm) };
    renderSettings();
    return document.getElementById('settings-stack').textContent;
  };

  let text = show({ prompt_budget: 32000, prompt_budget_source: 'fallback' });
  assert(text.includes('context 32000 (fallback — set context length on the provider)'),
    'a fallback ceiling is not named as one: ' + text.slice(0, 400));

  text = show({ context_length: 262144, prompt_budget: 226144, prompt_budget_source: 'derived' });
  assert(text.includes('context 262144 (derived)'), 'a window-derived ceiling lost its source: ' + text.slice(0, 400));
  assert(!text.includes('fallback'), 'a discovered window still reads as a fallback');

  text = show({ prompt_budget: 50000, prompt_budget_source: 'declared' });
  assert(text.includes('context 50000 (declared)'), "the operator's own ceiling lost its source: " + text.slice(0, 400));

  text = show({});
  assert(text.includes('context —'), 'with nothing known the line must stay a dash: ' + text.slice(0, 400));
});
</script>`

func TestSubstrateCardNamesThePromptBudgetSource(t *testing.T) {
	runPageInEngines(t, promptBudgetSourcePage, substrateModules(t))
}
