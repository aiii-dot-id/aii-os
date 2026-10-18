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
<div class="proj-space"><div style="--hue:200">
  <div class="proj-head" id="head"><h2 id="head-h2"><span class="dot"></span>Name</h2></div>
  <div class="proj-dir" id="dir">/srv/example</div>
  <div class="tabbar" id="tabbar"><button type="button" class="tab sel" id="tab">Overview</button></div>
</div></div>
<div class="proj-desc" id="desc">A description</div>
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
  want('tabbar', 'display', 'flex'); want('tabbar', 'column-gap', '6px');
  want('tabbar', 'border-bottom-style', 'solid'); want('tabbar', 'margin-top', '18px'); want('tabbar', 'margin-bottom', '14px');
  want('tabbar', 'padding-left', '22px');
  want('tab', 'text-transform', 'uppercase'); want('tab', 'border-top-left-radius', '999px');
  want('head', 'display', 'flex'); want('head', 'align-items', 'baseline'); want('head', 'column-gap', '14px');
  want('head', 'flex-wrap', 'wrap'); want('head', 'padding-top', '20px'); want('head', 'border-bottom-style', 'solid');
  want('head-h2', 'font-weight', '750'); want('head-h2', 'font-size', '22px'); want('head-h2', 'display', 'flex');
  want('dir', 'margin-top', '8px'); want('dir', 'margin-bottom', '4px'); want('dir', 'padding-left', '22px'); want('dir', 'font-size', '11px');
  want('desc', 'max-width', '640px'); want('desc', 'margin-bottom', '20px'); want('desc', 'padding-top', '10px'); want('desc', 'font-size', '13.5px');
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
	for _, sel := range []string{".card", ".card h3", ".tabbar", ".proj-head", ".proj-head h2", ".proj-dir", ".proj-desc"} {
		heads := regexp.MustCompile(`(?m)^`+regexp.QuoteMeta(sel)+` \{`).FindAllIndex(layoutCSS, -1)
		if len(heads) != 1 {
			t.Errorf("layout.css defines %s in %d top-level rules; one rule holds what the cascade computes", sel, len(heads))
		}
	}
	page := strings.Replace(cssCascadePage, "__LAYOUT_CSS__", string(layoutCSS), 1)
	page = strings.Replace(page, "__THEME_CSS__", string(themeCSS), 1)
	runPageInEnginesAtSize(t, page, map[string][]byte{}, 1280, 800)
}
