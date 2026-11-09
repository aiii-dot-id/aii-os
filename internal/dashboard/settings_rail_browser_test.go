//go:build !windows

package dashboard

import (
	"fmt"
	"testing"
)

// SETTINGS' LIST REACHES ITS LAST ITEM, AND OPENING ONE MOVES NOTHING ELSE. On the
// shipped page at the window heights people use (Chrome at 150% on a 1280 by 800
// window is 853 by 533 CSS pixels), with Technical's panels open under it: the list
// is taller than the window, so it scrolls on its own and its last item can be
// brought into view (with a person's own wheel, where the engine's driver gives
// one); opening the last panel by its address leaves the page and its bar where
// they were and shows that panel's item; and a focus ring is never cut at the
// list's edge. At 1024 by 640 the operator saw the list end at "Turn and tool
// limits" with no way to the two below.
const settingsRailJS = `
import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const say = f => sockets[0].say(f);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const stats = { name: 'Quill', version: 'test', build: 'test', ledger_seq: 7, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 };
const config = { llm: { provider: 'P', model: 'm', resolved_provider: 'P', resolved_model: 'm', timeout_seconds: 120 }, speech: {}, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [], dashboard: {}, cognition: {} };
const rail = () => document.querySelector('.stage > #place-tabs');
const items = () => [...document.querySelectorAll('#place-tabs .technical__nav .c-side-nav__item')];
const open = () => document.querySelector('#place-tabs .technical__nav [aria-current="page"]');
const inView = el => { const r = el.getBoundingClientRect(), box = rail().getBoundingClientRect(); return r.top >= box.top - 0.5 && r.bottom <= Math.min(box.bottom, innerHeight) + 0.5; };
const still = when => {
  check(document.scrollingElement.scrollTop === 0, when + ': the page itself moved ' + document.scrollingElement.scrollTop + ' px');
  check(Math.abs(document.getElementById('topbar').getBoundingClientRect().top) < 0.5, when + ': the bar moved to ' + Math.round(document.getElementById('topbar').getBoundingClientRect().top));
};

async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  say({ type: 'status', stats }); say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 7 } }); say({ type: 'config', config });
  await until(() => S.stats && S.stats.name === 'Quill');
  const when = innerWidth + ' by ' + innerHeight;

  // The last panel, by its address.
  location.hash = '#/settings/technical/layout';
  await until(() => open() && open().getAttribute('href') === '#/settings/technical/layout', 5000);
  const last = items()[items().length - 1];
  check(last && last === open(), when + ': the last item is not the open one: ' + (last && last.textContent));
  check(rail().scrollHeight > rail().clientHeight, when + ': the list fits the window, so this size proves nothing: ' + rail().scrollHeight + ' in ' + rail().clientHeight);
  still(when + ', the last panel opened');
  check(inView(open()), when + ': the open panel\'s item is out of view, at ' + Math.round(open().getBoundingClientRect().bottom) + ' of ' + innerHeight);

  // The first panel: the list at its top, then scrolled to its end by a person.
  location.hash = '#/settings/technical/sent';
  await until(() => open() && open().getAttribute('href') === '#/settings/technical/sent', 5000);
  rail().scrollTop = 0;
  await until(() => rail().scrollTop === 0);
  check(!inView(items()[items().length - 1]), when + ': the last item is in view at the list\'s top, so this size proves nothing');
  check(['auto', 'scroll'].includes(getComputedStyle(rail()).overflowY), when + ': the list does not scroll: overflow-y ' + getComputedStyle(rail()).overflowY);
  if (window.__wheel) {
    const b = rail().getBoundingClientRect();
    await window.__wheel(b.left + b.width / 2, b.top + b.height / 2, 2000);
  } else {
    rail().scrollTop = rail().scrollHeight;
  }
  await until(() => inView(items()[items().length - 1]), 3000);
  check(inView(items()[items().length - 1]), when + ': the last item cannot be brought into view: it ends at ' + Math.round(items()[items().length - 1].getBoundingClientRect().bottom) + ' of ' + innerHeight);
  still(when + ', the list scrolled to its end');

  // A focus ring (2 px, 2 px out) is whole inside the list's edges.
  const box = rail().getBoundingClientRect();
  for (const a of [...rail().querySelectorAll('a.c-side-nav__item')]) {
    const r = a.getBoundingClientRect();
    check(r.left - 4 >= box.left - 0.5 && r.right + 4 <= box.right + 0.5, when + ': the focus ring of "' + a.textContent.trim() + '" is cut: ' + Math.round(r.left) + ' to ' + Math.round(r.right) + ' in ' + Math.round(box.left) + ' to ' + Math.round(box.right));
  }
  report(problems.length ? 'FAIL at ' + when + ':\n  - ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestSettingsListReachesItsLastItem(t *testing.T) {
	page, modules, headers := shippedPageRig(t, settingsRailJS)
	for _, size := range [][2]int{{853, 533}, {1024, 640}, {1280, 800}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			runPageInEnginesAtSizeWithHeaders(t, page, modules, headers, size[0], size[1])
		})
	}
}
