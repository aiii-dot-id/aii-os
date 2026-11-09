//go:build !windows

package dashboard

import "testing"

// RECORD NAMES THEM WHEN THEIR NAME COMES, HOWEVER LATE. A page opened on
// Record can read the record before the identity's status that carries their
// name: its rows then said "Them" and kept saying it, while a reload a moment
// later named them (TestAReloadIsFastCompleteAndIdentical on main, 2026-11-06,
// quiet, 1 run in 3). Now the status names them in place, whenever it comes:
// the same rows, an open row still open, the reader's place kept, the filter
// named too (a review's read of 07a6964c: a wait with a cap still kept "Them"
// after a late status).
const recordNameJS = `import { report, until } from '/__harness.js';
const sockets = window.__sockets;
const say = m => sockets[sockets.length - 1].say(m);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const stats = name => ({ type: 'status', stats: { name, version: 'test', build: 'test', ledger_seq: 3, lifetime_ticks: 1, belief_count: 1, intention_count: 0, experience_count: 1, reflection_count: 0 } });
const rows = () => [...document.querySelectorAll('article[aria-label^="Entry "]')];
const row = seq => document.getElementById('record-' + seq);
const head = seq => (row(seq)?.querySelector('.record__line') || {}).textContent || '';
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  // Born, and not yet named on this page: the status comes without their name.
  say(stats('Unnamed'));
  location.hash = '#/identity/record';
  await until(() => rows().length === 3, 8000);
  check(rows().length === 3, 'rig: the record was not drawn: ' + rows().length + ' rows');
  check(head('3').includes(' · Them · '), 'rig: before their name came the row said ' + JSON.stringify(head('3')));
  const open = row('3').querySelector('.record__open');
  open.click();
  await until(() => open.getAttribute('aria-expanded') === 'true', 5000);
  const before = row('3'), top = before.getBoundingClientRect().top;
  // Their name comes later, as the status does.
  say(stats('Taylor'));
  await until(() => !head('3').includes(' · Them · '), 5000);
  check(head('3').includes(' · Taylor · ') && head('2').includes(' · Taylor · '), 'their rows do not name them: ' + JSON.stringify([head('3'), head('2')]));
  check(row('3') === before, 'the rows were drawn again, not named in place');
  check(open.getAttribute('aria-expanded') === 'true', 'the open row closed');
  check(Math.abs(row('3').getBoundingClientRect().top - top) <= 1, 'the reader\'s place moved: ' + Math.round(top) + ' to ' + Math.round(row('3').getBoundingClientRect().top));
  const who = document.querySelector('option[value="identity"]');
  check(who && who.textContent === 'Taylor', 'the filter calls them ' + (who && who.textContent));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

const recordNameRows = `{"version":1,"rows":[
{"seq":"3","time":"2026-11-05T10:02:00Z","type":"belief.create","ring":3,"bytes":60,"head":"{\"content\":\"A deploy goes to staging first.\"}"},
{"seq":"2","time":"2026-11-05T10:01:00Z","type":"experience.create","ring":3,"bytes":60,"head":"{\"content\":\"They rolled the deploy back.\"}"},
{"seq":"1","time":"2026-11-05T10:00:00Z","type":"ring0.genesis","ring":0,"bytes":10,"head":"{}"}
],"first_view_bytes":0,"range_bytes":0,"older":""}`

func TestRecordNamesThemWhenTheirNameComesHoweverLate(t *testing.T) {
	runRealPageServing(t, statusFakeSocketJS, recordNameJS, homeStyles, map[string][]byte{"/record": []byte(recordNameRows)})
}
