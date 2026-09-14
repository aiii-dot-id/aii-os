//go:build !windows

package dashboard

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .

func themedPage(t *testing.T, body string) (string, map[string][]byte) {
	t.Helper()
	theme, err := staticFS.ReadFile("static/theme.css")
	if err != nil {
		t.Fatal(err)
	}
	layout, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	modules := map[string][]byte{}
	for _, path := range []string{"static/theme.js", "static/theme-boot.js", "static/state.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	page := `<!doctype html><html><head><style>` + string(theme) + `</style><style>` + string(layout) + `</style></head><body>` + body + `</body></html>`
	return page, modules
}

// .
// .
func layoutTokens(t *testing.T) []string {
	t.Helper()
	layout, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`var\((--[a-z0-9-]+)`).FindAllStringSubmatch(string(layout), -1) {
		switch m[1] {
		case "--hue", "--bg2":
			continue
		}
		seen[m[1]] = true
	}
	var out []string
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestEveryLayoutTokenResolvesInBothThemes(t *testing.T) {
	tokens := layoutTokens(t)
	if len(tokens) < 30 {
		t.Fatalf("expected the stylesheet's token set, got %d: %v", len(tokens), tokens)
	}
	page, modules := themedPage(t, `<div id="probe"></div><script type="module">
import { run } from '/__harness.js';
run((assert) => {
  const tokens = `+jsStringArray(tokens)+`;
  for (const theme of ['dark', 'light']) {
    document.documentElement.setAttribute('data-theme', theme);
    const cs = getComputedStyle(document.documentElement);
    const missing = tokens.filter(n => cs.getPropertyValue(n).trim() === '');
    assert(missing.length === 0, theme + ' theme leaves tokens unresolved: ' + missing.join(', '));
  }
  // The greeting's gradient text must paint: with the gradient unresolved
  // the text is transparent on nothing.
  document.documentElement.setAttribute('data-theme', 'light');
  const g = document.createElement('div'); g.className = 'greet'; g.textContent = 'Good evening.'; document.body.appendChild(g);
  const bi = getComputedStyle(g).backgroundImage;
  assert(bi && bi !== 'none', 'the greeting gradient must resolve in the light theme, got ' + bi);
});
</script>`)
	runPageInEngines(t, page, modules)
}

// .
// .
func TestTextKeepsItsContrastInBothThemes(t *testing.T) {
	page, modules := themedPage(t, `<div id="probe"></div><script type="module">
import { run } from '/__harness.js';
run((assert) => {
  const probe = document.getElementById('probe');
  const rgb = (prop, value) => { probe.style.cssText = prop + ':' + value + ';'; const s = getComputedStyle(probe)[prop === 'background' ? 'backgroundColor' : 'color']; const m = s.match(/[\d.]+/g).map(Number); return { r: m[0], g: m[1], b: m[2], a: m.length > 3 ? m[3] : 1 }; };
  const lin = (c) => { c /= 255; return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); };
  const lum = (c) => 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b);
  const over = (fg, bg) => ({ r: fg.r * fg.a + bg.r * (1 - fg.a), g: fg.g * fg.a + bg.g * (1 - fg.a), b: fg.b * fg.a + bg.b * (1 - fg.a), a: 1 });
  const ratio = (a, b) => { const l1 = lum(a), l2 = lum(b); return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05); };
  // Labels (--faint) carry short uppercase captions, never running text: 3.5:1 is their floor; the rest is body or state text.
  const floors = [['--txt', 7], ['--dim', 4.5], ['--faint', 3.5], ['--good', 4.5], ['--warn', 4.5], ['--bad', 4.5], ['--acc2', 3.3]];
  for (const theme of ['dark', 'light']) {
    document.documentElement.setAttribute('data-theme', theme);
    const bg0 = rgb('background', 'var(--bg0)');
    const panel = over(rgb('background', 'var(--panel)'), bg0);
    for (const [tok, floor] of floors) {
      const c = rgb('color', 'var(' + tok + ')');
      for (const [name, bg] of [['page', bg0], ['panel', panel]]) {
        const r = ratio(c, bg);
        assert(r >= floor, theme + ' theme: ' + tok + ' on the ' + name + ' is ' + r.toFixed(2) + ':1, below its floor ' + floor + ':1');
      }
    }
  }
});
</script>`)
	runPageInEngines(t, page, modules)
}

// .
// .
// .
func TestThemeChoiceIsAppliedAtBootAndPersists(t *testing.T) {
	page, modules := themedPage(t, `<header class="stagebar"><button class="theme-toggle" id="theme-toggle" type="button" aria-pressed="false"></button></header>
<script src="/theme-boot.js"></script>
<script type="module">
import { run } from '/__harness.js';
import { initThemeSwitch, themeChoice, currentTheme, setThemeChoice } from '/theme.js';
run((assert) => {
  const root = document.documentElement;
  localStorage.removeItem('aii.theme');
  const system = matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  assert(window.aiiThemeBoot() === system && root.getAttribute('data-theme') === system, 'with no stored choice boot follows the browser (' + system + ')');
  assert(themeChoice() === 'system', 'no key means system, got ' + themeChoice());
  initThemeSwitch();
  const btn = document.getElementById('theme-toggle');
  btn.click();
  const flipped = system === 'light' ? 'dark' : 'light';
  assert(currentTheme() === flipped, 'the switch flips the theme to ' + flipped + ', got ' + currentTheme());
  assert(localStorage.getItem('aii.theme') === flipped, 'the choice is stored');
  assert(btn.getAttribute('aria-pressed') === (flipped === 'light' ? 'true' : 'false'), 'the switch reports its state');
  assert(/light|dark/.test(btn.title), 'the switch names what it would do: ' + btn.title);
  root.setAttribute('data-theme', 'wiped');
  assert(window.aiiThemeBoot() === flipped, 'a fresh boot reads the stored choice back');
  setThemeChoice('system');
  assert(localStorage.getItem('aii.theme') === null && currentTheme() === system, 'system removes the key and follows the browser again');
});
</script>`)
	runPageInEngines(t, page, modules)
}
