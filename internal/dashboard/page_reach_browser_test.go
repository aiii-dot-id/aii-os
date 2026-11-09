//go:build !windows

package dashboard

import "testing"

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

// A PAGE THAT IS NOT ON IS NOT THERE. Inactive views were only faded
// (opacity, pointer events), so Tab from Settings landed in Chat's
// invisible provider picker and a screen reader read pages nobody could
// see. After its fade an inactive view is hidden: out of the Tab order
// and out of what is announced; the view that is on is reachable.
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
	// The real page, at a desktop's width and a phone's: the sidebar's links
	// (components/top-bar), six on a desktop and four along the bottom of a
	// phone, each takes focus; the name, which opens the conversation, does; the place in
	// view is the one marked current; the hidden Update notice is not drawn;
	// and the links are one column, or on a phone one row, within the screen.
	for _, size := range [][2]int{{1280, 800}, {393, 852}} {
		s := New("127.0.0.1", 0, &WSHandler{})
		runDashboardInEnginesAtSize(t, s, "/", shellDriver+`
(async () => {
  const { ready } = await import('/recovery.js');
  await ready;
  await until(() => document.querySelector('.c-rail nav'));
  const items = [...document.querySelectorAll('#nav a.nav-item')].filter(a => a.getClientRects().length);
  if (items.length !== (innerWidth <= 767 ? 4 : 6)) return fail(items.length + ' places drawn at ' + innerWidth + ' px');
  // Their name is the bar's on a phone and the sidebar's head on a computer: the one shown.
  const who = [...document.querySelectorAll('.c-top-bar__who, .c-rail__who')].find(e => e.getClientRects().length);
  for (const a of [who, ...items]) {
    a.focus();
    if (document.activeElement !== a) return fail((a.textContent || a.className) + ' cannot take focus');
  }
  location.hash = '#/projects';
  await until(() => placeOn() === 'projects');
  if (document.querySelectorAll('#nav [aria-current="page"]').length !== 1 || placeOn() !== 'projects') return fail('the place in view is not the one marked current: ' + placeOn());
  const update = document.getElementById('nav-update');
  if (!update.hidden || getComputedStyle(update).display !== 'none') return fail('the hidden Update notice is drawn');
  const line = a => { const r = a.getBoundingClientRect(); return innerWidth <= 767 ? r.top : r.left; };
  if (!items.every(a => Math.abs(line(a) - line(items[0])) < 1)) return fail('the links are not one line');
  if (!items.every(a => a.getBoundingClientRect().right <= innerWidth + 0.5)) return fail('a place is off the screen');
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`, size[0], size[1])
	}
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
