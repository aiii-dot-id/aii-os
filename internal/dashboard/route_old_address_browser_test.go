//go:build !windows

package dashboard

import "testing"

// AN OLD DEEP ADDRESS KEEPS ITS ROW WHEN A GO COMES FIRST (a review's source
// read of the door's first gate, 2026-10-31): the address becomes
// #/record/7 and, before its hashchange is handled, the socket's open calls
// go() for the view shown. go() read the address with the raw router, which
// names no view for an old address, and wrote the Record list's own address
// over it, dropping the row. On the real page: with the Record already shown,
// and with another view shown, the go lands on #/identity/record/7.
const oldAddressPageJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  for (const shown of ['record', 'home']) {
    S.go(shown); await tick();
    // The address changes; its hashchange has not been handled yet.
    history.replaceState(null, '', '#/record/7');
    S.go(S.view);
    check(location.hash === '#/identity/record/7' && S.view === 'record', 'with ' + shown + ' shown, a go before the hashchange left ' + location.hash + ' (' + S.view + '), not #/identity/record/7');
    await tick();
    check(location.hash === '#/identity/record/7', 'with ' + shown + ' shown, the address moved on to ' + location.hash);
  }
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestAnOldDeepAddressKeepsItsRowWhenAGoComesFirst(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, oldAddressPageJS, nil)
}
