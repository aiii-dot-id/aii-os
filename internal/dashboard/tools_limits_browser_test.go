//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const toolsLimitsPage = `<!doctype html>
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
    { key: 'tools.webfetch_max_bytes', min: 1, max: 100000000, default: 4000000, unit: 'bytes', zero_is_default: true, card: 'tools', effective: { value: 4000000, level: 'default' } },
    { key: 'agency.max_tool_rounds', min: 1, max: 1000000, default: 30, unit: 'rounds', card: 'agency', per_model: 'model', unlimited: true, effective: { value: 30, level: 'default' } },
  ];
  S.tools = [{ name: 'web_fetch', description: 'Fetch a page.', enabled: true }];
  S.config = { llm: {}, dashboard: {}, prompt: {}, witness: {}, logs: {}, plugins: {}, agency: {}, tools: { webfetch_max_bytes: 0 },
    cognition: { settings, conditions: [], overrides_digest: 'd1' }, restart_required: [] };
  S.providersLoaded = true; S.view = 'settings';
  renderSettings();
  document.querySelector('[data-sec="tools"]').click();
  const st = document.getElementById('settings-stack');
  const field = st.querySelector('[data-cog-key="tools.webfetch_max_bytes"]');
  assert(field, 'the ceiling is drawn on the Tools page');
  assert(!st.querySelector('[data-cog-key="agency.max_tool_rounds"]'), 'another card’s limit is not drawn here');
  assert(field.placeholder === '4000000 (default)', 'blank is the default: ' + field.placeholder);
  const said = st.querySelector('[data-cog-applies="tools.webfetch_max_bytes"]').textContent;
  assert(said === 'A longer page is read to here, and says so; offset reaches no further. Applies from the next fetch.', 'the line under it: ' + said);
  assert(!st.querySelector('[data-lim-row="tools.webfetch_max_bytes"]'), 'a global key offers no override');
  field.value = '8000000';
  st.querySelector('[data-save="tools"]').click();
  const sent = frames.filter(f => f.type === 'config_set').at(-1).config;
  assert(JSON.stringify(sent) === '{"tools.webfetch_max_bytes":8000000}', 'the save sends the one key: ' + JSON.stringify(sent));
  S.config.tools.webfetch_max_bytes = 8000000;
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  renderSettings();
  assert(st.querySelector('[data-said="tools"]').textContent === 'Saved — applies from the next fetch.', 'the save says when it applies: ' + st.querySelector('[data-said="tools"]').textContent);
  assert(document.documentElement.scrollWidth <= window.innerWidth + 1, 'the page is wider than the screen');
});
</script>`

// THE WEB_FETCH CEILING IS SET ON THE TOOLS PAGE. The table's row for it
// is drawn there, by the renderer every limit uses: its bounds, its default
// in the blank field, the line saying what it bounds and when a change
// applies; a save sends that key alone and says when it applies.
func TestTheToolsPageSetsWhatWebFetchReads(t *testing.T) {
	css, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	runPageInEnginesAtSize(t, strings.Replace(toolsLimitsPage, "__CSS__", string(css), 1), limitsCardModules(t), 375, 812)
}
