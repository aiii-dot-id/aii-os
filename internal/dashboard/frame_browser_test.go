//go:build !windows

package dashboard

import "testing"

// THE FRAME FOLLOWS THE WINDOW: the text's middle block grows and shrinks
// with the window and leaves no odd-sized edges. On the real page at a phone's width, the desktop size and a
// wide window: the bar is the gutter in from its edges; Home's column, the
// conversation's and the reply box's are one column, the window less its
// gutters up to the measure, centred; About's tabs and the page under them
// share the place's column, and so do Settings' list and page (each on its
// own address at a phone's width); past the
// desktop size the type and the columns grow by one factor, so a line
// holds the same words; and nothing scrolls sideways.
const framePageJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
const $ = sel => document.querySelector(sel);
const box = sel => { const el = $(sel); return el && el.getClientRects().length ? el.getBoundingClientRect() : null; };
const near = (a, b) => Math.abs(a - b) < 1;
const f = n => Math.round(n * 10) / 10;
// tokens.css at this width: the growth past 1280 px, the gutter, and a
// column of a given desktop width, centred in the page.
const VW = innerWidth, CW = document.documentElement.clientWidth;
const grow = Math.min(Math.max((VW - 1280) * .0234375, 0), 30) / 100;
const gutter = VW < 768 ? 16 : Math.min(Math.max(16 + VW * .0125, 32), 48);
// The sidebar stands at the left of a desktop's page; the columns are centred in what is left.
const RAIL = VW < 768 ? 0 : VW < 1024 ? 184 : 224;
const column = desk => { const w = Math.min(CW - RAIL - 2 * gutter, desk * (1 + grow)); return { left: RAIL + (CW - RAIL - w) / 2, width: w }; };
// A classic scroll bar (this host's headless Chrome draws them) takes its
// room inside a scroller; the frame is measured without them. The thread
// keeps its own on both sides (scrollbar-gutter), so where one is drawn the
// conversation's column stays centred over the reply box.
const bare = () => { for (const el of document.querySelectorAll('*')) el.style.scrollbarWidth = 'none'; };
const go = async view => { S.go(view); await tick(); bare(); await tick(); };
const at = (what, b, want) => check(b && near(b.left, want.left) && near(b.width, want.width),
  what + ' is not the column at ' + VW + ' px: ' + (b ? f(b.left) + ' wide ' + f(b.width) : 'not drawn') + ', not ' + f(want.left) + ' wide ' + f(want.width));
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 1, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  await tick();
  const bar = getComputedStyle($('#topbar'));
  if (VW >= 768) check(near(parseFloat(bar.paddingRight), gutter), 'the bar is ' + bar.paddingRight + ' in at its end, not the gutter ' + f(gutter));
  const probe = document.createElement('span');
  probe.style.fontSize = 'var(--v-md)';
  document.body.append(probe);
  const voice = parseFloat(getComputedStyle(probe).fontSize);
  probe.remove();
  check(near(voice, 19 * (1 + grow)), 'their voice is ' + voice + ' px at ' + VW + ' px, not ' + f(19 * (1 + grow)));
  const reading = column(680);
  await go('chat');
  const conversation = box('#thread-inner');
  at("the conversation's column", conversation, reading);
  // The measure follows the type: where the window does not narrow it,
  // the column holds the same number of their voice's ems at every width.
  if (CW - RAIL - 2 * gutter > 680 * (1 + grow)) check(conversation && near(conversation.width / voice, 680 / 19), 'the column is ' + (conversation && f(conversation.width / voice)) + ' of their ems, not ' + f(680 / 19));
  at('the reply box', box('#composer-wrap .c-reply__box'), reading);
  const place = column(1056);
  await go('memory');
  at("About's tabs", box('#place-tabs'), place);
  at("About's page", box('#memory-stack'), place);
  await go('status');
  const list = box('#place-tabs'), page = box('#status-stack');
  if (VW >= 768) {
    // The list as it is seen: its box gives a focus ring room to either side.
    const seen = box('#place-tabs > .c-side-nav');
    check(seen && near(seen.left, place.left), "Settings' list does not start at the place's column: " + (seen && f(seen.left)) + ', not ' + f(place.left));
    check(page && near(page.right, place.left + place.width), "Settings' page does not end at the place's column: " + (page && f(page.right)) + ', not ' + f(place.left + place.width));
  } else {
    // At a phone's width (the board, step 14b; host 1413, 1625) a group's
    // page is the column with no list above it, and Settings' own address is
    // the list: each is measured on its own address.
    check(!list, "Settings' list is drawn above Status's page at " + VW + ' px');
    at("Settings' page", page, place);
    check(document.documentElement.scrollWidth <= CW, "Status's page scrolls sideways at " + VW + ' px: ' + document.documentElement.scrollWidth + ' wide');
    location.hash = '#/settings';
    await until(() => box('#place-tabs') && !box('#views'));
    bare(); await tick();
    check(!box('#views'), "Settings' own address shows a page at " + VW + ' px, not only the list');
    at("Settings' list", box('#place-tabs'), place);
  }
  check(document.documentElement.scrollWidth <= CW, 'the page scrolls sideways at ' + VW + ' px: ' + document.documentElement.scrollWidth + ' wide');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

var frameStyles = append([]string{"components/top-bar/top-bar.css", "components/tabs/tabs.css", "components/side-nav/side-nav.css", "views/settings/status.css"}, homeStyles...)

func TestTheFrameFollowsTheWindow(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"phone", 390, 844}, {"desktop", 1280, 800}, {"wide", 1920, 1080}} {
		t.Run(size.name, func(t *testing.T) {
			runRealPageAtSize(t, statusFakeSocketJS, framePageJS, frameStyles, size.w, size.h)
		})
	}
}
