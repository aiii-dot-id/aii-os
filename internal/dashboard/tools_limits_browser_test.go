//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// toolsLimitsPage draws Technical's limits over a read-back that carries the
// web_fetch ceiling (tools.webfetch_max_bytes, row 46) and saves a change.
const toolsLimitsPage = `<!doctype html>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>__CSS__</style>
<div id="settings-stack"></div><div id="technical-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { acceptSettingsConfig } from './views/settings.js';
import { drawLimits } from './views/settings/technical/limits.js';
import { assert, run } from './__harness.js';
run(() => {
  const settings = [
    { key: 'tools.webfetch_max_bytes', min: 1, max: 100000000, default: 4000000, unit: 'bytes', zero_is_default: true, card: 'tools', effective: { value: 4000000, level: 'default' } },
    { key: 'agency.max_tool_rounds', min: 1, max: 1000000, default: 30, unit: 'rounds', card: 'agency', per_model: 'model', unlimited: true, effective: { value: 30, level: 'default' } },
  ];
  S.tools = [{ name: 'web_fetch', description: 'Fetch a page.', enabled: true }];
  S.config = { llm: {}, dashboard: {}, prompt: {}, witness: {}, logs: {}, plugins: {}, agency: {}, tools: { webfetch_max_bytes: 0 },
    cognition: { settings, conditions: [], overrides_digest: 'd1' }, restart_required: [] };
  S.providersLoaded = true; S.view = 'technical';
  const st = document.getElementById('technical-stack');
  S.renderTechnical = () => drawLimits(st);
  S.renderTechnical();
  const field = st.querySelector('[data-cog-key="tools.webfetch_max_bytes"]');
  assert(field, 'the ceiling is drawn on the panel');
  assert(field.placeholder === 'Usually 4', 'blank is the usual value: ' + field.placeholder);
  const said = st.querySelector('[data-cog-applies="tools.webfetch_max_bytes"]').textContent;
  assert(said === 'A longer page is read up to here and says so. Applies from the next fetch.', 'the line under it: ' + said);
  assert(!st.querySelector('[data-lim-row="tools.webfetch_max_bytes"]'), 'a global key offers no override');
  field.value = '8';
  document.querySelector('[data-save="technical"]').click();
  const sent = frames.filter(f => f.type === 'config_set').at(-1).config;
  assert(JSON.stringify(sent) === '{"tools.webfetch_max_bytes":8000000}', 'the save sends the one key: ' + JSON.stringify(sent));
  S.config.tools.webfetch_max_bytes = 8000000;
  assert(acceptSettingsConfig('req-1'), 'the save is claimed');
  S.renderTechnical();
  assert(document.querySelector('[data-said="technical"]').textContent === 'Saved — applies from the next fetch.', 'the save says when it applies: ' + document.querySelector('[data-said="technical"]').textContent);
  assert(document.documentElement.scrollWidth <= window.innerWidth + 1, 'the page is wider than the screen');
});
</script>`

// THE WEB_FETCH CEILING IS SET WITH THE LIMITS. The table's row for it
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
