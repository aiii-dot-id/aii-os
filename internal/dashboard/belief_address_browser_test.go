//go:build !windows

package dashboard

import (
	"testing"
)

// A BELIEF'S ADDRESS OPENS ITS TRAIL ON A FRESH LOAD, in the page's own drawing, which
// a reader meets when About's section cannot run. The address is read before the
// module that opens a trail has come, so the module reads it when it comes.
const beliefAddressDriverJS = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
(async () => {
  // The rig opens at "/": the page is loaded again at the address, as a reload or a saved link does.
  if (location.hash !== '#/identity/beliefs/b1') { location.hash = '#/identity/beliefs/b1'; location.reload(); return; }
  const trail = () => document.querySelector('#beliefs-view .c-trail');
  await until(trail);
  if (!trail()) return report('FAIL: a fresh load at a belief\'s address opened no trail: ' + location.hash + ' | ' + (document.getElementById('beliefs-view') || document.body).innerText.slice(0, 120));
  if (location.hash !== '#/identity/beliefs/b1') return report('FAIL: the address moved: ' + location.hash);
  report('OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func TestABeliefsAddressOpensItsTrailOnAFreshLoad(t *testing.T) {
	runDashboardInEachEngine(t, func(t *testing.T) *Server {
		pressed := 0
		h := statusSectionHandler(&pressed)
		h.GetIdentity = func() (*IdentityState, error) {
			return &IdentityState{Beliefs: []BeliefItem{{ID: "b1", Statement: "A deploy should go to staging first."}}}, nil
		}
		return New("127.0.0.1", 0, h)
	}, "/", beliefAddressDriverJS)
}

// THE ADDRESS WAITS FOR THE IDENTITY, AND A BAD ONE BREAKS NOTHING. On the real page,
// driven through its own socket: About draws before the identity comes, and the
// identity then opens the belief the address names; a malformed address draws About
// and opens nothing, and a belief's address followed after it opens its trail.
const beliefAddressPageJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const live = () => sockets[sockets.length - 1];
const say = m => live().say(m);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const stats = { name: 'Mira', version: 'test', build: 'test', ledger_seq: 40, lifetime_ticks: 1, belief_count: 1, intention_count: 0, experience_count: 0, reflection_count: 0 };
const identity = { type: 'identity', identity: { beliefs: [{ id: 'b1', statement: 'A deploy should go to staging first.', ring: 3, status: 'confirmed', evidence_count: 4, confidence: 0.91 }] } };
const trail = () => document.querySelector('#beliefs-view .c-trail');
const row = () => document.querySelector('#beliefs-view .c-belief[data-belief-id="b1"]');
const asked = () => sent.filter(f => f.type === 'query' && f.query === 'belief_trail' && f.belief_id === 'b1').length;
const seen = () => location.hash + ' | ' + ((document.getElementById('beliefs-view') || {}).textContent || 'no Beliefs drawn').slice(0, 160);
async function run() {
  const start = location.hash;
  await until(() => sockets.length > 0);
  live().open();
  say({ type: 'status', stats }); say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 40 } });
  if (start === '#/identity/beliefs/b1') {
    // The identity is held: About draws first, with nothing to open.
    await until(() => document.querySelector('#beliefs-view .beliefs__search'), 8000);
    check(document.querySelector('#beliefs-view .beliefs__search') && !S.identity, 'rig: About did not draw before the identity came: ' + seen());
    say(identity);
    await until(trail, 8000);
    check(trail(), 'the identity came after About drew, and the belief the address names was not opened: ' + seen());
    check(asked() > 0, 'no trail was asked for the belief the address names');
  } else if (start === '#/identity/beliefs/bad%') {
    say(identity);
    await until(row, 8000);
    check(row(), 'a malformed address left About undrawn: ' + seen());
    check(!trail(), 'a malformed address opened a trail: ' + seen());
    location.hash = '#/identity/beliefs/b1';
    await until(trail, 8000);
    check(trail(), 'after a malformed address, a belief\'s address opened no trail: ' + seen());
  } else {
    check(false, 'rig: the page did not load at the address it was given: ' + start);
  }
  check(location.hash === '#/identity/beliefs/b1', 'the address moved: ' + location.hash);
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

// atAddress loads the page at hash, as a saved link does, before its modules run.
func atAddress(hash string) string {
	return statusFakeSocketJS + "history.replaceState(null, '', '" + hash + "');\n"
}

func TestABeliefsAddressWaitsForTheIdentity(t *testing.T) {
	runRealPage(t, atAddress("#/identity/beliefs/b1"), beliefAddressPageJS, beliefsStyles)
}

func TestAMalformedBeliefsAddressBreaksNothing(t *testing.T) {
	runRealPage(t, atAddress("#/identity/beliefs/bad%"), beliefAddressPageJS, beliefsStyles)
}
