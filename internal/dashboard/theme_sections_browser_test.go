//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

/*
THE JOIN BETWEEN THE THEME TIER AND THE SECTION TIER.

Both halves work. theme.js applies the operator's theme.json tokens to
:root and the frame recolours correctly. sections.js hands a section
its design tokens at handshake so an iframe can match the frame it
lives in. Neither is broken on its own terms.

The join is where they disagree, and the disagreement is structural:

  - theme.js writes tokens as an INLINE STYLE on documentElement
    (root.style.setProperty), because that is the mechanism with no
    parse step.
  - collectTokens() reads tokens out of STYLESHEET RULES, walking
    document.styleSheets for a rule whose selectorText is ':root'.

An inline style is not a stylesheet rule. So the two mechanisms are
looking at different places, and a token set by the operator's theme
is invisible to the collector that feeds sections. The frame turns
the new accent; every section keeps the compiled one. Nothing errors,
nothing logs — the frame and its contents simply drift apart.

This test establishes that semantics EMPIRICALLY in each engine we
ship on, rather than asserting it from a reading of the CSSOM spec,
because the whole fix rests on it being true.
*/

const themeSectionsPage = `<!doctype html>
<html><head><style>
  :root { --accent: rgb(10, 20, 30); --stable: rgb(40, 50, 60); }
</style></head>
<body>
<script type="module">
import { assert, run } from './__harness.js';

// The EXACT reading logic of sections.js collectTokens(), reproduced
// here. If that function changes shape, this test is measuring the
// wrong thing and should be updated deliberately.
function collectFromRules() {
  const out = {};
  for (let i = 0; i < document.styleSheets.length; i++) {
    let rules;
    try { rules = document.styleSheets[i].cssRules; } catch (err) { continue; }
    if (!rules) continue;
    for (let j = 0; j < rules.length; j++) {
      const r = rules[j];
      if (!r.selectorText || r.selectorText !== ':root' || !r.style) continue;
      for (let k = 0; k < r.style.length; k++) {
        const name = r.style[k];
        if (name && name.indexOf('--') === 0) out[name] = r.style.getPropertyValue(name).trim();
      }
    }
  }
  return out;
}

run(() => {
  const root = document.documentElement;

  // 1. Baseline before any theme: the collector sees the compiled
  //    defaults, which is the case that works today.
  const before = collectFromRules();
  assert(before['--accent'] === 'rgb(10, 20, 30)',
    'baseline collect wrong: ' + before['--accent']);

  // 2. Apply a theme the way theme.js applies one.
  root.style.setProperty('--accent', 'rgb(200, 100, 50)');

  // 3. THE FRAME IS CORRECT. The inline property wins the cascade,
  //    so anything in the frame reading var(--accent) recolours.
  const probe = document.createElement('div');
  probe.style.color = 'var(--accent)';
  document.body.appendChild(probe);
  assert(getComputedStyle(probe).color === 'rgb(200, 100, 50)',
    'frame did not take the themed value: ' + getComputedStyle(probe).color);

  // 4. THE DEFECT. The rule-walking collector still reports the
  //    COMPILED default, because an inline style is not a rule. This
  //    is the value a section would be handed at handshake.
  const after = collectFromRules();
  assert(after['--accent'] === 'rgb(10, 20, 30)',
    'engine surfaced inline props to a rule walk: ' + after['--accent']);

  // 5. And so the two disagree — the whole point.
  assert(after['--accent'] !== getComputedStyle(probe).color,
    'no divergence in this engine; the fix rests on a false premise');

  // 6. THE FIX'S PREMISE. Reading the effective value off the root
  //    element sees BOTH the compiled default and the theme override,
  //    which is why the fix overlays documentElement.style rather
  //    than replacing the rule walk.
  assert(root.style.getPropertyValue('--accent').trim() === 'rgb(200, 100, 50)',
    'inline read failed: ' + root.style.getPropertyValue('--accent'));
  assert(root.style.getPropertyValue('--stable').trim() === '',
    'a token NOT themed must not appear inline; the rule walk is still needed');
});
</script>
</body></html>`

func TestThemeTokensAreInvisibleToRuleWalk(t *testing.T) {
	runPageInEngines(t, themeSectionsPage, nil)
}
