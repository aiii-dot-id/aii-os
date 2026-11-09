//go:build !windows

package dashboard

import "testing"

// EVERY SELECT IS DRAWN AT A CONTROL'S HEIGHT, in every engine: WebKit keeps a select
// it draws the height of its words whatever min-height says, so the page draws each
// one itself. A sheet that sets its own background or padding keeps the chevron and
// keeps its words clear of it.
const selectHeightPage = `<!doctype html><meta charset="utf-8">
<link rel="stylesheet" href="/tokens/tokens.css"><link rel="stylesheet" href="/layout.css">
<link rel="stylesheet" href="/components/filter/filter.css"><link rel="stylesheet" href="/views/settings/people.css">
<link rel="stylesheet" href="/views/settings/access.css"><link rel="stylesheet" href="/views/firstrun/start.css">
<select data-s="bare"><option>Choose the recipient and channel</option></select>
<select data-s="filter" class="c-filter__select"><option>Choose the recipient and channel</option></select>
<select data-s="people" class="settings-people__select"><option>Choose the recipient and channel</option></select>
<select data-s="access" class="settings-access__select"><option>Choose the recipient and channel</option></select>
<div class="first-run__field"><select data-s="first-run"><option>Choose the recipient and channel</option></select></div>
<div class="store-bar"><select data-s="store-bar"><option>Choose the recipient and channel</option></select></div>
<script type="module">
import { assert, run } from '/__harness.js';

run(async () => {
  if (document.readyState !== 'complete') await new Promise(r => addEventListener('load', r, { once: true }));
  const want = innerWidth < 768 ? 44 : 40;
  const selects = [...document.querySelectorAll('select')];
  assert(selects.length === 6, 'not every select was drawn: ' + selects.length);
  for (const s of selects) {
    const css = getComputedStyle(s), h = s.getBoundingClientRect().height;
    assert(h >= want, s.dataset.s + ' is ' + h + ' tall, under ' + want + ' (appearance ' + css.appearance + ', min-height ' + css.minHeight + ')');
    assert(css.backgroundImage.includes('gradient'), s.dataset.s + ' lost its chevron: ' + css.backgroundImage);
    assert(parseFloat(css.paddingRight) >= 30, s.dataset.s + "'s words can run under its chevron: " + css.paddingRight);
  }
});
</script>`

func TestEverySelectIsDrawnAtAControlsHeight(t *testing.T) {
	css, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	modules := map[string][]byte{"/layout.css": css}
	t.Run("wide", func(t *testing.T) { runPageInEnginesAtSize(t, selectHeightPage, modules, 1280, 800) })
	t.Run("phone", func(t *testing.T) { runPageInEnginesAtSize(t, selectHeightPage, modules, 390, 844) })
}
