//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"strings"
	"testing"
)

func TestEverySettingsMenuEntrySaysWhatItHolds(t *testing.T) {
	page := `<!doctype html><html><body><div id="settings-stack"></div>
<script type="module">
import { S } from '/state.js';
import { renderSettings } from '/views/settings.js';
import { run } from '/__harness.js';
run(async (assert) => {
  S.view = 'settings'; S.providersLoaded = true; S.providers = [];
  S.config = { llm: {}, speech: {}, dashboard: { host: '192.0.2.10', port: 8181, tls: true, require_token: false },
    prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [] };
  renderSettings();
  const check = when => {
    const entries = [...document.querySelectorAll('[data-sec]')];
    assert(entries.length >= 16, when + ': the menu lists ' + entries.length + ' entries');
    const seen = new Set();
    for (const el of entries) {
      const tip = el.title.trim(), label = el.textContent.trim();
      assert(tip.length > 20 && tip.endsWith('.'), when + ': ' + label + ' has no sentence of its own: "' + tip + '"');
      assert(tip.toLowerCase() !== label.toLowerCase(), when + ': ' + label + ' is explained by its own label');
      assert(!seen.has(tip), when + ': two entries share one sentence: "' + tip + '"');
      seen.add(tip);
    }
  };
  check('first drawn');
  document.querySelector('[data-sec="witness"]').click();
  check('after a click');
  assert(document.querySelector('[data-sec="witness"]').title.includes('receipt'), 'Witness does not say what a witness does');
});
</script></body></html>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js", "static/util.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { providers: [], config: null, providersLoaded: false };`)
	modules["/ws.js"] = []byte(`export function send() { return 'req-1'; } export function query() { return 'req-1'; }`)
	modules["/pending.js"] = []byte(`export function pendingSlot() { return { arm(){}, claim(){ return false; }, drop(){}, waiting(){ return false; } }; } export function anyPending() { return false; }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, page, modules)
}
