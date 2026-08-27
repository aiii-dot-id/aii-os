//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"testing"
)

/*
Drives the REAL theme.js in every installed engine.

This closes a gap I named rather than papered over: theme.js had Go-side
validation tests and structural review, but nothing had ever watched a
token actually reach a rendered element. Reading setProperty and believing
it works is exactly the "comment describes runtime" mistake in another
costume — nothing executes a code review.

The assertions are cross-engine on purpose. getComputedStyle resolution of
a custom property through var() is the specific behaviour that differs
between engines, and it is the whole mechanism the theme tier rests on.
*/

const themePage = `<!doctype html>
<html><head><style>
  :root { --accent: rgb(10, 20, 30); --pad: 4px; }
  #probe { color: var(--accent); padding-left: var(--pad); }
</style></head>
<body>
<div id="probe">probe</div>
<script type="module">
import { assert, run } from './__harness.js';
import { onTheme } from './theme.js';

const probe = () => document.getElementById('probe');
const colorNow = () => getComputedStyle(probe()).color;
const padNow = () => getComputedStyle(probe()).paddingLeft;

run(() => {
  // 0. Baseline: the compiled default is in force.
  assert(colorNow() === 'rgb(10, 20, 30)', 'baseline colour wrong: ' + colorNow());

  // 1. A theme token overrides the compiled default by ordinary cascade,
  //    with no !important and no specificity war.
  onTheme({ v: 1, tokens: { '--accent': 'rgb(200, 100, 50)' } });
  assert(colorNow() === 'rgb(200, 100, 50)', 'token did not apply: ' + colorNow());

  // 2. A token dropped from the file must actually disappear, not linger
  //    from the previous apply. This is the bug the applied[] list exists
  //    to prevent, and it is invisible to any Go-side test.
  onTheme({ v: 1, tokens: { '--pad': '17px' } });
  assert(colorNow() === 'rgb(10, 20, 30)', 'dropped token lingered: ' + colorNow());
  assert(padNow() === '17px', 'second token did not apply: ' + padNow());

  // 3. null = absent/invalid theme = every token removed, compiled
  //    defaults restored. Deletion is a statement.
  onTheme(null);
  assert(colorNow() === 'rgb(10, 20, 30)', 'null did not restore colour: ' + colorNow());
  assert(padNow() === '4px', 'null did not restore padding: ' + padNow());

  // 4. Defence in depth: this module must be safe on its own terms. A
  //    name that is not a custom property is refused HERE, so a future
  //    caller cannot use the theme path to set real CSS properties.
  onTheme({ v: 1, tokens: { 'position': 'fixed', '--accent': 'rgb(1, 2, 3)' } });
  assert(getComputedStyle(probe()).position === 'static',
    'non-custom property was set: ' + getComputedStyle(probe()).position);
  assert(colorNow() === 'rgb(1, 2, 3)', 'valid token alongside refused one did not apply');

  // 5. A value the engine dislikes is DROPPED, not reinterpreted — the
  //    structural floor under the server allowlist. The previous good
  //    value must survive rather than the page being rewritten.
  onTheme({ v: 1, tokens: { '--accent': 'rgb(4, 5, 6)' } });
  const before = colorNow();
  assert(before === 'rgb(4, 5, 6)', 'setup for garbage test failed: ' + before);
  onTheme({ v: 1, tokens: { '--accent': 'not-a-colour-at-all' } });
  // var() with an unusable value falls back to the inherited/initial
  // colour; what must NOT happen is any other declaration changing.
  assert(padNow() === '4px', 'garbage value leaked into another property: ' + padNow());

  onTheme(null);
});
</script>
</body></html>`

func TestThemeAppliesInBrowsers(t *testing.T) {
	themeJS, err := staticFS.ReadFile("static/theme.js")
	if err != nil {
		t.Fatal(err)
	}
	runPageInEngines(t, themePage, map[string][]byte{
		"/theme.js": themeJS,
	})
}
