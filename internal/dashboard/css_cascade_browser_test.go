//go:build !windows

package dashboard

import (
	"regexp"
	"strings"
	"testing"
)

const cssCascadePage = `<!doctype html>
<html data-theme="light">
<style>__THEME_CSS__</style>
<style>__LAYOUT_CSS__</style>
<div class="card" id="card"><h3 id="card-h3">Title</h3></div>
<script type="module">
import { assert, run } from './__harness.js';
run(() => {
  const cs = id => getComputedStyle(document.getElementById(id));
  const want = (id, prop, value) => {
    const got = cs(id).getPropertyValue(prop);
    assert(got === value, '#' + id + ' ' + prop + ' is ' + JSON.stringify(got) + ', want ' + JSON.stringify(value));
  };
  want('card', 'padding-top', '18px'); want('card', 'padding-left', '20px');
  want('card', 'backdrop-filter', 'blur(12px)');
  assert(cs('card').boxShadow !== 'none', 'the card lost its shadow');
  want('card', 'border-top-width', '1px'); want('card', 'border-top-style', 'solid');
  want('card-h3', 'font-weight', '650'); want('card-h3', 'font-size', '11px');
  want('card-h3', 'text-transform', 'uppercase'); want('card-h3', 'margin-bottom', '0px'); want('card-h3', 'padding-top', '16px');
  // .tabbar, .proj-head, .proj-dir and .proj-desc went with the project page
  // they drew (step 12b, views/projects.css).
});
</script>`

func TestEachMergedRuleIsOneRuleAndKeepsItsCascade(t *testing.T) {
	layoutCSS, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	themeCSS, err := staticFS.ReadFile("static/theme.css")
	if err != nil {
		t.Fatal(err)
	}
	// Outside a media query each selector heads one rule.
	for _, sel := range []string{".card", ".card h3"} {
		heads := regexp.MustCompile(`(?m)^`+regexp.QuoteMeta(sel)+` \{`).FindAllIndex(layoutCSS, -1)
		if len(heads) != 1 {
			t.Errorf("layout.css defines %s in %d top-level rules; one rule holds what the cascade computes", sel, len(heads))
		}
	}
	page := strings.Replace(cssCascadePage, "__LAYOUT_CSS__", string(layoutCSS), 1)
	page = strings.Replace(page, "__THEME_CSS__", string(themeCSS), 1)
	runPageInEnginesAtSize(t, page, map[string][]byte{}, 1280, 800)
}
