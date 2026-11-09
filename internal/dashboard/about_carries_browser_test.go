//go:build !windows

package dashboard

import (
	"fmt"
	"testing"
	"time"
)

// A VISIT BEFORE THEIR BIRTH IS NO VISIT TO THEM (step 11; host 18:26Z,
// on the stack's shots). This browser's stamp of a last visit six days ago
// predates an identity whose record begins yesterday: it was left by
// another identity on this address. On the real page: Between you says
// "Since you first met" and lists their changes from the start; and a stamp
// after their birth is still a last visit (TestBetweenYouFromTheRecord).
const visitBeforeBirthJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const W = (await import('/strings/identity.js')).default;
const sockets = window.__sockets;
const $ = id => document.getElementById(id);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
const norm = s => (s || '').replace(/\s+/g, ' ').trim();
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 104, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 1, reflection_count: 0 } });
  sockets[0].say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 104 } });
  S.go('between');
  await until(() => document.getElementById('between-since'), 5000).catch(() => {});
  const head = norm((document.getElementById('between-since') || {}).textContent);
  check(head === W.sinceFirstMet, 'Between you does not say since you first met: ' + head);
  await until(() => document.querySelectorAll('.between__list li').length > 0, 5000).catch(() => {});
  check(norm($('between-stack').textContent).includes('Their first words.'), 'their changes are not listed from the start: ' + norm($('between-stack').textContent).slice(0, 200));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

func TestAVisitBeforeTheirBirthIsNoVisitToThem(t *testing.T) {
	at := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339Nano) }
	record := fmt.Sprintf(`{"version":1,"rows":[`+
		`{"seq":"3","time":%q,"type":"experience.create","ring":3,"bytes":40,"head":"{\"content\":\"Their first words.\"}"},`+
		`{"seq":"2","time":%q,"type":"system.witnessed","ring":0,"bytes":2,"head":"{}"},`+
		`{"seq":"1","time":%q,"type":"ring0.genesis","ring":0,"bytes":2,"head":"{}"}],"first_view_bytes":0,"range_bytes":0}`,
		at(time.Hour), at(20*time.Hour), at(24*time.Hour))
	first := fmt.Sprintf(`{"version":1,"rows":[{"seq":"1","time":%q,"type":"ring0.genesis","ring":0,"bytes":2,"head":"{}"}],"first_view_bytes":0,"range_bytes":0}`, at(24*time.Hour))
	runRealPageServing(t, sixDaysAgoJS+statusFakeSocketJS, visitBeforeBirthJS, append([]string{"views/identity/who.css", "components/tabs/tabs.css"}, homeStyles...),
		map[string][]byte{"/record": []byte(record), "/record?before=2": []byte(first)})
}

// ABOUT'S TABS SAY THEY SCROLL ON A PHONE (step 11; the stack's shots). At
// 390 by 844 the five tabs are wider than the screen: the row fades the side
// that has more (data-more), and the tab selected is brought into view, so
// none is cut with nothing said.
const tabsScrollJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 3, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  S.go('identity');
  const list = () => document.querySelector('#place-tabs [role="tablist"]');
  await until(() => list(), 5000);
  check(list().querySelectorAll('[role="tab"]').length === 5, 'About does not have its five tabs: ' + list().querySelectorAll('[role="tab"]').length);
  check(list().scrollWidth > list().clientWidth, 'the row is not wider than a phone, so this test proves nothing: ' + list().scrollWidth + ' in ' + list().clientWidth);
  await until(() => list().dataset.more, 2000).catch(() => {});
  check(list().dataset.more === 'end', 'a row cut at its end does not say it has more: ' + list().dataset.more);
  S.go('record'); await tick(); await tick();
  const tab = list().querySelector('[aria-selected="true"]'), box = list().getBoundingClientRect(), r = tab.getBoundingClientRect();
  check(tab.dataset.tab === 'record' && r.left >= box.left - 1 && r.right <= box.right + 1, 'the selected tab is not in view: ' + tab.dataset.tab + ' ' + Math.round(r.left) + '–' + Math.round(r.right) + ' in ' + Math.round(box.left) + '–' + Math.round(box.right));
  check(list().dataset.more === 'start' || list().dataset.more === 'both', 'scrolled to its end, the row does not say it has more at its start: ' + list().dataset.more);
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

func TestAboutsTabsSayTheyScrollOnAPhone(t *testing.T) {
	runRealPageAtSize(t, statusFakeSocketJS, tabsScrollJS, append([]string{"views/identity/who.css", "components/tabs/tabs.css"}, homeStyles...), 390, 844)
}
