//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"strings"
	"testing"
)

// The Plugins view, rendered from the SHIPPED module.
//
// The autoload level and the discovered-but-not-loaded list lived under
// Settings while this view showed a "COMING" placeholder, so an operator
// looking for plugin management was told it did not exist yet while the
// controls sat one tab away. The runtime was real the whole time —
// pluginhost activates by verified tier at boot. Only the path was wrong,
// and a feature reachable by no path is one that was not built.
//
// Operator ruling 2026-08-24: real controls here.

const pluginsViewPage = `<!doctype html>
<style>__LAYOUT_CSS__</style>
<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';
import { saved } from './views/settings.js';

run(() => {
  S.config = {
    plugins: {
      autoload: 'T2',
      skips: [{ dir: 'plugins/memoryd', id: 'org.example.memoryd', tier: 'T0',
                reason: 'unsigned — below the T2 auto-load level' }],
    },
  };
  renderPlugins();
  const st = document.getElementById('plugins-stack');
  const text = st.textContent;

  // 1. The real control is present and reflects the CURRENT setting —
  //    not a default, or the operator would silently change it by saving.
  const sel = document.getElementById('cfg-plevel');
  assert(sel !== null, 'the Plugins view has no auto-load control — it is still a placeholder');
  assert(sel.value === 'T2', 'the control shows ' + sel.value + ', not the configured T2');
  assert(sel.getBoundingClientRect().height > 0, 'the auto-load control is not visible');

  // 2. A verified-but-not-loaded package is NAMED, with its reason. This
  //    is the difference between "no plugins" and "plugins you refused".
  assert(text.includes('org.example.memoryd'),
    'a present, verified, unloaded package is not shown: ' + text.slice(0, 200));
  assert(text.includes('below the T2 auto-load level'),
    'the reason a package did not load is missing');

  // 3. Saving reaches the ONE config owner, tagged as the plugins section.
  const save = st.querySelector('[data-save]');
  assert(save !== null, 'there is no way to save the level that was just changed');
  save.click();
  assert(saved.length === 1 && saved[0] === 'plugins',
    'the save did not reach the config owner as the plugins section: ' + JSON.stringify(saved));

  // 4. The roadmap survives, BELOW the controls — it is what explains the
  //    composer microphone's reserved seat, kept deliberately.
  assert(text.includes('VOICE'), 'the voice roadmap card was lost in the move');
  const ctl = sel.getBoundingClientRect();
  const voice = [...st.querySelectorAll('h3')].find(h => h.textContent.includes('VOICE'));
  assert(voice && voice.getBoundingClientRect().top > ctl.top,
    'the roadmap sits above the controls — the placeholder is still the first thing an operator sees');
});
</script>`

func TestPluginsViewShowsRealControlsInBrowser(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	layoutCSS := read("layout.css")
	page := strings.Replace(pluginsViewPage, "__LAYOUT_CSS__", string(layoutCSS), 1)
	if page == pluginsViewPage {
		t.Fatal("stylesheet placeholder not substituted — the page would assert against no CSS at all")
	}
	runPageInEngines(t, page, map[string][]byte{
		"/views/plugins.js": read("views/plugins.js"),
		"/state.js":         read("state.js"),
		"/util.js":          read("util.js"),
		// settings.js owns config saving; stubbed here so the seam the
		// Plugins view consumes is observable. That it is a seam at all,
		// rather than a second copy of the state machine, is the point.
		"/views/settings.js": []byte(`export const saved = [];
export function saveConfigSection(section) { saved.push(section); }
export function configFeedbackHTML() { return ''; }`),
	})
}
