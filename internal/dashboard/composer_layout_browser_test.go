//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// THE REPLY BOX IS ONE ROW (the board ReplyBox), measured in a real engine
// against the shipped stylesheets and the shipped markup: "+", the text, then
// Send inside one box one line tall, as wide as the conversation's column;
// "+" opens its menu above it; a hidden control stays hidden.
// "+" COMES FIRST, on the left of the box, as in the interfaces people know, with Talk
// and Send on the right. A keyboard reaches it before the text. Its menu opens upward
// from its own side and stays within the box, at both widths. On a phone the text has
// a row of its own, full width, above that row of controls, however many lines it is.
const plusFirstJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const $ = id => document.getElementById(id);
const r = el => el.getBoundingClientRect();
const shown = el => !!el && el.getClientRects().length > 0;
const near = (a, b) => Math.abs(a - b) <= 1;
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  S.go('home');
  await until(() => shown($('msg-input')) && shown($('attach-btn')) && shown($('send-btn')), 5000).catch(() => {});
  const row = r(document.querySelector('.c-reply__box')), plus = r($('attach-btn')), text = r($('msg-input')), send = r($('send-btn'));
  const phone = innerWidth < 768;
  if (phone) {
    // The text has a row of its own, above "+" on the left and Talk and Send on the right.
    const ownRow = field => {
      check(field.left - row.left <= 6 && row.right - field.right <= 6, 'the text at ' + field.left + '-' + field.right + ' does not span the box at ' + row.left + '-' + row.right);
      for (const [name, b] of [['+', r($('attach-btn'))], ['Send', r($('send-btn'))]]) check(b.top >= field.bottom - 0.5, name + ' at ' + b.top + ' does not sit below the text ending at ' + field.bottom);
      check(r($('attach-btn')).left < r($('send-btn')).left, '+ is not left of Send');
      check(near(r($('attach-btn')).bottom, r($('send-btn')).bottom), '+ and Send are not on one row');
    };
    ownRow(text);
    $('msg-input').value = 'Please read the second sentence again.\nThe budget table can stay.\nAnd send it on when you are happy with it.';
    $('msg-input').dispatchEvent(new Event('input', { bubbles: true }));
    await until(() => r($('msg-input')).height > text.height, 3000).catch(() => {});
    check(r($('msg-input')).height > text.height, 'the text did not grow with several lines');
    ownRow(r($('msg-input')));
    $('msg-input').value = '';
    $('msg-input').dispatchEvent(new Event('input', { bubbles: true }));
    await until(() => near(r($('msg-input')).height, text.height), 3000).catch(() => {});
  } else {
    check(plus.right <= text.left, '+ at ' + plus.left + '-' + plus.right + ' is not left of the text at ' + text.left);
    check(text.right <= send.left, 'the text ending at ' + text.right + ' is not left of Send at ' + send.left);
  }
  check(near(plus.left - row.left, row.right - send.right), '+ sits ' + (plus.left - row.left) + ' px in from the left and Send ' + (row.right - send.right) + ' px in from the right');
  check($('attach-btn').compareDocumentPosition($('msg-input')) & Node.DOCUMENT_POSITION_FOLLOWING, 'a keyboard does not reach + before the text');
  check($('msg-input').compareDocumentPosition($('send-btn')) & Node.DOCUMENT_POSITION_FOLLOWING, 'a keyboard does not reach the text before Send');
  $('attach-btn').click();
  await until(() => shown($('plus-menu')), 3000).catch(() => {});
  const menu = r($('plus-menu')), box = r(document.querySelector('.c-reply__box'));
  check(shown($('plus-menu')), '+ does not open its menu');
  check(menu.bottom <= box.top + 0.5 && menu.top >= 0, 'the menu at ' + menu.top + '-' + menu.bottom + ' does not open upward on the screen, from a box at ' + box.top);
  check(near(menu.left, box.left), 'the menu starts at ' + menu.left + ', not at the left of the box, ' + box.left);
  check(menu.right <= box.right + 0.5, 'the menu ends at ' + menu.right + ', past the box at ' + box.right);
  check(document.documentElement.scrollWidth <= innerWidth, 'the page scrolls sideways');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK\n+ ' + Math.round(plus.left) + ', the text ' + Math.round(text.left) + '-' + Math.round(text.right) + ', Send ' + Math.round(send.right) + ' in ' + Math.round(row.left) + '-' + Math.round(row.right) + '; the menu ' + Math.round(menu.left) + '-' + Math.round(menu.right));
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestPlusComesFirstInTheReplyBox(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{{"phone", 390, 844}, {"window", 1280, 800}} {
		t.Run(size.name, func(t *testing.T) {
			runRealPageAtSize(t, statusFakeSocketJS, plusFirstJS, homeStyles, size.width, size.height)
		})
	}
}

func TestTheReplyBoxIsOneRowInBrowser(t *testing.T) {
	var css string
	for _, f := range []string{"static/tokens/tokens.css", "static/layout.css", "static/components/reply-box/reply-box.css"} {
		b, err := staticFS.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		css += string(b) + "\n"
	}
	index, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(index)
	start := strings.Index(html, `<div class="composer-wrap" id="composer-wrap">`)
	end := strings.Index(html, `<section class="view" id="view-projects">`)
	if start < 0 || end < start {
		t.Fatal("index.html has no composer-wrap before the projects view")
	}
	fragment := html[start:end]
	fragment = fragment[:strings.LastIndex(fragment, "</section>")]
	page := `<!doctype html><style>` + css + `</style>
<div style="width:1200px">
  <div class="thread-inner" id="column"><div style="height:1px"></div></div>
  ` + fragment + `
</div>
<script type="module">
import { assert, run } from './__harness.js';
run(() => {
  const r = el => el.getBoundingClientRect();
  const row = r(document.querySelector('.c-reply__box'));
  const text = r(document.getElementById('msg-input')), plus = r(document.getElementById('attach-btn')), send = r(document.getElementById('send-btn'));
  const column = document.getElementById('column');
  const inner = r(column), pad = parseFloat(getComputedStyle(column).paddingLeft);
  assert(row.height < 64, 'the reply box is not one line tall: ' + row.height + 'px');
  for (const [name, b] of [['the text', text], ['+', plus], ['Send', send]]) assert(b.top >= row.top - 0.5 && b.bottom <= row.bottom + 0.5 && b.left >= row.left - 0.5 && b.right <= row.right + 0.5, name + ' is outside the box');
  assert(plus.right <= text.left && text.right <= send.left, 'the row is not +, then the text, then Send');
  assert(Math.abs(row.width - (inner.width - 2 * pad)) < 1, 'the box is not as wide as the conversation column: ' + row.width + ' vs ' + (inner.width - 2 * pad));
  const menu = document.getElementById('plus-menu');
  menu.hidden = false;
  assert(r(menu).bottom <= plus.top + 0.5 && r(menu).height > 0, 'the + menu does not open above +');
  assert(r(menu).left >= row.left - 0.5 && r(menu).right <= row.right + 0.5, 'the + menu runs past the reply box: ' + r(menu).left + '–' + r(menu).right + ' in ' + row.left + '–' + row.right);
  menu.hidden = true;
  assert(getComputedStyle(menu).display === 'none', 'a hidden + menu still shows');
  const more = document.getElementById('mic-more');
  more.hidden = true;
  assert(getComputedStyle(more).display === 'none', 'a hidden control in the row still shows');
});
</script>`
	runPageInEngines(t, page, nil)
}
