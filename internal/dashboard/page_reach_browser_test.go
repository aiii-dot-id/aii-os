//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func reachCSS(t *testing.T) string {
	t.Helper()
	theme, err := staticFS.ReadFile("static/theme.css")
	if err != nil {
		t.Fatal(err)
	}
	layout, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	return "<style>" + string(theme) + "</style><style>" + string(layout) + "</style>"
}

func shippedNav(t *testing.T) string {
	t.Helper()
	index, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(index)
	start := strings.Index(html, `<nav class="nav" id="nav">`)
	end := strings.Index(html[start:], `</nav>`)
	if start < 0 || end < 0 {
		t.Fatal("index.html has no navigation")
	}
	return html[start : start+end+len(`</nav>`)]
}

func TestHiddenPagesLeaveTheTabOrder(t *testing.T) {
	page := `<!doctype html>` + reachCSS(t) + `
<div class="views" style="height:300px">
  <section class="view on" id="view-a"><button id="a">on the page</button></section>
  <section class="view" id="view-b"><select id="b"><option>hidden provider</option></select></section>
</div>
<script type="module">
import { assert, run, until } from './__harness.js';
run(async () => {
  const a = document.getElementById('a'), b = document.getElementById('b');
  b.focus();
  assert(document.activeElement !== b && getComputedStyle(b).visibility === 'hidden', 'an inactive page\'s control took focus');
  a.focus();
  assert(document.activeElement === a, 'the page that is on is not reachable');
  document.getElementById('view-a').classList.remove('on');
  document.getElementById('view-b').classList.add('on');
  await until(() => getComputedStyle(a).visibility === 'hidden', 3000);
  a.blur(); a.focus();
  assert(document.activeElement !== a, 'a page switched away from kept its controls reachable after its fade');
  b.focus();
  assert(document.activeElement === b, 'the page switched to is not reachable');
});
</script>`
	runPageInEngines(t, page, nil)
}

func TestTheNavigationIsReachableByKeyboard(t *testing.T) {
	desktop := `<!doctype html>` + reachCSS(t) + `<div class="app"><aside class="rail">` + shippedNav(t) + `</aside></div>
<script type="module">
import { assert, run } from './__harness.js';
run(() => {
  const items = Array.from(document.querySelectorAll('#nav .nav-item'));
  assert(items.length === 9, 'navigation items: ' + items.length);
  assert(items.every(el => el.tagName === 'BUTTON' && el.type === 'button'), 'a destination is not a button: ' + items.map(el => el.tagName).join());
  for (const el of items.filter(el => !el.hidden)) { el.focus(); assert(document.activeElement === el, (el.dataset.view || el.id) + ' cannot take focus'); }
  const chat = document.querySelector('.nav-item[data-view="chat"]');
  assert(chat.getAttribute('aria-current') === 'page' && items.filter(el => el.hasAttribute('aria-current')).length === 1, 'the page in view is not marked current');
  const update = document.getElementById('nav-update');
  assert(update.hidden && getComputedStyle(update).display === 'none', 'the hidden Update chip is drawn');
  const nav = document.getElementById('nav'), inner = nav.clientWidth - 20;
  const home = document.querySelector('.nav-item[data-view="home"]');
  assert(home.offsetWidth >= inner - 1 && getComputedStyle(home).textAlign === 'left', 'the rail items no longer fill the rail: ' + home.offsetWidth + ' of ' + inner);
});
</script>`
	runPageInEngines(t, desktop, nil)
	phone := `<!doctype html>` + reachCSS(t) + `<div class="app"><aside class="rail">` + shippedNav(t) + `</aside></div>
<script type="module">
import { assert, run } from './__harness.js';
run(() => {
  assert(innerWidth <= 767, 'not at phone width: ' + innerWidth);
  const items = Array.from(document.querySelectorAll('#nav .nav-item')).filter(el => !el.hidden);
  const top = items[0].getBoundingClientRect().top;
  assert(items.every(el => Math.abs(el.getBoundingClientRect().top - top) < 1), 'the phone bar is not one row');
  assert(items.every(el => el.getBoundingClientRect().right <= innerWidth + 0.5 && el.getBoundingClientRect().width < innerWidth / 2), 'a phone bar item is not sized to the bar');
});
</script>`
	runPageInEnginesAtSize(t, phone, nil, 393, 852)
}

func TestASaveBarWrapsAtPhoneWidth(t *testing.T) {
	page := `<!doctype html>` + reachCSS(t) + `
<div class="card" id="card" style="width:361px;box-sizing:border-box">
  <div class="savebar"><button class="btn">Save focus</button><button class="btn ghost">Work outside this project</button><button class="btn ghost">Close project</button><button class="btn ghost">Archive</button><span class="savenote show">saved</span></div>
</div>
<script type="module">
import { assert, run } from './__harness.js';
run(() => {
  const card = document.getElementById('card').getBoundingClientRect();
  const out = Array.from(document.querySelectorAll('.savebar > *')).filter(el => el.getBoundingClientRect().right > card.right + 0.5);
  assert(out.length === 0, 'outside the card: ' + out.map(el => el.textContent).join(', '));
  assert(document.documentElement.scrollWidth <= innerWidth, 'the page scrolls sideways');
});
</script>`
	runPageInEnginesAtSize(t, page, nil, 393, 852)
}
