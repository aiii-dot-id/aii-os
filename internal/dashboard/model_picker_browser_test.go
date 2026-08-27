//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"testing"
)

// modelPickerPage exercises fillModelPicker in a real engine: the
// preferred model wins, the backend's list is rendered whole, and a
// current model the backend does not know about is retained rather
// than silently replaced (a private alias must survive a refresh).
const modelPickerPage = `<!doctype html>
<select id="model"></select>
<script type="module">
import { run, assert } from '/__harness.js';
import { fillModelPicker } from '/model-picker.js';
run(() => {
  const select = document.getElementById('model');
  fillModelPicker(select, {
    models: ['m1', 'm2', 'm3'], configured_models: ['m2', 'm3'], default_model: 'm1'
  }, 'm2');
  assert(select.value === 'm2', 'preferred model');
  assert([...select.options].map(x => x.value).join(',') === 'm1,m2,m3', 'backend model list');
  fillModelPicker(select, { models: ['m1'] }, 'private-alias');
  assert(select.value === 'private-alias', 'current model not retained');
});
</script>`

// TestModelPickerInBrowser runs in every installed engine. It was
// Frame-only until the cross-engine harness landed: the old shape
// scraped --dump-dom output, and the RESULT CHANNEL — not the
// assertions — was what tied it to one browser.
func TestModelPickerInBrowser(t *testing.T) {
	module, err := staticFS.ReadFile("static/views/model-picker.js")
	if err != nil {
		t.Fatal(err)
	}
	runPageInEngines(t, modelPickerPage, map[string][]byte{
		"/model-picker.js": module,
	})
}
