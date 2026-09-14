//go:build !windows

// .
// .

package dashboard

import (
	"testing"
)

// .
// .
// .
// .
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

// .
// .
// .
// .
func TestModelPickerInBrowser(t *testing.T) {
	module, err := staticFS.ReadFile("static/views/model-picker.js")
	if err != nil {
		t.Fatal(err)
	}
	runPageInEngines(t, modelPickerPage, map[string][]byte{
		"/model-picker.js": module,
	})
}
