//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// capabilitiesCardPage draws the LLM panel and a provider's card over a
// read-back that carries what the running model takes and gives, and runs
// the script in check. __CSS__ is the page's stylesheet, for the width
// check.
const capabilitiesCardPage = `<!doctype html>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>__CSS__</style>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  const caps = {
    model: 'claude-opus-5',
    takes: [
      { kind: 'image', state: 'yes', from: "Anthropic's model list" },
      { kind: 'pdf', state: 'yes', from: 'the shipped list (models.dev (anthropic), 2026-10-20)', not_sent: true },
      { kind: 'audio', state: 'no', from: 'your providers.json' },
      { kind: 'video', state: 'unknown', from: 'nobody has said' },
    ],
    gives: [
      { kind: 'text', state: 'yes', from: 'the shipped list (models.dev (anthropic), 2026-10-20)' },
      { kind: 'image', state: 'no', from: 'the shipped list (models.dev (anthropic), 2026-10-20)' },
      { kind: 'audio', state: 'no', from: 'the shipped list (models.dev (anthropic), 2026-10-20)' },
      { kind: 'video', state: 'no', from: 'the shipped list (models.dev (anthropic), 2026-10-20)' },
    ],
    tools: { kind: 'tools', state: 'yes', from: 'measured when this model was chosen, 2026-10-20 16:10 UTC' },
    window: { tokens: 1000000, from: "Anthropic's model list" },
    output_limit: { from: 'nobody has said' },
  };
  S.config = { llm: { provider: 'Anthropic', model: 'claude-opus-5', resolved_provider: 'Anthropic', resolved_model: 'claude-opus-5', endpoint: 'https://api.anthropic.com', capabilities: caps },
    dashboard: { host: '127.0.0.1', port: 1, tls: false }, prompt: {}, witness: {}, logs: {}, plugins: {}, agency: {},
    cognition: { settings: [], conditions: [] }, restart_required: [] };
  S.providers = [{ name: 'Anthropic', api_type: 'anthropic', endpoint: 'https://api.anthropic.com', models: ['claude-opus-5'], chat: true, default_model: 'claude-opus-5', effort_model: 'claude-opus-5', capabilities: caps }];
  S.providersLoaded = true; S.view = 'settings';
  renderSettings();
  const st = document.getElementById('settings-stack');
  const lines = root => [...root.querySelectorAll('[data-cap-line]')].map(d => d.textContent);
  const want = [
    "image — yes · Anthropic's model list",
    'PDF — yes · the shipped list (models.dev (anthropic), 2026-10-20) · this host does not send it',
    'audio — no · your providers.json',
    'video — unknown · nobody has said',
    'text — yes · the shipped list (models.dev (anthropic), 2026-10-20)',
    'image — no · the shipped list (models.dev (anthropic), 2026-10-20)',
    'audio — no · the shipped list (models.dev (anthropic), 2026-10-20)',
    'video — no · the shipped list (models.dev (anthropic), 2026-10-20)',
    'tools — yes · measured when this model was chosen, 2026-10-20 16:10 UTC',
  ];
  const figures = root => [...root.querySelectorAll('[data-cap-figure]')].map(d => d.textContent);
  const wantFigures = ["window — 1,000,000 tokens · Anthropic's model list", 'output limit — unknown · nobody has said'];
  const panel = st.querySelector('[data-capabilities="claude-opus-5"]');
  assert(panel, 'the LLM panel draws what the running model takes and gives');
  assert(JSON.stringify(lines(panel)) === JSON.stringify(want), 'the panel says: ' + JSON.stringify(lines(panel)));
  assert(/Takes/.test(panel.textContent) && /Gives/.test(panel.textContent), 'the panel heads its two lists');
  assert(JSON.stringify(figures(panel)) === JSON.stringify(wantFigures), 'the panel\'s limits: ' + JSON.stringify(figures(panel)));
  assert(document.documentElement.scrollWidth <= window.innerWidth + 1, 'the panel is wider than the screen: ' + document.documentElement.scrollWidth + ' > ' + window.innerWidth);

  // The provider's card says the same of the model it will use.
  document.querySelector('[data-sec="providers"]').click();
  st.querySelector('[data-prov-row="0"]').click();
  const card = st.querySelector('[data-provider-editor] [data-capabilities="claude-opus-5"]');
  assert(card, 'the provider card draws its model\'s capabilities');
  assert(JSON.stringify(lines(card)) === JSON.stringify(want), 'the card says: ' + JSON.stringify(lines(card)));
  assert(JSON.stringify(figures(card)) === JSON.stringify(wantFigures), 'the card\'s limits: ' + JSON.stringify(figures(card)));
  assert(document.documentElement.scrollWidth <= window.innerWidth + 1, 'the card is wider than the screen: ' + document.documentElement.scrollWidth + ' > ' + window.innerWidth);
});
</script>`

func capabilitiesCardPageWithCSS(t *testing.T) string {
	t.Helper()
	css, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Replace(capabilitiesCardPage, "__CSS__", string(css), 1)
}

// THE CARD AND THE LLM PANEL SAY WHAT A MODEL TAKES AND GIVES, kind by
// kind, each with where that was said, and that a kind the model takes is
// not sent by this host where it is not; in a real browser, at phone
// width, where nothing scrolls sideways.
func TestTheCardSaysWhatAModelTakesAndGivesInARealBrowser(t *testing.T) {
	runPageInEnginesAtSize(t, capabilitiesCardPageWithCSS(t), limitsCardModules(t), 375, 812)
}
